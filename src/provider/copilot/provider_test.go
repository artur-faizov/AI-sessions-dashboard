package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	err := (&Provider{root: root}).Available()
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
	p := &Provider{}
	if p.Name() != "copilot" || !p.FatalWhenMissing() || p.PathSwitchable() || p.Candidates() != nil {
		t.Fatal("unexpected capabilities")
	}
	if err := p.SetDataPath("/x"); err == nil {
		t.Fatal("SetDataPath should fail")
	}
}
