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
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/watchfire-io/watchfire/internal/buildinfo"
)

const (
	maxAgentSlots   = 10
	maxProjectSlots = 10
	numStartModes   = 5 // generate-definition, generate-tasks, start-all, wildfire, open-gui
)

var (
	state    DaemonState
	onStart  func()
	onExit   func()
	portItem *systray.MenuItem

	// Serialized tray update channel (prevents concurrent Cocoa API calls)
	updateCh chan struct{}  // signal channel
	updateMu sync.Mutex    // protects latestAgents
	latestAgents []AgentInfo

	// Dynamic tray icons
	iconIdle   []byte
	iconActive []byte

	// Pre-allocated agent menu slots
	agentSlots   [maxAgentSlots]*systray.MenuItem
	agentOpenGUI [maxAgentSlots]*systray.MenuItem
	agentStop    [maxAgentSlots]*systray.MenuItem
	noAgentsItem *systray.MenuItem

	// Pre-allocated project menu slots (for idle projects)
	projectSlots       [maxProjectSlots]*systray.MenuItem
	projectGenDef      [maxProjectSlots]*systray.MenuItem
	projectGenTasks    [maxProjectSlots]*systray.MenuItem
	projectStartAll    [maxProjectSlots]*systray.MenuItem
	projectWildfire    [maxProjectSlots]*systray.MenuItem
	projectOpenGUIItem [maxProjectSlots]*systray.MenuItem

	updateItem  *systray.MenuItem
	openGUIItem *systray.MenuItem
	quitItem    *systray.MenuItem

	// Maps slot index → project ID for actions
	slotMu         sync.RWMutex
	slotProjects   [maxAgentSlots]string
	projSlotIDs    [maxProjectSlots]string
	previousAgents map[string]AgentInfo // keyed by projectID, for notification transitions

	// Cache generated icons by hex color
	iconCache   = make(map[string][]byte)
	iconCacheMu sync.RWMutex
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

func onReady() {
	// Generate tray icons
	iconIdle = iconData
	iconActive = generateActiveIcon(iconData)
	systray.SetTemplateIcon(iconIdle, iconIdle)
	systray.SetTooltip(formatTooltip(0, 0))

	// Header
	header := systray.AddMenuItem("Watchfire Daemon", "")
	header.Disable()

	// Version
	versionItem := systray.AddMenuItem(fmt.Sprintf("Version: %s", buildinfo.Version), "")
	versionItem.Disable()

	// Port
	portItem = systray.AddMenuItem("Starting...", "")
	portItem.Disable()

	systray.AddSeparator()

	// Pre-allocate agent slots (hidden by default)
	for i := 0; i < maxAgentSlots; i++ {
		agentSlots[i] = systray.AddMenuItem("", "")
		agentOpenGUI[i] = agentSlots[i].AddSubMenuItem("Open in GUI", "")
		agentStop[i] = agentSlots[i].AddSubMenuItem("Stop Agent", "")
		agentSlots[i].Hide()
	}

	// "No active agents" placeholder
	noAgentsItem = systray.AddMenuItem("No active agents", "")
	noAgentsItem.Disable()

	systray.AddSeparator()

	// Pre-allocate project slots (for idle projects with start actions)
	for i := 0; i < maxProjectSlots; i++ {
		projectSlots[i] = systray.AddMenuItem("", "")
		projectGenDef[i] = projectSlots[i].AddSubMenuItem("Generate Definition", "")
		projectGenTasks[i] = projectSlots[i].AddSubMenuItem("Plan Tasks", "")
		projectStartAll[i] = projectSlots[i].AddSubMenuItem("Run All", "")
		projectWildfire[i] = projectSlots[i].AddSubMenuItem("Wildfire", "")
		projectOpenGUIItem[i] = projectSlots[i].AddSubMenuItem("Open in GUI", "")
		projectSlots[i].Hide()
	}

	systray.AddSeparator()

	// Update notice (hidden until update is detected)
	updateItem = systray.AddMenuItem("Update Available", "A new version is available")
	updateItem.Hide()

	// Actions
	openGUIItem = systray.AddMenuItem("Open GUI", "Launch Watchfire GUI")
	quitItem = systray.AddMenuItem("Quit", "Shut down Watchfire daemon")

	// Initialize previous agents map
	previousAgents = make(map[string]AgentInfo)

	// Start serialized tray update processor
	updateCh = make(chan struct{}, 1)
	go processUpdates()

	// Start the daemon services
	if onStart != nil {
		onStart()
	}

	// Update port display now that server is started
	if state != nil {
		portItem.SetTitle(fmt.Sprintf("Running on port: %d", state.Port()))
		updateTooltip()
	}

	// Poll for update availability (check every 30s until found)
	go pollForUpdate()

	// Handle click events
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
	default:
		log.Printf("Open GUI not supported on %s", runtime.GOOS)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open GUI: %v", err)
	}
}

