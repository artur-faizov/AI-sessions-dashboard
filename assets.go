package main

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed src/index.html
var indexHTML string

//go:embed src/i18n.js
var i18nJS string

// loadPage returns the dashboard HTML with the {{VERSION}} placeholder substituted.
func loadPage(dev bool) string {
	s := indexHTML
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
	return i18nJS
}
