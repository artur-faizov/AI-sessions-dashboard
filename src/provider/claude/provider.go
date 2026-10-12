// Package claude serves the dashboard from Claude Code session transcripts.
package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads Claude Code session transcripts from the projects dir.
type Provider struct {
	root  string
	mu    sync.Mutex
	cache map[string]cachedFile
}

var _ provider.Provider = (*Provider)(nil)

type cachedFile struct {
	mtime  int64
	parsed *claudeFile
}

// New resolves the projects dir: CLAUDE_PROJECTS_DIR, else ~/.claude/projects.
func New() *Provider {
	p := &Provider{}
	if env := os.Getenv("CLAUDE_PROJECTS_DIR"); env != "" {
		p.root = provider.ExpandUser(env)
	} else {
		home, _ := os.UserHomeDir()
		p.root = filepath.Join(home, ".claude", "projects")
	}
	return p
}

func (*Provider) Name() string { return "claude" }

// available reports whether the projects dir exists; the error names the path.
func (c *Provider) Available() error {
	if info, err := os.Stat(c.root); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("directory not found: %s", c.root)
}

func (c *Provider) DataPath() string     { return c.root }
func (*Provider) FatalWhenMissing() bool { return true }

// Path switching is not supported: the projects dir comes from
// CLAUDE_PROJECTS_DIR or ~/.claude/projects at startup.
func (*Provider) PathSwitchable() bool { return false }
func (*Provider) Candidates() []string { return nil }
func (*Provider) SetDataPath(string) error {
	return errors.New("path switching is not supported for the claude provider")
}

func (c *Provider) BuildTree(nowMS int64) ([]*provider.SessionNode, error) {
	return c.claudeTree(nowMS)
}

func (c *Provider) SessionCalls(sid string) ([]provider.CallRec, error) {
	f := c.findSessionFile(sid)
	if f == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return f.calls(), nil
}

// SessionSegments returns the session's tool and human-wait spans plus one
// subagent span per child transcript.
func (c *Provider) SessionSegments(sid string) ([]provider.Seg, error) {
	idx := c.scanIndex()
	s := idx.lookup(sid)
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	segs := s.file.segments()
	mids := s.file.issuers()
	for _, a := range idx.agents {
		if a.parent == sid && a.file.firstMS > 0 && a.file.lastMS > a.file.firstMS {
			name := a.meta.AgentType
			if name == "" {
				name = a.meta.Description
			}
			segs = append(segs, provider.Seg{
				Kind: provider.SegSub, Start: a.file.firstMS, End: a.file.lastMS,
				Mid: mids[a.meta.ToolUseID], Name: name, Sub: a.id,
			})
		}
	}
	return segs, nil
}

func (c *Provider) SessionPrompt(sid string) ([]string, error) {
	f := c.findSessionFile(sid)
	if f == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return f.prompt(), nil
}

func (c *Provider) CallTrigger(mid string) (*provider.TriggerResp, error) {
	for _, s := range c.scanIndex().all() {
		if res, ok := s.file.trigger(mid); ok {
			return res, nil
		}
	}
	return nil, nil
}

// SessionTriggers resolves the triggers of all the given calls of one session
// with a single index scan (CallTrigger scans the whole projects dir per call).
func (c *Provider) SessionTriggers(sid string, calls []provider.CallRec) (map[string]*provider.TriggerResp, error) {
	f := c.findSessionFile(sid)
	if f == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	out := make(map[string]*provider.TriggerResp, len(calls))
	for _, cr := range calls {
		if t, ok := f.trigger(cr.Mid); ok {
			out[cr.Mid] = t
		}
	}
	return out, nil
}
