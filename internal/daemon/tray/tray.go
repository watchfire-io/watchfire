//go:build cgo

package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/daemon/focus"
)

// Pre-allocated slot pool. Every menu surface that may flex with project
// counts uses Show / Hide / SetTitle / SetIcon on these fixed slots — Cocoa
// hates structural mutations to a live menu, so we set up the topology once
// in onReady and only mutate visibility / labels afterwards.
const (
	maxAttention = 10
	maxWorking   = 20
	maxIdleSlots = MaxIdleProjects
	maxNotifSubs = MaxNotifications
)

var (
	state    DaemonState
	onStart  func()
	onExit   func()
	portItem *systray.MenuItem

	// Serialized rebuild channel (prevents concurrent Cocoa API calls).
	rebuildCh chan struct{}

	// Dynamic tray icons.
	iconIdle   []byte
	iconActive []byte

	// Header & port.
	headerItem *systray.MenuItem

	// Pre-allocated section header + row pools.
	attentionHeader *systray.MenuItem
	attentionRows   [maxAttention]*systray.MenuItem

	workingHeader *systray.MenuItem
	workingRows   [maxWorking]*systray.MenuItem

	idleHeader   *systray.MenuItem
	idleRows     [maxIdleSlots]*systray.MenuItem
	idleOverflow *systray.MenuItem

	openWatchfireItem *systray.MenuItem
	openDashboardItem *systray.MenuItem

	notifRoot *systray.MenuItem
	notifRows [maxNotifSubs]*systray.MenuItem
	notifNone *systray.MenuItem // "No notifications today" placeholder inside the submenu

	updateItem *systray.MenuItem
	quitItem   *systray.MenuItem

	// Maps slot index → click action for click handler routing.
	slotMu                sync.RWMutex
	attentionActions      [maxAttention]ClickAction
	workingActions        [maxWorking]ClickAction
	idleActions           [maxIdleSlots]ClickAction
	notifActions          [maxNotifSubs]ClickAction
	previousActiveAgents  map[string]AgentInfo // for completion-detection notifications

	// Cache generated icons by hex color.
	iconCache   = make(map[string][]byte)
	iconCacheMu sync.RWMutex

	// Focus event bus — emits clicks the GUI subscribes to via gRPC.
	focusBus *focus.Bus
)

// Run starts the system tray. This blocks the calling goroutine (must be main).
// onStartFn is called when the tray is ready (launch gRPC server here).
// onExitFn is called when the tray exits (cleanup here).
func Run(s DaemonState, onStartFn, onExitFn func()) {
	state = s
	onStart = onStartFn
	onExit = onExitFn
	systray.Run(onReady, onQuit)
}

// Quit signals the tray to exit.
func Quit() {
	systray.Quit()
}

// FocusBus returns the focus event bus the tray emits clicks on. Nil before
// onReady runs. The daemon's gRPC service subscribes to this bus to fan
// FocusEvents out to GUI clients.
func FocusBus() *focus.Bus {
	return focusBus
}

// setTrayIcon sets the tray icon using the appropriate API for each platform.
func setTrayIcon(data []byte) {
	if runtime.GOOS == "darwin" {
		systray.SetTemplateIcon(data, data)
	} else {
		systray.SetIcon(data)
	}
}

