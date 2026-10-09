// Package folder serves the dashboard from a directory of exported sessions:
// one JSON file per top-level session, subagents included. The same package
// owns the file format, so export and import cannot drift apart.
package folder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Version is the export file format version.
const Version = 1

// SourceName is the Source label imported sessions carry in the tree.
const SourceName = "folder"

// File is one exported top-level session with all its subagents.
type File struct {
	Version    int                              `json:"version"`
	ExportedAt int64                            `json:"exportedAt"`
	Source     string                           `json:"source"`             // provider the session was exported from
	Tree       *provider.SessionNode            `json:"tree"`               // root session, children nested
	Names      map[string]string                `json:"names,omitempty"`    // local renames: session id -> name
	Sessions   map[string]*SessionData          `json:"sessions"`           // per session id, root and descendants
	Triggers   map[string]*provider.TriggerResp `json:"triggers,omitempty"` // by message id
}

// SessionData is the per-session payload behind /api/prompt and /api/calls.
type SessionData struct {
	Prompt []string           `json:"prompt"`
	Calls  []provider.CallRec `json:"calls"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// FileName is the export file name for a session id.
func FileName(id string) string { return unsafeName.ReplaceAllString(id, "_") + ".json" }

// Collect builds the export File for root from the source provider: calls,
// prompt texts and triggers for root and every descendant. names are the local
// renames, filtered to the exported sessions.
func Collect(src provider.Provider, root *provider.SessionNode, names map[string]string, nowMS int64) (*File, error) {
	f := &File{
		Version: Version, ExportedAt: nowMS, Source: src.Name(), Tree: root,
		Sessions: map[string]*SessionData{}, Triggers: map[string]*provider.TriggerResp{},
	}
	var walk func(n *provider.SessionNode) error
	walk = func(n *provider.SessionNode) error {
		calls, err := src.SessionCalls(n.ID)
		if err != nil {
			return fmt.Errorf("calls of %s: %w", n.ID, err)
		}
		prompt, err := src.SessionPrompt(n.ID)
		if err != nil {
			return fmt.Errorf("prompt of %s: %w", n.ID, err)
		}
		f.Sessions[n.ID] = &SessionData{Prompt: prompt, Calls: calls}
		if err := collectTriggers(src, n.ID, calls, f.Triggers); err != nil {
			return err
		}
		if nm := names[n.ID]; nm != "" {
			if f.Names == nil {
				f.Names = map[string]string{}
			}
			f.Names[n.ID] = nm
		}
		for _, k := range n.Children {
			if err := walk(k); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return f, nil
}

// triggerBatcher is implemented by sources that can resolve all triggers of a
// session at once, which is much cheaper than a CallTrigger lookup per call.
type triggerBatcher interface {
	SessionTriggers(sid string, calls []provider.CallRec) (map[string]*provider.TriggerResp, error)
}

// collectTriggers adds the triggers of calls (of session sid) to into.
func collectTriggers(src provider.Provider, sid string, calls []provider.CallRec, into map[string]*provider.TriggerResp) error {
	if b, ok := src.(triggerBatcher); ok {
		m, err := b.SessionTriggers(sid, calls)
		if err != nil {
			return fmt.Errorf("triggers of %s: %w", sid, err)
		}
		for mid, t := range m {
			into[mid] = t
		}
		return nil
	}
	for _, c := range calls {
		if _, done := into[c.Mid]; done {
			continue
		}
		t, err := src.CallTrigger(c.Mid)
		if err != nil {
			return fmt.Errorf("trigger %s: %w", c.Mid, err)
		}
		if t != nil {
			into[c.Mid] = t
		}
	}
	return nil
}

// ErrExists is returned by Write when the target file is already there.
var ErrExists = errors.New("file already exists")

// Write stores f as <dir>/<root id>.json without ever overwriting an existing
// file (ErrExists). It creates dir when missing.
func Write(dir string, f *File) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, FileName(f.Tree.ID))
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return path, ErrExists
	}
	if err != nil {
		return path, err
	}
	if err := json.NewEncoder(out).Encode(f); err != nil {
		out.Close()
		os.Remove(path)
		return path, err
	}
	return path, out.Close()
}
