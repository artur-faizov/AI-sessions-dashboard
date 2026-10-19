package opencode

import (
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestFirstCallContext(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	for _, m := range [][]any{
		{"u1", 1000, `{"role":"user"}`},
		{"a1", 2000, `{"role":"assistant","tokens":{"input":12000,"output":5,"cache":{"read":500,"write":0}}}`},
		{"a2", 3000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":12600,"write":0}}}`},
	} {
		if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, m[0], "s1", m[1], m[1], m[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range [][]any{
		{"p1", "u1", `{"type":"text","text":"fix the bug please"}`},
		{"p2", "u1", `{"type":"text","synthetic":true,"text":"Called the Read tool with the following input"}`},
		{"p3", "u1", `{"type":"file","mime":"image/png","filename":"shot.png"}`},
	} {
		if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, p[0], p[1], "s1", 1000, 1000, p[2]); err != nil {
			t.Fatal(err)
		}
	}
	p := activateDB(t, path)
	r, err := p.CallContext("a1")
	if err != nil || r == nil {
		t.Fatalf("context %v %v", r, err)
	}
	if r.Ctx != 12500 || provider.SumTokens(r.Items) != 12500 {
		t.Fatalf("ctx %d sum %d", r.Ctx, provider.SumTokens(r.Items))
	}
	kinds := []string{}
	for _, it := range r.Items {
		kinds = append(kinds, it.Kind)
	}
	if len(kinds) != 4 || kinds[0] != "user" || kinds[1] != "reminders" || kinds[2] != "files" || kinds[3] != "hidden" {
		t.Fatalf("kinds %v", kinds)
	}
	if r, _ := p.CallContext("a2"); r != nil {
		t.Fatal("only the first call has a context composition")
	}
	if r, _ := p.CallContext("nope"); r != nil {
		t.Fatal("unknown call")
	}
}
