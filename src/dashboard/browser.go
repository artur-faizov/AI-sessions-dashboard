package dashboard

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url in the system browser; failures are non-fatal.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		if runtime.GOOS != "windows" {
			// best-effort: try rundll32 on windows-like environments is skipped here
		}
	}
}
