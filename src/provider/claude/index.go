package claude

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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

func (u claudeUsage) max(o claudeUsage) claudeUsage {
	return claudeUsage{max(u.in, o.in), max(u.out, o.out), max(u.cr, o.cr), max(u.cw, o.cw)}
}

type claudeToolUse struct {
	id    string
	name  string
	input any
}

type claudeToolResult struct {
	id      string
	content string
	isErr   bool
}

type claudeMsg struct {
	mid         string // top-level line uuid
	role        string // "user" or "assistant"
	timeMS      int64
	endMS       int64 // first line of a request: timestamp of its last line
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

const claudeTitleMaxRunes = 80

// claudeFile is a parsed transcript (main session or subagent).
type claudeFile struct {
	path       string
	messages   []*claudeMsg
	toolNames  map[string]string // tool_use id -> tool name
	toolInputs map[string]any    // tool_use id -> tool input
	title      string
	cwd        string
	firstMS    int64
	lastMS     int64
	// continuedIn names the session Claude Code continued this one under;
	// that transcript opens with a copy of this one's history.
	continuedIn string
}

// --- raw JSON shapes ---

type claudeRawLine struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	RequestID string `json:"requestId"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
	IsMeta    bool   `json:"isMeta"`
	AITitle   string `json:"aiTitle"`
	// ContinuedIn is set on a "continued-in" line.
	ContinuedIn string        `json:"continuedInSessionId"`
	Message     *claudeRawMsg `json:"message"`
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
	IsError   bool            `json:"is_error"`
}

// claudeAgentMeta is a subagent's <stem>.meta.json. A nested agent (launched
// by another subagent) names its launcher in ParentAgentID, without the
// "agent-" prefix of the file stem; older Claude Code versions omit it.
type claudeAgentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	ToolUseID     string `json:"toolUseId"`
	ParentAgentID string `json:"parentAgentId"`
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
	"<bash-stderr>", "<task-notification>", "<agent-message",
	"Another Claude session sent a message:",
}

var (
	claudeCmdNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	claudeCmdArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// claudeSlashCommand renders a slash command that carries arguments (e.g.
// "/openspec-explore <question>") as human text; bare commands such as
// "/model" stay noise.
func claudeSlashCommand(t string) string {
	name := claudeCmdNameRe.FindStringSubmatch(t)
	args := claudeCmdArgsRe.FindStringSubmatch(t)
	if name == nil || args == nil {
		return ""
	}
	a := strings.TrimSpace(args[1])
	if a == "" {
		return ""
	}
	n := strings.TrimSpace(name[1])
	if !strings.HasPrefix(n, "/") {
		n = "/" + n
	}
	return n + " " + a
}

// claudeHumanText returns the real human text of a plain-string user content.
func claudeHumanText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	if cmd := claudeSlashCommand(t); cmd != "" {
		return cmd
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
	f := &claudeFile{path: path, toolNames: map[string]string{}, toolInputs: map[string]any{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	seen := map[string]bool{}
	seenReq := map[string]*claudeMsg{}
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
		case "continued-in":
			f.continuedIn = raw.ContinuedIn
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
		m := &claudeMsg{mid: raw.UUID, role: raw.Type, timeMS: ts}
		dupReq := false
		if raw.Type == "assistant" {
			if raw.UUID != "" {
				if seen[raw.UUID] {
					continue // retry duplicate: keep first
				}
				seen[raw.UUID] = true
			}
			// One API request is written as multiple assistant lines (one per
			// content block), each repeating the usage block. Count usage once
			// per request, on its first line, to avoid overcounting. Early lines
			// may carry a partial streaming output count (e.g. 8 instead of 3020),
			// so keep the max of each field seen across the request's lines.
			if raw.RequestID != "" {
				if first := seenReq[raw.RequestID]; first != nil {
					dupReq = true
					if ts > first.endMS {
						first.endMS = ts
					}
					if u := raw.Message.Usage; u != nil {
						first.usage = first.usage.max(claudeUsage{
							in:  u.InputTokens,
							out: u.OutputTokens,
							cr:  u.CacheReadInputTokens,
							cw:  u.CacheCreationInputTokens,
						})
						first.hasUsage = first.usage.nonzero() && first.model != "<synthetic>"
					}
				} else {
					seenReq[raw.RequestID] = m
					m.endMS = ts
				}
			}
			m.model = raw.Message.Model
			if !dupReq {
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
		}
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
						if b.ID != "" {
							f.toolInputs[b.ID] = b.Input
						}
					case "tool_result":
						m.toolResults = append(m.toolResults, claudeToolResult{
							id:      b.ToolUseID,
							content: claudeBlockContentString(b.Content),
							isErr:   b.IsError,
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
	parent   string // resolved launcher: a session id, or an agent id when nested (subagents only)
}

type claudeIndex struct {
	sessions map[string]*claudeSession
	agents   map[string]*claudeSession
	merged   map[string]string // continued session id -> the id it is shown under
}

// canon maps a continued session's id to the session it is shown under.
func (idx *claudeIndex) canon(id string) string {
	if t, ok := idx.merged[id]; ok {
		return t
	}
	return id
}

func (idx *claudeIndex) lookup(id string) *claudeSession {
	if s, ok := idx.sessions[idx.canon(id)]; ok {
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
		merged:   map[string]string{},
	}
	slugDirs, err := os.ReadDir(c.root.Get())
	if err != nil {
		return idx
	}
	for _, slug := range slugDirs {
		if !slug.IsDir() {
			continue
		}
		slugPath := filepath.Join(c.root.Get(), slug.Name())
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
	idx.mergeContinued()
	for _, a := range idx.agents {
		a.fsParent = idx.canon(a.fsParent)
		a.parent = idx.resolveAgentParent(a)
	}
	// Break any cycle (corrupt meta: agents naming each other as launcher) by
	// hanging the agents caught in it off their session, so every parent
	// chain ends at a session.
	for _, a := range idx.agents {
		seen := map[string]bool{a.id: true}
		for p := idx.agents[a.parent]; p != nil; p = idx.agents[p.parent] {
			if seen[p.id] {
				a.parent = a.fsParent
				break
			}
			seen[p.id] = true
		}
	}
	return idx
}

// mergeContinued shows a session Claude Code continued under a new id as one
// session under the latest id: the transcripts of the chain are joined, and
// the history each one copies from the one before is counted once.
func (idx *claudeIndex) mergeContinued() {
	prev := map[string]*claudeSession{} // session -> the one continued in it
	for id, s := range idx.sessions {
		if t := s.file.continuedIn; t != "" && t != id && idx.sessions[t] != nil {
			prev[t] = s
		}
	}
	// Walk back from each chain's latest session (one not continued itself);
	// a loop of corrupt markers has no latest one and stays apart.
	for id, last := range idx.sessions {
		if prev[id] == nil || last.file.continuedIn != "" && idx.sessions[last.file.continuedIn] != nil {
			continue
		}
		files := []*claudeFile{last.file}
		for p := prev[id]; p != nil && p != last; p = prev[p.id] {
			if _, done := idx.merged[p.id]; done {
				break
			}
			idx.merged[p.id] = id
			files = append([]*claudeFile{p.file}, files...)
		}
		last.file = joinClaudeFiles(files)
	}
	for old := range idx.merged {
		delete(idx.sessions, old)
	}
}

// joinClaudeFiles joins the transcripts of one continued conversation, oldest
// first, skipping lines a later transcript copied from an earlier one. The
// cached parses are left untouched.
func joinClaudeFiles(fs []*claudeFile) *claudeFile {
	j := &claudeFile{path: fs[0].path, toolNames: map[string]string{}, toolInputs: map[string]any{}}
	seen := map[string]bool{}
	for _, f := range fs {
		for _, m := range f.messages {
			if m.mid != "" {
				if seen[m.mid] {
					continue
				}
				seen[m.mid] = true
			}
			j.messages = append(j.messages, m)
		}
		maps.Copy(j.toolNames, f.toolNames)
		maps.Copy(j.toolInputs, f.toolInputs)
		if f.title != "" {
			j.title = f.title
		}
		if j.cwd == "" {
			j.cwd = f.cwd
		}
		if f.firstMS > 0 && (j.firstMS == 0 || f.firstMS < j.firstMS) {
			j.firstMS = f.firstMS
		}
		j.lastMS = max(j.lastMS, f.lastMS)
	}
	return j
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

// resolveAgentParent finds the transcript that launched a subagent: the agent
// its meta names as launcher, else the main transcript holding the launching
// tool_use, else an agent transcript holding it (a nested agent from before
// parentAgentId), else the filesystem nesting (the session-id dir). Agents of
// every depth sit in their session's one subagents/ dir.
func (idx *claudeIndex) resolveAgentParent(a *claudeSession) string {
	if pid := a.meta.ParentAgentID; pid != "" {
		if p, ok := idx.agents["agent-"+pid]; ok && p != a {
			return p.id
		}
	}
	if id := a.meta.ToolUseID; id != "" {
		// A resumed session copies its history, launch lines included, so the
		// session whose dir holds the agent comes first.
		if s, ok := idx.sessions[a.fsParent]; ok {
			if _, ok := s.file.toolNames[id]; ok {
				return s.id
			}
		}
		if s := claudeLauncher(idx.sessions, id, func(*claudeSession) bool { return true }); s != nil {
			return s.id
		}
		// A fork's transcript repeats its own launch, so the agent itself
		// never counts as its launcher.
		if s := claudeLauncher(idx.agents, id, func(s *claudeSession) bool {
			return s != a && s.fsParent == a.fsParent
		}); s != nil {
			return s.id
		}
	}
	return a.fsParent
}

// claudeLauncher picks, among the transcripts that pass ok, the one holding
// tool_use id. Several may hold it (copied history): the earliest started
// wins, then the lowest id, so the choice does not depend on map order.
func claudeLauncher(ss map[string]*claudeSession, id string, ok func(*claudeSession) bool) *claudeSession {
	var best *claudeSession
	for _, s := range ss {
		if _, has := s.file.toolNames[id]; !has || !ok(s) {
			continue
		}
		if best == nil || s.file.firstMS < best.file.firstMS ||
			(s.file.firstMS == best.file.firstMS && s.id < best.id) {
			best = s
		}
	}
	return best
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
	if s := c.scanIndex().lookup(sid); s != nil {
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

// asksHuman reports whether an assistant line calls AskUserQuestion.
func (m *claudeMsg) asksHuman() bool {
	for _, tu := range m.toolUses {
		if provider.IsAskTool(tu.name) {
			return true
		}
	}
	return false
}

func (f *claudeFile) calls() []provider.CallRec {
	calls := []provider.CallRec{}
	var prev, lastIn *claudeMsg // lastIn: last line not written by the assistant
	// A request spans several assistant lines (one per content block) and
	// only its first line carries usage, so a question on a later line still
	// belongs to the last call. A call also asks when it ends the turn (a
	// human message or the transcript end follows) with a question in its text.
	last := -1
	var texts []string
	endTurn := func() {
		if last >= 0 && provider.AsksInText(texts) {
			calls[last].Ask = true
		}
	}
	for _, m := range f.messages {
		if m.role == "user" && !m.meta {
			if t := f.humanTrig(m); t == provider.TrigText || t == provider.TrigImage {
				endTurn()
			}
			last, texts = -1, nil
		}
		if m.role == "assistant" && m.hasUsage {
			// The request runs from the last input line (user text or tool
			// result) to the last line of its streamed response.
			var dur, end int64
			if lastIn != nil && m.endMS > lastIn.timeMS {
				dur, end = m.endMS-lastIn.timeMS, m.endMS
			}
			calls = append(calls, provider.CallRec{
				Mid: m.mid, Time: m.timeMS,
				In: m.usage.in, Cr: m.usage.cr, Cw: m.usage.cw,
				Out: m.usage.out, Reason: 0,
				Ctx:   m.usage.in + m.usage.cr + m.usage.cw,
				Hu:    f.humanTrig(prev),
				Model: m.model, Dur: dur, End: end,
			})
			last, texts = len(calls)-1, nil
		}
		if m.role == "assistant" && last >= 0 {
			texts = append(texts, m.texts...)
			if m.asksHuman() {
				calls[last].Ask = true
			}
		}
		prev = m
		if m.role != "assistant" {
			lastIn = m
		}
	}
	endTurn()
	return calls
}

// issuers maps tool_use id -> the AI call (its first line's mid) whose
// response carried that tool_use. A request spans several assistant lines and
// only the first carries usage, so later lines belong to the last call.
func (f *claudeFile) issuers() map[string]string {
	out := map[string]string{}
	cur := ""
	for _, m := range f.messages {
		if m.role != "assistant" {
			continue
		}
		if m.hasUsage {
			cur = m.mid
		}
		for _, tu := range m.toolUses {
			out[tu.id] = cur
		}
	}
	return out
}

// segments lists the spans a transcript spent in tools and waiting for the
// human, from a tool_use line to its tool_result line. Subagent launches are
// left out: the launch returns at once, the run itself is a child transcript.
// When the session is live (nowMS > 0), a tool_use still without its
// tool_result is running and its span ends at nowMS.
func (f *claudeFile) segments(nowMS int64) []provider.Seg {
	segs := []provider.Seg{}
	starts := map[string]int64{}
	inputs := map[string]any{}
	var open []string
	mids := f.issuers()
	add := func(id string, end int64, isErr bool, out any) {
		st := starts[id]
		if end <= st {
			return
		}
		name := f.toolNames[id]
		kind := provider.SegTool
		switch name {
		case "AskUserQuestion", "ExitPlanMode":
			kind = provider.SegHuman
		case "Agent", "Task":
			return
		}
		segs = append(segs, provider.Seg{
			Kind: kind, Start: st, End: end,
			Mid: mids[id], Name: name, Err: isErr, Run: out == nil,
			Input:  provider.ToolInput(inputs[id]),
			Output: provider.ToolOutput(out),
		})
	}
	for _, m := range f.messages {
		for _, tu := range m.toolUses {
			starts[tu.id] = m.timeMS
			inputs[tu.id] = tu.input
			open = append(open, tu.id)
		}
		if m.role != "user" {
			continue
		}
		for _, tr := range m.toolResults {
			if _, ok := starts[tr.id]; !ok {
				continue
			}
			add(tr.id, m.timeMS, tr.isErr, tr.content)
			delete(starts, tr.id)
		}
	}
	if nowMS > 0 {
		for _, id := range open {
			if _, ok := starts[id]; ok {
				add(id, nowMS, false, nil)
			}
		}
	}
	return segs
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

// promptTitle derives a fallback title from the first human prompt: its first
// non-empty line, clipped to claudeTitleMaxRunes. Empty when there is none.
func (f *claudeFile) promptTitle() string {
	for _, m := range f.messages {
		if m.role != "user" || m.human == "" {
			continue
		}
		for _, line := range strings.Split(m.human, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if r := []rune(line); len(r) > claudeTitleMaxRunes {
				line = string(r[:claudeTitleMaxRunes]) + "…"
			}
			return line
		}
	}
	return ""
}

func (f *claudeFile) stats() (in, out, cr, cw, maxctx int64, model string, beats []provider.Beat, byModel map[string]*provider.SubtreeSums) {
	byModel = map[string]*provider.SubtreeSums{}
	var lastIn *claudeMsg // the request goes out right after the last input line
	for _, m := range f.messages {
		if m.role != "assistant" {
			lastIn = m
		}
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
		provider.ByModelAdd(byModel, m.model, &provider.SubtreeSums{
			In: m.usage.in, Out: m.usage.out,
			Cr: m.usage.cr, Cw: m.usage.cw,
		})
		b := provider.Beat{m.timeMS, max(m.endMS, m.timeMS)}
		if lastIn != nil && lastIn.timeMS < b[0] {
			b[0] = lastIn.timeMS
		}
		beats = append(beats, b)
	}
	return in, out, cr, cw, maxctx, model, beats, byModel
}

// trigger builds the trigger response for a call mid, reporting ok=false when
// the mid is not in this transcript.
func (f *claudeFile) trigger(mid string) (*provider.TriggerResp, bool) {
	for i, m := range f.messages {
		if m.mid != mid {
			continue
		}
		if i == 0 {
			return &provider.TriggerResp{Case: "start", Time: nil, Parts: []any{}, Reply: f.reply(i)}, true
		}
		// A slash command writes its expansion as isMeta lines right after
		// the typed prompt; show the prompt rather than the empty expansion.
		j := i - 1
		for j > 0 && f.messages[j].role == "user" && f.messages[j].meta && f.messages[j-1].role == "user" {
			j--
		}
		prev := f.messages[j]
		res := &provider.TriggerResp{Time: prev.timeMS, Parts: []any{}, Reply: f.reply(i)}
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

// reply collects what the call starting at messages[i] showed the human: the
// text and AskUserQuestion blocks of its request lines (the call's line and
// the usage-less lines after it), or nil when there are none.
func (f *claudeFile) reply(i int) []any {
	var parts []any
	for j := i; j < len(f.messages); j++ {
		m := f.messages[j]
		if m.role != "assistant" || (j > i && m.hasUsage) {
			break
		}
		for _, txt := range m.texts {
			if strings.TrimSpace(txt) == "" {
				continue
			}
			t, cut := provider.Clip(txt)
			parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
		}
		for _, tu := range m.toolUses {
			if provider.IsAskTool(tu.name) {
				in, cutI := provider.Clip(tu.input)
				parts = append(parts, provider.TriggerPartTool{Kind: "tool", Tool: tu.name, Input: in, CutI: cutI})
			}
		}
	}
	return parts
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
		in, cutI := provider.Clip(provider.InputText(f.toolInputs[tr.id]))
		out, cutO := provider.Clip(tr.content)
		parts = append(parts, provider.TriggerPartTool{
			Kind: "tool", Tool: tool, Status: nil,
			Input: in, CutI: cutI, Output: out, CutO: cutO,
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
	launched := map[string][]*claudeSession{} // parent id -> its subagents
	for _, a := range idx.agents {
		launched[a.parent] = append(launched[a.parent], a)
	}
	// Subagents nest under their launcher at any depth, walked down from the
	// session; one whose chain does not reach a shown session is an orphan
	// and is skipped.
	var attach func(parent *provider.SessionNode)
	attach = func(parent *provider.SessionNode) {
		for _, a := range launched[parent.ID] {
			n := claudeNode(a, parent.ID, nowMS)
			// group with the parent's project even if the agent ran in a subdir
			n.Dir = parent.Dir
			nodes[a.id] = n
			parent.Children = append(parent.Children, n)
			attach(n)
		}
	}
	for sid, s := range idx.sessions {
		if s.file.title == "" && s.file.promptTitle() == "" {
			continue // no user request (opened and closed): hide
		}
		n := claudeNode(s, nil, nowMS)
		nodes[sid] = n
		attach(n)
	}

	var roots []*provider.SessionNode
	for _, n := range nodes {
		if n.Parent == nil {
			roots = append(roots, n)
		}
	}

	provider.DropClosed(roots, provider.ProcDirs("claude"))
	live := c.liveStatuses()
	for _, r := range roots {
		if l, ok := live[r.ID]; ok && r.Active {
			r.State = liveState(l, idx.lookup(r.ID).file)
		}
	}
	for id, n := range nodes {
		if n.Active || nowMS-n.Updated < provider.BeatWindowMS {
			f := idx.lookup(id).file
			_, _, _, _, _, _, beats, _ := f.stats()
			sort.Slice(beats, func(i, j int) bool { return beats[i][0] < beats[j][0] })
			// A busy session whose transcript ends on an input line has its
			// next request in flight.
			if k := len(f.messages); n.State == provider.StateBusy && k > 0 && f.messages[k-1].role != "assistant" {
				beats = append(beats, provider.Beat{f.messages[k-1].timeMS, 0})
			}
			if beats == nil {
				beats = []provider.Beat{}
			}
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
			provider.ByModelMerge(n.ByModel, ch.ByModel)
		}
		n.Subtree = &t
		claudeSortNodes(n.Children)
		return t
	}
	for _, r := range roots {
		fill(r)
	}
	claudeSortNodes(roots)
	if roots == nil {
		roots = []*provider.SessionNode{}
	}
	return roots, nil
}

// claudeSortNodes orders nodes newest first. A resumed session copies its
// history and so shares its start time with the one it continues: the id
// settles such ties, else the two would swap places from scan to scan.
func claudeSortNodes(ns []*provider.SessionNode) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Created != ns[j].Created {
			return ns[i].Created > ns[j].Created
		}
		return ns[i].ID < ns[j].ID
	})
}

func claudeNode(s *claudeSession, parent any, nowMS int64) *provider.SessionNode {
	in, out, cr, cw, maxctx, model, _, byModel := s.file.stats()
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
		Beats:    []provider.Beat{},
		Children: []*provider.SessionNode{},
		Subtree:  nil,
		ByModel:  byModel,
		Source:   "claude",
	}
	if n.Dur = updated - n.Created; n.Dur < 0 {
		n.Dur = 0
	}
	n.Dur = provider.ActiveDur(n.Dur, s.file.calls())
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
		n.Title = s.file.promptTitle()
	}
	if n.Title == "" {
		n.Title = "(untitled)"
	}
	return n
}
