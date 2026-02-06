package tray

import (
	"fmt"
	"log"
	"sync"

	"github.com/getlantern/systray"
)

const maxAgentSlots = 10

var (
	state    DaemonState
	onStart  func()
	onExit   func()
	portItem *systray.MenuItem

	// Pre-allocated agent menu slots
	agentSlots   [maxAgentSlots]*systray.MenuItem
	agentOpenGUI [maxAgentSlots]*systray.MenuItem
	agentStop    [maxAgentSlots]*systray.MenuItem
	noAgentsItem *systray.MenuItem
	openGUIItem  *systray.MenuItem
	quitItem     *systray.MenuItem

	// Maps slot index → project ID for stop actions
	slotMu       sync.RWMutex
	slotProjects [maxAgentSlots]string
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
	systray.SetTemplateIcon(iconData, iconData)
	systray.SetTooltip(formatTooltip(0, 0))

	// Header
	header := systray.AddMenuItem("Watchfire Daemon", "")
	header.Disable()

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

	// Actions
	openGUIItem = systray.AddMenuItem("Open GUI", "Launch Watchfire GUI")
	quitItem = systray.AddMenuItem("Quit", "Shut down Watchfire daemon")

	// Start the daemon services
	if onStart != nil {
		onStart()
	}

	// Update port display now that server is started
	if state != nil {
		portItem.SetTitle(fmt.Sprintf("Running on port: %d", state.Port()))
		updateTooltip()
	}

	// Handle click events
	go handleClicks()
}

func onQuit() {
	if onExit != nil {
		onExit()
	}
}

func handleClicks() {
	for {
		select {
		case <-openGUIItem.ClickedCh:
			log.Println("Open GUI: not yet implemented")

		case <-quitItem.ClickedCh:
			if state != nil {
				state.RequestShutdown()
			}

		// Agent slot clicks
		case <-agentOpenGUI[0].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[0].ClickedCh:
			stopAgentAtSlot(0)
		case <-agentOpenGUI[1].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[1].ClickedCh:
			stopAgentAtSlot(1)
		case <-agentOpenGUI[2].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[2].ClickedCh:
			stopAgentAtSlot(2)
		case <-agentOpenGUI[3].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[3].ClickedCh:
			stopAgentAtSlot(3)
		case <-agentOpenGUI[4].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[4].ClickedCh:
			stopAgentAtSlot(4)
		case <-agentOpenGUI[5].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[5].ClickedCh:
			stopAgentAtSlot(5)
		case <-agentOpenGUI[6].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[6].ClickedCh:
			stopAgentAtSlot(6)
		case <-agentOpenGUI[7].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[7].ClickedCh:
			stopAgentAtSlot(7)
		case <-agentOpenGUI[8].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[8].ClickedCh:
			stopAgentAtSlot(8)
		case <-agentOpenGUI[9].ClickedCh:
			log.Println("Open in GUI: not yet implemented")
		case <-agentStop[9].ClickedCh:
			stopAgentAtSlot(9)
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

// UpdateAgents refreshes the agent menu items and tooltip.
func UpdateAgents(agents []AgentInfo) {
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

	// Hide all slots first
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
			agentSlots[i].Show()
		}
	}

	updateTooltip()
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
		return fmt.Sprintf("● %s — Chat", agent.ProjectName)
	case "task":
		return fmt.Sprintf("● %s — Task #%04d: %s", agent.ProjectName, agent.TaskNumber, agent.TaskTitle)
	case "start-all":
		return fmt.Sprintf("● %s — Start All (Task #%04d)", agent.ProjectName, agent.TaskNumber)
	case "wildfire":
		return fmt.Sprintf("🔥 %s — Wildfire (Task #%04d)", agent.ProjectName, agent.TaskNumber)
	default:
		return fmt.Sprintf("● %s", agent.ProjectName)
	}
}
