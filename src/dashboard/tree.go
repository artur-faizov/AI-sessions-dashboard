package dashboard

import (
	"database/sql"
	"encoding/json"
	"sort"
)

type sessionNode struct {
	ID       string         `json:"id"`
	Parent   any            `json:"parent"`
	Title    string         `json:"title"`
	Agent    any            `json:"agent"`
	Model    any            `json:"model"`
	Dir      string         `json:"dir"`
	MaxCtx   int64          `json:"maxctx"`
	In       int64          `json:"in"`
	Out      int64          `json:"out"`
	Reason   int64          `json:"reason"`
	Cr       int64          `json:"cr"`
	Cw       int64          `json:"cw"`
	Created  int64          `json:"created"`
	Updated  int64          `json:"updated"`
	Active   bool           `json:"active"`
	Dur      int64          `json:"dur"`
	Beats    []int64        `json:"beats"`
	Children []*sessionNode `json:"children"`
	Subtree  *subtreeSums   `json:"subtree"`
	Source   string         `json:"source"`
}

type subtreeSums struct {
	In     int64 `json:"in"`
	Out    int64 `json:"out"`
	Reason int64 `json:"reason"`
	Cr     int64 `json:"cr"`
	Cw     int64 `json:"cw"`
}

type nullable = sql.NullString

func ns(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

// decodeData parses a message/part data JSON column into a generic value map.
func decodeData(s string) map[string]any {
	var d map[string]any
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return nil
	}
	return d
}

func num(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		i, _ := t.Int64()
		return i
	case string:
		return 0
	}
	return 0
}

func sub(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// sessionMaxContexts computes per-session max context size (fresh input + cache
// read + write) across that session's own AI calls, for non-archived sessions.
func sessionMaxContexts(pool *sql.DB) (map[string]int64, error) {
	out := map[string]int64{}
	rows, err := pool.Query(
		`SELECT m.session_id AS sid, m.data FROM message m
		 JOIN session s ON s.id = m.session_id
		 WHERE s.time_archived IS NULL AND s.directory != ''
		   AND m.data LIKE '%"role":"assistant"%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, data string
		if err := rows.Scan(&sid, &data); err != nil {
			return nil, err
		}
		d := decodeData(data)
		if d == nil || d["role"] != "assistant" {
			continue
		}
		t := sub(d, "tokens")
		cache := sub(t, "cache")
		ctx := num(t["input"]) + num(cache["read"]) + num(cache["write"])
		if ctx > 0 && ctx > out[sid] {
			out[sid] = ctx
		}
	}
	return out, rows.Err()
}

// sessionBeats returns timestamps (ms) of the session's own AI calls since sinceMs.
func sessionBeats(pool *sql.DB, sessionID string, sinceMs int64) ([]int64, error) {
	rows, err := pool.Query(
		`SELECT time_created, data FROM message
		 WHERE session_id = ? AND time_created >= ? AND data LIKE '%"role":"assistant"%'`,
		sessionID, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var beats []int64
	for rows.Next() {
		var tc int64
		var data string
		if err := rows.Scan(&tc, &data); err != nil {
			return nil, err
		}
		d := decodeData(data)
		if d == nil || d["role"] != "assistant" {
			continue
		}
		t := sub(d, "tokens")
		cache := sub(t, "cache")
		if num(t["input"]) != 0 || num(t["output"]) != 0 || num(t["reasoning"]) != 0 ||
			num(cache["read"]) != 0 || num(cache["write"]) != 0 {
			beats = append(beats, tc)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(beats, func(i, j int) bool { return beats[i] < beats[j] })
	return beats, nil
}

func buildTree(nowMS int64) ([]*sessionNode, error) {
	_, pool := poolFor()
	if pool == nil {
		return nil, errDBNotOpen
	}
	maxctx, err := sessionMaxContexts(pool)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(
		`SELECT id, parent_id, title, agent, model, directory,
		         tokens_input, tokens_output, tokens_reasoning,
		         tokens_cache_read, tokens_cache_write,
		         time_created, time_updated
		 FROM session WHERE time_archived IS NULL AND directory != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := map[string]*sessionNode{}
	for rows.Next() {
		var (
			id, dir                                   string
			parent, title, agent, model               sql.NullString
			in, out, reason, cr, cw, created, updated int64
		)
		if err := rows.Scan(&id, &parent, &title, &agent, &model, &dir,
			&in, &out, &reason, &cr, &cw, &created, &updated); err != nil {
			return nil, err
		}
		var modelV any
		if model.Valid {
			modelV = model.String
			var parsed any
			if json.Unmarshal([]byte(model.String), &parsed) == nil {
				modelV = parsed
			}
		}
		upd := updated
		nodes[id] = &sessionNode{
			ID:       id,
			Parent:   ns(parent),
			Title:    titleString(title),
			Agent:    ns(agent),
			Model:    modelV,
			Dir:      dir,
			MaxCtx:   maxctx[id],
			In:       in,
			Out:      out,
			Reason:   reason,
			Cr:       cr,
			Cw:       cw,
			Created:  created,
			Updated:  upd,
			Active:   upd > 0 && nowMS-upd < activeWindowMS,
			Dur:      upd - created,
			Beats:    []int64{},
			Children: []*sessionNode{},
			Source:   "opencode",
		}
		if n := nodes[id]; n.Dur < 0 {
			n.Dur = 0
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var roots []*sessionNode
	for _, n := range nodes {
		if p, ok := n.Parent.(string); ok {
			if parent, exists := nodes[p]; exists {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}

	for _, n := range nodes {
		if n.Active {
			beats, err := sessionBeats(pool, n.ID, nowMS-activeWindowMS)
			if err != nil {
				return nil, err
			}
			if beats == nil {
				beats = []int64{}
			}
			n.Beats = beats
		}
	}

	var fill func(n *sessionNode) subtreeSums
	fill = func(n *sessionNode) subtreeSums {
		t := subtreeSums{In: n.In, Out: n.Out, Reason: n.Reason, Cr: n.Cr, Cw: n.Cw}
		for _, c := range n.Children {
			st := fill(c)
			t.In += st.In
			t.Out += st.Out
			t.Reason += st.Reason
			t.Cr += st.Cr
			t.Cw += st.Cw
		}
		n.Subtree = &t
		sort.Slice(n.Children, func(i, j int) bool {
			return (n.Children[i].Created) > (n.Children[j].Created)
		})
		return t
	}
	for _, r := range roots {
		fill(r)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Created > roots[j].Created })
	if roots == nil {
		roots = []*sessionNode{}
	}
	return roots, nil
}

func titleString(t sql.NullString) string {
	if t.Valid && t.String != "" {
		return t.String
	}
	return "(untitled)"
}
