// Package cmd implements the watchfired Cobra commands.
package cmd

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/server"
	"github.com/watchfire-io/watchfire/internal/daemon/tray"
	"github.com/watchfire-io/watchfire/internal/models"
)

var (
	foreground bool
	port       int
)

var rootCmd = &cobra.Command{
	Use:     "watchfired",
	Short:   "Watchfire daemon",
	Version: buildinfo.Version,
	RunE:    runDaemon,
}

func init() {
	rootCmd.Flags().BoolVar(&foreground, "foreground", false, "Run in foreground (for development)")
	rootCmd.Flags().IntVar(&port, "port", 0, "Port to listen on (0 for dynamic allocation)")
}

// Execute runs the daemon CLI.
// Before normal Cobra dispatch, it checks for the --sandbox-exec flag
// which is used by the Landlock helper to apply sandbox restrictions.
func Execute() {
	// Intercept --sandbox-exec before Cobra — this is a fast-path for the
	// Landlock sandbox helper which needs to apply restrictions then exec().
	if len(os.Args) > 2 && os.Args[1] == "--sandbox-exec" {
		runLandlockHelper(os.Args[2:])
		return // never reached on success (exec replaces process)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	log.SetPrefix("[watchfired] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Ensure global directory exists — the lockfile lives inside it.
	if err := config.EnsureGlobalDir(); err != nil {
		log.Fatalf("Failed to create global directory: %v", err)
	}

	// Acquire the OS-level singleton lock BEFORE any other startup work.
	// This is the authoritative "am I alone?" check — it closes the
	// TOCTOU window between the legacy IsDaemonRunning() check and the
	// post-waitForPort SaveDaemonInfo write, which let two daemons
	// race to bind dynamic ports + add tray icons during app launch.
	lockFile, err := config.AcquireDaemonLock()
	if err != nil {
		if errors.Is(err, config.ErrDaemonLockHeld) {
			// A duplicate spawn is the expected outcome of a startup
			// race, not an error — exit cleanly with status 0. Read
			// daemon.yaml as a friendly diagnostic so the user knows
			// which daemon is the live one.
			if _, info, ierr := config.IsDaemonRunning(); ierr == nil && info != nil {
				log.Printf("daemon already running on port %d (PID %d), exiting", info.Port, info.PID)
			} else {
				log.Println("daemon already running, exiting")
			}
			return nil
		}
		return fmt.Errorf("acquire daemon lock: %w", err)
	}

	if foreground {
		log.Println("Running in foreground mode (with system tray)")
	} else {
		log.Println("Running in background mode (with system tray)")
	}

	// Hold the lock for the full process lifetime — released only after
	// tray.Run returns (i.e. signal-driven shutdown completes).
	runWithTray(port, lockFile)

	return nil
}

// runWithTray runs the daemon with a system tray icon on the main goroutine.
// systray.Run must occupy the main goroutine on macOS (Cocoa requirement).
//
// lockFile is the singleton-daemon flock acquired in runDaemon — it is
// held for the lifetime of this call and released in onExit (after the
// gRPC server stops and daemon.yaml is removed).
func runWithTray(port int, lockFile *os.File) {
	var srv *server.Server

	onStart := func() {
		var err error
		srv, err = server.New(port)
		if err != nil {
			log.Fatalf("Failed to create server: %v", err)
		}

		log.Printf("Daemon starting on port %d (PID %d)", srv.Port(), os.Getpid())

		// Wire agent state changes to tray updates
		srv.AgentManager().SetOnChange(func() {
			trayState := server.NewTrayState(srv)
			tray.UpdateAgents(trayState.ActiveAgents())
		})

		// Start serving gRPC first
		go func() {
			if err := srv.Serve(); err != nil {
				log.Printf("Server error: %v", err)
				tray.Quit()
			}
		}()

		// Wait for the port to actually accept connections before writing daemon.yaml
		if err := waitForPort(srv.Port(), 5*time.Second); err != nil {
			log.Fatalf("Server failed to become ready: %v", err)
		}

		// NOW write daemon.yaml — clients can safely connect
		daemonInfo := models.NewDaemonInfo("localhost", srv.Port(), os.Getpid())
		if err := config.SaveDaemonInfo(daemonInfo); err != nil {
			log.Fatalf("Failed to write daemon info: %v", err)
		}

		log.Printf("Daemon ready on port %d (PID %d)", srv.Port(), os.Getpid())

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

		// Release the singleton lock last — once closed, a queued
		// duplicate spawn can succeed. Do NOT delete the lockfile;
		// flock release is tied to the file handle, not the path.
		if lockFile != nil {
			_ = lockFile.Close()
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

func (l *lazyDaemonState) StopAgent(projectID string) {
	if srv := l.getSrv(); srv != nil {
		server.NewTrayState(srv).StopAgent(projectID)
	}
}

func (l *lazyDaemonState) UpdateAvailable() (available bool, version string) {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).UpdateAvailable()
	}
	return false, ""
}

func (l *lazyDaemonState) Projects() []tray.ProjectInfo {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).Projects()
	}
	return nil
}

func (l *lazyDaemonState) StartAgent(projectID, mode string) {
	if srv := l.getSrv(); srv != nil {
		server.NewTrayState(srv).StartAgent(projectID, mode)
	}
}

func (l *lazyDaemonState) RequestShutdown() {
	if srv := l.getSrv(); srv != nil {
		server.NewTrayState(srv).RequestShutdown()
	}
}

func (l *lazyDaemonState) FailedTaskCounts() map[string]int {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).FailedTaskCounts()
	}
	return nil
}

func (l *lazyDaemonState) LogsDir() string {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).LogsDir()
	}
	return ""
}

func (l *lazyDaemonState) DigestsDir() string {
	if srv := l.getSrv(); srv != nil {
		return server.NewTrayState(srv).DigestsDir()
	}
	return ""
}

// waitForPort polls until a TCP connection to the given port succeeds or the timeout expires.
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("localhost:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("port %d not ready after %s", port, timeout)
}
