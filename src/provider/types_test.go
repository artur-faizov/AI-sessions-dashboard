package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden files are byte dumps of the live API taken before the provider
// extraction (opencode fixture); the DTOs must keep serializing identically.
func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := golden(t, name); string(b) != want {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", name, b, want)
	}
}

func TestGoldenSessionTree(t *testing.T) {
	child := &SessionNode{
		ID: "s2", Parent: "s1", Title: "(untitled)", Agent: "general", Model: nil, Dir: "/proj",
		In: 10, Out: 2, Cr: 100, Cw: 5, Created: 2000, Updated: 3000, Dur: 1000,
		Beats: []int64{}, Children: []*SessionNode{},
		Subtree: &SubtreeSums{In: 10, Out: 2, Cr: 100, Cw: 5}, Source: "opencode",
	}
	root := &SessionNode{
		ID: "s1", Parent: nil, Title: "root title", Agent: "build",
		Model: map[string]any{"id": "m", "provider": "p"}, Dir: "/proj", MaxCtx: 320,
		In: 100, Out: 20, Reason: 3, Cr: 1000, Cw: 50, Created: 1000, Updated: 9000, Dur: 8000,
		Beats: []int64{}, Children: []*SessionNode{child},
		Subtree: &SubtreeSums{In: 110, Out: 22, Reason: 3, Cr: 1100, Cw: 55}, Source: "opencode",
	}
	other := &SessionNode{
		ID: "s3", Parent: nil, Title: "other <&> ü", Agent: nil, Model: "plain-model", Dir: "/p2",
		In: 1, Out: 1, Reason: 1, Cr: 1, Cw: 1, Created: 500, Updated: 600, Dur: 100,
		Beats: []int64{}, Children: []*SessionNode{},
		Subtree: &SubtreeSums{In: 1, Out: 1, Reason: 1, Cr: 1, Cw: 1}, Source: "opencode",
	}
	assertGolden(t, "tree.golden.json", []*SessionNode{root, other})
}

func TestGoldenSourceIsLastField(t *testing.T) {
	b, _ := json.Marshal(&SessionNode{Source: "x"})
	if !strings.HasSuffix(string(b), `"source":"x"}`) {
		t.Fatalf("source must be the last JSON field: %s", b)
	}
}

func TestGoldenCalls(t *testing.T) {
	calls := []CallRec{
		{Mid: "a1", Time: 1100, In: 10, Cr: 100, Cw: 7, Out: 5, Reason: 2, Ctx: 117, Hu: true},
		{Mid: "a2", Time: 1200, In: 20, Cr: 300, Cw: 0, Out: 6, Reason: 0, Ctx: 320, Hu: false},
	}
	assertGolden(t, "calls.golden.json", map[string]any{"calls": calls})
}

func TestGoldenTriggers(t *testing.T) {
	assertGolden(t, "trigger_text.golden.json", &TriggerResp{
		Case: "user", Time: int64(1000),
		Parts: []any{TriggerPartText{Kind: "text", Text: "hello <world> é"}},
	})
	in, cutI := Clip(map[string]any{"cmd": "ls <x>"})
	assertGolden(t, "trigger_tool.golden.json", &TriggerResp{
		Case: "assistant", Time: int64(1100),
		Parts: []any{TriggerPartTool{Kind: "tool", Tool: "bash", Status: "completed",
			Input: in, CutI: cutI, Output: "ok"}},
	})
}
