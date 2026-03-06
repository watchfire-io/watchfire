//go:build darwin

package notify

/*
#cgo darwin LDFLAGS: -framework UserNotifications -framework Foundation
#include <stdlib.h>

extern void SendDarwinNotification(const char *title, const char *message, const char *iconPath);
*/
import "C"

import (
	"os"
	"unsafe"
)

type darwinNotifier struct{}

func init() {
	platform = &darwinNotifier{}
}

func (d *darwinNotifier) Send(title, message string, icon []byte) error {
	// Write icon to a temp file for the notification attachment.
	f, err := os.CreateTemp("", "watchfire-notify-*.png")
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(icon); err != nil {
		return err
	}
	f.Close()

	cTitle := C.CString(title)
	cMessage := C.CString(message)
	cIconPath := C.CString(f.Name())
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cMessage))
	defer C.free(unsafe.Pointer(cIconPath))

	C.SendDarwinNotification(cTitle, cMessage, cIconPath)
	return nil
}
