package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// --- normalized transcript model ---

type claudeUsage struct{ in, out, cr, cw int64 }

func (u claudeUsage) nonzero() bool {
	return u.in != 0 || u.out != 0 || u.cr != 0 || u.cw != 0
}

type claudeToolUse struct {
	id    string
	name  string
	input any
}

type claudeToolResult struct {
	id      string
	content string
}

type claudeMsg struct {
	mid         string // top-level line uuid
	role        string // "user" or "assistant"
	timeMS      int64
	usage       claudeUsage
	model       string
	hasUsage    bool
	human       string // real human text on user lines
	meta        bool   // isMeta noise line
	txtBlk      bool   // user content array carries a real human text block
	imgBlk      bool   // user content array carries an image block
	texts       []string
	toolUses    []claudeToolUse
	toolResults []claudeToolResult
}

// claudeFile is a parsed transcript (main session or subagent).
type claudeFile struct {
	path      string
	messages  []*claudeMsg
	toolNames map[string]string // tool_use id -> tool name
	title     string
	cwd       string
	firstMS   int64
	lastMS    int64
}

// --- raw JSON shapes ---

type claudeRawLine struct {
	Type      string        `json:"type"`
	UUID      string        `json:"uuid"`
	Timestamp string        `json:"timestamp"`
	Cwd       string        `json:"cwd"`
	SessionID string        `json:"sessionId"`
	IsMeta    bool          `json:"isMeta"`
	AITitle   string        `json:"aiTitle"`
	Message   *claudeRawMsg `json:"message"`
}

type claudeRawMsg struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *claudeRawUsage `json:"usage"`
}

type claudeRawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type claudeRawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     any             `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type claudeAgentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

// --- parsing ---

func claudeParseMS(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

var claudeNoisePrefixes = []string{
	"<system-reminder>", "<command-name>", "<command-message>", "<command-args>",
	"<local-command-caveat", "<local-command-stdout", "<bash-input>", "<bash-stdout>",
	"<bash-stderr>",
}

// claudeHumanText returns the real human text of a plain-string user content.
func claudeHumanText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	for _, p := range claudeNoisePrefixes {
		if strings.HasPrefix(t, p) {
			return ""
		}
	}
	return t
}

func claudeBlockContentString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		out, _ := provider.Clip(v)
		return out
	}
	return ""
}

// parseClaudeFile parses one JSONL transcript; malformed lines are skipped.
func parseClaudeFile(path string) *claudeFile {
	f := &claudeFile{path: path, toolNames: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw claudeRawLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		switch raw.Type {
		case "ai-title":
			if raw.AITitle != "" {
				f.title = raw.AITitle
			}
			continue
		case "user", "assistant":
		default:
			continue // noise / unknown types
		}
		if raw.Message == nil {
			continue
		}
		ts := claudeParseMS(raw.Timestamp)
		if ts == 0 {
			continue // no valid timestamp: skip silently
		}
		if raw.Type == "assistant" {
			if raw.UUID != "" {
				if seen[raw.UUID] {
					continue // retry duplicate: keep first
				}
				seen[raw.UUID] = true
			}
		}
		m := &claudeMsg{mid: raw.UUID, role: raw.Type, timeMS: ts}
		if raw.Type == "user" {
			m.meta = raw.IsMeta
		}
		if raw.Cwd != "" && f.cwd == "" {
			f.cwd = raw.Cwd
		}
		if f.firstMS == 0 || ts < f.firstMS {
			f.firstMS = ts
		}
		if ts > f.lastMS {
			f.lastMS = ts
		}
		if raw.Type == "assistant" {
			m.model = raw.Message.Model
			if raw.Message.Usage != nil {
				m.usage = claudeUsage{
					in:  raw.Message.Usage.InputTokens,
					out: raw.Message.Usage.OutputTokens,
					cr:  raw.Message.Usage.CacheReadInputTokens,
					cw:  raw.Message.Usage.CacheCreationInputTokens,
				}
			}
			if m.usage.nonzero() && m.model != "<synthetic>" {
				m.hasUsage = true
			}
		}
		// content: plain string or block array
		var blocks []claudeRawBlock
		if len(raw.Message.Content) > 0 {
			var s string
			if json.Unmarshal(raw.Message.Content, &s) == nil {
				if raw.Type == "user" && !raw.IsMeta {
					m.human = claudeHumanText(s)
				}
				if m.human == "" {
					m.texts = []string{s}
				}
			} else if json.Unmarshal(raw.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					switch b.Type {
					case "text":
						m.texts = append(m.texts, b.Text)
						if raw.Type == "user" && claudeHumanText(b.Text) != "" {
							m.txtBlk = true
						}
					case "image":
						if raw.Type == "user" {
							m.imgBlk = true
						}
					case "tool_use":
						m.toolUses = append(m.toolUses, claudeToolUse{id: b.ID, name: b.Name, input: b.Input})
						if b.ID != "" && b.Name != "" {
							f.toolNames[b.ID] = b.Name
						}
					case "tool_result":
						m.toolResults = append(m.toolResults, claudeToolResult{
							id:      b.ToolUseID,
							content: claudeBlockContentString(b.Content),
						})
					}
				}
			}
		}
		f.messages = append(f.messages, m)
	}
	return f
}

// --- index over the projects dir ---

type claudeSession struct {
	id       string // session uuid or agent file stem
	file     *claudeFile
	dirHint  string // slug dir name, used when no cwd in the transcript
	isAgent  bool
	meta     claudeAgentMeta
	fsParent string // session-id dir the subagents/ dir nests under
	parent   string // resolved parent session id (subagents only)
}

type claudeIndex struct {
	sessions map[string]*claudeSession
	agents   map[string]*claudeSession
}

func (idx *claudeIndex) lookup(id string) *claudeSession {
	if s, ok := idx.sessions[id]; ok {
		return s
	}
	return idx.agents[id]
}

func (idx *claudeIndex) all() []*claudeSession {
	out := make([]*claudeSession, 0, len(idx.sessions)+len(idx.agents))
	for _, s := range idx.sessions {
		out = append(out, s)
	}
	for _, s := range idx.agents {
		out = append(out, s)
	}
	return out
}

// scanIndex walks the root dir and parses every main and subagent transcript,
// reusing cached parses keyed by file mtime.
func (c *Provider) scanIndex() *claudeIndex {
	idx := &claudeIndex{
		sessions: map[string]*claudeSession{},
		agents:   map[string]*claudeSession{},
	}
	slugDirs, err := os.ReadDir(c.root)
	if err != nil {
		return idx
	}
	for _, slug := range slugDirs {
		if !slug.IsDir() {
			continue
		}
		slugPath := filepath.Join(c.root, slug.Name())
		entries, err := os.ReadDir(slugPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				sub := filepath.Join(slugPath, e.Name(), "subagents")
				c.scanAgentDir(idx, sub, slug.Name(), e.Name())
				continue
			}
			if !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			sid := strings.TrimSuffix(e.Name(), ".jsonl")
			idx.sessions[sid] = &claudeSession{
				id:      sid,
				file:    c.loadFile(filepath.Join(slugPath, e.Name())),
				dirHint: slug.Name(),
			}
		}
	}
	for _, a := range idx.agents {
		a.parent = idx.resolveAgentParent(a)
	}
	return idx
}

func (c *Provider) scanAgentDir(idx *claudeIndex, sub, slug, parentSID string) {
	entries, err := os.ReadDir(sub)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".jsonl")
		metaPath := filepath.Join(sub, stem+".meta.json")
		var meta claudeAgentMeta
		if data, err := os.ReadFile(metaPath); err == nil {
			_ = json.Unmarshal(data, &meta)
		}
		idx.agents[stem] = &claudeSession{
			id:       stem,
			file:     c.loadFile(filepath.Join(sub, e.Name())),
			dirHint:  slug,
			isAgent:  true,
			meta:     meta,
			fsParent: parentSID,
		}
	}
}

// resolveAgentParent prefers a tool_use id match in a main transcript, then
// falls back to the filesystem nesting (the session-id dir).
func (idx *claudeIndex) resolveAgentParent(a *claudeSession) string {
	if a.meta.ToolUseID != "" {
		for _, s := range idx.sessions {
			if _, ok := s.file.toolNames[a.meta.ToolUseID]; ok {
				return s.id
			}
		}
	}
	if a.fsParent != "" {
		return a.fsParent
	}
	return ""
}

// loadFile parses a transcript, caching by mtime.
func (c *Provider) loadFile(path string) *claudeFile {
	info, err := os.Stat(path)
	if err != nil {
		return &claudeFile{path: path, toolNames: map[string]string{}}
	}
	mtime := info.ModTime().UnixNano()
	c.mu.Lock()
	if cf, ok := c.cache[path]; ok && cf.mtime == mtime {
		c.mu.Unlock()
		return cf.parsed
	}
	c.mu.Unlock()
	f := parseClaudeFile(path)
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cachedFile{}
	}
	c.cache[path] = cachedFile{mtime: mtime, parsed: f}
	c.mu.Unlock()
	return f
}

func (c *Provider) findSessionFile(sid string) *claudeFile {
	idx := c.scanIndex()
	if s, ok := idx.sessions[sid]; ok {
		return s.file
	}
	if s, ok := idx.agents[sid]; ok {
		return s.file
	}
	return nil
}

// --- per-file derived data ---

// humanTrig classifies the human input a user line carries: plain-string text
// (text), an AskUserQuestion tool result (question), an ExitPlanMode tool
// result (approval), or an image-only content array (image). Assistant
// messages, tool outputs and isMeta noise lines are never human triggers.
func (f *claudeFile) humanTrig(m *claudeMsg) provider.Trig {
	if m == nil || m.role != "user" || m.meta {
		return provider.TrigNone
	}
	if m.human != "" {
		return provider.TrigText
	}
	for _, tr := range m.toolResults {
		switch f.toolNames[tr.id] {
		case "AskUserQuestion":
			return provider.TrigQuestion
		case "ExitPlanMode":
			return provider.TrigApproval
		}
	}
	if m.imgBlk && !m.txtBlk {
		return provider.TrigImage
	}
	return provider.TrigNone
}

func (f *claudeFile) calls() []provider.CallRec {
	calls := []provider.CallRec{}
	var prev *claudeMsg
	for _, m := range f.messages {
		if m.role == "assistant" && m.hasUsage {
			calls = append(calls, provider.CallRec{
				Mid: m.mid, Time: m.timeMS,
				In: m.usage.in, Cr: m.usage.cr, Cw: m.usage.cw,
				Out: m.usage.out, Reason: 0,
				Ctx: m.usage.in + m.usage.cr + m.usage.cw,
				Hu:  f.humanTrig(prev),
			})
		}
		prev = m
	}
	return calls
}

func (f *claudeFile) prompt() []string {
	texts := []string{}
	for _, m := range f.messages {
		if m.role == "user" && m.human != "" {
			texts = append(texts, m.human)
			total := 0
			for _, x := range texts {
				total += len([]rune(x))
			}
			if total > 20000 {
				break
			}
		}
	}
	return texts
}

func (f *claudeFile) stats() (in, out, cr, cw, maxctx int64, model string, beats []int64) {
	for _, m := range f.messages {
		if m.role != "assistant" || !m.hasUsage {
			continue
		}
		in += m.usage.in
		out += m.usage.out
		cr += m.usage.cr
		cw += m.usage.cw
		if ctx := m.usage.in + m.usage.cr + m.usage.cw; ctx > maxctx {
			maxctx = ctx
		}
		if m.model != "" {
			model = m.model
		}
		beats = append(beats, m.timeMS)
	}
	return in, out, cr, cw, maxctx, model, beats
}

// trigger builds the trigger response for a call mid, reporting ok=false when
// the mid is not in this transcript.
func (f *claudeFile) trigger(mid string) (*provider.TriggerResp, bool) {
	for i, m := range f.messages {
		if m.mid != mid {
			continue
		}
		if i == 0 {
			return &provider.TriggerResp{Case: "start", Time: nil, Parts: []any{}}, true
		}
		prev := f.messages[i-1]
		res := &provider.TriggerResp{Time: prev.timeMS, Parts: []any{}}
		if prev.role == "user" {
			res.Case = "user"
			res.Parts = f.userParts(prev)
		} else {
			res.Case = "assistant"
			res.Parts = f.assistantParts(prev)
		}
		return res, true
	}
	return nil, false
}

func (f *claudeFile) userParts(m *claudeMsg) []any {
	parts := []any{}
	if m.human != "" {
		t, cut := provider.Clip(m.human)
		parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
	}
	for _, tr := range m.toolResults {
		tool := f.toolNames[tr.id]
		if tool == "" {
			tool = "tool"
		}
		out, cutO := provider.Clip(tr.content)
		parts = append(parts, provider.TriggerPartTool{
			Kind: "tool", Tool: tool, Status: nil,
			Input: "", Output: out, CutO: cutO,
		})
	}
	for _, tu := range m.toolUses {
		in, cutI := provider.Clip(tu.input)
		parts = append(parts, provider.TriggerPartTool{
			Kind: "tool", Tool: tu.name, Status: nil,
			Input: in, CutI: cutI, Output: "",
		})
	}
	return parts
}

func (f *claudeFile) assistantParts(m *claudeMsg) []any {
	parts := []any{}
	for _, txt := range m.texts {
		if txt == "" {
			continue
		}
		t, cut := provider.Clip(txt)
		parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
	}
	for _, tu := range m.toolUses {
		in, cutI := provider.Clip(tu.input)
		parts = append(parts, provider.TriggerPartTool{
			Kind: "tool", Tool: tu.name, Status: nil,
			Input: in, CutI: cutI, Output: "",
		})
	}
	return parts
}

// --- tree assembly ---

func (c *Provider) claudeTree(nowMS int64) ([]*provider.SessionNode, error) {
	idx := c.scanIndex()
	nodes := map[string]*provider.SessionNode{}
	for sid, s := range idx.sessions {
		nodes[sid] = claudeNode(s, nil, nowMS)
	}
	for _, a := range idx.agents {
		parent, ok := nodes[a.parent]
		if !ok {
			continue // orphan subagent: skip
		}
		n := claudeNode(a, a.parent, nowMS)
		nodes[a.id] = n
		parent.Children = append(parent.Children, n)
	}

	var roots []*provider.SessionNode
	for _, n := range nodes {
		if n.Parent == nil {
			roots = append(roots, n)
		}
	}

	for id, n := range nodes {
		if n.Active {
			_, _, _, _, _, _, beats := idx.lookup(id).file.stats()
			sort.Slice(beats, func(i, j int) bool { return beats[i] < beats[j] })
			n.Beats = beats
		}
	}

	var fill func(n *provider.SessionNode) provider.SubtreeSums
	fill = func(n *provider.SessionNode) provider.SubtreeSums {
		t := provider.SubtreeSums{In: n.In, Out: n.Out, Reason: n.Reason, Cr: n.Cr, Cw: n.Cw}
		for _, ch := range n.Children {
			st := fill(ch)
			t.In += st.In
			t.Out += st.Out
			t.Reason += st.Reason
			t.Cr += st.Cr
			t.Cw += st.Cw
		}
		n.Subtree = &t
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Created > n.Children[j].Created
		})
		return t
	}
	for _, r := range roots {
		fill(r)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Created > roots[j].Created })
	if roots == nil {
		roots = []*provider.SessionNode{}
	}
	return roots, nil
}

func claudeNode(s *claudeSession, parent any, nowMS int64) *provider.SessionNode {
	in, out, cr, cw, maxctx, model, _ := s.file.stats()
	var modelV any
	if model != "" {
		modelV = model
	}
	updated := s.file.lastMS
	n := &provider.SessionNode{
		ID:       s.id,
		Parent:   parent,
		Title:    s.file.title,
		Agent:    nil,
		Model:    modelV,
		Dir:      s.file.cwd,
		MaxCtx:   maxctx,
		In:       in,
		Out:      out,
		Reason:   0,
		Cr:       cr,
		Cw:       cw,
		Created:  s.file.firstMS,
		Updated:  updated,
		Active:   updated > 0 && nowMS-updated < provider.ActiveWindowMS,
		Beats:    []int64{},
		Children: []*provider.SessionNode{},
		Source:   "claude",
	}
	if n.Dur = updated - n.Created; n.Dur < 0 {
		n.Dur = 0
	}
	if n.Dir == "" {
		n.Dir = s.dirHint
	}
	if s.isAgent {
		agent := s.meta.AgentType
		if agent == "" {
			agent = "tool"
		}
		n.Agent = agent
		if n.Title == "" {
			n.Title = s.meta.Description
			if n.Title == "" {
				n.Title = agent
			}
		}
	}
	if n.Title == "" {
		n.Title = "(untitled)"
	}
	return n
}
