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
		fmt.Printf("  %s %s %s\n",
			styleBrand.Render("Watchfire"),
			styleVersion.Render(buildinfo.Version),
			styleHint.Render("("+buildinfo.Codename+")"),
		)
		fmt.Printf("    %s  %s\n", styleLabel.Render("Commit"), styleValue.Render(buildinfo.CommitHash))
		fmt.Printf("    %s   %s\n", styleLabel.Render("Built"), styleValue.Render(buildinfo.BuildDate))
		fmt.Printf("    %s %s\n", styleLabel.Render("OS/Arch"), styleValue.Render(runtime.GOOS+"/"+runtime.GOARCH))
		fmt.Printf("    %s      %s\n", styleLabel.Render("Go"), styleValue.Render(runtime.Version()))
	},
}
