//go:build darwin

package notify

import "log"

// darwinNotifier is a placeholder — native macOS notifications via cgo are
// disabled for now to simplify cross-platform builds. Re-enable by restoring
// the cgo implementation and notify_darwin.m.
type darwinNotifier struct{}

func init() {
	platform = &darwinNotifier{}
}

func (d *darwinNotifier) Send(title, message string, icon []byte) error {
	log.Printf("Notification: %s — %s", title, message)
	return nil
}
