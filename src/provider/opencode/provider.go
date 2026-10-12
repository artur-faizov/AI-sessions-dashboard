// Package opencode serves the dashboard from an opencode sqlite database.
package opencode

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads a runtime-switchable, read-only opencode sqlite DB.
type Provider struct {
	mu      sync.RWMutex
	path    string
	gen     int     // bumped on every path switch
	pool    *sql.DB // one pool per generation; nil until the path validates
	cliPath string  // the --db override, kept for Candidates
}

var _ provider.Provider = (*Provider)(nil)

// New resolves the DB path (cliDBPath → DB_PATH env → OS defaults) and returns
// a provider pointed at it. A missing DB is not an error: the path stays the
// first candidate and the UI lets the user pick another.
func New(cliDBPath string) *Provider {
	p := &Provider{cliPath: cliDBPath}
	path, found := resolveDB(cliDBPath)
	valid := ""
	if found {
		valid = path
	}
	p.reset(path, valid)
	return p
}

// reset swaps the active database; validPath non-empty opens the pool.
func (p *Provider) reset(path, validPath string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.path = path
	p.gen++
	old := p.pool
	p.pool = nil
	if old != nil {
		old.Close()
	}
	if validPath != "" {
		pool, _ := sql.Open("sqlite", roDSN(validPath))
		p.pool = pool
	}
}

func (p *Provider) current() (string, *sql.DB, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.path, p.pool, p.gen
}

// ensurePool lazily opens the pool for the current path (e.g. the DB file
// appeared after startup).
func (p *Provider) ensurePool() {
	path, pool, _ := p.current()
	if path == "" || pool != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pool == nil && p.path != "" {
		if np, err := sql.Open("sqlite", roDSN(p.path)); err == nil {
			p.pool = np
		}
	}
}

// withPool runs fn holding the read lock, so a concurrent SetDataPath cannot
// close the pool mid-request: each read is served entirely by one generation.
func (p *Provider) withPool(fn func(*sql.DB) error) error {
	p.ensurePool()
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.pool == nil {
		return errDBNotOpen
	}
	return fn(p.pool)
}

func (p *Provider) Name() string { return "opencode" }

// Available reports whether the current DB validates; the error names the path.
func (p *Provider) Available() error {
	path, _, _ := p.current()
	if path == "" {
		resolved, found := resolveDB(p.cliPath)
		if found {
			return nil
		}
		return errors.New("file not found: " + resolved)
	}
	if ok, msg := validateDB(path); !ok {
		return errors.New(msg)
	}
	return nil
}

func (p *Provider) DataPath() string {
	path, _, _ := p.current()
	return path
}

func (p *Provider) FatalWhenMissing() bool { return false }
func (p *Provider) PathSwitchable() bool   { return true }

// Candidates lists autodetected DB paths: --db, DB_PATH, then OS defaults.
func (p *Provider) Candidates() []string {
	return dbCandidates(p.cliPath, os.Getenv("DB_PATH"))
}

// SetDataPath validates path as an opencode DB and switches to it. On failure
// the previous path stays active and the error names the rejected path.
func (p *Provider) SetDataPath(path string) error {
	ok, msg := validateDB(path)
	if !ok {
		if path != "" && !strings.Contains(msg, path) {
			msg = path + ": " + msg
		}
		return errors.New(msg)
	}
	pool, err := sql.Open("sqlite", roDSN(path))
	if err != nil {
		return errors.New(path + ": " + err.Error())
	}
	p.mu.Lock()
	old := p.pool
	p.path = path
	p.gen++
	p.pool = pool
	p.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

func (p *Provider) BuildTree(nowMS int64) (out []*provider.SessionNode, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = buildTree(pool, nowMS)
		return err
	})
	return out, err
}

func (p *Provider) SessionPrompt(sid string) (out []string, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = sessionPrompt(pool, sid)
		return err
	})
	return out, err
}

func (p *Provider) SessionCalls(sid string) (out []provider.CallRec, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = sessionCalls(pool, sid)
		return err
	})
	return out, err
}

func (p *Provider) SessionSegments(sid string) (out []provider.Seg, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = sessionSegments(pool, sid)
		return err
	})
	return out, err
}

func (p *Provider) CallTrigger(mid string) (out *provider.TriggerResp, err error) {
	err = p.withPool(func(pool *sql.DB) error {
		out, err = callTrigger(pool, mid)
		return err
	})
	return out, err
}
