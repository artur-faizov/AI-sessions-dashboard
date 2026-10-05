package dashboard

import (
	"os"
	"strings"

	webui "github.com/artur-faizov/token-monitor/src"
)

// loadPage returns the dashboard HTML with the {{VERSION}} placeholder substituted.
func loadPage(dev bool) string {
	s := webui.IndexHTML()
	if dev {
		if b, err := os.ReadFile("src/index.html"); err == nil {
			s = string(b)
		}
	}
	return strings.ReplaceAll(s, "{{VERSION}}", Version)
}

// loadI18n returns the i18n script source.
func loadI18n(dev bool) string {
	if dev {
		if b, err := os.ReadFile("src/i18n.js"); err == nil {
			return string(b)
		}
	}
	return webui.I18nJS()
}
