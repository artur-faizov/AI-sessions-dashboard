package main

import (
	"os"

	dashboard "github.com/artur-faizov/token-monitor/src/dashboard"
	"github.com/artur-faizov/token-monitor/src/provider"
	"github.com/artur-faizov/token-monitor/src/provider/claude"
	"github.com/artur-faizov/token-monitor/src/provider/codex"
	"github.com/artur-faizov/token-monitor/src/provider/copilot"
	"github.com/artur-faizov/token-monitor/src/provider/folder"
	"github.com/artur-faizov/token-monitor/src/provider/opencode"
)

func main() {
	args := os.Args[1:]
	// Order is priority: the first provider is the default.
	providers := []provider.Provider{
		opencode.New(opencode.ParseDBFlag(args)),
		claude.New(),
		copilot.New(),
		folder.New(),
		codex.New(),
	}
	os.Exit(dashboard.Run(providers, args))
}
