//go:build linux

package tray

import (
	"log"
	"sync"
)

// Run starts the tray (no-op on Linux). Blocks until Quit is called.
func Run(s DaemonState, onStartFn, onExitFn func()) {
	onStart = onStartFn
	onExit = onExitFn

	log.Println("System tray not supported on Linux, running without tray")

	mu.Lock()
	running = true
	mu.Unlock()

	if onStart != nil {
		onStart()
	}

	select {}
}

// Quit signals the tray to exit.
func Quit() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	running = false

	if onExit != nil {
		onExit()
	}
}

// UpdateAgents is a no-op on Linux.
func UpdateAgents(agents []AgentInfo) {
}

// UpdateProjects is a no-op on Linux.
func UpdateProjects(projects []ProjectInfo) {
}

// SetUpdateAvailable is a no-op on Linux.
func SetUpdateAvailable(available bool, version string) {
}

var (
	onStart, onExit func()
	mu              sync.RWMutex
	running         bool
)
