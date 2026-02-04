// Package main is the entry point for the watchfired daemon.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/server"
	"github.com/watchfire-io/watchfire/internal/daemon/tray"
	"github.com/watchfire-io/watchfire/internal/models"
)

func main() {
	// Parse flags
	foreground := flag.Bool("foreground", false, "Run in foreground (for development)")
	port := flag.Int("port", 0, "Port to listen on (0 for dynamic allocation)")
	flag.Parse()

	log.SetPrefix("[watchfired] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Ensure global directory exists
	if err := config.EnsureGlobalDir(); err != nil {
		log.Fatalf("Failed to create global directory: %v", err)
	}

	// Check if daemon is already running
	running, info, err := config.IsDaemonRunning()
	if err != nil {
		log.Fatalf("Failed to check daemon status: %v", err)
	}
	if running {
		log.Fatalf("Daemon already running on port %d (PID %d)", info.Port, info.PID)
	}

	if *foreground {
		log.Println("Running in foreground mode (no system tray)")
		runForeground(*port)
	} else {
		log.Println("Running in background mode (with system tray)")
		runWithTray(*port)
	}
}

// runForeground runs the daemon without a system tray, blocking on signals.
func runForeground(port int) {
	srv, err := server.New(port)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	daemonInfo := models.NewDaemonInfo("localhost", srv.Port(), os.Getpid())
	if err := config.SaveDaemonInfo(daemonInfo); err != nil {
		log.Fatalf("Failed to write daemon info: %v", err)
	}

	log.Printf("Daemon started on port %d (PID %d)", srv.Port(), os.Getpid())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("Server error: %v", err)
	}

	srv.Stop()

	if err := config.RemoveAgentState(); err != nil {
		log.Printf("Failed to remove agent state: %v", err)
	}
	if err := config.RemoveDaemonInfo(); err != nil {
		log.Printf("Failed to remove daemon info: %v", err)
	}

	fmt.Println("Daemon stopped")
}

// runWithTray runs the daemon with a system tray icon on the main goroutine.
// systray.Run must occupy the main goroutine on macOS (Cocoa requirement).
func runWithTray(port int) {
	var srv *server.Server

	onStart := func() {
		var err error
		srv, err = server.New(port)
		if err != nil {
			log.Fatalf("Failed to create server: %v", err)
		}

		daemonInfo := models.NewDaemonInfo("localhost", srv.Port(), os.Getpid())
		if err := config.SaveDaemonInfo(daemonInfo); err != nil {
			log.Fatalf("Failed to write daemon info: %v", err)
		}

		log.Printf("Daemon started on port %d (PID %d)", srv.Port(), os.Getpid())

		// Serve gRPC in background
		go func() {
			if err := srv.Serve(); err != nil {
				log.Printf("Server error: %v", err)
				tray.Quit()
			}
		}()

		// Handle OS signals — quit tray on SIGINT/SIGTERM
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			sig := <-sigCh
			log.Printf("Received signal %v, shutting down...", sig)
			tray.Quit()
		}()
	}

	onExit := func() {
		if srv != nil {
			srv.Stop()
		}

		if err := config.RemoveAgentState(); err != nil {
			log.Printf("Failed to remove agent state: %v", err)
		}
		if err := config.RemoveDaemonInfo(); err != nil {
			log.Printf("Failed to remove daemon info: %v", err)
		}

		fmt.Println("Daemon stopped")
	}

	// We need a DaemonState before the server is created, so we use a
	// lazy wrapper that defers to the real TrayState once the server exists.
	lazyState := &lazyDaemonState{getSrv: func() *server.Server { return srv }}

	// This blocks the main goroutine until tray exits.
	tray.Run(lazyState, onStart, onExit)
}

// lazyDaemonState wraps server.TrayState with lazy initialization.
// The server is nil at tray startup and created inside onStart.
type lazyDaemonState struct {
	getSrv func() *server.Server
}

func (l *lazyDaemonState) Port() int {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).Port()
	}
	return 0
}

func (l *lazyDaemonState) ProjectCount() int {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).ProjectCount()
	}
	return 0
}

func (l *lazyDaemonState) ActiveAgents() []tray.AgentInfo {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).ActiveAgents()
	}
	return nil
}

func (l *lazyDaemonState) RequestShutdown() {
	if srv := l.getSrv(); srv != nil {
		server.NewTrayState(srv).RequestShutdown()
	}
}