func handleClicks() {
	// Watch agent slot clicks via goroutines
	for i := 0; i < maxAgentSlots; i++ {
		i := i
		go func() {
			for range agentOpenGUI[i].ClickedCh {
				openGUI()
			}
		}()
		go func() {
			for range agentStop[i].ClickedCh {
				stopAgentAtSlot(i)
			}
		}()
	}

	// Watch project slot clicks via goroutines
	for i := 0; i < maxProjectSlots; i++ {
		i := i
		go func() {
			for range projectGenDef[i].ClickedCh {
				startProjectAtSlot(i, "generate-definition")
			}
		}()
		go func() {
			for range projectGenTasks[i].ClickedCh {
				startProjectAtSlot(i, "generate-tasks")
			}
		}()
		go func() {
			for range projectStartAll[i].ClickedCh {
				startProjectAtSlot(i, "start-all")
			}
		}()
		go func() {
			for range projectWildfire[i].ClickedCh {
				startProjectAtSlot(i, "wildfire")
			}
		}()
		go func() {
			for range projectOpenGUIItem[i].ClickedCh {
				openGUI()
			}
		}()
	}

	// Global menu items — block on these in the main goroutine
	for {
		select {
		case <-updateItem.ClickedCh:
			log.Println("Update requested from tray — run 'watchfire update'")
		case <-openGUIItem.ClickedCh:
			openGUI()
		case <-quitItem.ClickedCh:
			if state != nil {
				state.RequestShutdown()
			}
		}
	}
}

// stopAgentAtSlot stops the agent assigned to the given menu slot.
func stopAgentAtSlot(slot int) {
	slotMu.RLock()
	projectID := slotProjects[slot]
	slotMu.RUnlock()

	if projectID == "" || state == nil {
		return
	}

	log.Printf("Stopping agent for project %s (slot %d)", projectID, slot)
	go state.StopAgent(projectID)
}

// startProjectAtSlot starts an agent for the project in the given slot with the specified mode.
func startProjectAtSlot(slot int, mode string) {
	slotMu.RLock()
	projectID := projSlotIDs[slot]
	slotMu.RUnlock()

	if projectID == "" || state == nil {
		return
	}

	log.Printf("Starting %s for project %s (slot %d)", mode, projectID, slot)
	go state.StartAgent(projectID, mode)
}

// UpdateAgents sends an agent update to the serialized update processor.
// This is non-blocking and safe to call from any goroutine concurrently.
func UpdateAgents(agents []AgentInfo) {
	updateMu.Lock()
	latestAgents = agents
	updateMu.Unlock()

	// Signal that there's an update (non-blocking)
	select {
	case updateCh <- struct{}{}:
	default:
		// Signal already pending — processUpdates will pick up latestAgents
	}
}

// processUpdates drains updateCh and applies updates serially with debouncing.
// This ensures all systray/Cocoa API calls happen on a single goroutine.
func processUpdates() {
	for range updateCh {
		// Debounce: wait briefly for more updates before applying
		time.Sleep(50 * time.Millisecond)

		// Drain any pending signals
	drain:
		for {
			select {
			case <-updateCh:
			default:
				break drain
			}
		}

		// Read the latest agents snapshot
		updateMu.Lock()
		agents := latestAgents
		updateMu.Unlock()

		applyAgentUpdate(agents)
	}
}

// applyAgentUpdate performs the actual systray updates. Only called from processUpdates.
func applyAgentUpdate(agents []AgentInfo) {
	// Detect agent completions for notifications
	detectCompletions(previousAgents, agents)

	// Update previous agents map
	newPrevious := make(map[string]AgentInfo, len(agents))
	for _, a := range agents {
		newPrevious[a.ProjectID] = a
	}
	previousAgents = newPrevious

	// Update slot → project ID mapping
	slotMu.Lock()
	for i := 0; i < maxAgentSlots; i++ {
		slotProjects[i] = ""
	}
	for i, agent := range agents {
		if i >= maxAgentSlots {
			break
		}
		slotProjects[i] = agent.ProjectID
	}
	slotMu.Unlock()

	// Swap tray icon based on active agents
	if len(agents) > 0 {
		systray.SetTemplateIcon(iconActive, iconActive)
	} else {
		systray.SetTemplateIcon(iconIdle, iconIdle)
	}

	// Hide all agent slots first
	for i := 0; i < maxAgentSlots; i++ {
		agentSlots[i].Hide()
	}

	if len(agents) == 0 {
		noAgentsItem.Show()
	} else {
		noAgentsItem.Hide()
		for i, agent := range agents {
			if i >= maxAgentSlots {
				break
			}
			agentSlots[i].SetTitle(formatAgentTitle(agent))
			if icon := getColoredCircleIcon(agent.ProjectColor); icon != nil {
				agentSlots[i].SetIcon(icon)
			}
			agentSlots[i].Show()
		}
	}

	// Update project slots (idle projects only)
	updateProjectSlots(agents)

	updateTooltip()
}

