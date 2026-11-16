package dashboard

import (
	"errors"
	"fmt"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// fakeProvider is an in-memory provider.Provider for shell tests.
type fakeProvider struct {
	name       string
	fatal      bool
	switchable bool
	cands      []string
	validPaths map[string]bool // paths SetDataPath accepts (and Available reports present)

	mu       sync.Mutex
	path     string
	tree     []*provider.SessionNode
	calls    map[string][]provider.CallRec
	prompts  map[string][]string
	triggers map[string]*provider.TriggerResp
}

var _ provider.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Available() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.validPaths[f.path] {
		return nil
	}
	return fmt.Errorf("file not found: %s", f.path)
}

func (f *fakeProvider) DataPath() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path
}

func (f *fakeProvider) FatalWhenMissing() bool { return f.fatal }
func (f *fakeProvider) PathSwitchable() bool   { return f.switchable }
func (f *fakeProvider) Candidates() []string   { return f.cands }

func (f *fakeProvider) SetDataPath(path string) error {
	if !f.switchable {
		return errors.New("unsupported")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.validPaths[path] {
		return fmt.Errorf("file not found: %s", path)
	}
	f.path = path
	return nil
}

func (f *fakeProvider) BuildTree(int64) ([]*provider.SessionNode, error) {
	if f.tree == nil {
		return []*provider.SessionNode{}, nil
	}
	return f.tree, nil
}

func (f *fakeProvider) SessionCalls(sid string) ([]provider.CallRec, error) {
	c, ok := f.calls[sid]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return c, nil
}

func (f *fakeProvider) SessionPrompt(sid string) ([]string, error) {
	p, ok := f.prompts[sid]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return p, nil
}

func (f *fakeProvider) CallTrigger(mid string) (*provider.TriggerResp, error) {
	return f.triggers[mid], nil
}

// newLenient returns a switchable, non-fatal provider (opencode-like) whose
// data is present at path.
func newLenient(name, path string) *fakeProvider {
	return &fakeProvider{
		name: name, switchable: true, path: path,
		cands:      []string{path, "/default/" + name + ".db"},
		validPaths: map[string]bool{path: true},
	}
}

// newStrict returns a non-switchable provider that must have its data present
// (claude-like); present controls whether its data exists.
func newStrict(name, path string, present bool) *fakeProvider {
	return &fakeProvider{
		name: name, fatal: true, path: path,
		validPaths: map[string]bool{path: present},
	}
}
