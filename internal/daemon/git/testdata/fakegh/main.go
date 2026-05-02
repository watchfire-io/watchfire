// Fakegh is a stand-in for the real `gh` CLI used by pr_test.go. The test
// binary writes the argv it received to FAKEGH_LOG and chooses its exit
// behaviour from FAKEGH_MODE so a single binary can drive the happy path,
// auth-failure, and api-failure cases.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	mode := os.Getenv("FAKEGH_MODE") // "", "unauthenticated", "api_fail"

	if logPath := os.Getenv("FAKEGH_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(f, strings.Join(args, "\x1f"))
			_ = f.Close()
		}
	}

	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		if mode == "unauthenticated" {
			fmt.Fprintln(os.Stderr, "fakegh: not logged in")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "fakegh: authenticated")
		os.Exit(0)
	}

	if len(args) >= 1 && args[0] == "api" {
		if mode == "api_fail" {
			fmt.Fprintln(os.Stderr, "fakegh: HTTP 422 unprocessable")
			os.Exit(1)
		}
		resp := map[string]any{
			"html_url": "https://github.com/owner/repo/pull/42",
			"number":   42,
		}
		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "fakegh: unhandled args: %v\n", args)
	os.Exit(2)
}
