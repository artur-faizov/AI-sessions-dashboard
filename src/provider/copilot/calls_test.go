package copilot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestCallsOutputOnly(t *testing.T) {
	p, _ := fixtureRoot(t)
	calls, err := p.SessionCalls("plain-0001")
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.CallRec{
		{Mid: "m-p1", Time: parseMS("2026-01-01T10:00:03Z"), Out: 100, Hu: true},
		{Mid: "m-p2", Time: parseMS("2026-01-01T10:00:07Z"), Out: 50},
		{Mid: "m-p3", Time: parseMS("2026-01-01T10:00:09Z"), Out: 5},
		{Mid: "m-p4", Time: parseMS("2026-01-01T10:00:11Z"), Out: 8, Hu: true},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
}

func TestCallsSubagent(t *testing.T) {
	p, _ := fixtureRoot(t)
	calls, err := p.SessionCalls("sub-0002/call-B")
	if err != nil || len(calls) != 2 || calls[0].Mid != "m-b1" || calls[1].Mid != "m-b2" || calls[0].Out != 7 || calls[0].Hu {
		t.Fatalf("calls = %+v err=%v", calls, err)
	}
}

func TestCallsShutdownRootAreOutputOnly(t *testing.T) {
	p, _ := fixtureRoot(t)
	calls, _ := p.SessionCalls("shutdown-0003")
	if len(calls) != 2 || calls[0] != (provider.CallRec{Mid: "m-d1", Time: parseMS("2026-01-01T10:00:02Z"), Out: 100, Hu: true}) {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCallsFromUsageRows(t *testing.T) {
	p := fixtureWithDB(t)
	calls, _ := p.SessionCalls("rows-0005")
	want := []provider.CallRec{
		{Mid: "m-r1", Time: parseMS("2026-01-01T10:00:02Z"), In: 10, Cr: 100, Cw: 5, Out: 20, Reason: 2, Ctx: 115, Hu: true},
		{Mid: "m-r2", Time: parseMS("2026-01-01T10:00:03Z"), In: 12, Cr: 200, Out: 30, Ctx: 212},
	}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %+v", calls)
	}
	sub, _ := p.SessionCalls("rows-0005/call-R")
	if len(sub) != 1 || sub[0].Mid != "m-r3" || sub[0].In != 7 || sub[0].Out != 9 {
		t.Fatalf("sub calls = %+v", sub)
	}
}

func TestCallsAndPromptUnknownID(t *testing.T) {
	p, _ := fixtureRoot(t)
	for _, id := range []string{"missing", "sub-0002/nope"} {
		if _, err := p.SessionCalls(id); err == nil || err.Error() != "session not found: "+id {
			t.Errorf("SessionCalls(%q) err = %v", id, err)
		}
		if _, err := p.SessionPrompt(id); err == nil || err.Error() != "session not found: "+id {
			t.Errorf("SessionPrompt(%q) err = %v", id, err)
		}
	}
}

func TestPromptRoot(t *testing.T) {
	p, _ := fixtureRoot(t)
	got, err := p.SessionPrompt("plain-0001")
	if err != nil || strings.Join(got, "|") != "Explain the parser|Thanks" {
		t.Fatalf("prompt = %q err=%v", got, err)
	}
}

func TestPromptSubagent(t *testing.T) {
	p, _ := fixtureRoot(t)
	got, err := p.SessionPrompt("sub-0002/call-B")
	if err != nil || len(got) != 1 || got[0] != "Run go test" {
		t.Fatalf("prompt = %q err=%v", got, err)
	}
}

func TestTriggerText(t *testing.T) {
	p, _ := fixtureRoot(t)
	res, err := p.CallTrigger("m-p1")
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "trigger_text.golden.json", res)
}

func TestTriggerTool(t *testing.T) {
	p, _ := fixtureRoot(t)
	res, err := p.CallTrigger("m-b2")
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "trigger_tool.golden.json", res)
}

func TestTriggerStartAndUnknown(t *testing.T) {
	p, _ := fixtureRoot(t)
	res, _ := p.CallTrigger("m-b1")
	if res == nil || res.Case != "start" {
		t.Fatalf("subagent first call trigger = %+v", res)
	}
	res, _ = p.CallTrigger("m-p3")
	if res == nil || res.Case != "user" || res.Parts[0].(provider.TriggerPartText).Text != "skill context body" {
		t.Fatalf("injected user trigger = %+v", res)
	}
	if res, err := p.CallTrigger("no-such-mid"); res != nil || err != nil {
		t.Fatalf("unknown mid = %+v, %v", res, err)
	}
}

func TestCallsFromOTelSpans(t *testing.T) {
	p, _ := fixtureRoot(t)
	calls, err := p.SessionCalls("otel-0006")
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.CallRec{
		{Mid: "m-o1", Time: parseMS("2026-01-01T11:00:02Z"), In: 7, Cr: 23545, Cw: 448, Out: 86, Ctx: 24000, Hu: true},
		{Mid: "m-o2", Time: parseMS("2026-01-01T11:00:09Z"), In: 16, Cr: 24000, Cw: 600, Out: 120, Reason: 12, Ctx: 24616},
		// No matched span: output from outputTokens only, ctx 0.
		{Mid: "m-o3", Time: parseMS("2026-01-01T11:00:10Z"), Out: 30, Hu: true},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
}

func TestCallsOTelSubagentOutputOnly(t *testing.T) {
	p, _ := fixtureRoot(t)
	calls, _ := p.SessionCalls("otel-0006/call-O")
	want := []provider.CallRec{
		{Mid: "m-oa1", Time: parseMS("2026-01-01T11:00:05Z"), Out: 200},
		{Mid: "m-oa2", Time: parseMS("2026-01-01T11:00:06Z"), Out: 300},
	}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCallsOTelGrowingExport(t *testing.T) {
	p, root := fixtureRoot(t)
	calls, _ := p.SessionCalls("otel-0006")
	if calls[2].Ctx != 0 {
		t.Fatalf("m-o3 should be unmatched first: %+v", calls[2])
	}
	// Completes the partial last line, then adds a span for api-o3.
	appendFile(t, filepath.Join(root, "otel", "export.jsonl"), "\n"+
		`{"type":"span","spanId":"sp-o3","name":"chat","attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"otel-0006","gen_ai.response.id":"api-o3","gen_ai.usage.input_tokens":25000,"gen_ai.usage.output_tokens":31,"gen_ai.usage.cache_read_input_tokens":24616}}`+"\n")
	calls, _ = p.SessionCalls("otel-0006")
	if calls[2] != (provider.CallRec{Mid: "m-o3", Time: parseMS("2026-01-01T11:00:10Z"), In: 384, Cr: 24616, Out: 31, Ctx: 25000, Hu: true}) {
		t.Fatalf("m-o3 = %+v", calls[2])
	}
	roots, _ := p.BuildTree(fixtureNow)
	if n := findNode(roots, "otel-0006"); n.MaxCtx != 25000 {
		t.Fatalf("maxctx = %d", n.MaxCtx)
	}
}
