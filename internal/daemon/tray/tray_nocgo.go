//go:build !cgo

package tray

import (
	"log"
	"sync"

	"github.com/watchfire-io/watchfire/internal/daemon/focus"
)

var (
	onStart  func()
	onExit   func()
	done     = make(chan struct{})
	mu       sync.Mutex
	running  bool
	focusBus *focus.Bus
)

// Run starts the daemon without a system tray (CGO not available).
// It calls onStartFn to allow daemon initialization, then blocks until Quit().
func Run(s DaemonState, onStartFn, onExitFn func()) {
	_ = s
	onStart = onStartFn
	onExit = onExitFn
	focusBus = focus.New()

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
func UpdateAgents(_ []AgentInfo) {}

// Refresh is a no-op without a tray.
func Refresh() {}

// FocusBus returns the (always-empty in headless) focus bus. Returns a real
// Bus so the daemon's gRPC service can subscribe even without a tray; it
// simply never receives events.
func FocusBus() *focus.Bus {
	return focusBus
}
