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

// shapeLine and requestLine build the stored-request lines of a transcript;
// withRef ties an assistant line to its request.
func shapeLine(hash, system string, tools ...string) string {
	return `{"type":"api-request-shape","shapeHash":"` + hash + `","shape":{"system":[{"type":"text","text":"` + system + `"}],"tools":[` + strings.Join(tools, ",") + `]}}`
}

func requestLine(id, hash string) string {
	return `{"type":"api-request","id":"` + id + `","shapeHash":"` + hash + `","tail":[]}`
}

func withRef(line, ref string) string {
	return strings.Replace(line, `{"type":"assistant",`, `{"type":"assistant","requestRef":"`+ref+`",`, 1)
}

func toolJSON(name string, descLen int) string {
	return `{"name":"` + name + `","description":"` + strings.Repeat("d", descLen) + `","input_schema":{"type":"object"}}`
}

func TestClaudeFirstCallContextShape(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	bash, mcp := toolJSON("Bash", 800), toolJSON("mcp__ide__open_file", 400)
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", strings.Repeat("u", 400)),
		`{"type":"attachment","attachment":{"type":"deferred_tools_delta"},"rendered":[{"content":"`+strings.Repeat("n", 400)+`"}]}`,
		`{"type":"attachment","attachment":{"type":"prompt_snapshot","systemPrompt":["stale snapshot"]}}`,
		// the first request failed and was retried with one more tool
		shapeLine("h1", strings.Repeat("s", 4000), bash),
		requestLine("r1", "h1"),
		shapeLine("h2", strings.Repeat("s", 2000), bash, mcp),
		requestLine("r2", "h2"),
		withRef(assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 2, 5, 600, 400, textBlock), "r2"),
	)
	if _, err := p.BuildTree(0); err != nil {
		t.Fatal(err)
	}
	r, err := p.CallContext("a1")
	if err != nil || r == nil {
		t.Fatalf("context %v %v", r, err)
	}
	if r.Ctx != 1002 || provider.SumTokens(r.Items) != 1002 {
		t.Fatalf("ctx %d sum %d", r.Ctx, provider.SumTokens(r.Items))
	}
	by := map[string]provider.ContextItem{}
	for _, it := range r.Items {
		if it.Kind == "hidden" || it.Est != provider.EstText {
			t.Fatalf("a stored request leaves nothing inferred: %s", jdump(r.Items))
		}
		by[it.Kind+"/"+it.Label] = it
	}
	if len(r.Items) != 5 || by["user/"].Tokens == 0 || by["tools/deferred_tools_delta"].Tokens == 0 {
		t.Fatalf("items %s", jdump(r.Items))
	}
	// the system prompt comes from the shape of the answered request (r2)
	if sys := by["system/"]; sys.Text != strings.Repeat("s", 2000) || sys.Tokens < 2*by["user/"].Tokens {
		t.Fatalf("system %s", jdump(sys))
	}
	if by["tools/Bash"].Text != bash || by["mcp/ide: open_file"].Text != mcp {
		t.Fatalf("tools %s", jdump(r.Items))
	}
	if by["tools/Bash"].Tokens <= by["mcp/ide: open_file"].Tokens {
		t.Fatalf("tool tokens follow the schema size: %s", jdump(r.Items))
	}
}

func TestClaudeFirstCallContextNoShape(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", strings.Repeat("u", 400)),
		`{"type":"attachment","attachment":{"type":"prompt_snapshot","systemPrompt":["`+strings.Repeat("s", 4000)+`"]}}`,
		// a shape no request of the first call refers to must not be used
		shapeLine("h1", "other", toolJSON("Bash", 800)),
		requestLine("r1", "h1"),
		withRef(assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 100, 5, 0, 9900, textBlock), "r9"),
	)
	if _, err := p.BuildTree(0); err != nil {
		t.Fatal(err)
	}
	r, err := p.CallContext("a1")
	if err != nil || r == nil {
		t.Fatalf("context %v %v", r, err)
	}
	by := map[string]provider.ContextItem{}
	for _, it := range r.Items {
		by[it.Kind] = it
	}
	if len(r.Items) != 3 || by["user"].Tokens != 100 || by["system"].Tokens != 1000 || by["hidden"].Tokens != 8900 {
		t.Fatalf("items %s", jdump(r.Items))
	}
}