func onReady() {
	iconIdle = iconData
	iconActive = generateActiveIcon(iconData)
	setTrayIcon(iconIdle)
	systray.SetTooltip(formatTooltip(0, 0))

	focusBus = focus.New()

	// === Header ===
	headerItem = systray.AddMenuItem("Watchfire (starting…)", "")
	headerItem.Disable()

	versionItem := systray.AddMenuItem(fmt.Sprintf("Version: %s", buildinfo.Version), "")
	versionItem.Disable()

	portItem = systray.AddMenuItem("Starting…", "")
	portItem.Disable()

	systray.AddSeparator()

	// === Section: Needs attention ===
	attentionHeader = systray.AddMenuItem("⚠  Needs attention", "")
	attentionHeader.Disable()
	attentionHeader.Hide()
	for i := 0; i < maxAttention; i++ {
		attentionRows[i] = systray.AddMenuItem("", "")
		attentionRows[i].Hide()
	}

	// === Section: Working ===
	workingHeader = systray.AddMenuItem("●  Working", "")
	workingHeader.Disable()
	workingHeader.Hide()
	for i := 0; i < maxWorking; i++ {
		workingRows[i] = systray.AddMenuItem("", "")
		workingRows[i].Hide()
	}

	// === Section: Idle ===
	idleHeader = systray.AddMenuItem("○  Idle", "")
	idleHeader.Disable()
	idleHeader.Hide()
	for i := 0; i < maxIdleSlots; i++ {
		idleRows[i] = systray.AddMenuItem("", "")
		idleRows[i].Hide()
	}
	idleOverflow = systray.AddMenuItem("", "")
	idleOverflow.Disable()
	idleOverflow.Hide()

	systray.AddSeparator()

	// === Open Watchfire / Open Dashboard ===
	openWatchfireItem = systray.AddMenuItem("Open Watchfire", "Launch Watchfire GUI")
	openDashboardItem = systray.AddMenuItem("Open Dashboard…", "Open the Watchfire dashboard")

	systray.AddSeparator()

	// === Notifications submenu ===
	notifRoot = systray.AddMenuItem("Notifications (0 today) ▸", "Recent notifications")
	for i := 0; i < maxNotifSubs; i++ {
		notifRows[i] = notifRoot.AddSubMenuItem("", "")
		notifRows[i].Hide()
	}
	notifNone = notifRoot.AddSubMenuItem("No notifications today", "")
	notifNone.Disable()

	systray.AddSeparator()

	// === Update / Quit ===
	updateItem = systray.AddMenuItem("Update Available", "A new version is available")
	updateItem.Hide()
	quitItem = systray.AddMenuItem("Quit Watchfire", "Shut down Watchfire daemon")

	// State trackers.
	previousActiveAgents = make(map[string]AgentInfo)

	// Start the rebuild loop.
	rebuildCh = make(chan struct{}, 1)
	go processRebuilds()

	// Start the daemon services.
	if onStart != nil {
		onStart()
	}

	// Now that the server is up, populate everything.
	if state != nil {
		portItem.SetTitle(fmt.Sprintf("Running on port: %d", state.Port()))
	}

	// Initial rebuild + click handlers.
	Refresh()
	go pollForUpdate()
	go handleClicks()
}

func onQuit() {
	if onExit != nil {
		onExit()
	}
}

func pollForUpdate() {
	if state == nil {
		return
	}
	for {
		time.Sleep(30 * time.Second)
		available, version := state.UpdateAvailable()
		if available {
			updateItem.SetTitle(fmt.Sprintf("Update Available — v%s", version))
			updateItem.Show()
			return
		}
	}
}

func openGUI() {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-b", "io.watchfire.app")
	case "linux":
		cmd = exec.Command("xdg-open", "watchfire://")
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "watchfire://")
	default:
		log.Printf("Open GUI not supported on %s", runtime.GOOS)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open GUI: %v", err)
	}
}

// emitFocus translates a tray ClickAction into a focus.Event and posts it on
// the bus. Best-effort GUI launch happens unconditionally so a click brings
// the GUI to the foreground even if no subscriber is currently attached.
func emitFocus(action ClickAction) {
	switch action.Kind {
	case ClickFocusMain, ClickFocusTasks, ClickFocusTask:
		openGUI()
		var target focus.Target
		switch action.Kind {
		case ClickFocusMain:
			target = focus.TargetMain
		case ClickFocusTasks:
			target = focus.TargetTasks
		case ClickFocusTask:
			target = focus.TargetTask
		}
		if focusBus != nil {
			focusBus.Emit(focus.Event{
				ProjectID:  action.ProjectID,
				Target:     target,
				TaskNumber: action.TaskNumber,
			})
		}
	case ClickFocusDigest:
		openGUI()
		if focusBus != nil {
			focusBus.Emit(focus.Event{
				Target:     focus.TargetDigest,
				DigestDate: action.DigestDate,
			})
		}
	case ClickOpenWatchfire:
		openGUI()
	case ClickOpenDashboard:
		openGUI()
		if focusBus != nil {
			focusBus.Emit(focus.Event{Target: focus.TargetMain})
		}
	case ClickUpdateAvail:
		log.Println("Update requested from tray — run 'watchfire update'")
	case ClickQuitWatchfire:
		if state != nil {
			state.RequestShutdown()
		}
	}
}

