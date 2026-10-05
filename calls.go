package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
)

const triggerMax = 60000

type callRec struct {
	Mid    string `json:"mid"`
	Time   int64  `json:"time"`
	In     int64  `json:"in"`
	Cr     int64  `json:"cr"`
	Cw     int64  `json:"cw"`
	Out    int64  `json:"out"`
	Reason int64  `json:"reason"`
	Ctx    int64  `json:"ctx"`
	Hu     bool   `json:"hu"`
}

type triggerPartText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Cut  bool   `json:"cut"`
}

type triggerPartTool struct {
	Kind   string `json:"kind"`
	Tool   any    `json:"tool"`
	Status any    `json:"status"`
	Input  string `json:"input"`
	CutI   bool   `json:"cut_i"`
	Output string `json:"output"`
	CutO   bool   `json:"cut_o"`
}

type triggerResp struct {
	Case  string `json:"case"`
	Time  any    `json:"time"`
	Parts []any  `json:"parts"`
}

// realUserText reports whether a parsed text-part JSON carries real human text.
func realUserText(d map[string]any) (string, bool) {
	if d == nil || d["type"] != "text" {
		return "", false
	}
	s, _ := d["text"].(string)
	t := strings.TrimSpace(s)
	if t == "" || d["synthetic"] == true {
		return "", false
	}
	if strings.HasPrefix(t, "<system-reminder>") {
		return "", false
	}
	return t, true
}

// sessionPrompt collects the session's first real user text parts, in message
// order, stopping once the collected texts exceed 20000 characters.
func sessionPrompt(pool *sql.DB, sessionID string) ([]string, error) {
	rows, err := pool.Query(
		`SELECT p.data FROM part p
		 JOIN message m ON m.id = p.message_id
		 WHERE p.session_id = ? AND m.data LIKE '%"role":"user"%'
		 ORDER BY m.time_created, p.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	texts := []string{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		d := decodeData(data)
		if t, ok := realUserText(d); ok {
			texts = append(texts, t)
			total := 0
			for _, x := range texts {
				total += len([]rune(x))
			}
			if total > 20000 {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return texts, nil
}

func humanMessageIDs(pool *sql.DB, sessionID string) (map[string]bool, error) {
	rows, err := pool.Query(
		`SELECT p.message_id AS mid, p.data FROM part p
		 JOIN message m ON m.id = p.message_id
		 WHERE p.session_id = ? AND m.data LIKE '%"role":"user"%'`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var mid, data string
		if err := rows.Scan(&mid, &data); err != nil {
			return nil, err
		}
		if _, ok := realUserText(decodeData(data)); ok {
			out[mid] = true
		}
	}
	return out, rows.Err()
}

type msgRow struct {
	id      string
	created int64
	data    string
}

// sessionCalls returns one record per assistant message that recorded token usage.
func sessionCalls(pool *sql.DB, sessionID string) ([]callRec, error) {
	rows, err := pool.Query(
		`SELECT id, time_created, data FROM message
		 WHERE session_id = ? ORDER BY time_created, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []msgRow
	for rows.Next() {
		var m msgRow
		if err := rows.Scan(&m.id, &m.created, &m.data); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	human, err := humanMessageIDs(pool, sessionID)
	if err != nil {
		return nil, err
	}
	calls := []callRec{}
	var prev *msgRow
	for _, m := range msgs {
		d := decodeData(m.data)
		if d != nil && d["role"] == "assistant" {
			t := sub(d, "tokens")
			cache := sub(t, "cache")
			in := num(t["input"])
			cr := num(cache["read"])
			cw := num(cache["write"])
			out := num(t["output"])
			reason := num(t["reasoning"])
			if in != 0 || cr != 0 || cw != 0 || out != 0 || reason != 0 {
				calls = append(calls, callRec{
					Mid: m.id, Time: m.created, In: in, Cr: cr, Cw: cw,
					Out: out, Reason: reason, Ctx: in + cr + cw,
					Hu: prev != nil && human[prev.id],
				})
			}
		}
		mm := m
		prev = &mm
	}
	return calls, nil
}

// clip truncates a trigger part value at triggerMax characters; non-string
// values are rendered as indented JSON (unicode kept raw).
func clip(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		if err := enc.Encode(v); err != nil {
			return "", false
		}
		s = strings.TrimRight(buf.String(), "\n")
	}
	r := []rune(s)
	if len(r) > triggerMax {
		return string(r[:triggerMax]), true
	}
	return s, false
}

// callTrigger returns the message content preceding the given assistant message.
func callTrigger(pool *sql.DB, messageID string) (*triggerResp, error) {
	var sessionID string
	var created int64
	var data string
	err := pool.QueryRow(
		"SELECT id, session_id, time_created, data FROM message WHERE id = ?",
		messageID).Scan(&messageID, &sessionID, &created, &data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if d := decodeData(data); d == nil || d["role"] != "assistant" {
		return nil, nil
	}
	var prev msgRow
	err = pool.QueryRow(
		`SELECT id, time_created, data FROM message
		 WHERE session_id = ? AND (time_created < ? OR (time_created = ? AND id < ?))
		 ORDER BY time_created DESC, id DESC LIMIT 1`,
		sessionID, created, created, messageID).Scan(&prev.id, &prev.created, &prev.data)
	if err == sql.ErrNoRows {
		return &triggerResp{Case: "start", Time: nil, Parts: []any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	pd := decodeData(prev.data)
	if pd == nil {
		pd = map[string]any{}
	}
	var parts []any
	prows, err := pool.Query("SELECT data FROM part WHERE message_id = ? ORDER BY id", prev.id)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var pdata string
		if err := prows.Scan(&pdata); err != nil {
			return nil, err
		}
		pj := decodeData(pdata)
		if pj == nil {
			continue
		}
		switch pj["type"] {
		case "text":
			if txt, _ := pj["text"].(string); txt != "" {
				t, cut := clip(txt)
				parts = append(parts, triggerPartText{Kind: "text", Text: t, Cut: cut})
			}
		case "tool":
			st := sub(pj, "state")
			inS, cutI := clip(st["input"])
			outS, cutO := clip(st["output"])
			var status any
			if v, ok := st["status"]; ok {
				status = v
			}
			parts = append(parts, triggerPartTool{
				Kind: "tool", Tool: pj["tool"], Status: status,
				Input: inS, CutI: cutI, Output: outS, CutO: cutO,
			})
		}
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	if parts == nil {
		parts = []any{}
	}
	res := &triggerResp{Time: prev.created, Parts: parts}
	if pd["role"] == "user" {
		res.Case = "user"
	} else {
		res.Case = "assistant"
	}
	return res, nil
}
