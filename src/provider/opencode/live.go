package opencode

import (
	"database/sql"
	"errors"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// opencode keeps a pending permission prompt in memory only, so a tool that
// waits for one is told from a running tool by timing:
//   - bash: no child process started since the tool did (after shellGraceMS)
//   - a fast tool (edit, read, ...): still running after fastToolMS
const (
	shellGraceMS = 1500
	fastToolMS   = 3000
)

var fastTools = map[string]bool{
	"edit": true, "write": true, "patch": true, "multiedit": true, "apply_patch": true,
	"read": true, "glob": true, "grep": true, "list": true, "todowrite": true, "todoread": true,
}

// sessionState derives the live state of an active session from its newest
// message: a user message or an assistant step that ended in tool calls is
// busy, a finished assistant turn is idle, and an unfinished one is busy
// unless one of its running tools waits for the human. children are the
// start times of the opencode process children in the session's dir (nil
// when unknown).
func sessionState(pool *sql.DB, sid string, nowMS int64, children []int64) (string, error) {
	var mid, data string
	err := pool.QueryRow(
		`SELECT id, data FROM message WHERE session_id = ? ORDER BY time_created DESC, id DESC LIMIT 1`,
		sid).Scan(&mid, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	d := decodeData(data)
	if d == nil {
		return "", nil
	}
	if d["role"] != "assistant" {
		return provider.StateBusy, nil
	}
	if num(sub(d, "time")["completed"]) > 0 {
		if d["finish"] == "tool-calls" && d["error"] == nil {
			return provider.StateBusy, nil
		}
		return provider.StateIdle, nil
	}
	rows, err := pool.Query(`SELECT data FROM part WHERE message_id = ?`, mid)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	state := provider.StateBusy
	for rows.Next() {
		var pdata string
		if err := rows.Scan(&pdata); err != nil {
			return "", err
		}
		pd := decodeData(pdata)
		if pd == nil || pd["type"] != "tool" {
			continue
		}
		st := sub(pd, "state")
		if st["status"] != "running" && st["status"] != "pending" {
			continue
		}
		tool, _ := pd["tool"].(string)
		if provider.IsAskTool(tool) {
			return provider.StateAsk, nil
		}
		if toolAwaitsPermission(tool, num(sub(st, "time")["start"]), nowMS, children) {
			state = provider.StatePerm
		}
	}
	return state, rows.Err()
}

// toolAwaitsPermission guesses whether a running tool started at startMS is
// blocked on a permission prompt rather than doing its work.
func toolAwaitsPermission(tool string, startMS, nowMS int64, children []int64) bool {
	if startMS <= 0 {
		return false
	}
	ran := nowMS - startMS
	switch {
	case tool == "bash":
		if children == nil || ran < shellGraceMS {
			return false
		}
		for _, c := range children {
			if c >= startMS-1000 { // clock ticks vs ms: allow a little skew
				return false
			}
		}
		return true
	case fastTools[tool]:
		return ran > fastToolMS
	}
	return false
}