func handleClicks() {
	// Each pre-allocated slot has a dedicated goroutine that lives the
	// lifetime of the tray. Slot click handlers consult the current action
	// stored in the slot*Actions arrays — the rebuild path swaps these under
	// slotMu, so a click during a refresh races to a stable-pre-or-post
	// snapshot and never to a torn one.
	for i := 0; i < maxAttention; i++ {
		i := i
		go func() {
			for range attentionRows[i].ClickedCh {
				slotMu.RLock()
				a := attentionActions[i]
				slotMu.RUnlock()
				emitFocus(a)
			}
		}()
	}
	for i := 0; i < maxWorking; i++ {
		i := i
		go func() {
			for range workingRows[i].ClickedCh {
				slotMu.RLock()
				a := workingActions[i]
				slotMu.RUnlock()
				emitFocus(a)
			}
		}()
	}
	for i := 0; i < maxIdleSlots; i++ {
		i := i
		go func() {
			for range idleRows[i].ClickedCh {
				slotMu.RLock()
				a := idleActions[i]
				slotMu.RUnlock()
				emitFocus(a)
			}
		}()
	}
	for i := 0; i < maxNotifSubs; i++ {
		i := i
		go func() {
			for range notifRows[i].ClickedCh {
				slotMu.RLock()
				a := notifActions[i]
				slotMu.RUnlock()
				emitFocus(a)
			}
		}()
	}

	// Notifications root: opening it triggers a refresh so the submenu
	// reflects the latest log file content.
	go func() {
		for range notifRoot.ClickedCh {
			Refresh()
		}
	}()

	for {
		select {
		case <-updateItem.ClickedCh:
			emitFocus(ClickAction{Kind: ClickUpdateAvail})
		case <-openWatchfireItem.ClickedCh:
			emitFocus(ClickAction{Kind: ClickOpenWatchfire})
		case <-openDashboardItem.ClickedCh:
			emitFocus(ClickAction{Kind: ClickOpenDashboard})
		case <-quitItem.ClickedCh:
			emitFocus(ClickAction{Kind: ClickQuitWatchfire})
		}
	}
}

// Refresh signals that the menu should be rebuilt. Safe to call concurrently
// from any goroutine — the request is coalesced into a single rebuild via
// a buffered channel so a flurry of fsnotify events doesn't thrash the menu.
func Refresh() {
	if rebuildCh == nil {
		return
	}
	select {
	case rebuildCh <- struct{}{}:
	default:
	}
}

// UpdateAgents is kept as a compatibility shim for the daemon's existing
// agent-state subscription, which calls UpdateAgents on every state change.
// We ignore the snapshot (Refresh re-reads everything via DaemonState) but
// still drive the completion-detection path so OS notifications continue
// to fire on agent stop.
func UpdateAgents(agents []AgentInfo) {
	detectCompletions(previousActiveAgents, agents)
	newPrev := make(map[string]AgentInfo, len(agents))
	for _, a := range agents {
		newPrev[a.ProjectID] = a
	}
	previousActiveAgents = newPrev
	Refresh()
}

