package opencode

import (
	"database/sql"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

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

// msgParts is what a session's parts say about its messages.
type msgParts struct {
	human map[string]provider.Trig // message id -> human input it carries
	asks  map[string]bool          // assistant messages with a question tool
	texts map[string][]string      // assistant message id -> its text parts
}

// humanTriggers maps message id -> the kind of human input a message carries:
// user text parts (text), user file parts (image), and completed question
// tool parts on assistant messages (question). It also collects the assistant
// messages that put a question tool to the human (in any status) and the
// assistant text parts, in part order.
func humanTriggers(pool *sql.DB, sessionID string) (*msgParts, error) {
	rows, err := pool.Query(
		`SELECT p.message_id AS mid, m.data AS mdata, p.data AS pdata FROM part p
		 JOIN message m ON m.id = p.message_id
		 WHERE p.session_id = ? ORDER BY p.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	asks := map[string]bool{}
	texts := map[string][]string{}
	textMsgs := map[string]bool{}
	fileMsgs := map[string]bool{}
	questionMsgs := map[string]bool{}
	for rows.Next() {
		var mid, mdata, pdata string
		if err := rows.Scan(&mid, &mdata, &pdata); err != nil {
			return nil, err
		}
		md, pd := decodeData(mdata), decodeData(pdata)
		if md == nil || pd == nil {
			continue
		}
		switch md["role"] {
		case "user":
			if _, ok := realUserText(pd); ok {
				textMsgs[mid] = true
			}
			if pd["type"] == "file" {
				fileMsgs[mid] = true
			}
		case "assistant":
			if txt, _ := pd["text"].(string); pd["type"] == "text" && pd["synthetic"] != true {
				texts[mid] = append(texts[mid], txt)
			}
			if tool, _ := pd["tool"].(string); pd["type"] == "tool" && tool == "question" {
				asks[mid] = true
				if sub(pd, "state")["status"] == "completed" {
					questionMsgs[mid] = true
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]provider.Trig{}
	for mid := range textMsgs {
		out[mid] = provider.TrigText
	}
	for mid := range questionMsgs {
		out[mid] = provider.TrigQuestion
	}
	for mid := range fileMsgs {
		if out[mid] == "" {
			out[mid] = provider.TrigImage
		}
	}
	return &msgParts{human: out, asks: asks, texts: texts}, nil
}

type msgRow struct {
	id      string
	created int64
	data    string
}

// sessionCalls returns one record per assistant message that recorded token usage.
func sessionCalls(pool *sql.DB, sessionID string) ([]provider.CallRec, error) {
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
	mp, err := humanTriggers(pool, sessionID)
	if err != nil {
		return nil, err
	}
	calls := []provider.CallRec{}
	var prev *msgRow
	// A call also asks when it ends the turn (a human message or the session
	// end follows) with a question in its text.
	last := -1
	var texts []string
	endTurn := func() {
		if last >= 0 && provider.AsksInText(texts) {
			calls[last].Ask = true
		}
	}
	for _, m := range msgs {
		d := decodeData(m.data)
		if d != nil && d["role"] == "user" {
			if t := mp.human[m.id]; t == provider.TrigText || t == provider.TrigImage {
				endTurn()
			}
			last, texts = -1, nil
		}
		if d != nil && d["role"] == "assistant" {
			t := sub(d, "tokens")
			cache := sub(t, "cache")
			in := num(t["input"])
			cr := num(cache["read"])
			cw := num(cache["write"])
			out := num(t["output"])
			reason := num(t["reasoning"])
			if in != 0 || cr != 0 || cw != 0 || out != 0 || reason != 0 {
				var hu provider.Trig
				if prev != nil {
					hu = mp.human[prev.id]
				}
				mdl, _ := d["modelID"].(string)
				tm := sub(d, "time")
				var dur, end int64
				if c, e := num(tm["created"]), num(tm["completed"]); c > 0 && e > c {
					dur, end = e-c, e
				}
				calls = append(calls, provider.CallRec{
					Mid: m.id, Time: m.created, In: in, Cr: cr, Cw: cw,
					Out: out, Reason: reason, Ctx: in + cr + cw,
					Hu: hu, Ask: mp.asks[m.id], Model: mdl, Dur: dur, End: end,
				})
				last, texts = len(calls)-1, nil
			}
			if last >= 0 {
				texts = append(texts, mp.texts[m.id]...)
			}
		}
		mm := m
		prev = &mm
	}
	endTurn()
	return calls, nil
}

// sessionSegments lists the spans a session spent in tools (state start to
// end), waiting for the human (question tool) and waiting for a subagent (task
// tool, whose span covers the child session's run).
func sessionSegments(pool *sql.DB, sessionID string) ([]provider.Seg, error) {
	rows, err := pool.Query(`SELECT message_id, data FROM part WHERE session_id = ? AND data LIKE '%"type":"tool"%'`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	segs := []provider.Seg{}
	for rows.Next() {
		var mid, data string
		if err := rows.Scan(&mid, &data); err != nil {
			return nil, err
		}
		d := decodeData(data)
		if d == nil || d["type"] != "tool" {
			continue
		}
		st := sub(d, "state")
		tm := sub(st, "time")
		start, end := num(tm["start"]), num(tm["end"])
		if start <= 0 || end <= start {
			continue
		}
		tool, _ := d["tool"].(string)
		seg := provider.Seg{Kind: provider.SegTool, Start: start, End: end, Mid: mid, Name: tool, Err: st["status"] == "error",
			Input: provider.ToolInput(st["input"]), Output: provider.ToolOutput(st["output"])}
		switch {
		case tool == "task":
			seg.Kind = provider.SegSub
			seg.Sub, _ = sub(st, "metadata")["sessionId"].(string)
			if t, _ := sub(st, "input")["subagent_type"].(string); t != "" {
				seg.Name = t
			}
		case provider.IsAskTool(tool):
			seg.Kind = provider.SegHuman
		}
		segs = append(segs, seg)
	}
	return segs, rows.Err()
}

// callReply collects what an assistant message showed the human: its text
// parts and its question tool parts, or nil when there are none.
func callReply(pool *sql.DB, messageID string) ([]any, error) {
	rows, err := pool.Query("SELECT data FROM part WHERE message_id = ? ORDER BY id", messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parts []any
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		pj := decodeData(data)
		if pj == nil {
			continue
		}
		tool, _ := pj["tool"].(string)
		switch {
		case pj["type"] == "text" && pj["synthetic"] != true:
			if txt, _ := pj["text"].(string); strings.TrimSpace(txt) != "" {
				t, cut := provider.Clip(txt)
				parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
			}
		case pj["type"] == "tool" && provider.IsAskTool(tool):
			st := sub(pj, "state")
			inS, cutI := provider.Clip(st["input"])
			outS, cutO := provider.Clip(st["output"])
			parts = append(parts, provider.TriggerPartTool{
				Kind: "tool", Tool: tool, Status: st["status"],
				Input: inS, CutI: cutI, Output: outS, CutO: cutO,
			})
		}
	}
	return parts, rows.Err()
}

// callTrigger returns the message content preceding the given assistant message.
func callTrigger(pool *sql.DB, messageID string) (*provider.TriggerResp, error) {
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
	reply, err := callReply(pool, messageID)
	if err != nil {
		return nil, err
	}
	var prev msgRow
	err = pool.QueryRow(
		`SELECT id, time_created, data FROM message
		 WHERE session_id = ? AND (time_created < ? OR (time_created = ? AND id < ?))
		 ORDER BY time_created DESC, id DESC LIMIT 1`,
		sessionID, created, created, messageID).Scan(&prev.id, &prev.created, &prev.data)
	if err == sql.ErrNoRows {
		return &provider.TriggerResp{Case: "start", Time: nil, Parts: []any{}, Reply: reply}, nil
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
				t, cut := provider.Clip(txt)
				parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
			}
		case "tool":
			st := sub(pj, "state")
			inS, cutI := provider.Clip(st["input"])
			outS, cutO := provider.Clip(st["output"])
			var status any
			if v, ok := st["status"]; ok {
				status = v
			}
			parts = append(parts, provider.TriggerPartTool{
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
	res := &provider.TriggerResp{Time: prev.created, Parts: parts, Reply: reply}
	if pd["role"] == "user" {
		res.Case = "user"
	} else {
		res.Case = "assistant"
	}
	return res, nil
}
