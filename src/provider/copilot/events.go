package copilot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- per-session model ---

type itemKind int

const (
	kindUser itemKind = iota
	kindAssistant
	kindTool
)

// item is one entry of an agent stream, in file order.
type item struct {
	kind   itemKind
	timeMS int64

	// user
	text  string
	human bool // typed by the user (not skill/system-injected)

	// assistant
	mid       string
	model     string
	outTokens int64

	// tool execution (completed)
	toolName string
	toolIn   any
	toolOut  any
	status   string
}

// stream is the ordered event history of the root ("" key) or one subagent.
type stream struct {
	items          []*item
	firstMS        int64
	lastMS         int64
	lastModel      string
	assistantCount int
}

func (st *stream) touch(ms int64) {
	if ms == 0 {
		return
	}
	if st.firstMS == 0 || ms < st.firstMS {
		st.firstMS = ms
	}
	if ms > st.lastMS {
		st.lastMS = ms
	}
}

type subagent struct {
	id          string // toolCallId of the starting task call (== agentId of its events)
	parent      string // agentId of the requesting stream; "" = root session
	name        string
	displayName string
	model       string
	startMS     int64
	endMS       int64
	durationMS  int64
	completed   bool
}

// shutdownUsage is one session.shutdown event summed over its models.
type shutdownUsage struct {
	in, out, cr, cw, reason int64
}

type taskRequest struct {
	owner       string // agentId of the assistant message that requested it
	description string
	prompt      string
}

// session is a parsed events.jsonl plus workspace.yaml.
type session struct {
	id         string
	dir        string
	ws         workspace
	wsOK       bool
	startCwd   string
	startModel string
	startMS    int64
	firstMS    int64
	lastMS     int64

	streams   map[string]*stream
	subagents map[string]*subagent
	order     []string // subagent ids in start order
	shutdowns []shutdownUsage
	tasks     map[string]*taskRequest // toolCallId -> requesting call info
	mids      map[string]midRef
}

type midRef struct {
	agent string
	index int
}

func newSession(id, dir string) *session {
	return &session{
		id:        id,
		dir:       dir,
		streams:   map[string]*stream{"": {}},
		subagents: map[string]*subagent{},
		tasks:     map[string]*taskRequest{},
		mids:      map[string]midRef{},
	}
}

func (s *session) stream(agentID string) *stream {
	st, ok := s.streams[agentID]
	if !ok {
		st = &stream{}
		s.streams[agentID] = st
	}
	return st
}

// --- raw JSON shapes ---

type rawEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	AgentID   string          `json:"agentId"`
	Data      json.RawMessage `json:"data"`
}

type rawSessionStart struct {
	StartTime     string `json:"startTime"`
	SelectedModel string `json:"selectedModel"`
	Context       struct {
		Cwd string `json:"cwd"`
	} `json:"context"`
}

type rawUserMessage struct {
	Content string `json:"content"`
	Source  string `json:"source"`
}

type rawToolRequest struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
}

type rawAssistantMessage struct {
	MessageID    string           `json:"messageId"`
	Model        string           `json:"model"`
	OutputTokens int64            `json:"outputTokens"`
	ToolRequests []rawToolRequest `json:"toolRequests"`
}

type rawToolStart struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Arguments  any    `json:"arguments"`
}

type rawToolComplete struct {
	ToolCallID string `json:"toolCallId"`
	Success    *bool  `json:"success"`
	Result     *struct {
		Content any `json:"content"`
	} `json:"result"`
	Error any `json:"error"`
}

type rawSubagent struct {
	ToolCallID       string `json:"toolCallId"`
	AgentName        string `json:"agentName"`
	AgentDisplayName string `json:"agentDisplayName"`
	Model            string `json:"model"`
	DurationMS       int64  `json:"durationMs"`
}

type rawTokenCount struct {
	TokenCount int64 `json:"tokenCount"`
}

type rawTokenDetails struct {
	Input *rawTokenCount `json:"input"`
}

type rawShutdown struct {
	ModelMetrics map[string]struct {
		Usage struct {
			OutputTokens     int64 `json:"outputTokens"`
			CacheReadTokens  int64 `json:"cacheReadTokens"`
			CacheWriteTokens int64 `json:"cacheWriteTokens"`
			ReasoningTokens  int64 `json:"reasoningTokens"`
		} `json:"usage"`
		TokenDetails rawTokenDetails `json:"tokenDetails"`
	} `json:"modelMetrics"`
}

