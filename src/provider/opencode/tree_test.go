package opencode

import (
	"database/sql"
	"testing"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func nowMS() int64 { return time.Now().UnixMilli() }

func seedSession(t *testing.T, pool *sql.DB, sid string, updated int64, parentID, directory any, archived any, createdShiftH int64) {
	t.Helper()
	now := nowMS()
	if directory == nil {
		directory = "/d"
	}
	if _, err := pool.Exec(
		`INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sid, parentID, "t-"+sid, "agent", nil, directory,
		0, 0, 0, 0, 0,
		now-createdShiftH*3600*1000, updated, archived,
	); err != nil {
		t.Fatal(err)
	}
}

func seedMsg(t *testing.T, pool *sql.DB, mid, sid string, tc int64, role string, usage bool) {
	t.Helper()
	d := map[string]any{"role": role}
	if usage {
		d["tokens"] = map[string]any{"input": 10, "output": 5,
			"cache": map[string]any{"read": 0, "write": 0}, "reasoning": 0}
		d["time"] = map[string]any{"created": tc, "completed": tc + 1000}
	}
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`,
		mid, sid, tc, tc, jdump(d)); err != nil {
		t.Fatal(err)
	}
}

func flatten(roots []*provider.SessionNode) map[string]*provider.SessionNode {
	out := map[string]*provider.SessionNode{}
	var walk func([]*provider.SessionNode)
	walk = func(ns []*provider.SessionNode) {
		for _, n := range ns {
			out[n.ID] = n
			walk(n.Children)
		}
	}
	walk(roots)
	return out
}

func TestActiveMarksSessionsUpdatedWithin15Min(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "fresh", now-5*60*1000, nil, nil, nil, 1)
	seedSession(t, pool, "stale", now-20*60*1000, nil, nil, nil, 1)
	p := activateDB(t, path)
	nodes := flatten(mustTree(t, p))
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
	if !nodes["fresh"].Active || nodes["stale"].Active {
		t.Fatalf("fresh=%v stale=%v", nodes["fresh"].Active, nodes["stale"].Active)
	}
}

func TestActiveBoundaryStrictlyInside15Min(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "edge", now-15*60*1000, nil, nil, nil, 1)
	seedSession(t, pool, "inside", now-14*60*1000, nil, nil, nil, 1)
	p := activateDB(t, path)
	nodes := flatten(mustTree(t, p))
	if nodes["edge"].Active {
		t.Fatal("edge must be inactive")
	}
	if !nodes["inside"].Active {
		t.Fatal("inside must be active")
	}
}

func TestArchivedAndDirlessStayOutOfTree(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "kept", now, nil, nil, nil, 1)
	seedSession(t, pool, "archived", now, nil, nil, now, 1)
	seedSession(t, pool, "nodir", now, nil, "", nil, 1)
	p := activateDB(t, path)
	nodes := flatten(mustTree(t, p))
	if len(nodes) != 1 || nodes["kept"] == nil {
		t.Fatalf("want only kept, got %v", keys(nodes))
	}
}

func TestBeatsListAICallTimesInsideWindow(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "fresh", now-5*60*1000, nil, nil, nil, 1)
	seedMsg(t, pool, "m1", "fresh", now-2*60*1000, "assistant", true)
	seedMsg(t, pool, "m2", "fresh", now-10*60*1000, "assistant", true)
	seedMsg(t, pool, "m3", "fresh", now-25*60*60*1000, "assistant", true)
	seedMsg(t, pool, "m4", "fresh", now-60*1000, "user", true)
	seedMsg(t, pool, "m5", "fresh", now-11*60*1000, "assistant", false) // unfinished, not last: abandoned
	p := activateDB(t, path)
	n := flatten(mustTree(t, p))["fresh"]
	want := []provider.Beat{{now - 10*60*1000, now - 10*60*1000 + 1000}, {now - 2*60*1000, now - 2*60*1000 + 1000}}
	if len(n.Beats) != 2 || n.Beats[0] != want[0] || n.Beats[1] != want[1] {
		t.Fatalf("beats %v, want %v (sorted)", n.Beats, want)
	}
	if !n.Active {
		t.Fatal("fresh must be active")
	}
}

func TestBeatsKeepOnlyTheLastUnfinishedCallOfAnActiveSession(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "live", now-60*1000, nil, nil, nil, 1)
	seedMsg(t, pool, "m1", "live", now-5*60*1000, "assistant", true)
	seedMsg(t, pool, "m2", "live", now-4*60*1000, "assistant", false) // abandoned
	seedMsg(t, pool, "m3", "live", now-30*1000, "assistant", false)   // in flight
	p := activateDB(t, path)
	n := flatten(mustTree(t, p))["live"]
	want := []provider.Beat{{now - 5*60*1000, now - 5*60*1000 + 1000}, {now - 30*1000, 0}}
	if len(n.Beats) != 2 || n.Beats[0] != want[0] || n.Beats[1] != want[1] {
		t.Fatalf("beats %v, want %v", n.Beats, want)
	}
}

