package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestNewUsesCopilotHome(t *testing.T) {
	t.Setenv("COPILOT_HOME", "/tmp/cp-home")
	if got := New().DataPath(); got != "/tmp/cp-home" {
		t.Fatalf("DataPath = %q", got)
	}
}

func TestNewDefaultsToHomeDotCopilot(t *testing.T) {
	t.Setenv("COPILOT_HOME", "")
	home, _ := os.UserHomeDir()
	if got := New().DataPath(); got != filepath.Join(home, ".copilot") {
		t.Fatalf("DataPath = %q", got)
	}
}

func TestAvailableMissingRootNamesPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nope")
	err := (&Provider{root: provider.NewRoot(root)}).Available()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "session-state")) {
		t.Fatalf("err = %v", err)
	}
}

func TestAvailableExistingRoot(t *testing.T) {
	p, _ := fixtureRoot(t)
	if err := p.Available(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilities(t *testing.T) {
	p, root := fixtureRoot(t)
	if p.Name() != "copilot" || !p.FatalWhenMissing() || !p.PathSwitchable() || p.DefaultPath() != root {
		t.Fatal("unexpected capabilities")
	}
	if err := p.SetDataPath("/x"); err == nil || p.DataPath() != root {
		t.Fatal("a root without session-state must be rejected")
	}
}
