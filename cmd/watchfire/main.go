// Package main is the entry point for the watchfire CLI/TUI.
package main

import (
	"os"

	"github.com/watchfire-io/watchfire/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
