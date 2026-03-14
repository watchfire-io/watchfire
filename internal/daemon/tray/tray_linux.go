//go:build linux && !cgo

// No-op tray implementation for Linux builds without CGO.
// When built with CGO_ENABLED=1 (the default for install-linux), tray.go is used instead
// and provides full AppIndicator support via github.com/getlantern/systray.

package tray

import (
	"log"
	"sync"
)

var (
	onStart, onExit func()
	mu              sync.Mutex
	running         bool
	done            = make(chan struct{})
)

// Run starts the tray. Without CGO this is a no-op that still calls onStartFn
// and blocks until Quit is called.
func Run(s DaemonState, onStartFn, onExitFn func()) {
	onStart = onStartFn
	onExit = onExitFn

	log.Println("[watchfire] system tray unavailable (built without CGO); run with CGO_ENABLED=1 to enable")

	mu.Lock()
	running = true
	mu.Unlock()

	if onStart != nil {
		onStart()
	}

	<-done
}

// Quit unblocks Run and calls the exit callback.
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

// UpdateAgents is a no-op without CGO.
func UpdateAgents(agents []AgentInfo) {}

