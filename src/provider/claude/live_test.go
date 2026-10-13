package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func writeLive(t *testing.T, p *Provider, status, waitingFor string) {
	t.Helper()
	writeLines(t, filepath.Join(p.liveDir(), "1.json"),
		fmt.Sprintf(`{"pid":%d,"sessionId":"s1","status":%q,"waitingFor":%q,"statusUpdatedAt":1}`,
			os.Getpid(), status, waitingFor))
}

func TestClaudeLiveState(t *testing.T) {
	const ask = `{"type":"tool_use","id":"call_q","name":"AskUserQuestion","input":{"question":"which?"}}`
	const bash = `{"type":"tool_use","id":"call_b","name":"Bash","input":{"command":"ls"}}`
	cases := []struct {
		name, status, waitingFor, block, want string
	}{
		{"busy", "busy", "", textBlock, provider.StateBusy},
		{"idle", "idle", "", textBlock, provider.StateIdle},
		{"question", "waiting", "permission prompt", ask, provider.StateAsk},
		{"permission", "waiting", "permission prompt", bash, provider.StatePerm},
		{"elicitation", "waiting", "input needed", textBlock, provider.StateAsk},
		{"own dialog", "waiting", "dialog open", textBlock, provider.StateIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, dir := claudeFixture(t, "-home-x-proj")
			writeLines(t, filepath.Join(dir, "s1.jsonl"),
				userLine("u1", "2026-09-03T13:28:27.000Z", "do it"),
				assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, c.block))
			writeLive(t, p, c.status, c.waitingFor)
			roots, err := p.BuildTree(claudeParseMS("2026-09-03T13:28:29.000Z"))
			if err != nil {
				t.Fatal(err)
			}
			if n := findNode(roots, "s1"); n == nil || n.State != c.want {
				t.Fatalf("state %q, want %q", n.State, c.want)
			}
		})
	}
}

func TestClaudeLiveStateNeedsActiveAndLiveFile(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "do it"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0, textBlock))
	roots, err := p.BuildTree(claudeParseMS("2026-09-03T13:28:29.000Z"))
	if err != nil {
		t.Fatal(err)
	}
	if n := findNode(roots, "s1"); n.State != "" {
		t.Fatalf("no live file: state %q, want empty", n.State)
	}
	writeLive(t, p, "busy", "")
	roots, err = p.BuildTree(claudeParseMS("2026-09-03T15:00:00.000Z"))
	if err != nil {
		t.Fatal(err)
	}
	if n := findNode(roots, "s1"); n.State != "" {
		t.Fatalf("inactive session: state %q, want empty", n.State)
	}
}

func TestClaudeRunningToolSegOnlyWhileLive(t *testing.T) {
	p, dir := claudeFixture(t, "-home-x-proj")
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		userLine("u1", "2026-09-03T13:28:27.000Z", "do it"),
		assistantLine("a1", "2026-09-03T13:28:28.000Z", "m1", 10, 5, 0, 0,
			`{"type":"tool_use","id":"call_r","name":"Read","input":{"path":"x"}}`),
		userToolResultLine("u2", "2026-09-03T13:28:29.000Z", "call_r", "body"),
		assistantLine("a2", "2026-09-03T13:28:30.000Z", "m1", 10, 5, 0, 0,
			`{"type":"tool_use","id":"call_b","name":"Bash","input":{"command":"sleep 60"}}`))
	segs, err := p.SessionSegments("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || segs[0].Name != "Read" || segs[0].Run {
		t.Fatalf("not live: segs = %+v", segs)
	}
	writeLive(t, p, "busy", "")
	segs, err = p.SessionSegments("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || !segs[1].Run || segs[1].Input != "sleep 60" || segs[1].Start != claudeParseMS("2026-09-03T13:28:30.000Z") {
		t.Fatalf("live: segs = %+v", segs)
	}
}
