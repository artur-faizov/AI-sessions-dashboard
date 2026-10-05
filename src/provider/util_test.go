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
