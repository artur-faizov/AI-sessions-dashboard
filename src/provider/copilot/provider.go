// Package copilot serves the dashboard from GitHub Copilot CLI session data
// (~/.copilot/session-state, session-store.db, and the OpenTelemetry file export).
package copilot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads Copilot CLI sessions from the data root.
type Provider struct {
	root  *provider.Root
	mu    sync.Mutex
	cache map[string]cachedSession

	otelEnv string // COPILOT_OTEL_FILE_EXPORTER_PATH at startup
	otel    otelReader

	dbWarnOnce sync.Once
}

var _ provider.Provider = (*Provider)(nil)

// New resolves the data root (COPILOT_HOME, else ~/.copilot) and the
// OpenTelemetry export path (COPILOT_OTEL_FILE_EXPORTER_PATH).
func New() *Provider {
	p := &Provider{}
	if env := os.Getenv("COPILOT_OTEL_FILE_EXPORTER_PATH"); env != "" {
		p.otelEnv = provider.ExpandUser(env)
	}
	if env := os.Getenv("COPILOT_HOME"); env != "" {
		p.root = provider.NewRoot(provider.ExpandUser(env))
	} else {
		home, _ := os.UserHomeDir()
		p.root = provider.NewRoot(filepath.Join(home, ".copilot"))
	}
	return p
}

func (*Provider) Name() string { return "copilot" }

func (p *Provider) stateDir() string { return filepath.Join(p.root.Get(), "session-state") }
func (p *Provider) storeDB() string  { return filepath.Join(p.root.Get(), "session-store.db") }

// Available reports whether <root>/session-state exists; the error names the path.
func (p *Provider) Available() error { return rootAt(p.root.Get()) }

func rootAt(root string) error {
	dir := filepath.Join(root, "session-state")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("directory not found: %s", dir)
}

func (p *Provider) DataPath() string     { return p.root.Get() }
func (p *Provider) DefaultPath() string  { return p.root.Default() }
func (*Provider) FatalWhenMissing() bool { return true }

// The root defaults to COPILOT_HOME or ~/.copilot and can be switched at
// runtime to another directory holding session-state/.
func (*Provider) PathSwitchable() bool   { return true }
func (p *Provider) Candidates() []string { return []string{p.root.Default()} }
func (p *Provider) SetDataPath(path string) error {
	if err := p.root.Switch(path, rootAt); err != nil {
		return err
	}
	p.mu.Lock()
	p.cache = map[string]cachedSession{}
	p.mu.Unlock()
	return nil
}

func (p *Provider) BuildTree(nowMS int64) ([]*provider.SessionNode, error) {
	return p.buildTree(nowMS), nil
}

func (p *Provider) SessionCalls(sid string) ([]provider.CallRec, error) {
	s, agentID, ok := p.lookupNode(sid)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return s.calls(agentID, p.loadTokens()), nil
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
