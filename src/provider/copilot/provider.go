// Package copilot serves the dashboard from GitHub Copilot CLI session data
// (~/.copilot/session-state and session-store.db).
package copilot

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads Copilot CLI sessions from the data root.
type Provider struct {
	root  string
	mu    sync.Mutex
	cache map[string]cachedSession

	dbWarnOnce sync.Once
}

var _ provider.Provider = (*Provider)(nil)

// New resolves the data root: COPILOT_HOME, else ~/.copilot.
func New() *Provider {
	p := &Provider{}
	if env := os.Getenv("COPILOT_HOME"); env != "" {
		p.root = provider.ExpandUser(env)
	} else {
		home, _ := os.UserHomeDir()
		p.root = filepath.Join(home, ".copilot")
	}
	return p
}

func (*Provider) Name() string { return "copilot" }

func (p *Provider) stateDir() string { return filepath.Join(p.root, "session-state") }
func (p *Provider) storeDB() string  { return filepath.Join(p.root, "session-store.db") }

// Available reports whether <root>/session-state exists; the error names the path.
func (p *Provider) Available() error {
	dir := p.stateDir()
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("directory not found: %s", dir)
}

func (p *Provider) DataPath() string     { return p.root }
func (*Provider) FatalWhenMissing() bool { return true }

// Path switching is not supported: the root comes from COPILOT_HOME or
// ~/.copilot at startup.
func (*Provider) PathSwitchable() bool { return false }
func (*Provider) Candidates() []string { return nil }
func (*Provider) SetDataPath(string) error {
	return errors.New("path switching is not supported for the copilot provider")
}

func (p *Provider) BuildTree(nowMS int64) ([]*provider.SessionNode, error) {
	return p.buildTree(nowMS), nil
}

func (p *Provider) SessionCalls(sid string) ([]provider.CallRec, error) {
	s, agentID, ok := p.lookupNode(sid)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return s.calls(agentID, p.loadUsage()), nil
}

func (p *Provider) SessionPrompt(sid string) ([]string, error) {
	s, agentID, ok := p.lookupNode(sid)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return s.prompt(agentID), nil
}

func (p *Provider) CallTrigger(mid string) (*provider.TriggerResp, error) {
	for _, s := range p.scan() {
		if res, ok := s.trigger(mid); ok {
			return res, nil
		}
	}
	return nil, nil
}

func (p *Provider) warnDB(err error) {
	p.dbWarnOnce.Do(func() {
		log.Printf("copilot: session-store.db unavailable, using event-file token data: %v", err)
	})
}
