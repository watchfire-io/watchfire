//go:build linux

package notify

import (
	"os"

	"github.com/gen2brain/beeep"
)

type linuxNotifier struct{}

func init() {
	platform = &linuxNotifier{}
}

func (l *linuxNotifier) Send(title, message string, icon []byte) error {
	f, err := os.CreateTemp("", "watchfire-notify-*.png")
	if err != nil {
		return beeep.Notify(title, message, "")
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(icon); err != nil {
		return beeep.Notify(title, message, "")
	}
	if err := f.Close(); err != nil {
		return beeep.Notify(title, message, "")
	}
	return beeep.Notify(title, message, f.Name())
}
