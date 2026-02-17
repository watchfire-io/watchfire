package server

import (
	"log"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/updater"
)

// UpdateState holds the result of the latest update check.
type UpdateState struct {
	mu               sync.RWMutex
	Available        bool
	LatestVersion    string
	ReleaseURL       string
	LastChecked      time.Time
}

// startUpdateCheck runs an update check in a background goroutine based on settings.
func (s *Server) startUpdateCheck() {
	go func() {
		settings, err := config.LoadSettings()
		if err != nil {
			log.Printf("[update] Failed to load settings: %v", err)
			return
		}

		if !settings.Updates.CheckOnStartup {
			return
		}

		// Check frequency
		if settings.Updates.LastChecked != nil {
			since := time.Since(*settings.Updates.LastChecked)
			switch settings.Updates.CheckFrequency {
			case "daily":
				if since < 24*time.Hour {
					return
				}
			case "weekly":
				if since < 7*24*time.Hour {
					return
				}
			// "every_launch" — always check
			}
		}

		result, err := updater.CheckForUpdate()
		if err != nil {
			log.Printf("[update] Check failed: %v", err)
			return
		}

		// Update last_checked timestamp in settings
		now := time.Now()
		settings.Updates.LastChecked = &now
		if saveErr := config.SaveSettings(settings); saveErr != nil {
			log.Printf("[update] Failed to save last_checked: %v", saveErr)
		}

		s.updateState.mu.Lock()
		s.updateState.LastChecked = now
		if result.Available {
			s.updateState.Available = true
			s.updateState.LatestVersion = result.LatestVersion
			s.updateState.ReleaseURL = result.ReleaseURL
			log.Printf("[update] Update available: v%s → v%s", result.CurrentVersion, result.LatestVersion)
		} else {
			s.updateState.Available = false
			log.Printf("[update] Up to date (v%s)", result.CurrentVersion)
		}
		s.updateState.mu.Unlock()
	}()
}

// GetUpdateState returns the current update state.
func (s *Server) GetUpdateState() (available bool, version, url string) {
	s.updateState.mu.RLock()
	defer s.updateState.mu.RUnlock()
	return s.updateState.Available, s.updateState.LatestVersion, s.updateState.ReleaseURL
}