// --- parsing ---

func parseMS(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// parseSession streams events.jsonl into a session model. Lines that are not
// JSON, and event types the dashboard does not use, are skipped.
func parseSession(id, dir string) *session {
	s := newSession(id, dir)
	s.ws, s.wsOK = readWorkspace(filepath.Join(dir, "workspace.yaml"))
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return s
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256*1024)
	pending := map[string]*rawToolStart{}
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.apply(line, pending)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	return s
}

func (s *session) apply(line []byte, pending map[string]*rawToolStart) {
	var ev rawEvent
	if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
		return
	}
	ts := parseMS(ev.Timestamp)
	if ts > 0 {
		if s.firstMS == 0 || ts < s.firstMS {
			s.firstMS = ts
		}
		if ts > s.lastMS {
			s.lastMS = ts
		}
	}
	switch ev.Type {
	case "session.start":
		var d rawSessionStart
		if json.Unmarshal(ev.Data, &d) == nil {
			if s.startCwd == "" {
				s.startCwd = d.Context.Cwd
			}
			if s.startModel == "" {
				s.startModel = d.SelectedModel
			}
			if s.startMS == 0 {
				s.startMS = parseMS(d.StartTime)
			}
		}
	case "user.message":
		var d rawUserMessage
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		st := s.stream(ev.AgentID)
		st.touch(ts)
		st.items = append(st.items, &item{
			kind: kindUser, timeMS: ts, text: d.Content,
			human: d.Source == "" && strings.TrimSpace(d.Content) != "",
		})
	case "assistant.message":
		var d rawAssistantMessage
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		st := s.stream(ev.AgentID)
		st.touch(ts)
		mid := d.MessageID
		if mid == "" {
			mid = ev.ID
		}
		st.items = append(st.items, &item{
			kind: kindAssistant, timeMS: ts, mid: mid, model: d.Model, outTokens: d.OutputTokens,
		})
		st.assistantCount++
		if d.Model != "" {
			st.lastModel = d.Model
		}
		if mid != "" {
			s.mids[mid] = midRef{agent: ev.AgentID, index: len(st.items) - 1}
		}
		for _, tr := range d.ToolRequests {
			if tr.ToolCallID == "" {
				continue
			}
			t := &taskRequest{owner: ev.AgentID}
			var args struct {
				Description string `json:"description"`
				Prompt      string `json:"prompt"`
			}
			if json.Unmarshal(tr.Arguments, &args) == nil {
				t.description, t.prompt = args.Description, args.Prompt
			}
			s.tasks[tr.ToolCallID] = t
		}
	case "tool.execution_start":
		var d rawToolStart
		if json.Unmarshal(ev.Data, &d) == nil && d.ToolCallID != "" {
			pending[d.ToolCallID] = &d
			if _, ok := s.tasks[d.ToolCallID]; !ok {
				s.tasks[d.ToolCallID] = &taskRequest{owner: ev.AgentID}
			}
		}
		s.stream(ev.AgentID).touch(ts)
	case "tool.execution_complete":
		var d rawToolComplete
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		it := &item{kind: kindTool, timeMS: ts, status: "completed"}
		if start := pending[d.ToolCallID]; start != nil {
			it.toolName, it.toolIn = start.ToolName, start.Arguments
			delete(pending, d.ToolCallID)
		}
		if d.Success != nil && !*d.Success {
			it.status = "error"
		}
		if d.Result != nil {
			it.toolOut = d.Result.Content
		}
		if it.toolOut == nil && d.Error != nil {
			if m, ok := d.Error.(map[string]any); ok && m["message"] != nil {
				it.toolOut = m["message"]
			} else {
				it.toolOut = d.Error
			}
		}
		st := s.stream(ev.AgentID)
		st.touch(ts)
		st.items = append(st.items, it)
	case "subagent.started":
		var d rawSubagent
		if json.Unmarshal(ev.Data, &d) != nil || d.ToolCallID == "" {
			return
		}
		sa := s.subagents[d.ToolCallID]
		if sa == nil {
			sa = &subagent{id: d.ToolCallID}
			s.subagents[d.ToolCallID] = sa
			s.order = append(s.order, d.ToolCallID)
		}
		sa.name, sa.displayName, sa.model, sa.startMS = d.AgentName, d.AgentDisplayName, d.Model, ts
		s.stream(d.ToolCallID).touch(ts)
	case "subagent.completed":
		var d rawSubagent
		if json.Unmarshal(ev.Data, &d) != nil || d.ToolCallID == "" {
			return
		}
		sa := s.subagents[d.ToolCallID]
		if sa == nil {
			sa = &subagent{id: d.ToolCallID, name: d.AgentName, displayName: d.AgentDisplayName, model: d.Model}
			s.subagents[d.ToolCallID] = sa
			s.order = append(s.order, d.ToolCallID)
		}
		sa.completed, sa.endMS, sa.durationMS = true, ts, d.DurationMS
		s.stream(d.ToolCallID).touch(ts)
	case "session.shutdown":
		var d rawShutdown
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		var u shutdownUsage
		for _, m := range d.ModelMetrics {
			// Fresh input comes only from tokenDetails.input: usage.inputTokens
			// includes cache read/write and is never used.
			if m.TokenDetails.Input != nil {
				u.in += m.TokenDetails.Input.TokenCount
			}
			u.out += m.Usage.OutputTokens
			u.cr += m.Usage.CacheReadTokens
			u.cw += m.Usage.CacheWriteTokens
			u.reason += m.Usage.ReasoningTokens
		}
		s.shutdowns = append(s.shutdowns, u)
	}
}

