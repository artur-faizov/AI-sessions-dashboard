package opencode

import (
	"database/sql"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// imageTokens is a rough token cost of one attached image (providers bill by
// pixels, which the DB does not record).
const imageTokens = 1600

// firstCallContext tells what the input context of a session's first AI call
// was made of, or nil when mid is not that call. opencode stores only the user
// messages; the system prompt, tool schemas and instruction files (AGENTS.md)
// are the inferred rest of the call's context.
func firstCallContext(pool *sql.DB, mid string) (*provider.ContextResp, error) {
	var sessionID string
	err := pool.QueryRow("SELECT session_id FROM message WHERE id = ?", mid).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	calls, err := sessionCalls(pool, sessionID)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 || calls[0].Mid != mid {
		return nil, nil
	}
	first := calls[0]
	rows, err := pool.Query(
		`SELECT m.data, p.data FROM part p
		 JOIN message m ON m.id = p.message_id
		 WHERE p.session_id = ? AND (m.time_created < ? OR (m.time_created = ? AND m.id < ?))
		 ORDER BY m.time_created, m.id, p.id`,
		sessionID, first.Time, first.Time, mid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []provider.ContextItem
	for rows.Next() {
		var mdata, pdata string
		if err := rows.Scan(&mdata, &pdata); err != nil {
			return nil, err
		}
		md, pd := decodeData(mdata), decodeData(pdata)
		if md == nil || pd == nil || md["role"] != "user" {
			continue
		}
		switch pd["type"] {
		case "text":
			if t, ok := realUserText(pd); ok {
				items = append(items, provider.TextItem("user", "", t))
			} else if s, _ := pd["text"].(string); strings.TrimSpace(s) != "" && pd["ignored"] != true {
				items = append(items, provider.TextItem("reminders", "", s))
			}
		case "file":
			// a text file's content reaches the model as a synthetic text part
			name, _ := pd["filename"].(string)
			if mime, _ := pd["mime"].(string); strings.HasPrefix(mime, "image/") {
				items = append(items, provider.ContextItem{Kind: "files", Label: name, Tokens: imageTokens, Est: provider.EstText})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &provider.ContextResp{Mid: mid, Ctx: first.Ctx, Items: provider.FitContext(first.Ctx, items)}, nil
}

func (p *Provider) CallContext(mid string) (out *provider.ContextResp, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = firstCallContext(pool, mid)
		return err
	})
	return out, err
}
