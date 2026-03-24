//go:build !cgo

package tray

import (
	"log"
	"sync"
)

var (
	onStart func()
	onExit  func()
	done    = make(chan struct{})
	mu      sync.Mutex
	running bool
)

// Run starts the daemon without a system tray (CGO not available).
// It calls onStartFn to allow daemon initialization, then blocks until Quit().
func Run(s DaemonState, onStartFn, onExitFn func()) {
	onStart = onStartFn
	onExit = onExitFn

	log.Println("[watchfire] System tray unavailable (built without CGO); daemon running headless")

	mu.Lock()
	running = true
	mu.Unlock()

	if onStart != nil {
		onStart()
	}

	<-done // Block until Quit()
}

// Quit signals the daemon to shut down.
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
	close(done)
}

// UpdateAgents is a no-op without a tray.
func UpdateAgents(agents []AgentInfo) {}
