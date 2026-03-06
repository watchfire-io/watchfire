package notify

// Notifier sends desktop notifications.
type Notifier interface {
	Send(title, message string, icon []byte) error
}

var platform Notifier

// Send dispatches a notification using the platform-specific backend.
func Send(title, message string) error {
	return platform.Send(title, message, iconData)
}