// processRebuilds drains rebuildCh, debounces aggressive call patterns, and
// applies a single rebuild per tick. Capped at ≤ 4 Hz (250 ms minimum
// interval between successive rebuilds).
func processRebuilds() {
	const debounce = 250 * time.Millisecond
	for range rebuildCh {
		time.Sleep(debounce)
		// Drain any pending signals that arrived during the debounce window.
	drain:
		for {
			select {
			case <-rebuildCh:
			default:
				break drain
			}
		}
		applyRebuild()
	}
}

// applyRebuild reads the current daemon snapshot and rewrites the tray slots.
// Only invoked from processRebuilds, so no concurrent Cocoa traffic.
func applyRebuild() {
	if state == nil {
		return
	}

	in := snapshotInputs()
	tree := BuildMenu(in)

	// Walk tree top-down. The systray slots were created in the same
	// order onReady built the tree, so we step through both lists in
	// lockstep, mapping nodes to slot pools by section.
	applyTreeToSlots(tree)

	// Tooltip + tray icon.
	activeCount := 0
	for _, p := range in.Projects {
		if p.Status == ProjectWorking {
			activeCount++
		}
	}
	systray.SetTooltip(formatTooltip(len(in.Projects), activeCount))
	if activeCount > 0 {
		setTrayIcon(iconActive)
	} else {
		setTrayIcon(iconIdle)
	}
}

// snapshotInputs builds a MenuInputs from the live daemon state.
func snapshotInputs() MenuInputs {
	projects := state.Projects()
	agents := state.ActiveAgents()
	failedCounts := state.FailedTaskCounts()
	if failedCounts == nil {
		failedCounts = map[string]int{}
	}
	logsDir := state.LogsDir()
	projectIDs := make([]string, 0, len(projects))
	projectNames := make(map[string]string, len(projects))
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ProjectID)
		projectNames[p.ProjectID] = p.ProjectName
	}
	notifs, todayCount := LoadRecentNotifications(logsDir, projectIDs, projectNames, time.Now())
	latestDigest := LoadLatestDigest(state.DigestsDir())

	agentByID := make(map[string]AgentInfo, len(agents))
	for _, a := range agents {
		agentByID[a.ProjectID] = a
	}

	infos := make([]ProjectMenuInfo, 0, len(projects))
	for _, p := range projects {
		status := ProjectIdle
		var taskTitle string
		var taskNumber int32
		if a, ok := agentByID[p.ProjectID]; ok && a.Mode != "chat" {
			status = ProjectWorking
			taskTitle = a.TaskTitle
			taskNumber = int32(a.TaskNumber)
		}
		if failedCounts[p.ProjectID] > 0 {
			// Failed wins over working — a busted run with a still-restarting
			// agent still reads as "needs attention" first.
			status = ProjectFailed
		}
		infos = append(infos, ProjectMenuInfo{
			ProjectID:         p.ProjectID,
			ProjectName:       p.ProjectName,
			ProjectColor:      p.ProjectColor,
			Status:            status,
			FailedCount:       failedCounts[p.ProjectID],
			CurrentTaskTitle:  taskTitle,
			CurrentTaskNumber: taskNumber,
		})
	}
	SortProjects(infos)

	updateAvail, updateVer := state.UpdateAvailable()
	return MenuInputs{
		DaemonRunning:           true,
		Projects:                infos,
		Notifications:           notifs,
		NotificationsTodayCount: todayCount,
		LatestDigest:            latestDigest,
		UpdateAvailable:         updateAvail,
		UpdateVersion:           updateVer,
	}
}

