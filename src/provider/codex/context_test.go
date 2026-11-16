package codex

import (
	"reflect"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func metaBase(id, base string) string {
	return row("session_meta", map[string]any{"id": id, "cwd": "/project/" + id, "base_instructions": map[string]any{"text": base}}, 1000)
}
func message(role string, blocks ...string) string {
	content := []any{}
	for _, b := range blocks {
		content = append(content, map[string]any{"type": "input_text", "text": b})
	}
	return row("response_item", map[string]any{"type": "message", "role": role, "content": content}, 1050)
}
func mustContext(t *testing.T, p *Provider, mid string) *provider.ContextResp {
	t.Helper()
	r, err := p.CallContext(mid)
	if err != nil || r == nil {
		t.Fatalf("context %v %v", r, err)
	}
	if r.Mid != mid || provider.SumTokens(r.Items) != r.Ctx {
		t.Fatalf("mid %q ctx %d sum %d", r.Mid, r.Ctx, provider.SumTokens(r.Items))
	}
	return r
}
func kindLabels(items []provider.ContextItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Kind+"|"+it.Label)
	}
	return out
}

func TestFirstCallContextRecordedRequest(t *testing.T) {
	u := usage{Input: 2000, Cached: 500, Output: 20, Total: 2020}
	two := usage{Input: 4100, Cached: 2500, Output: 40, Total: 4140}
	base := "You are a synthetic agent. " + strings.Repeat("Follow the rule. ", 20)
	tools := []any{
		map[string]any{"type": "namespace", "name": "functions", "tools": []any{map[string]any{"type": "function", "name": "exec", "description": "Run a command"}}},
		map[string]any{"type": "namespace", "namespace": "mcp__browser", "tools": []any{map[string]any{"type": "function", "name": "open"}}},
		map[string]any{"type": "function", "name": "plain"},
	}
	data := metaBase("s", base) + row("response_item", map[string]any{"type": "additional_tools", "tools": tools}, 1010)
	// The stored developer copy of the base instructions is the same prompt.
	data += message("developer", base)
	data += message("developer", "## Memory\n\nNotes", "<skills_instructions>\nA skill\n</skills_instructions>", "<permissions instructions>\nSandbox\n</permissions instructions>")
	data += message("user", "# AGENTS.md instructions for /project/s\n\n<INSTRUCTIONS>\nGuidance\n</INSTRUCTIONS>", "<environment_context>\n<cwd>/project/s</cwd>\n</environment_context>")
	// The client-side state snapshot is not sent to the model.
	data += row("world_state", map[string]any{"state": strings.Repeat("Guidance ", 2000)}, 1060) + context("model")
	data += row("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Fix the sample"}, map[string]any{"type": "input_image", "image_url": "synthetic-image"}}}, 1100)
	data += assistant("On it") + rich("s", "r1", u, &u, 1300) + user("Next") + assistant("Done") + rich("s", "r2", usage{Input: 2100, Cached: 2000, Output: 20, Total: 2120}, &two, 1500)
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": data})
	calls := mustCalls(t, p, "s")
	r := mustContext(t, p, calls[0].Mid)
	if r.Ctx != 2000 || r.Ctx != calls[0].Ctx {
		t.Fatalf("ctx %d, call %+v", r.Ctx, calls[0])
	}
	want := []string{"system|", "tools|functions.exec", "mcp|browser: open", "tools|plain", "instructions|memory", "skills|skills instructions", "reminders|permissions instructions", "instructions|/project/s", "reminders|environment context", "user|", "files|image"}
	if got := kindLabels(r.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("items %q", got)
	}
	if r.Items[0].Text != base || !strings.Contains(r.Items[1].Text, "Run a command") || r.Items[9].Text != "Fix the sample" {
		t.Fatalf("texts: %+v", r.Items)
	}
	if r, _ := p.CallContext(calls[1].Mid); r != nil {
		t.Fatal("only the first call has a context composition")
	}
	if r, _ := p.CallContext("absent"); r != nil {
		t.Fatal("unknown call")
	}
}

func TestFirstCallContextWithoutToolSchemas(t *testing.T) {
	u := usage{Input: 5000, Output: 20, Total: 5020}
	base := strings.Repeat("x", 400)
	data := metaBase("s", base) + message("developer", "<collaboration_mode>Default</collaboration_mode>") + user("Summarize this") + assistant("Summary") + rich("s", "r1", u, &u, 1300)
	// A subagent stores its own variant of the base instructions and gets its
	// task from the parent agent, with the payload encrypted.
	variant := strings.Repeat("y", 800)
	task := []any{map[string]any{"type": "input_text", "text": strings.Repeat("z", 40)}, map[string]any{"type": "encrypted_content", "encrypted_content": strings.Repeat("A", 1600)}}
	sub := metaBase("sub", base) + message("developer", variant) + row("response_item", map[string]any{"type": "agent_message", "author": "/root", "recipient": "/root/sub", "content": task}, 1100) + rich("sub", "r1", u, &u, 1300)
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": data, "sessions/sub.jsonl": sub})
	r := mustContext(t, p, "codex:s:r1")
	if got := kindLabels(r.Items); !reflect.DeepEqual(got, []string{"system|", "reminders|collaboration mode", "user|", "hidden|"}) {
		t.Fatalf("items %q", got)
	}
	if r.Items[0].Tokens != 100 || r.Items[0].Text != base || r.Items[3].Est != provider.EstInferred {
		t.Fatalf("unknown tool schemas must stay the hidden rest: %+v", r.Items)
	}
	r = mustContext(t, p, "codex:sub:r1")
	if got := kindLabels(r.Items); !reflect.DeepEqual(got, []string{"system|", "user|from /root", "hidden|"}) {
		t.Fatalf("subagent items %q", got)
	}
	if r.Items[0].Tokens != 200 || r.Items[0].Text != variant || r.Items[1].Tokens != 310 {
		t.Fatalf("subagent tokens: %+v", r.Items)
	}
}

func TestFirstCallContextSkipsInheritedHistory(t *testing.T) {
	u := usage{Input: 10, Output: 2, Total: 12}
	data := metaBase("s", "Base") + user("Parent prompt") + assistant("Parent reply") + rich("parent", "r1", u, &u, 1200) + user("Fork prompt") + rich("s", "r2", u, &u, 1300)
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": data})
	calls := mustCalls(t, p, "s")
	if len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
	if r, err := p.CallContext(calls[0].Mid); r != nil || err != nil {
		t.Fatalf("a fork's first call also holds its parent's history: %+v %v", r, err)
	}
}
