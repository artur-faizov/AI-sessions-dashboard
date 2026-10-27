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

func userBlocksLine(uuid, ts, blocks string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":%q,"cwd":"/home/x/proj","sessionId":"s1","message":{"role":"user","content":[%s]}}`,
		uuid, ts, blocks)
}

func assistantLine(uuid, ts, model string, in, out, cr, cw int64, blocks string) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":%q,"cwd":"/home/x/proj","sessionId":"s1","message":{"role":"assistant","model":%q,"content":[%s],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		uuid, ts, model, blocks, in, cw, cr, out)
}

const textBlock = `{"type":"text","text":"hello there"}`

const imageBlock = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n"}}`

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

func TestClaudeSplitRequestKeepsFinalUsage(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	// One request streamed as three lines: early lines carry a partial
	// output count, the last one the final count.
	req := func(uuid, ts string, out int64) string {
		l := assistantLine(uuid, ts, "m1", 2, out, 100, 50, textBlock)
		return strings.Replace(l, `"type":"assistant",`, `"type":"assistant","requestId":"r1",`, 1)
	}
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.679Z", "hi"),
		req("a1", "2026-09-03T13:28:28.000Z", 8),
		req("a2", "2026-09-03T13:28:29.000Z", 8),
		req("a3", "2026-09-03T13:28:30.000Z", 3020),
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatal("session node missing")
	}
	if n.In != 2 || n.Out != 3020 || n.Cr != 100 || n.Cw != 50 {
		t.Fatalf("totals in=%d out=%d cr=%d cw=%d, want 2/3020/100/50", n.In, n.Out, n.Cr, n.Cw)
	}
	calls, err := p.SessionCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Mid != "a1" {
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
	if calls[0].Hu != provider.TrigText {
		t.Fatal("first call follows real user text: hu must be text")
	}
	if calls[1].Hu != provider.TrigNone {
		t.Fatal("call after assistant: hu must be none")
	}
	if calls[2].Hu != provider.TrigNone {
		t.Fatal("call after tool_result user message: hu must be none")
	}
	if calls[0].Ctx != 10 {
		t.Fatalf("ctx %d", calls[0].Ctx)
	}
}

func TestClaudeCallsTriggerKinds(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "typed message"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock),
		assistantLine("q1", "2026-09-03T13:28:29.000Z", "m1", 0, 0, 0, 0,
			`{"type":"tool_use","id":"call_q","name":"AskUserQuestion","input":{"question":"which?"}}`),
		userToolResultLine("u2", "2026-09-03T13:28:30.000Z", "call_q", "User answered: yes"),
		assistantLine("a2", "2026-09-03T13:28:31.000Z", "m1", 20, 6, 0, 0, textBlock),
		assistantLine("p1", "2026-09-03T13:28:32.000Z", "m1", 0, 0, 0, 0,
			`{"type":"tool_use","id":"call_p","name":"ExitPlanMode","input":{"plan":"do it"}}`),
		userToolResultLine("u3", "2026-09-03T13:28:33.000Z", "call_p", "plan approved"),
		assistantLine("a3", "2026-09-03T13:28:34.000Z", "m1", 15, 3, 0, 0, textBlock),
		assistantLine("b1", "2026-09-03T13:28:35.000Z", "m1", 0, 0, 0, 0,
			`{"type":"tool_use","id":"call_b","name":"Bash","input":{"command":"ls"}}`),
		userToolResultLine("u4", "2026-09-03T13:28:36.000Z", "call_b", "file list"),
		assistantLine("a4", "2026-09-03T13:28:37.000Z", "m1", 12, 2, 0, 0, textBlock),
		`{"type":"user","uuid":"u5","isMeta":true,"timestamp":"2026-09-03T13:28:38.000Z","message":{"role":"user","content":"meta noise"}}`,
		assistantLine("a5", "2026-09-03T13:28:39.000Z", "m1", 8, 1, 0, 0, textBlock),
		userBlocksLine("u6", "2026-09-03T13:28:40.000Z", imageBlock),
		assistantLine("a6", "2026-09-03T13:28:41.000Z", "m1", 9, 2, 0, 0, textBlock),
		userBlocksLine("u7", "2026-09-03T13:28:42.000Z", textBlock+","+imageBlock),
		assistantLine("a7", "2026-09-03T13:28:43.000Z", "m1", 7, 1, 0, 0, textBlock),
	)
	calls, err := p.SessionCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	wantMids := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	if len(calls) != len(wantMids) {
		t.Fatalf("calls %s", jdump(calls))
	}
	wantHu := []provider.Trig{
		provider.TrigText,     // a1 after plain-string user text
		provider.TrigQuestion, // a2 after AskUserQuestion tool_result
		provider.TrigApproval, // a3 after ExitPlanMode tool_result
		provider.TrigNone,     // a4 after Bash tool_result
		provider.TrigNone,     // a5 after isMeta line
		provider.TrigImage,    // a6 after image-only content array
		provider.TrigNone,     // a7 after text+image array (has a human text block)
	}
	for i, c := range calls {
		if c.Mid != wantMids[i] {
			t.Fatalf("call %d mid %s, want %s", i, c.Mid, wantMids[i])
		}
		if c.Hu != wantHu[i] {
			t.Fatalf("call %s hu=%q, want %q", c.Mid, c.Hu, wantHu[i])
		}
		// only a1 asks: its AskUserQuestion block sits on a later, usage-less line
		if c.Ask != (c.Mid == "a1") {
			t.Fatalf("call %s ask=%v", c.Mid, c.Ask)
		}
	}
}

