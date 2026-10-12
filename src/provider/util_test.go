package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClipNilAndShortString(t *testing.T) {
	if s, cut := Clip(nil); s != "" || cut {
		t.Fatalf("nil: %q %v", s, cut)
	}
	if s, cut := Clip("abc"); s != "abc" || cut {
		t.Fatalf("short: %q %v", s, cut)
	}
}

func TestClipTruncatesAtTriggerMaxRunes(t *testing.T) {
	s, cut := Clip(strings.Repeat("ж", TriggerMax+5))
	if !cut || len([]rune(s)) != TriggerMax {
		t.Fatalf("cut=%v len=%d", cut, len([]rune(s)))
	}
}

func TestClipNonStringAsIndentedJSON(t *testing.T) {
	s, cut := Clip(map[string]any{"cmd": "ls <x>"})
	if cut || s != "{\n \"cmd\": \"ls <x>\"\n}" {
		t.Fatalf("got %q cut=%v", s, cut)
	}
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := ExpandUser("~"); got != home {
		t.Fatalf("~ -> %q", got)
	}
	if got := ExpandUser("~/a/b.db"); got != filepath.Join(home, "a", "b.db") {
		t.Fatalf("~/a/b.db -> %q", got)
	}
	if got := ExpandUser("/abs/~x"); got != "/abs/~x" {
		t.Fatalf("abs path changed: %q", got)
	}
}

func TestActiveWindowIs15Minutes(t *testing.T) {
	if ActiveWindowMS != 15*60*1000 {
		t.Fatalf("ActiveWindowMS = %d", ActiveWindowMS)
	}
}

func TestActiveDurExcludesHumanWaits(t *testing.T) {
	calls := []CallRec{
		{Time: 1000},
		{Time: 4000, Hu: TrigText},     // waited 3000
		{Time: 5000},                   // agent-driven: not a wait
		{Time: 9000, Hu: TrigApproval}, // waited 4000
	}
	if got := WaitBeforeMS(calls, 0); got != 0 {
		t.Fatalf("first call wait = %d", got)
	}
	if got := ActiveDur(10000, calls); got != 3000 {
		t.Fatalf("ActiveDur = %d", got)
	}
	if got := ActiveDur(2000, calls); got != 0 {
		t.Fatalf("must clamp at 0, got %d", got)
	}
}

func TestAsksInText(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"Which ticket should I use?", true},
		{"**Which ticket should I use?**\n1. A\n2. B\n\nPlan once you answer: a long plan.", true},
		{"Pick one (A or B?) please", true},
		{"Done. Tests pass.", false},
		{"Use `a ?: b` here.", false},
		{"```\nx = a ? b : c\n```\nDone.", false},
		{"See https://x.io/p?q=1 for details.", false},
		{"Match `foo?bar` with the regex.", false},
	} {
		if got := AsksInText([]string{c.text}); got != c.want {
			t.Errorf("AsksInText(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
