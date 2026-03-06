package tray

import (
	"fmt"
	"log"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
)

func notifyAgentDone(projectName, mode string) {
	title := "Watchfire"
	msg := fmt.Sprintf("%s — %s completed", projectName, mode)
	if err := notify.Send(title, msg); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}

func notifyAgentError(projectName, mode string) {
	title := "Watchfire"
	msg := fmt.Sprintf("%s — %s stopped", projectName, mode)
	if err := notify.Send(title, msg); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}
