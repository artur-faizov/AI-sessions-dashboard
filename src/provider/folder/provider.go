package folder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Provider reads exported session files from a runtime-switchable folder.
type Provider struct {
	mu     sync.Mutex
	path   string
	recent []string
	cache  map[string]cachedFile
	byID   map[string]*SessionData
	byMsg  map[string]*provider.TriggerResp
}

var _ provider.Provider = (*Provider)(nil)

type cachedFile struct {
	mtime, size int64
	file        *File // nil when the file is not a valid export
}

// New returns a provider with no folder selected yet.
func New() *Provider { return &Provider{cache: map[string]cachedFile{}} }

func (*Provider) Name() string { return "folder" }

func (p *Provider) DataPath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.path
}

func (p *Provider) Available() error {
	path := p.DataPath()
	if path == "" {
		return fmt.Errorf("no folder selected")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return fmt.Errorf("directory not found: %s", path)
	}
	return nil
}

func (*Provider) FatalWhenMissing() bool { return false }
func (*Provider) PathSwitchable() bool   { return true }

// DefaultPath is empty: no folder is read until one is picked.
func (*Provider) DefaultPath() string { return "" }

// SetDataPath switches to the export folder path; an empty path stops reading one.
func (p *Provider) SetDataPath(path string) error {
	path = provider.ExpandUser(strings.TrimSpace(path))
	if path == "" {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.path = ""
		p.cache = map[string]cachedFile{}
		p.byID, p.byMsg = nil, nil
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("directory not found: %s", path)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.path = path
	p.cache = map[string]cachedFile{}
	p.byID, p.byMsg = nil, nil
	p.remember(path)
	return nil
}

// Remember records dir as a recently used folder (shown in the picker).
func (p *Provider) Remember(dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remember(dir)
}

func (p *Provider) remember(dir string) {
	out := []string{dir}
	for _, r := range p.recent {
		if r != dir && len(out) < 8 {
			out = append(out, r)
		}
	}
	p.recent = out
}

func (p *Provider) Candidates() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.recent...)
}

// load parses every export file in the folder (cached by mtime+size) and
// refreshes the per-session and per-message indexes. Roots come back newest first.
func (p *Provider) load() ([]*File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, err := os.ReadDir(p.path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []*File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		full := filepath.Join(p.path, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		seen[full] = true
		c, ok := p.cache[full]
		if !ok || c.mtime != info.ModTime().UnixNano() || c.size != info.Size() {
			c = cachedFile{mtime: info.ModTime().UnixNano(), size: info.Size(), file: parse(full)}
			p.cache[full] = c
		}
		if c.file != nil {
			files = append(files, c.file)
		}
	}
	for k := range p.cache {
		if !seen[k] {
			delete(p.cache, k)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Tree.Updated > files[j].Tree.Updated })
	p.byID, p.byMsg = map[string]*SessionData{}, map[string]*provider.TriggerResp{}
	for _, f := range files {
		for id, d := range f.Sessions {
			p.byID[id] = d
		}
		for mid, t := range f.Triggers {
			p.byMsg[mid] = t
		}
	}
	return files, nil
}

// parse reads one export file; nil for anything that is not a valid export.
func parse(path string) *File {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f File
	if json.Unmarshal(b, &f) != nil || f.Version != Version || f.Tree == nil {
		return nil
	}
	relabel(f.Tree, f.Names)
	return &f
}

// relabel marks every node as imported and applies the exported local names
// as titles.
func relabel(n *provider.SessionNode, names map[string]string) {
	n.Source = SourceName
	if nm := names[n.ID]; nm != "" {
		n.Title = nm
	}
	if n.Children == nil {
		n.Children = []*provider.SessionNode{}
	}
	for _, k := range n.Children {
		relabel(k, names)
	}
}

func (p *Provider) BuildTree(int64) ([]*provider.SessionNode, error) {
	files, err := p.load()
	if err != nil {
		return nil, err
	}
	out := make([]*provider.SessionNode, 0, len(files))
	for _, f := range files {
		out = append(out, f.Tree)
	}
	return out, nil
}

func (p *Provider) session(sid string) (*SessionData, error) {
	if _, err := p.load(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.byID[sid]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return d, nil
}

func (p *Provider) SessionCalls(sid string) ([]provider.CallRec, error) {
	d, err := p.session(sid)
	if err != nil {
		return nil, err
	}
	if d.Calls == nil {
		return []provider.CallRec{}, nil
	}
	return d.Calls, nil
}

func (p *Provider) SessionPrompt(sid string) ([]string, error) {
	d, err := p.session(sid)
	if err != nil {
		return nil, err
	}
	if d.Prompt == nil {
		return []string{}, nil
	}
	return d.Prompt, nil
}

func (p *Provider) CallTrigger(mid string) (*provider.TriggerResp, error) {
	if _, err := p.load(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byMsg[mid], nil
}