// updateProjectSlots shows idle projects with start actions.
func updateProjectSlots(agents []AgentInfo) {
	if state == nil {
		return
	}

	// Build set of projects that have running agents
	agentSet := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		agentSet[a.ProjectID] = struct{}{}
	}

	projects := state.Projects()

	// Hide all project slots first
	for i := 0; i < maxProjectSlots; i++ {
		projectSlots[i].Hide()
	}

	slotMu.Lock()
	for i := 0; i < maxProjectSlots; i++ {
		projSlotIDs[i] = ""
	}

	slot := 0
	for _, proj := range projects {
		if slot >= maxProjectSlots {
			break
		}
		// Skip projects that already have an agent running
		if _, hasAgent := agentSet[proj.ProjectID]; hasAgent {
			continue
		}
		projSlotIDs[slot] = proj.ProjectID
		projectSlots[slot].SetTitle(fmt.Sprintf("\U0001F4C1 %s", proj.ProjectName))
		if icon := getColoredCircleIcon(proj.ProjectColor); icon != nil {
			projectSlots[slot].SetIcon(icon)
		}
		projectSlots[slot].Show()
		slot++
	}
	slotMu.Unlock()
}

// detectCompletions fires OS notifications for agents that have stopped since the last update.
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

func updateTooltip() {
	if state == nil {
		return
	}
	agents := state.ActiveAgents()
	activeCount := len(agents)
	systray.SetTooltip(formatTooltip(state.ProjectCount(), activeCount))
}

func formatTooltip(projects, active int) string {
	return fmt.Sprintf("Watchfire — %d projects, %d active", projects, active)
}

func formatAgentTitle(agent AgentInfo) string {
	switch agent.Mode {
	case "chat":
		return fmt.Sprintf("%s — Chat", agent.ProjectName)
	case "task":
		return fmt.Sprintf("%s — Task #%04d: %s", agent.ProjectName, agent.TaskNumber, agent.TaskTitle)
	case "start-all":
		return fmt.Sprintf("%s — Start All (Task #%04d)", agent.ProjectName, agent.TaskNumber)
	case "wildfire":
		return fmt.Sprintf("%s — Wildfire (Task #%04d)", agent.ProjectName, agent.TaskNumber)
	default:
		return agent.ProjectName
	}
}

// generateActiveIcon overlays a small orange dot (notification badge) on the bottom-right
// of the given PNG icon data.
func generateActiveIcon(baseData []byte) []byte {
	src, err := png.Decode(bytes.NewReader(baseData))
	if err != nil {
		log.Printf("Failed to decode tray icon for active variant: %v", err)
		return baseData
	}

	bounds := src.Bounds()
	img := image.NewRGBA(bounds)
	draw.Draw(img, bounds, src, bounds.Min, draw.Src)

	// Draw orange dot in the bottom-right corner
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

// getColoredCircleIcon returns a PNG icon of a colored circle for the given hex color.
// Icons are cached to avoid regenerating them.
func getColoredCircleIcon(hexColor string) []byte {
	if hexColor == "" {
		hexColor = "#808080" // Default gray
	}

	// Check cache first
	iconCacheMu.RLock()
	if icon, ok := iconCache[hexColor]; ok {
		iconCacheMu.RUnlock()
		return icon
	}
	iconCacheMu.RUnlock()

	// Generate new icon
	icon := generateColoredCircle(hexColor, 16)

	// Cache it
	iconCacheMu.Lock()
	iconCache[hexColor] = icon
	iconCacheMu.Unlock()

	return icon
}

// generateColoredCircle creates a PNG image of a filled circle with the given color.
func generateColoredCircle(hexColor string, size int) []byte {
	r, g, b := parseHexColor(hexColor)
	fillColor := color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}

	// Create image with transparent background
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Draw filled circle using midpoint algorithm
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

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// parseHexColor parses a hex color string (#RGB or #RRGGBB) into RGB values.
func parseHexColor(hex string) (r, g, b int) {
	if hex == "" || hex[0] != '#' {
		return 128, 128, 128 // Default gray
	}
	hex = hex[1:]

	switch len(hex) {
	case 3: // #RGB
		_, _ = fmt.Sscanf(hex, "%1x%1x%1x", &r, &g, &b)
		r *= 17 // Expand 0-15 to 0-255
		g *= 17
		b *= 17
	case 6: // #RRGGBB
		_, _ = fmt.Sscanf(hex, "%2x%2x%2x", &r, &g, &b)
	default:
		return 128, 128, 128 // Default gray
	}
	return
}
