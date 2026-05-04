package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser shells out to the platform-native default-browser launcher
// to open the OAuth authorization URL. Failure is reported to the caller
// so the gRPC handler can surface the URL via a fallback (the user can
// copy-paste it into a browser manually).
//
// Override `BrowserOpener` from tests to capture the URL without
// touching the host.
var BrowserOpener = openDefaultBrowser

func openDefaultBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("oauth: unsupported platform %q for browser launch", runtime.GOOS)
	}
}
