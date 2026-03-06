//go:build !darwin && !linux

package notify

import "log"

type noopNotifier struct{}

func init() {
	platform = &noopNotifier{}
}

func (n *noopNotifier) Send(title, message string, icon []byte) error {
	log.Printf("Notifications not supported on this platform: %s — %s", title, message)
	return nil
}