func TestClaudeCallsAskInText(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	q := `{"type":"text","text":"Done. Which ticket should I use?"}`
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "start"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, q), // mid-turn: a tool follows
		assistantLine("t1", "2026-09-03T13:28:29.000Z", "m1", 0, 0, 0, 0,
			`{"type":"tool_use","id":"call_b","name":"Bash","input":{"command":"ls"}}`),
		userToolResultLine("u2", "2026-09-03T13:28:30.000Z", "call_b", "file list"),
		assistantLine("a2", "2026-09-03T13:28:31.000Z", "m1", 20, 6, 0, 0, textBlock),
		assistantLine("x2", "2026-09-03T13:28:32.000Z", "m1", 0, 0, 0, 0, q), // later line of a2's request
		userLine("u3", "2026-09-03T13:28:33.000Z", "PLN-1"),
		assistantLine("a3", "2026-09-03T13:28:34.000Z", "m1", 15, 3, 0, 0, textBlock), // turn end, no question
		userLine("u4", "2026-09-03T13:28:35.000Z", "thanks"),
		assistantLine("a4", "2026-09-03T13:28:36.000Z", "m1", 12, 2, 0, 0, q), // transcript end: still waiting
	)
	calls, err := p.SessionCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a1": false, "a2": true, "a3": false, "a4": true}
	if len(calls) != len(want) {
		t.Fatalf("calls %s", jdump(calls))
	}
	for _, c := range calls {
		if c.Ask != want[c.Mid] {
			t.Fatalf("call %s ask=%v", c.Mid, c.Ask)
		}
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

func TestClaudeTriggerSkipsSlashCommandExpansion(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z",
			"<command-message>propose</command-message>\n<command-name>/propose</command-name>\n<command-args>add a topic</command-args>"),
		`{"type":"user","uuid":"u2","isMeta":true,"timestamp":"2026-09-03T13:28:27.500Z","message":{"role":"user","content":[{"type":"text","text":"skill body"}]}}`,
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock),
	)
	res, err := p.CallTrigger("a1")
	if err != nil || res == nil {
		t.Fatalf("a1: %v %v", res, err)
	}
	if res.Case != "user" || len(res.Parts) != 1 {
		t.Fatalf("trigger wrong: %s", jdump(res))
	}
	tp, ok := res.Parts[0].(provider.TriggerPartText)
	if !ok || tp.Text != "/propose add a topic" {
		t.Fatalf("part wrong: %s", jdump(res.Parts))
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

func TestClaudeEmptySessionHidden(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"), "garbage line")
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if findNode(roots, "s1") != nil {
		t.Fatal("session without a user request must be hidden")
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
	if len(calls) != 1 || calls[0].Mid != "a9" || calls[0].Hu != provider.TrigText {
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
	sent := time.UnixMilli(now - 90_000).UTC().Format(time.RFC3339Nano)
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", sent, "hi"),
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
	// the beat runs from the prompt that sent the request to the reply
	if len(n.Beats) != 1 || n.Beats[0] != (provider.Beat{now - 90_000, now - 60_000}) {
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

func TestPromptTitleFallback(t *testing.T) {
	f := &claudeFile{messages: []*claudeMsg{
		{role: "assistant"},
		{role: "user", human: "\n  first line  \nsecond"},
	}}
	if got := f.promptTitle(); got != "first line" {
		t.Fatalf("got %q", got)
	}
	f.messages[1].human = strings.Repeat("x", 100)
	if got := f.promptTitle(); got != strings.Repeat("x", claudeTitleMaxRunes)+"…" {
		t.Fatalf("not clipped: %q", got)
	}
	if got := (&claudeFile{}).promptTitle(); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestClaudeSlashCommandWithArgsIsPrompt(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"),
		userLine("u2", "2026-09-03T13:28:28.000Z", "<command-message>openspec-explore</command-message>\n<command-name>/openspec-explore</command-name>\n<command-args>revise the change</command-args>"),
		userLine("u3", "2026-09-03T13:28:29.000Z", "Another Claude session sent a message:\n<agent-message from=\"a1\">report</agent-message>"),
		userLine("u4", "2026-09-03T13:28:30.000Z", "<task-notification>\n<task-id>a1</task-id>\n</task-notification>"),
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(roots, "s1")
	if n == nil {
		t.Fatal("session started by a slash command with args must be shown")
	}
	if n.Title != "/openspec-explore revise the change" {
		t.Fatalf("title %q", n.Title)
	}
	texts, _ := p.SessionPrompt("s1")
	if len(texts) != 1 {
		t.Fatalf("prompt texts: %s", jdump(texts))
	}
}

func TestClaudeAITitleKeepsSessionVisible(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"ai-title","aiTitle":"Named session"}`,
		userLine("u1", "2026-09-03T13:28:27.000Z", "<command-name>/clear</command-name>"),
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if n := findNode(roots, "s1"); n == nil || n.Title != "Named session" {
		t.Fatalf("ai-titled session: %s", jdump(n))
	}
}

func TestClaudeSubagentInheritsParentDir(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "main.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "explore"),
	)
	writeLines(t, filepath.Join(dir, "main", "subagents", "agent-x.jsonl"),
		`{"type":"user","uuid":"u9","timestamp":"2026-09-03T13:28:29.000Z","cwd":"/home/x/proj/sub/dir","message":{"role":"user","content":"sub prompt"}}`,
	)
	roots, err := p.BuildTree(0)
	if err != nil {
		t.Fatal(err)
	}
	a := findNode(roots, "agent-x")
	if a == nil || a.Dir != "/home/x/proj" {
		t.Fatalf("subagent dir: %s", jdump(a))
	}
}
