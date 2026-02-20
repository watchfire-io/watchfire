package cmd

import (
	"fmt"
	"runtime"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
)

// Styles for daemon version output (matching CLI styles).
var (
	dStyleBrand   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "30", Dark: "45"})
	dStyleVersion = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "40"})
	dStyleLabel   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "240"})
	dStyleValue   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "15"})
	dStyleHint    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "240"})
)

var daemonVersionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("  %s %s %s\n",
			dStyleBrand.Render("watchfired"),
			dStyleVersion.Render(buildinfo.Version),
			dStyleHint.Render("("+buildinfo.Codename+")"),
		)
		fmt.Printf("    %s  %s\n", dStyleLabel.Render("Commit"), dStyleValue.Render(buildinfo.CommitHash))
		fmt.Printf("    %s   %s\n", dStyleLabel.Render("Built"), dStyleValue.Render(buildinfo.BuildDate))
		fmt.Printf("    %s %s\n", dStyleLabel.Render("OS/Arch"), dStyleValue.Render(runtime.GOOS+"/"+runtime.GOARCH))
		fmt.Printf("    %s      %s\n", dStyleLabel.Render("Go"), dStyleValue.Render(runtime.Version()))
	},
}

func init() {
	rootCmd.AddCommand(daemonVersionCmd)
}
