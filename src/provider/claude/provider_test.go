package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// --- fixture helpers ---

// claudeFixture creates a provider rooted at a temp dir; slug is one project
// slug dir, e.g. "-home-x-proj".
func claudeFixture(t *testing.T, slug string) (*Provider, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Provider{root: root}, dir
}

func writeLines(t *testing.T, path string, lines ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, l := range lines {
		switch v := l.(type) {
		case string:
			sb.WriteString(v)
		default:
			sb.WriteString(fmt.Sprint(v))
		}
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func userLine(uuid, ts, content string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":%q,"cwd":"/home/x/proj","sessionId":"s1","message":{"role":"user","content":%s}}`,
		uuid, ts, mustJSON(content))
}

func userToolResultLine(uuid, ts, toolUseID, content string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":%q,"cwd":"/home/x/proj","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`,
		uuid, ts, toolUseID, mustJSON(content))
}

func assistantLine(uuid, ts, model string, in, out, cr, cw int64, blocks string) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":%q,"cwd":"/home/x/proj","sessionId":"s1","message":{"role":"assistant","model":%q,"content":[%s],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		uuid, ts, model, blocks, in, cw, cr, out)
}

const textBlock = `{"type":"text","text":"hello there"}`

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func findNode(nodes []*provider.SessionNode, id string) *provider.SessionNode {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
		if found := findNode(n.Children, id); found != nil {
			return found
		}
	}
	return nil
}

// --- tests ---

func TestClaudeMalformedLineSkipped(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		"this is not json",
		userLine("u1", "2026-09-03T13:28:27.679Z", "fix the bug"),
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-03T13:28:28.000Z","cwd":"/home/x/proj","sessionId":"s1","message":{"role":"assistant","model":"m1","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
		`{"type":"unknown-thing","uuid":"z"}`,
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatalf("session node missing: %s", jdump(roots))
	}
	if n.In != 10 || n.Out != 5 {
		t.Fatalf("totals in=%d out=%d, want 10/5", n.In, n.Out)
	}
}

func TestClaudeDedupAndZeroUsageExcluded(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	dup := assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock)
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "hi"),
		dup,
		dup, // retry duplicate: same line uuid
		assistantLine("a2", "2026-09-03T13:28:29.000Z", "m1", 0, 0, 0, 0, textBlock),          // zero usage
		assistantLine("a3", "2026-09-03T13:28:30.000Z", "<synthetic>", 7, 0, 0, 0, textBlock), // synthetic
		assistantLine("a4", "2026-09-03T13:28:31.000Z", "m1", 100, 20, 30, 40, textBlock),
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatal("session node missing")
	}
	if n.In != 110 || n.Out != 25 || n.Cr != 30 || n.Cw != 40 {
		t.Fatalf("totals in=%d out=%d cr=%d cw=%d", n.In, n.Out, n.Cr, n.Cw)
	}
	if n.MaxCtx != 170 {
		t.Fatalf("maxctx %d, want 170", n.MaxCtx)
	}
	calls, err := p.SessionCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Mid != "a1" || calls[1].Mid != "a4" {
		t.Fatalf("calls %s", jdump(calls))
	}
}

func TestClaudeSubagentParenting(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "main.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "explore the repo"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0,
			`{"type":"tool_use","id":"call_1","name":"Task","input":{"query":"x"}}`),
		assistantLine("a2", "2026-09-03T13:28:40.000Z", "m1", 20, 6, 0, 0, textBlock),
	)
	// subagent with matching meta toolUseId
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-matched.jsonl"),
		userLine("u9", "2026-09-03T13:28:29.000Z", "sub prompt"),
		assistantLine("a9", "2026-09-03T13:28:30.000Z", "m1", 50, 8, 0, 0, textBlock),
	)
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-matched.meta.json"),
		`{"agentType":"Explore","description":"find files","toolUseId":"call_1"}`)
	// subagent with no meta: filesystem fallback parent
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-fallback.jsonl"),
		assistantLine("a8", "2026-09-03T13:28:31.000Z", "m1", 7, 2, 0, 0, textBlock),
	)

	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	main := findNode(roots, "main")
	if main == nil {
		t.Fatal("main node missing")
	}
	if len(main.Children) != 2 {
		t.Fatalf("want 2 children, got %s", jdump(main.Children))
	}
	matched := findNode(roots, "agent-matched")
	if matched == nil {
		t.Fatal("matched subagent missing")
	}
	if matched.Parent != "main" || matched.Agent != "Explore" || matched.Source != "claude" {
		t.Fatalf("matched child wrong: %s", jdump(matched))
	}
	if matched.Title != "find files" {
		t.Fatalf("title %q, want meta description", matched.Title)
	}
	if matched.In != 50 || matched.Out != 8 {
		t.Fatalf("subagent totals %d/%d", matched.In, matched.Out)
	}
	fallback := findNode(roots, "agent-fallback")
	if fallback == nil || fallback.Parent != "main" {
		t.Fatalf("fallback child wrong: %s", jdump(fallback))
	}
	// subtree sums include children
	if main.Subtree.In != 10+20+50+7 {
		t.Fatalf("subtree in %d", main.Subtree.In)
	}
}

func TestClaudeCallsHuFlag(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "real question"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock),
		assistantLine("a0", "2026-09-03T13:28:28.500Z", "m1", 3, 1, 0, 0,
			`{"type":"tool_use","id":"call_1","name":"Read","input":{"path":"x"}}`),
		userToolResultLine("u2", "2026-09-03T13:28:29.000Z", "call_1", "file contents"),
		assistantLine("a2", "2026-09-03T13:28:30.000Z", "m1", 20, 6, 0, 0, textBlock),
	)
	calls, err := p.SessionCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("want 3 calls, got %s", jdump(calls))
	}
	if !calls[0].Hu {
		t.Fatal("first call follows real user text: hu must be true")
	}
	if calls[1].Hu {
		t.Fatal("call after assistant: hu must be false")
	}
	if calls[2].Hu {
		t.Fatal("call after tool_result user message: hu must be false")
	}
	if calls[0].Ctx != 10 {
		t.Fatalf("ctx %d", calls[0].Ctx)
	}
}

func TestClaudeTriggerCases(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock), // first line: start
		userLine("u2", "2026-09-03T13:28:29.000Z", "go fix it"),
		assistantLine("a2", "2026-09-03T13:28:30.000Z", "m1", 20, 6, 0, 0, textBlock), // prev: user
		assistantLine("a3", "2026-09-03T13:28:31.000Z", "m1", 15, 3, 0, 0,
			`{"type":"tool_use","id":"call_9","name":"Read","input":{"path":"a.go"}}`), // prev: assistant
		assistantLine("a4", "2026-09-03T13:28:32.000Z", "m1", 5, 1, 0, 0, textBlock),
	)

	res, err := p.CallTrigger("a1")
	if err != nil || res == nil {
		t.Fatalf("a1: %v %v", res, err)
	}
	if res.Case != "start" || res.Time != nil || len(res.Parts) != 0 {
		t.Fatalf("start case wrong: %s", jdump(res))
	}

	res, err = p.CallTrigger("a2")
	if err != nil || res == nil {
		t.Fatalf("a2: %v %v", res, err)
	}
	if res.Case != "user" || res.Time == nil {
		t.Fatalf("user case wrong: %s", jdump(res))
	}
	if len(res.Parts) != 1 {
		t.Fatalf("user parts: %s", jdump(res))
	}
	tp, ok := res.Parts[0].(provider.TriggerPartText)
	if !ok || tp.Text != "go fix it" || tp.Kind != "text" {
		t.Fatalf("user part wrong: %s", jdump(res.Parts))
	}

	res, err = p.CallTrigger("a4")
	if err != nil || res == nil {
		t.Fatalf("a4: %v %v", res, err)
	}
	if res.Case != "assistant" || len(res.Parts) != 1 {
		t.Fatalf("assistant case wrong: %s", jdump(res))
	}
	tp2, ok := res.Parts[0].(provider.TriggerPartTool)
	if !ok || tp2.Tool != "Read" || tp2.Input == "" || tp2.Output != "" {
		t.Fatalf("assistant tool part wrong: %s", jdump(res.Parts))
	}

	res, err = p.CallTrigger("nope")
	if err != nil || res != nil {
		t.Fatalf("not-found must return nil,nil, got %v %v", res, err)
	}
}

func TestClaudeTriggerUserToolResultPart(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		assistantLine("a0", "2026-09-03T13:28:27.000Z", "m1", 9, 2, 0, 0,
			`{"type":"tool_use","id":"call_1","name":"Read","input":{"path":"x"}}`),
		userToolResultLine("u1", "2026-09-03T13:28:28.000Z", "call_1", "file contents here"),
		assistantLine("a1", "2026-09-03T13:28:29.000Z", "m1", 10, 5, 0, 0, textBlock),
	)
	res, err := p.CallTrigger("a1")
	if err != nil || res == nil {
		t.Fatalf("a1: %v %v", res, err)
	}
	if res.Case != "user" || len(res.Parts) != 1 {
		t.Fatalf("tool_result trigger wrong: %s", jdump(res))
	}
	tp, ok := res.Parts[0].(provider.TriggerPartTool)
	if !ok || tp.Tool != "Read" || tp.Output != "file contents here" || tp.Input != "" {
		t.Fatalf("tool part wrong: %s", jdump(res.Parts))
	}
}

func TestClaudePromptSkipsNoise(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "<command-name>/model</command-name>"),
		userLine("u2", "2026-09-03T13:28:28.000Z", `<system-reminder>noise</system-reminder>`),
		`{"type":"user","uuid":"u3","isMeta":true,"timestamp":"2026-09-03T13:28:28.500Z","message":{"role":"user","content":"meta noise"}}`,
		userLine("u4", "2026-09-03T13:28:29.000Z", "real prompt here"),
		userToolResultLine("u5", "2026-09-03T13:28:30.000Z", "call_1", "res"),
		userLine("u6", "2026-09-03T13:28:31.000Z", "second real text"),
	)
	texts, err := p.SessionPrompt("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 || texts[0] != "real prompt here" || texts[1] != "second real text" {
		t.Fatalf("prompt texts: %s", jdump(texts))
	}
}

func TestClaudeEmptySessionStillServed(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"), "garbage line")
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatal("empty session must still yield a node")
	}
	if n.Title != "(untitled)" || n.Created != 0 || n.Updated != 0 || n.Active {
		t.Fatalf("empty node wrong: %s", jdump(n))
	}
	if n.Dir != "-home-x-proj" {
		t.Fatalf("dir fallback %q", n.Dir)
	}
	if n.Beats == nil || n.Children == nil {
		t.Fatal("beats and children must be non-nil")
	}
	calls, err := p.SessionCalls("s1")
	if err != nil || len(calls) != 0 {
		t.Fatalf("calls for empty session: %v %v", calls, err)
	}
}

func TestClaudeMtimeCacheRescan(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	path := filepath.Join(dir, "s1.jsonl")
	writeLines(t, path,
		userLine("u1", "2026-09-03T13:28:27.679Z", "hi"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock),
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if n := findNode(roots, "s1"); n == nil || n.In != 10 {
		t.Fatalf("first scan: %s", jdump(roots))
	}
	writeLines(t, path,
		userLine("u1", "2026-09-03T13:28:27.679Z", "hi"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock),
		assistantLine("a2", "2026-09-03T13:28:29.000Z", "m1", 500, 50, 0, 0, textBlock),
	)
	// ensure mtime differs from the cached value
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	roots, err = p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil || n.In != 510 {
		t.Fatalf("second scan must see updated data: %s", jdump(n))
	}
}

func TestClaudeSubagentCallsServed(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "main.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "go"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0,
			`{"type":"tool_use","id":"call_1","name":"Task","input":{}}`),
	)
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-x.jsonl"),
		userLine("u9", "2026-09-03T13:28:29.000Z", "sub"),
		assistantLine("a9", "2026-09-03T13:28:30.000Z", "m1", 50, 8, 0, 0, textBlock),
	)
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-x.meta.json"),
		`{"agentType":"Explore","toolUseId":"call_1"}`)
	calls, err := p.SessionCalls("agent-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Mid != "a9" || !calls[0].Hu {
		t.Fatalf("subagent calls: %s", jdump(calls))
	}
	// trigger for a subagent mid must be found
	res, err := p.CallTrigger("a9")
	if err != nil || res == nil || res.Case != "user" {
		t.Fatalf("subagent trigger: %v %v", res, err)
	}
}

func TestClaudeSessionCallsNotFound(t *testing.T) {
	p, _ := claudeFixture(t, "-home-x-proj")
	if _, err := p.SessionCalls("nope"); err == nil {
		t.Fatal("unknown session must error")
	}
	if _, err := p.SessionPrompt("nope"); err == nil {
		t.Fatal("unknown session prompt must error")
	}
}

func TestClaudeActiveBeatsAndDur(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	now := time.Now().UnixMilli()
	ts := time.UnixMilli(now - 60_000).UTC().Format(time.RFC3339Nano)
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "hi"),
		assistantLine("a1", ts, "m1", 10, 5, 0, 0, textBlock),
	)
	roots, err := p.BuildTree(now)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatal("node missing")
	}
	if !n.Active {
		t.Fatal("session updated 1min ago must be active")
	}
	if len(n.Beats) != 1 {
		t.Fatalf("active session beats: %s", jdump(n.Beats))
	}
	if n.Dur <= 0 {
		t.Fatalf("dur %d", n.Dur)
	}
}

func jdump(o any) string {
	b, _ := json.Marshal(o)
	return string(b)
}

func TestClaudeProviderRootFromEnv(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude-projects")
	t.Setenv("CLAUDE_PROJECTS_DIR", root)
	p := New()
	if p.DataPath() != root {
		t.Fatalf("root %s, want %s", p.DataPath(), root)
	}
	if !p.FatalWhenMissing() {
		t.Fatal("claude must exit when its data dir is missing")
	}
	err := p.Available()
	if err == nil || !strings.Contains(err.Error(), root) {
		t.Fatalf("Available() must name %s, got %v", root, err)
	}
}

func TestClaudeProviderDefaultRoot(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", "")
	p := New()
	if !strings.HasSuffix(p.DataPath(), filepath.Join(".claude", "projects")) {
		t.Fatalf("default root %s, want ~/.claude/projects", p.DataPath())
	}
}

func TestClaudeProviderAvailableOnExistingDir(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	if err := New().Available(); err != nil {
		t.Fatalf("existing dir must be available, got %v", err)
	}
}

func TestClaudeProviderNotPathSwitchable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_DIR", root)
	p := New()
	if p.Name() != "claude" || p.PathSwitchable() || p.Candidates() != nil {
		t.Fatalf("name=%s switchable=%v candidates=%v", p.Name(), p.PathSwitchable(), p.Candidates())
	}
	err := p.SetDataPath(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("SetDataPath must be unsupported, got %v", err)
	}
	if p.DataPath() != root {
		t.Fatalf("data path changed to %s", p.DataPath())
	}
}