// resolveParents sets each subagent's parent from the agentId of the
// assistant message that requested its task call. subagent.started itself
// carries the subagent's own id, so it cannot be used for nesting.
func (s *session) resolveParents() {
	for _, sa := range s.subagents {
		sa.parent = ""
		if t := s.tasks[sa.id]; t != nil && t.owner != sa.id {
			if _, ok := s.subagents[t.owner]; ok {
				sa.parent = t.owner
			}
		}
	}
	// Break any cycle by re-rooting its members.
	for _, sa := range s.subagents {
		seen := map[string]bool{sa.id: true}
		for p := sa.parent; p != ""; p = s.subagents[p].parent {
			if seen[p] {
				sa.parent = ""
				break
			}
			seen[p] = true
		}
	}
}

// --- discovery and caching ---

type cachedSession struct {
	mtime  int64
	size   int64
	wsTime int64
	parsed *session
}

// scan parses every <root>/session-state/<id>/events.jsonl, reusing cached
// parses keyed by path, mtime and size.
func (p *Provider) scan() []*session {
	entries, err := os.ReadDir(p.stateDir())
	if err != nil {
		return nil
	}
	var out []*session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s := p.loadSession(e.Name()); s != nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (p *Provider) loadSession(id string) *session {
	dir := filepath.Join(p.stateDir(), id)
	path := filepath.Join(dir, "events.jsonl")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil
	}
	var wsTime int64
	if wi, err := os.Stat(filepath.Join(dir, "workspace.yaml")); err == nil {
		wsTime = wi.ModTime().UnixNano()
	}
	mtime, size := info.ModTime().UnixNano(), info.Size()
	p.mu.Lock()
	if c, ok := p.cache[path]; ok && c.mtime == mtime && c.size == size && c.wsTime == wsTime {
		p.mu.Unlock()
		return c.parsed
	}
	p.mu.Unlock()
	s := parseSession(id, dir)
	s.resolveParents()
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]cachedSession{}
	}
	p.cache[path] = cachedSession{mtime: mtime, size: size, wsTime: wsTime, parsed: s}
	p.mu.Unlock()
	return s
}

// lookupNode resolves a node id ("<sid>" or "<sid>/<agentId>").
func (p *Provider) lookupNode(nodeID string) (*session, string, bool) {
	sid, agentID, _ := strings.Cut(nodeID, "/")
	if sid == "" || sid == "." || sid == ".." || strings.ContainsAny(sid, `\`) {
		return nil, "", false
	}
	s := p.loadSession(sid)
	if s == nil {
		return nil, "", false
	}
	if agentID != "" {
		if _, ok := s.subagents[agentID]; !ok {
			return nil, "", false
		}
	}
	return s, agentID, true
}
