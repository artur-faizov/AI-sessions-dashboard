package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// claudeProvider reads Claude Code session transcripts from the projects dir.
type claudeProvider struct {
	root  string
	mu    sync.Mutex
	cache map[string]cachedFile
}

type cachedFile struct {
	mtime  int64
	parsed *claudeFile
}

// newClaudeProvider resolves the projects dir: CLAUDE_PROJECTS_DIR, else ~/.claude/projects.
func newClaudeProvider() *claudeProvider {
	p := &claudeProvider{}
	if env := os.Getenv("CLAUDE_PROJECTS_DIR"); env != "" {
		p.root = expandUser(env)
	} else {
		home, _ := os.UserHomeDir()
		p.root = filepath.Join(home, ".claude", "projects")
	}
	return p
}

func (*claudeProvider) name() string { return "claude" }

// available reports whether the projects dir exists; the error names the path.
func (c *claudeProvider) available() error {
	if info, err := os.Stat(c.root); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("directory not found: %s", c.root)
}

func (c *claudeProvider) dataPath() string     { return c.root }
func (*claudeProvider) fatalWhenMissing() bool { return true }

func (c *claudeProvider) buildTree(nowMS int64) ([]*sessionNode, error) {
	return c.claudeTree(nowMS)
}

func (c *claudeProvider) sessionCalls(sid string) ([]callRec, error) {
	f := c.findSessionFile(sid)
	if f == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return f.calls(), nil
}

func (c *claudeProvider) sessionPrompt(sid string) ([]string, error) {
	f := c.findSessionFile(sid)
	if f == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return f.prompt(), nil
}

func (c *claudeProvider) callTrigger(mid string) (*triggerResp, error) {
	for _, s := range c.scanIndex().all() {
		if res, ok := s.file.trigger(mid); ok {
			return res, nil
		}
	}
	return nil, nil
}
