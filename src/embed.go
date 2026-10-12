// Package webui embeds the dashboard frontend assets.
package webui

import (
	"embed"
)

//go:embed index.html
var indexHTML string

//go:embed i18n.js
var i18nJS string

//go:embed sounds/*.oga
var sounds embed.FS

// IndexHTML returns the raw embedded dashboard HTML.
func IndexHTML() string { return indexHTML }

// I18nJS returns the raw embedded i18n script source.
func I18nJS() string { return i18nJS }

// Sound returns the embedded alert sound sounds/<name>, false when unknown.
func Sound(name string) ([]byte, bool) {
	b, err := sounds.ReadFile("sounds/" + name)
	return b, err == nil
}
