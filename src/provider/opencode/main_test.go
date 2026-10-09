package opencode

import (
	"os"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestMain(m *testing.M) {
	provider.ProcDirs = func(string) map[string]int { return nil } // heuristic only
	os.Exit(m.Run())
}
