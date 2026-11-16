package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWorkspaceFull(t *testing.T) {
	ws, ok := readWorkspace(filepath.Join("testdata", "home", "session-state", "plain-0001", "workspace.yaml"))
	if !ok {
		t.Fatal("ok = false")
	}
	if ws.id != "plain-0001" || ws.cwd != "/work/proj" || ws.name != "Explain the parser" {
		t.Fatalf("ws = %+v", ws)
	}
	if ws.createdMS != parseMS("2026-01-01T10:00:00.000Z") || ws.updatedMS != parseMS("2026-01-01T10:00:01.000Z") {
		t.Fatalf("timestamps = %d %d", ws.createdMS, ws.updatedMS)
	}
}

func TestReadWorkspaceMissing(t *testing.T) {
	if _, ok := readWorkspace(filepath.Join(t.TempDir(), "workspace.yaml")); ok {
		t.Fatal("ok = true for missing file")
	}
}

func TestReadWorkspaceGarbled(t *testing.T) {
	if _, ok := readWorkspace(filepath.Join("testdata", "home", "session-state", "garbled-0004", "workspace.yaml")); ok {
		t.Fatal("ok = true for garbled file")
	}
}

func TestReadWorkspaceQuotedAndColons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.yaml")
	os.WriteFile(path, []byte("name: \"fix: the thing\"\ncwd: /a/b\nsummary: |\n  multi\n  line\ncreated_at: bad-date\n"), 0o644)
	ws, ok := readWorkspace(path)
	if !ok || ws.name != "fix: the thing" || ws.cwd != "/a/b" || ws.createdMS != 0 {
		t.Fatalf("ws = %+v ok=%v", ws, ok)
	}
}

func TestGarbledWorkspaceFallsBackToSessionStart(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	n := findNode(roots, "garbled-0004")
	if n == nil {
		t.Fatal("node missing")
	}
	if n.Dir != "/work/other" || n.Created != parseMS("2026-01-01T10:00:05.000Z") ||
		n.Updated != parseMS("2026-01-01T10:00:07.000Z") || n.Title != "(untitled)" || n.Model != "model-a" {
		t.Fatalf("node = %+v", n)
	}
}