// applyTreeToSlots maps a BuildMenu output to the pre-allocated systray slots.
// The tree's section nodes are recognised by their Disabled+title prefix, so
// we don't need to plumb explicit kinds across the boundary.
func applyTreeToSlots(tree []MenuNode) {
	// Header: first node.
	if len(tree) > 0 {
		headerItem.SetTitle(tree[0].Title)
	}

	// Iterate through tree, dispatching to the right pool. Sections are
	// keyed by their leading marker (⚠ / ● / ○) so the layout stays
	// declarative.
	attentionRowsUsed := 0
	workingRowsUsed := 0
	idleRowsUsed := 0
	overflowText := ""
	notifRootTitle := "Notifications (0 today) ▸"
	notifRowsUsed := 0
	updateAvail := false
	updateAvailTitle := ""

	currentSection := ""

	hideAllAttention()
	hideAllWorking()
	hideAllIdle()
	hideAllNotifRows()

	for _, node := range tree {
		if node.Title == "---" {
			currentSection = ""
			continue
		}
		// Section header detection (unicode-prefixed, set by BuildMenu).
		switch {
		case node.Disabled && strings.HasPrefix(node.Title, "⚠"):
			currentSection = "attention"
			attentionHeader.SetTitle(node.Title)
			attentionHeader.Show()
			continue
		case node.Disabled && strings.HasPrefix(node.Title, "●"):
			currentSection = "working"
			workingHeader.SetTitle(node.Title)
			workingHeader.Show()
			continue
		case node.Disabled && strings.HasPrefix(node.Title, "○"):
			currentSection = "idle"
			idleHeader.SetTitle(node.Title)
			idleHeader.Show()
			continue
		}
		// Idle overflow row.
		if node.Disabled && strings.HasPrefix(node.Title, "…") {
			overflowText = node.Title
			continue
		}
		// Notifications root.
		if node.OnClick.Kind == ClickReloadNotifs {
			notifRootTitle = node.Title
			for i, child := range node.Children {
				if i >= maxNotifSubs {
					break
				}
				notifRows[i].SetTitle(child.Title)
				if child.Disabled {
					notifRows[i].Disable()
				} else {
					notifRows[i].Enable()
				}
				notifRows[i].Show()
				slotMu.Lock()
				notifActions[i] = child.OnClick
				slotMu.Unlock()
				notifRowsUsed++
			}
			continue
		}
		// Update Available.
		if node.OnClick.Kind == ClickUpdateAvail {
			updateAvail = true
			updateAvailTitle = node.Title
			continue
		}
		// "Open Watchfire" / "Open Dashboard…" / "Quit Watchfire" rows are
		// fixed slots; their titles never change so we don't rewrite them.
		if node.OnClick.Kind == ClickOpenWatchfire ||
			node.OnClick.Kind == ClickOpenDashboard ||
			node.OnClick.Kind == ClickQuitWatchfire {
			continue
		}

		// Project rows: dispatch by current section.
		switch currentSection {
		case "attention":
			if attentionRowsUsed < maxAttention {
				attentionRows[attentionRowsUsed].SetTitle(formatRow(node))
				attentionRows[attentionRowsUsed].Show()
				slotMu.Lock()
				attentionActions[attentionRowsUsed] = node.OnClick
				slotMu.Unlock()
				attentionRowsUsed++
			}
		case "working":
			if workingRowsUsed < maxWorking {
				workingRows[workingRowsUsed].SetTitle(formatRow(node))
				workingRows[workingRowsUsed].Show()
				slotMu.Lock()
				workingActions[workingRowsUsed] = node.OnClick
				slotMu.Unlock()
				workingRowsUsed++
			}
		case "idle":
			if idleRowsUsed < maxIdleSlots {
				idleRows[idleRowsUsed].SetTitle(formatRow(node))
				idleRows[idleRowsUsed].Show()
				slotMu.Lock()
				idleActions[idleRowsUsed] = node.OnClick
				slotMu.Unlock()
				idleRowsUsed++
			}
		}
	}

	// Hide section headers with zero rows.
	if attentionRowsUsed == 0 {
		attentionHeader.Hide()
	}
	if workingRowsUsed == 0 {
		workingHeader.Hide()
	}
	if idleRowsUsed == 0 {
		idleHeader.Hide()
	}

	// Idle overflow row.
	if overflowText != "" {
		idleOverflow.SetTitle(overflowText)
		idleOverflow.Show()
	} else {
		idleOverflow.Hide()
	}

	// Notifications.
	notifRoot.SetTitle(notifRootTitle)
	if notifRowsUsed == 0 {
		notifNone.Show()
	} else {
		notifNone.Hide()
	}

	// Update banner.
	if updateAvail {
		updateItem.SetTitle(updateAvailTitle)
		updateItem.Show()
	} else {
		updateItem.Hide()
	}
}

