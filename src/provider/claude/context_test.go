package claude

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestClaudeFirstCallContext(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	sys := strings.Repeat("s", 4000) // ~1000 tokens
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"attachment","attachment":{"type":"hook_success","hookName":"SessionStart:startup","content":"hook out"},"rendered":[{"content":"`+strings.Repeat("h", 400)+`"}]}`,
		userLine("u1", "2026-09-03T13:28:27.000Z", strings.Repeat("u", 400)),
		`{"type":"attachment","attachment":{"type":"instructions","files":[{"path":"/home/x/CLAUDE.md","content":"rules"}]},"rendered":[{"content":"`+strings.Repeat("i", 800)+`"}]}`,
		`{"type":"attachment","attachment":{"type":"prompt_snapshot","systemPrompt":["`+sys+`"]}}`,
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 400, 5, 0, 9600, textBlock),
		userLine("u2", "2026-09-03T13:28:29.000Z", "next"),
		assistantLine("a2", "2026-09-03T13:28:30.000Z", "m1", 10, 5, 10000, 0, textBlock),
	)
	if _, err := p.BuildTree(0); err != nil {
		t.Fatal(err)
	}
	r, err := p.CallContext("a1")
	if err != nil || r == nil {
		t.Fatalf("context %v %v", r, err)
	}
	if r.Ctx != 10000 || provider.SumTokens(r.Items) != 10000 {
		t.Fatalf("ctx %d sum %d", r.Ctx, provider.SumTokens(r.Items))
	}
	by := map[string]provider.ContextItem{}
	for _, it := range r.Items {
		by[it.Kind] = it
	}
	// the tail (hook 100 + user 100 + instructions 200) is scaled to input_tokens 400
	if by["hooks"].Tokens+by["user"].Tokens+by["instructions"].Tokens != 400 {
		t.Fatalf("tail %s", jdump(r.Items))
	}
	if by["instructions"].Label != "/home/x/CLAUDE.md" || by["hooks"].Label != "SessionStart:startup" {
		t.Fatalf("labels %s", jdump(r.Items))
	}
	if by["system"].Tokens != 1000 || by["hidden"].Tokens != 8600 || by["hidden"].Est != provider.EstInferred {
		t.Fatalf("prefix split %s", jdump(r.Items))
	}
	if r, _ := p.CallContext("a2"); r != nil {
		t.Fatal("only the first call has a context composition")
	}
}