func TestBeatsForRecentInactiveSessions(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "recent", now-20*60*1000, nil, nil, nil, 1)
	seedMsg(t, pool, "m6", "recent", now-25*60*1000, "assistant", true)
	seedSession(t, pool, "stale", now-25*60*60*1000, nil, nil, nil, 1)
	seedMsg(t, pool, "m7", "stale", now-25*60*60*1000, "assistant", true)
	p := activateDB(t, path)
	nodes := flatten(mustTree(t, p))
	if n := nodes["recent"]; n.Active || len(n.Beats) != 1 || n.Beats[0][0] != now-25*60*1000 {
		t.Fatalf("recent: active %v beats %v, want inactive with one beat", n.Active, n.Beats)
	}
	if n := nodes["stale"]; len(n.Beats) != 0 {
		t.Fatalf("stale beats %v, want empty", n.Beats)
	}
}

func TestSubtreeSumsOverDescendants(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	now := nowMS()
	seedSession(t, pool, "parent", now-5*60*1000, nil, nil, nil, 1)
	seedSession(t, pool, "child", now-4*60*1000, "parent", nil, nil, 2)
	if _, err := pool.Exec(`UPDATE session SET tokens_input=100, tokens_output=10,
		tokens_reasoning=1, tokens_cache_read=1000, tokens_cache_write=200 WHERE id='parent'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`UPDATE session SET tokens_input=50, tokens_output=5,
		tokens_reasoning=0, tokens_cache_read=500, tokens_cache_write=0 WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	p := activateDB(t, path)
	roots := mustTree(t, p)
	if len(roots) != 1 || roots[0].ID != "parent" {
		t.Fatalf("parent must be sole root, got %v", keys(flatten(roots)))
	}
	root := roots[0]
	if len(root.Children) != 1 || root.Children[0].ID != "child" {
		t.Fatal("child not nested")
	}
	st := root.Subtree
	if st.In != 150 || st.Out != 15 || st.Reason != 1 || st.Cr != 1500 || st.Cw != 200 {
		t.Fatalf("subtree sums wrong: %+v", st)
	}
}

func TestHuFlagsCallsTriggeredByHumanInput(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	rows := []struct {
		mid  string
		tc   int64
		data string
	}{
		{"u1", 1000, `{"role":"user","time":{"created":1000}}`},
		{"a1", 2000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"a2", 3000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"u2", 4000, `{"role":"user","time":{"created":4000}}`},
		{"a3", 5000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"u3", 6000, `{"role":"user","time":{"created":6000}}`},
		{"a4", 7000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
		{"u4", 8000, `{"role":"user","time":{"created":8000}}`},
		{"a5", 9000, `{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":0,"write":0},"reasoning":0}}`},
	}
	for _, r := range rows {
		if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`,
			r.mid, "s1", r.tc, r.tc, r.data); err != nil {
			t.Fatal(err)
		}
	}
	parts := []struct {
		pid, mid string
		tc       int64
		data     string
	}{
		{"p1", "u1", 1000, `{"type":"text","text":"fix the bug"}`},
		{"p2", "u2", 4000, `{"type":"text","synthetic":true,"text":"Called the Read tool with input: ..."}`},
		{"p3", "u2", 4001, `{"type":"text","text":"<system-reminder>user opened a file</system-reminder>"}`},
		{"p4", "u3", 6000, `{"type":"text","text":"thanks"}`},
	}
	for _, p := range parts {
		if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`,
			p.pid, p.mid, "s1", p.tc, p.tc, p.data); err != nil {
			t.Fatal(err)
		}
	}
	p := activateDB(t, path)
	_, pool2 := p.poolFor()
	calls, err := sessionCalls(pool2, "s1")
	if err != nil {
		t.Fatal(err)
	}
	var mids []string
	for _, c := range calls {
		mids = append(mids, c.Mid)
	}
	wantMids := []string{"a1", "a2", "a3", "a4", "a5"}
	if len(mids) != len(wantMids) {
		t.Fatalf("mids %v", mids)
	}
	for i := range wantMids {
		if mids[i] != wantMids[i] {
			t.Fatalf("mids %v, want %v", mids, wantMids)
		}
	}
	wantHu := []provider.Trig{provider.TrigText, provider.TrigNone, provider.TrigNone, provider.TrigText, provider.TrigNone}
	for i, c := range calls {
		if c.Hu != wantHu[i] {
			t.Fatalf("call %s hu=%q, want %q", c.Mid, c.Hu, wantHu[i])
		}
	}
}

func TestCallsKeepTokenFields(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	seedMsg(t, pool, "u1", "s1", 1000, "user", true)
	seedMsg(t, pool, "a1", "s1", 2000, "assistant", true)
	p := activateDB(t, path)
	_, pool2 := p.poolFor()
	calls, err := sessionCalls(pool2, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.In != 10 || c.Out != 5 || c.Ctx != 10 {
		t.Fatalf("token fields wrong: %+v", c)
	}
}

// helpers

func fixturePool(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := tempDBPath(t)
	return openRW(t, path), path
}

func mustTree(t *testing.T, p *Provider) []*provider.SessionNode {
	t.Helper()
	nodes, err := p.BuildTree(nowMS())
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

func keys(m map[string]*provider.SessionNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
