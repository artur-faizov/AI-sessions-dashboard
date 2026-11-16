package opencode

import (
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestSessionState(t *testing.T) {
	const now = 100000
	cases := []struct {
		name     string
		msg      string
		part     string // tool part of the message, "" for none
		children []int64
		want     string
	}{
		{"no messages", "", "", nil, ""},
		{"user message", `{"role":"user"}`, "", nil, provider.StateBusy},
		{"turn finished", `{"role":"assistant","finish":"stop","time":{"completed":5}}`, "", nil, provider.StateIdle},
		{"aborted", `{"role":"assistant","finish":"tool-calls","error":{"name":"MessageAbortedError"},"time":{"completed":5}}`, "", nil, provider.StateIdle},
		{"step ended in tool calls", `{"role":"assistant","finish":"tool-calls","time":{"completed":5}}`, "", nil, provider.StateBusy},
		{"streaming", `{"role":"assistant"}`, "", nil, provider.StateBusy},
		{"question", `{"role":"assistant"}`,
			`{"type":"tool","tool":"question","state":{"status":"running","time":{"start":99000}}}`, nil, provider.StateAsk},
		{"bash with a fresh child", `{"role":"assistant"}`,
			`{"type":"tool","tool":"bash","state":{"status":"running","time":{"start":90000}}}`, []int64{1000, 90200}, provider.StateBusy},
		{"bash without a child", `{"role":"assistant"}`,
			`{"type":"tool","tool":"bash","state":{"status":"running","time":{"start":90000}}}`, []int64{1000}, provider.StatePerm},
		{"bash, processes unknown", `{"role":"assistant"}`,
			`{"type":"tool","tool":"bash","state":{"status":"running","time":{"start":90000}}}`, nil, provider.StateBusy},
		{"bash just started", `{"role":"assistant"}`,
			`{"type":"tool","tool":"bash","state":{"status":"running","time":{"start":99500}}}`, []int64{}, provider.StateBusy},
		{"edit stuck", `{"role":"assistant"}`,
			`{"type":"tool","tool":"edit","state":{"status":"running","time":{"start":90000}}}`, nil, provider.StatePerm},
		{"edit running", `{"role":"assistant"}`,
			`{"type":"tool","tool":"edit","state":{"status":"running","time":{"start":99000}}}`, nil, provider.StateBusy},
		{"slow tool", `{"role":"assistant"}`,
			`{"type":"tool","tool":"webfetch","state":{"status":"running","time":{"start":1000}}}`, nil, provider.StateBusy},
		{"edit completed", `{"role":"assistant"}`,
			`{"type":"tool","tool":"edit","state":{"status":"completed","time":{"start":1000}}}`, nil, provider.StateBusy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool, _ := fixturePool(t)
			defer pool.Close()
			if c.msg != "" {
				if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "old", "s1", 1, 1, `{"role":"user"}`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "m1", "s1", 2, 2, c.msg); err != nil {
					t.Fatal(err)
				}
			}
			if c.part != "" {
				if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, "p1", "m1", "s1", 3, 3, c.part); err != nil {
					t.Fatal(err)
				}
			}
			got, err := sessionState(pool, "s1", now, c.children)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("state %q, want %q", got, c.want)
			}
		})
	}
}
