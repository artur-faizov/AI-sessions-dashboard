package folder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// src is a minimal in-memory source provider for export tests.
type src struct {
	provider.Provider // unused methods panic on purpose
}

func (src) Name() string { return "opencode" }
func (src) SessionCalls(sid string) ([]provider.CallRec, error) {
	return []provider.CallRec{{Mid: "m-" + sid, Time: 5, In: 10}}, nil
}
func (src) SessionPrompt(sid string) ([]string, error) { return []string{"hello " + sid}, nil }
func (src) CallTrigger(mid string) (*provider.TriggerResp, error) {
	return &provider.TriggerResp{Case: "text", Parts: []any{}}, nil
}

func tree() *provider.SessionNode {
	kid := &provider.SessionNode{ID: "kid", Parent: "root", Title: "child", Updated: 7, Children: []*provider.SessionNode{}, Source: "opencode"}
	return &provider.SessionNode{ID: "root", Title: "orig", Updated: 9, Children: []*provider.SessionNode{kid}, Source: "opencode"}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := Collect(src{}, tree(), map[string]string{"root": "My name", "other": "x"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Names) != 1 || len(f.Sessions) != 2 || len(f.Triggers) != 2 {
		t.Fatalf("collect: names=%v sessions=%d triggers=%d", f.Names, len(f.Sessions), len(f.Triggers))
	}
	if _, err := Write(dir, f); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, f); !errors.Is(err, ErrExists) {
		t.Fatalf("second write: want ErrExists, got %v", err)
	}

	p := New()
	if err := p.Available(); err == nil {
		t.Fatal("no folder selected must be unavailable")
	}
	if err := p.SetDataPath(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing dir must be rejected")
	}
	if err := p.SetDataPath(dir); err != nil {
		t.Fatal(err)
	}
	nodes, err := p.BuildTree(0)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("tree: %v %v", nodes, err)
	}
	n := nodes[0]
	if n.Title != "My name" || n.Source != SourceName || n.Children[0].Source != SourceName {
		t.Fatalf("node: %+v", n)
	}
	calls, err := p.SessionCalls("kid")
	if err != nil || len(calls) != 1 || calls[0].Mid != "m-kid" {
		t.Fatalf("calls: %v %v", calls, err)
	}
	if pr, _ := p.SessionPrompt("root"); len(pr) != 1 || pr[0] != "hello root" {
		t.Fatalf("prompt: %v", pr)
	}
	if tr, _ := p.CallTrigger("m-root"); tr == nil || tr.Case != "text" {
		t.Fatalf("trigger: %v", tr)
	}
	if _, err := p.SessionCalls("missing"); err == nil {
		t.Fatal("unknown session must error")
	}
	if c := p.Candidates(); len(c) != 1 || c[0] != dir {
		t.Fatalf("candidates: %v", c)
	}
}

func TestIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"version":99}`), 0o644)
	os.WriteFile(filepath.Join(dir, "b.json"), []byte(`not json`), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(`x`), 0o644)
	p := New()
	if err := p.SetDataPath(dir); err != nil {
		t.Fatal(err)
	}
	nodes, err := p.BuildTree(0)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("want empty tree, got %v %v", nodes, err)
	}
}

// batchSrc resolves triggers per session; the per-call lookup must stay unused.
type batchSrc struct{ src }

func (batchSrc) CallTrigger(string) (*provider.TriggerResp, error) { panic("per-call lookup used") }
func (batchSrc) SessionTriggers(sid string, calls []provider.CallRec) (map[string]*provider.TriggerResp, error) {
	out := map[string]*provider.TriggerResp{}
	for _, c := range calls {
		out[c.Mid] = &provider.TriggerResp{Case: "batch", Parts: []any{}}
	}
	return out, nil
}

func TestCollectUsesBatchTriggers(t *testing.T) {
	f, err := Collect(batchSrc{}, tree(), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Triggers) != 2 || f.Triggers["m-root"].Case != "batch" {
		t.Fatalf("triggers: %v", f.Triggers)
	}
}
