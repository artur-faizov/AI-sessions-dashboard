package copilot

import (
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
