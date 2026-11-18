// Package claude serves the dashboard from Claude Code session transcripts.
package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads Claude Code session transcripts from the projects dir.
type Provider struct {
	root  *provider.Root
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
		p.root = provider.NewRoot(provider.ExpandUser(env))
	} else {
		home, _ := os.UserHomeDir()
		p.root = provider.NewRoot(filepath.Join(home, ".claude", "projects"))
	}
	return p
}

func (*Provider) Name() string { return "claude" }

// available reports whether the projects dir exists; the error names the path.
func (c *Provider) Available() error { return dirAt(c.root.Get()) }

func dirAt(path string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("directory not found: %s", path)
}

func (c *Provider) DataPath() string     { return c.root.Get() }
func (c *Provider) DefaultPath() string  { return c.root.Default() }
func (*Provider) FatalWhenMissing() bool { return true }

// The projects dir defaults to CLAUDE_PROJECTS_DIR or ~/.claude/projects and
// can be switched at runtime to another directory of the same layout.
func (*Provider) PathSwitchable() bool   { return true }
func (c *Provider) Candidates() []string { return []string{c.root.Default()} }
func (c *Provider) SetDataPath(path string) error {
	if err := c.root.Switch(path, dirAt); err != nil {
		return err
	}
	c.mu.Lock()
	c.cache = map[string]cachedFile{}
	c.mu.Unlock()
	return nil
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
// subagent span per child transcript. Tools still running are included while
// a Claude Code process runs the session.
func (c *Provider) SessionSegments(sid string) ([]provider.Seg, error) {
	idx := c.scanIndex()
	s := idx.lookup(sid)
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	sid = s.id
	root := sid // a subagent runs in its root session's process
	for a, ok := idx.agents[root]; ok && a.parent != ""; a, ok = idx.agents[root] {
		root = a.parent
	}
	var nowMS int64
	if _, ok := c.liveStatuses()[root]; ok {
		nowMS = time.Now().UnixMilli()
	}
	segs := s.file.segments(nowMS)
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
