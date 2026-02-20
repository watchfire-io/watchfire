// Package main is the entry point for the watchfired daemon.
package main

import (
	cmd "github.com/watchfire-io/watchfire/internal/daemon/cmd"
)

func main() {
	cmd.Execute()
}
