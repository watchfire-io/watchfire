package tray

import (
	"fmt"
	"log"
	"os"

	"github.com/gen2brain/beeep"
)

var notifyIconPath string

func init() {
	f, err := os.CreateTemp("", "watchfire-icon-*.png")
	if err != nil {
		return
	}
	if _, err := f.Write(iconNotifyData); err != nil {
		f.Close()
		return
	}
	f.Close()
	notifyIconPath = f.Name()
}

func notifyAgentDone(projectName, mode string) {
	title := "Watchfire"
	msg := fmt.Sprintf("%s — %s completed", projectName, mode)
	if err := beeep.Notify(title, msg, notifyIconPath); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}

func notifyAgentError(projectName, mode string) {
	title := "Watchfire"
	msg := fmt.Sprintf("%s — %s stopped", projectName, mode)
	if err := beeep.Notify(title, msg, notifyIconPath); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}
