// Package codex reads local Codex rollout JSONL files without modifying them.
package codex

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/artur-faizov/token-monitor/src/provider"
)

type Provider struct {
	root  *provider.Root
	mu    sync.Mutex
	files map[string]*cachedFile
}

type cachedFile struct {
	info   os.FileInfo
	offset int64
	s      *session
}

var _ provider.Provider = (*Provider)(nil)
var _ provider.SegmentProvider = (*Provider)(nil)
var _ provider.ContextProvider = (*Provider)(nil)

func New() *Provider {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".codex")
	}
	return &Provider{root: provider.NewRoot(provider.ExpandUser(root)), files: map[string]*cachedFile{}}
}
func (*Provider) Name() string           { return "codex" }
func (p *Provider) DataPath() string     { return p.root.Get() }
func (p *Provider) DefaultPath() string  { return p.root.Default() }
func (*Provider) FatalWhenMissing() bool { return true }

// The root defaults to CODEX_HOME or ~/.codex and can be switched at runtime
// to another directory holding sessions/ or archived_sessions/.
func (*Provider) PathSwitchable() bool   { return true }
func (p *Provider) Candidates() []string { return []string{p.root.Default()} }
func (p *Provider) SetDataPath(path string) error {
	if err := p.root.Switch(path, rootAt); err != nil {
		return err
	}
	p.mu.Lock()
	p.files = nil
	p.mu.Unlock()
	return nil
}
func (p *Provider) Available() error { return rootAt(p.root.Get()) }

func rootAt(root string) error {
	for _, name := range []string{"sessions", "archived_sessions"} {
		if st, err := os.Stat(filepath.Join(root, name)); err == nil && st.IsDir() {
			return nil
		}
	}
	return fmt.Errorf("directories not found: %s or %s", filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions"))
}

// scan is called with mu held. Cached parser state is changed only by complete
// appended rows; an unterminated row remains at offset until its newline arrives.
func (p *Provider) scan() map[string]*session {
	if p.files == nil {
		p.files = map[string]*cachedFile{}
	}
	paths := []string{}
	for _, name := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(p.root.Get(), name), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	sort.Strings(paths)
	want := map[string]bool{}
	for _, path := range paths {
		want[path] = true
		p.refresh(path)
	}
	for path := range p.files {
		if !want[path] {
			delete(p.files, path)
		}
	}
	// Prefer the most informative copy, then union distinct rich response IDs.
	// Legacy offsets identify positions within a rollout, so copying a rollout
	// into archived_sessions does not create a second set of calls.
	groups := map[string][]*session{}
	for _, path := range paths {
		if f := p.files[path]; f != nil && f.s.id != "" {
			groups[f.s.id] = append(groups[f.s.id], f.s)
		}
	}
	out := map[string]*session{}
	for id, ss := range groups {
		out[id] = mergeSessions(ss)
	}
	return out
}

func (p *Provider) refresh(path string) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		delete(p.files, path)
		return
	}
	f := p.files[path]
	if f == nil || f.info == nil || !os.SameFile(f.info, info) || info.Size() < f.info.Size() || (info.Size() == f.info.Size() && !info.ModTime().Equal(f.info.ModTime())) {
		f = &cachedFile{s: newSession()}
		p.files[path] = f
	}
	if f.info != nil && info.Size() == f.info.Size() && info.ModTime().Equal(f.info.ModTime()) {
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err = fh.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(fh, 256*1024)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break
		}
		offset := f.offset
		f.offset += int64(len(line))
		f.s.parse(line, offset)
	}
	f.info = info
}

func (p *Provider) SessionCalls(sid string) ([]provider.CallRec, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.scan()[sid]
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return append([]provider.CallRec{}, s.calls...), nil
}
func (p *Provider) SessionPrompt(sid string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.scan()[sid]
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	return append([]string{}, s.prompts...), nil
}
func (p *Provider) CallTrigger(mid string) (*provider.TriggerResp, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.scan() {
		if tr := s.triggers[mid]; tr != nil {
			return cloneTrigger(tr), nil
		}
	}
	return nil, nil
}
func (p *Provider) SessionTriggers(sid string, calls []provider.CallRec) (map[string]*provider.TriggerResp, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.scan()[sid]
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	out := map[string]*provider.TriggerResp{}
	for _, c := range calls {
		if t := s.triggers[c.Mid]; t != nil {
			out[c.Mid] = cloneTrigger(t)
		}
	}
	return out, nil
}
func cloneTrigger(t *provider.TriggerResp) *provider.TriggerResp {
	c := *t
	c.Parts = append([]any{}, t.Parts...)
	c.Reply = append([]any{}, t.Reply...)
	return &c
}
