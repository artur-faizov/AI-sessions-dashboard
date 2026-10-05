package copilot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTreeGolden(t *testing.T) {
	p := fixtureWithDB(t)
	roots, err := p.BuildTree(fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "tree.golden.json", roots)
}

func TestTreeNestedAndUnfinishedSubagents(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "sub-0002")
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d", len(root.Children))
	}
	a := findNode(roots, "sub-0002/call-A")
	if len(a.Children) != 1 || a.Children[0].ID != "sub-0002/call-B" || a.Children[0].Parent != "sub-0002/call-A" {
		t.Fatalf("nested child missing under call-A: %+v", a.Children)
	}
	if a.Agent != "explore" || a.Model != "model-b" || a.Title != "Explore code" || a.Dur != 11000 || a.Dir != "/work/proj" {
		t.Fatalf("call-A = %+v", a)
	}
	c := findNode(roots, "sub-0002/call-C")
	if c == nil || c.Parent != "sub-0002" || c.Active {
		t.Fatalf("unfinished call-C = %+v", c)
	}
	if root.Source != "copilot" || c.Source != "copilot" {
		t.Fatal("source must be copilot")
	}
}

func touchEvents(t *testing.T, root, sid string, at time.Time) {
	t.Helper()
	path := filepath.Join(root, "session-state", sid, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"assistant.message","id":"live","timestamp":"` + at.UTC().Format(time.RFC3339Nano) +
		`","data":{"messageId":"m-live","outputTokens":1}}` + "\n")
	f.Close()
}

func TestActiveNeedsLockAndRecentEvent(t *testing.T) {
	p, root := fixtureRoot(t)
	now := time.Now()
	touchEvents(t, root, "plain-0001", now.Add(-time.Minute))
	touchEvents(t, root, "sub-0002", now.Add(-time.Minute))

	roots, _ := p.BuildTree(now.UnixMilli())
	if findNode(roots, "plain-0001").Active {
		t.Fatal("recent session without lock must not be active")
	}
	lock := filepath.Join(root, "session-state", "plain-0001", "inuse.123.lock")
	os.WriteFile(lock, []byte("123"), 0o644)
	roots, _ = p.BuildTree(now.UnixMilli())
	n := findNode(roots, "plain-0001")
	if !n.Active || len(n.Beats) == 0 {
		t.Fatalf("locked recent session must be active with beats: %+v", n)
	}
	roots, _ = p.BuildTree(now.Add(11 * time.Minute).UnixMilli())
	if findNode(roots, "plain-0001").Active {
		t.Fatal("stale lock must not mark active")
	}
}
