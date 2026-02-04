package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// VersionInfo holds version information.
type VersionInfo struct {
	Version  string `json:"version"`
	Codename string `json:"codename"`
}

var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		info := loadVersionInfo()
		fmt.Printf("Watchfire %s (%s)\n", info.Version, info.Codename)
		fmt.Printf("  OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  Go: %s\n", runtime.Version())
	},
}

func loadVersionInfo() VersionInfo {
	// Default version info
	info := VersionInfo{
		Version:  "0.1.0",
		Codename: "Ember",
	}

	// Try to load from version.json
	// First, try relative to executable
	execPath, err := os.Executable()
	if err == nil {
		versionPath := filepath.Join(filepath.Dir(execPath), "..", "version.json")
		if data, err := os.ReadFile(versionPath); err == nil {
			_ = json.Unmarshal(data, &info)
			return info
		}
	}

	// Try current directory
	if data, err := os.ReadFile("version.json"); err == nil {
		_ = json.Unmarshal(data, &info)
	}

	return info
}
