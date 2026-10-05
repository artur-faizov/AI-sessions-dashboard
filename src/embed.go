// Package webui embeds the dashboard frontend assets.
package webui

import (
	_ "embed"
)

//go:embed index.html
var indexHTML string

//go:embed i18n.js
var i18nJS string

// IndexHTML returns the raw embedded dashboard HTML.
func IndexHTML() string { return indexHTML }

// I18nJS returns the raw embedded i18n script source.
func I18nJS() string { return i18nJS }
