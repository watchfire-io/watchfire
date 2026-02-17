package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
)

var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Watchfire %s (%s)\n", buildinfo.Version, buildinfo.Codename)
		fmt.Printf("  Commit: %s\n", buildinfo.CommitHash)
		fmt.Printf("  Built:  %s\n", buildinfo.BuildDate)
		fmt.Printf("  OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  Go: %s\n", runtime.Version())
	},
}
