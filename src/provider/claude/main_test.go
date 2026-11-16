package claude

import (
	"os"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestMain(m *testing.M) {
	provider.ProcDirs = func(string) map[string]int { return nil } // heuristic only
	provider.ProcChildren = func(string) map[string][]int64 { return nil }
	os.Exit(m.Run())
}
