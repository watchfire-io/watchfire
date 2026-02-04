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
	"github.com/watchfire-io/watchfire/internal/models"
)

func main() {
	// Parse flags
	foreground := flag.Bool("foreground", false, "Run in foreground (for development)")
	port := flag.Int("port", 0, "Port to listen on (0 for dynamic allocation)")
	flag.Parse()

	log.SetPrefix("[watchfired] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	if *foreground {
		log.Println("Running in foreground mode")
	}

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

	// Create and start the gRPC server
	srv, err := server.New(*port)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Write daemon info file
	daemonInfo := models.NewDaemonInfo("localhost", srv.Port(), os.Getpid())
	if err := config.SaveDaemonInfo(daemonInfo); err != nil {
		log.Fatalf("Failed to write daemon info: %v", err)
	}

	log.Printf("Daemon started on port %d (PID %d)", srv.Port(), os.Getpid())

	// Start serving in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	// Wait for shutdown signal or server error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("Server error: %v", err)
	}

	// Graceful shutdown
	srv.Stop()

	// Clean up daemon info file
	if err := config.RemoveDaemonInfo(); err != nil {
		log.Printf("Failed to remove daemon info: %v", err)
	}

	fmt.Println("Daemon stopped")
}
