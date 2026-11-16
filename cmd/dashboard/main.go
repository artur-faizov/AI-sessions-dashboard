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
	// All are shown together, in this order. folder (exported sessions) stays
	// last: a session it holds is dropped when a live provider already has it.
	providers := []provider.Provider{
		opencode.New(opencode.ParseDBFlag(args)),
		claude.New(),
		copilot.New(),
		codex.New(),
		folder.New(),
	}
	os.Exit(dashboard.Run(providers, args))
}