func hideAllAttention() {
	for i := 0; i < maxAttention; i++ {
		attentionRows[i].Hide()
	}
}

func hideAllWorking() {
	for i := 0; i < maxWorking; i++ {
		workingRows[i].Hide()
	}
}

func hideAllIdle() {
	for i := 0; i < maxIdleSlots; i++ {
		idleRows[i].Hide()
	}
}

func hideAllNotifRows() {
	for i := 0; i < maxNotifSubs; i++ {
		notifRows[i].Hide()
	}
}

// formatRow renders a project row. Title and subtitle are joined with " — "
// to read clearly inside the menu bar's monoline rows.
func formatRow(node MenuNode) string {
	if node.Subtitle == "" {
		return node.Title
	}
	return fmt.Sprintf("%s — %s", node.Title, node.Subtitle)
}

// detectCompletions fires OS notifications for agents that have stopped since
// the last update.
func detectCompletions(old map[string]AgentInfo, current []AgentInfo) {
	if old == nil {
		return
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, a := range current {
		currentSet[a.ProjectID] = struct{}{}
	}
	for pid, agent := range old {
		if _, stillRunning := currentSet[pid]; !stillRunning {
			notifyAgentDone(agent.ProjectName, agent.Mode)
		}
	}
}

func formatTooltip(projects, active int) string {
	return fmt.Sprintf("Watchfire — %d projects, %d active", projects, active)
}

// generateActiveIcon overlays a small orange dot (notification badge) on the
// bottom-right of the given PNG icon data.
func generateActiveIcon(baseData []byte) []byte {
	src, err := png.Decode(bytes.NewReader(baseData))
	if err != nil {
		log.Printf("Failed to decode tray icon for active variant: %v", err)
		return baseData
	}

	bounds := src.Bounds()
	img := image.NewRGBA(bounds)
	draw.Draw(img, bounds, src, bounds.Min, draw.Src)

	dotColor := color.RGBA{R: 255, G: 140, B: 0, A: 255}
	dotRadius := bounds.Dx() / 5
	if dotRadius < 3 {
		dotRadius = 3
	}
	cx := bounds.Max.X - dotRadius - 1
	cy := bounds.Max.Y - dotRadius - 1

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= dotRadius*dotRadius {
				img.Set(x, y, dotColor)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return baseData
	}
	return buf.Bytes()
}

// getColoredCircleIcon returns a PNG icon of a colored circle for the given
// hex color, cached.
func getColoredCircleIcon(hexColor string) []byte {
	if hexColor == "" {
		hexColor = "#808080"
	}
	iconCacheMu.RLock()
	if icon, ok := iconCache[hexColor]; ok {
		iconCacheMu.RUnlock()
		return icon
	}
	iconCacheMu.RUnlock()

	icon := generateColoredCircle(hexColor, 16)
	iconCacheMu.Lock()
	iconCache[hexColor] = icon
	iconCacheMu.Unlock()
	return icon
}

func generateColoredCircle(hexColor string, size int) []byte {
	r, g, b := parseHexColor(hexColor)
	fillColor := color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := size/2, size/2
	radius := size/2 - 1

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, fillColor)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func parseHexColor(hex string) (r, g, b int) {
	if hex == "" || hex[0] != '#' {
		return 128, 128, 128
	}
	hex = hex[1:]

	switch len(hex) {
	case 3:
		_, _ = fmt.Sscanf(hex, "%1x%1x%1x", &r, &g, &b)
		r *= 17
		g *= 17
		b *= 17
	case 6:
		_, _ = fmt.Sscanf(hex, "%2x%2x%2x", &r, &g, &b)
	default:
		return 128, 128, 128
	}
	return
}
