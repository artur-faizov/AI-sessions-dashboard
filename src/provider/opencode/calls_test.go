package opencode

import (
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestSessionPromptExtractsRealUserTexts(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	long := strings.Repeat("x", 30000)
	msgs := []struct {
		mid  string
		tc   int64
		data string
	}{
		{"u1", 1000, `{"role":"user","time":{"created":1000}}`},
		{"a1", 2000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"u2", 3000, `{"role":"user","time":{"created":3000}}`},
	}
	for _, m := range msgs {
		if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, m.mid, "s1", m.tc, m.tc, m.data); err != nil {
			t.Fatal(err)
		}
	}
	parts := []struct {
		pid, mid string
		tc       int64
		data     string
	}{
		{"p1", "u1", 1100, `{"type":"text","text":"fix the bug"}`},
		{"p2", "u1", 1200, `{"type":"text","synthetic":true,"text":"system note"}`},
		{"p3", "u1", 1300, `{"type":"text","text":"<system-reminder>noise</system-reminder>"}`},
		{"p4", "u1", 1400, `{"type":"text","text":"` + long + `"}`},
	}
	for _, p := range parts {
		if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, p.pid, p.mid, "s1", p.tc, p.tc, p.data); err != nil {
			t.Fatal(err)
		}
	}
	p := activateDB(t, path)
	_, poolRO := p.poolFor()
	texts, err := sessionPrompt(poolRO, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 || texts[0] != "fix the bug" {
		t.Fatalf("texts %v", texts)
	}
	if len(texts[1]) != 30000 {
		t.Fatalf("second text len %d", len(texts[1]))
	}
}

func TestSessionCallsTriggerKinds(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	msgs := []struct {
		mid  string
		tc   int64
		data string
	}{
		{"u1", 1000, `{"role":"user","time":{"created":1000}}`},
		{"a1", 2000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"q1", 3000, `{"role":"assistant"}`},
		{"a2", 4000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"q2", 5000, `{"role":"assistant"}`},
		{"a3", 6000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"u2", 7000, `{"role":"user","time":{"created":7000}}`},
		{"a4", 8000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
	}
	for _, m := range msgs {
		if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, m.mid, "s1", m.tc, m.tc, m.data); err != nil {
			t.Fatal(err)
		}
	}
	parts := []struct {
		pid, mid string
		tc       int64
		data     string
	}{
		{"p1", "u1", 1100, `{"type":"text","text":"hello"}`},
		{"p2", "q1", 3100, `{"type":"tool","tool":"question","state":{"status":"completed","input":{},"output":"User answered: yes"}}`},
		{"p3", "q2", 5100, `{"type":"tool","tool":"question","state":{"status":"error"}}`},
		{"p4", "u2", 7100, `{"type":"file","mime":"image/png","filename":"shot.png","url":"data:image/png;base64,AA=="}`},
	}
	for _, p := range parts {
		if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, p.pid, p.mid, "s1", p.tc, p.tc, p.data); err != nil {
			t.Fatal(err)
		}
	}
	p := activateDB(t, path)
	_, poolRO := p.poolFor()
	calls, err := sessionCalls(poolRO, "s1")
	if err != nil {
		t.Fatal(err)
	}
	wantHu := []provider.Trig{provider.TrigText, provider.TrigQuestion, provider.TrigNone, provider.TrigImage}
	if len(calls) != len(wantHu) {
		t.Fatalf("calls %+v", calls)
	}
	for i, c := range calls {
		if c.Hu != wantHu[i] {
			t.Fatalf("call %s hu=%q, want %q", c.Mid, c.Hu, wantHu[i])
		}
	}
}

func TestCallTriggerCasesAndClipping(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	long := strings.Repeat("y", 70000)
	msgs := []struct {
		mid  string
		tc   int64
		data string
	}{
		{"u1", 1000, `{"role":"user","time":{"created":1000}}`},
		{"a1", 2000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"a2", 3000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
	}
	for _, m := range msgs {
		if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, m.mid, "s1", m.tc, m.tc, m.data); err != nil {
			t.Fatal(err)
		}
	}
	parts := []struct {
		pid, mid string
		tc       int64
		data     string
	}{
		{"p1", "u1", 1100, `{"type":"text","text":"` + long + `"}`},
		{"p2", "u1", 1200, `{"type":"text","text":"after the long part"}`},
		{"p3", "a1", 2100, `{"type":"text","text":"assistant reply"}`},
	}
	for _, p := range parts {
		if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, p.pid, p.mid, "s1", p.tc, p.tc, p.data); err != nil {
			t.Fatal(err)
		}
	}
	p := activateDB(t, path)
	_, poolRO := p.poolFor()

	// a1 is preceded by the user message u1
	res, err := callTrigger(poolRO, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Case != "user" {
		t.Fatalf("case %q", res.Case)
	}
	first := res.Parts[0].(provider.TriggerPartText)
	if !first.Cut || len([]rune(first.Text)) != provider.TriggerMax {
		t.Fatalf("long text not clipped: cut=%v len=%d", first.Cut, len(first.Text))
	}

	// a2 is preceded by another assistant message
	res, err = callTrigger(poolRO, "a2")
	if err != nil {
		t.Fatal(err)
	}
	if res.Case != "assistant" {
		t.Fatalf("case %q", res.Case)
	}

	// unknown message → nil (→ 404 at the API layer)
	res, err = callTrigger(poolRO, "nope")
	if err != nil || res != nil {
		t.Fatalf("want nil,nil; got %+v %v", res, err)
	}

	// a user message id → nil (not an assistant message)
	res, err = callTrigger(poolRO, "u1")
	if err != nil || res != nil {
		t.Fatalf("want nil,nil for user msg; got %+v %v", res, err)
	}
}

func TestCallTriggerStartCase(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "a1", "s1", 100, 100,
		`{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`); err != nil {
		t.Fatal(err)
	}
	p := activateDB(t, path)
	_, poolRO := p.poolFor()
	res, err := callTrigger(poolRO, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Case != "start" || res.Time != nil || len(res.Parts) != 0 {
		t.Fatalf("start case wrong: %+v", res)
	}
}

func TestCallTriggerNonStringRenderedAsIndentedJSON(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "u1", "s1", 100, 100,
		`{"role":"user","time":{"created":100}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "a1", "s1", 200, 200,
		`{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, "p1", "u1", "s1", 150, 150,
		`{"type":"tool","tool":"bash","state":{"status":"ok","input":{"cmd":"ls"},"output":"files"}}`); err != nil {
		t.Fatal(err)
	}
	p := activateDB(t, path)
	_, poolRO := p.poolFor()
	res, err := callTrigger(poolRO, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Case != "user" {
		t.Fatalf("case %q", res.Case)
	}
	tool := res.Parts[0].(provider.TriggerPartTool)
	if tool.Kind != "tool" || tool.Tool != "bash" || tool.Status != "ok" {
		t.Fatalf("tool part wrong: %+v", tool)
	}
	if tool.Input != "{\n \"cmd\": \"ls\"\n}" {
		t.Fatalf("input not rendered as indented JSON: %q", tool.Input)
	}
	if tool.Output != "files" {
		t.Fatalf("string output kept raw: %q", tool.Output)
	}
	if tool.CutI || tool.CutO {
		t.Fatalf("unexpected cut flags: %+v", tool)
	}
}
