package copilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// usageRow is one assistant_usage_events row (one API call).
type usageRow struct {
	id                      int64
	model                   string
	in, out, cr, cw, reason int64
	timeMS                  int64
}

type usageKey struct{ session, agent string }

// usageIndex groups usage rows by (session_id, agent_id); the root agent is "".
type usageIndex map[usageKey][]usageRow

func roDSN(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("-._~/:", r) {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return "file:" + b.String() + "?mode=ro"
}

// readUsage loads assistant_usage_events read-only. A missing DB or a missing
// table yields no rows and no error; other failures (e.g. a locked DB) return
// the error so the caller can log it, and are otherwise treated as no rows.
func readUsage(path string) (usageIndex, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	pool, err := sql.Open("sqlite", roDSN(path))
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	var name string
	err = pool.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='assistant_usage_events'`).Scan(&name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(`SELECT id, session_id, COALESCE(agent_id, ''), COALESCE(model, ''),
		COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		COALESCE(cache_read_tokens, 0), COALESCE(cache_write_tokens, 0),
		COALESCE(reasoning_tokens, 0), COALESCE(token_details_json, ''), COALESCE(created_at, '')
		FROM assistant_usage_events ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := usageIndex{}
	for rows.Next() {
		var (
			r                    usageRow
			sid, agent, det, cat string
		)
		if err := rows.Scan(&r.id, &sid, &agent, &r.model, &r.in, &r.out, &r.cr, &r.cw, &r.reason, &det, &cat); err != nil {
			return nil, err
		}
		if fresh, ok := freshInput(det); ok {
			r.in = fresh
		}
		r.timeMS = parseDBTime(cat)
		k := usageKey{session: sid, agent: agent}
		idx[k] = append(idx[k], r)
	}
	return idx, rows.Err()
}

// freshInput reads input.tokenCount from token_details_json (the same shape as
// session.shutdown tokenDetails), which excludes cache tokens.
func freshInput(js string) (int64, bool) {
	if js == "" {
		return 0, false
	}
	var d rawTokenDetails
	if json.Unmarshal([]byte(js), &d) != nil || d.Input == nil {
		return 0, false
	}
	return d.Input.TokenCount, true
}

func parseDBTime(s string) int64 {
	if s == "" {
		return 0
	}
	if ms := parseMS(s); ms > 0 {
		return ms
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func (p *Provider) loadUsage() usageIndex {
	idx, err := readUsage(p.storeDB())
	if err != nil {
		p.warnDB(err)
		return nil
	}
	return idx
}

// --- token source selection ---

type tokenSource int

const (
	srcOutputOnly tokenSource = iota
	srcShutdown
	srcRows
)

type nodeTokens struct {
	in, out, cr, cw, reason, maxctx int64
	source                          tokenSource
}

// nodeTokens picks the token source for a node under the no-substitution rule:
// per-call rows, else (roots only) shutdown metrics, else per-message
// outputTokens. A field no source reports stays 0; maxctx is set only from
// per-call rows.
func (s *session) nodeTokens(agentID string, usage usageIndex) nodeTokens {
	if rows := usage[usageKey{session: s.id, agent: agentID}]; len(rows) > 0 {
		t := nodeTokens{source: srcRows}
		for _, r := range rows {
			t.in += r.in
			t.out += r.out
			t.cr += r.cr
			t.cw += r.cw
			t.reason += r.reason
			if ctx := r.in + r.cr + r.cw; ctx > t.maxctx {
				t.maxctx = ctx
			}
		}
		return t
	}
	if agentID == "" && len(s.shutdowns) > 0 {
		t := nodeTokens{source: srcShutdown}
		for _, u := range s.shutdowns {
			t.in += u.in
			t.out += u.out
			t.cr += u.cr
			t.cw += u.cw
			t.reason += u.reason
		}
		return t
	}
	t := nodeTokens{source: srcOutputOnly}
	if st := s.streams[agentID]; st != nil {
		for _, it := range st.items {
			if it.kind == kindAssistant {
				t.out += it.outTokens
			}
		}
	}
	return t
}
