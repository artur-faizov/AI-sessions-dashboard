package codex

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

type usage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Write  int64 `json:"cache_write_input_tokens"`
	Output int64 `json:"output_tokens"`
	Reason int64 `json:"reasoning_output_tokens"`
	Total  int64 `json:"total_tokens"`
}

func (u usage) normalized() provider.CallRec {
	i, o := max(0, u.Input), max(0, u.Output)
	cr := min(max(0, u.Cached), i)
	cw := min(max(0, u.Write), i-cr)
	reason := min(max(0, u.Reason), o)
	return provider.CallRec{In: i - cr - cw, Cr: cr, Cw: cw, Out: o - reason, Reason: reason, Ctx: i}
}
func (u usage) delta(prev usage) usage {
	return usage{Input: max(0, u.Input-prev.Input), Cached: max(0, u.Cached-prev.Cached), Write: max(0, u.Write-prev.Write), Output: max(0, u.Output-prev.Output), Reason: max(0, u.Reason-prev.Reason), Total: max(0, u.Total-prev.Total)}
}
func (u usage) decreased(p usage) bool {
	return u.Input < p.Input || u.Output < p.Output || u.Total < p.Total
}

type rawRecord struct {
	Timestamp json.RawMessage `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}
type tool struct {
	id, name      string
	input, output any
	start, end    int64
	mid           string
	complete      bool
}
type session struct {
	id, parent, dir, model, agent string
	first, last, started          int64
	busy                          bool
	calls                         []provider.CallRec
	prompts                       []string
	triggers                      map[string]*provider.TriggerResp
	tools                         map[string]*tool
	toolOrder                     []string
	seen                          map[string]int
	snapshots                     map[usage]int
	compatibility                 map[usage]int
	foreignCompatibility          map[usage]int
	checkpoint                    *usage
	pending                       *provider.TriggerResp
	hu                            provider.Trig
	response                      bool
	responseTrigger               *provider.TriggerResp
	responseHu                    provider.Trig
	texts                         []string
	responseTextSource            string
	responseTools                 []string
	lastMessage                   map[string]messageMark
}
type messageMark struct{ text, source string }

func newSession() *session {
	return &session{triggers: map[string]*provider.TriggerResp{}, tools: map[string]*tool{}, seen: map[string]int{}, snapshots: map[usage]int{}, compatibility: map[usage]int{}, foreignCompatibility: map[usage]int{}, lastMessage: map[string]messageMark{}}
}
func timestamp(v json.RawMessage) int64 {
	var s string
	if json.Unmarshal(v, &s) == nil {
		if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
			return t.UnixMilli()
		}
	}
	var n int64
	_ = json.Unmarshal(v, &n)
	return n
}
func (s *session) parse(line []byte, offset int64) {
	var r rawRecord
	if json.Unmarshal(line, &r) != nil {
		return
	}
	var p struct {
		Type        string          `json:"type"`
		ID          string          `json:"id"`
		Parent      string          `json:"parent_thread_id"`
		Cwd         string          `json:"cwd"`
		Model       string          `json:"model"`
		AgentRole   string          `json:"agent_role"`
		Nick        string          `json:"agent_nickname"`
		AgentPath   string          `json:"agent_path"`
		Timestamp   json.RawMessage `json:"timestamp"`
		Source      json.RawMessage `json:"source"`
		ResponseID  string          `json:"response_id"`
		ThreadID    string          `json:"thread_id"`
		Usage       *usage          `json:"usage"`
		ThreadUsage *usage          `json:"thread_token_usage"`
		Info        *struct {
			Total *usage `json:"total_token_usage"`
			Last  *usage `json:"last_token_usage"`
		} `json:"info"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Message     string          `json:"message"`
		CallID      string          `json:"call_id"`
		Name        string          `json:"name"`
		Namespace   string          `json:"namespace"`
		Arguments   json.RawMessage `json:"arguments"`
		Input       json.RawMessage `json:"input"`
		Output      json.RawMessage `json:"output"`
		Started     int64           `json:"started_at"`
		Completed   int64           `json:"completed_at"`
		Item        json.RawMessage `json:"item"`
		StartedMS   int64           `json:"started_at_ms"`
		CompletedMS int64           `json:"completed_at_ms"`
	}
	if json.Unmarshal(r.Payload, &p) != nil {
		return
	}
	t := timestamp(r.Timestamp)
	if t > 0 {
		if s.first == 0 || t < s.first {
			s.first = t
		}
		s.last = max(s.last, t)
	}
	switch r.Type {
	case "session_meta":
		if p.ID != "" {
			s.id = p.ID
		}
		s.parent = p.Parent
		s.dir = p.Cwd
		if st := timestamp(p.Timestamp); st > 0 {
			s.first = st
		}
		s.agent = agentName(p.AgentRole, p.Nick, p.AgentPath)
		var source struct {
			Sub struct {
				Spawn struct {
					Parent string `json:"parent_thread_id"`
					Role   string `json:"agent_role"`
					Nick   string `json:"agent_nickname"`
					Path   string `json:"agent_path"`
				} `json:"thread_spawn"`
			} `json:"subagent"`
		}
		if json.Unmarshal(p.Source, &source) == nil {
			sp := source.Sub.Spawn
			if s.parent == "" {
				s.parent = sp.Parent
			}
			if s.agent == "" {
				s.agent = agentName(sp.Role, sp.Nick, sp.Path)
			}
		}
	case "turn_context":
		if p.Model != "" {
			s.model = p.Model
		}
		if s.dir == "" {
			s.dir = p.Cwd
		}
	case "token_usage_record":
		// Forked transcripts may contain the parent's inherited history.
		if s.id != "" && p.ThreadID != "" && p.ThreadID != s.id {
			if p.ThreadUsage != nil {
				s.foreignCompatibility[*p.ThreadUsage]++
			}
			return
		}
		if s.id == "" {
			s.id = p.ThreadID
		}
		if p.Usage == nil {
			return
		}
		s.rich(p.ResponseID, *p.Usage, p.ThreadUsage, t, offset)
	case "response_item":
		switch p.Type {
		case "message":
			texts := []string{}
			image := false
			for _, c := range p.Content {
				if c.Type == "input_text" || c.Type == "output_text" {
					// Codex may prepend context/instruction blocks to the real
					// prompt in the same message. Filter each block before joining
					// so a wrapper cannot hide the user's following text.
					if p.Role == "user" && !realPrompt(c.Text) {
						continue
					}
					texts = append(texts, c.Text)
				}
				if c.Type == "input_image" {
					image = true
				}
			}
			s.message(p.Role, strings.Join(texts, "\n"), image, "response", t)
		case "function_call", "custom_tool_call":
			s.beginResponse()
			name := p.Name
			if p.Namespace != "" {
				name = p.Namespace + "." + name
			}
			in := p.Arguments
			if len(in) == 0 {
				in = p.Input
			}
			if p.CallID != "" && s.tools[p.CallID] == nil {
				s.tools[p.CallID] = &tool{id: p.CallID, name: name, input: decodeValue(in), start: t}
				s.toolOrder = append(s.toolOrder, p.CallID)
				s.responseTools = append(s.responseTools, p.CallID)
			}
		case "function_call_output", "custom_tool_call_output":
			s.toolOutput(p.CallID, decodeValue(p.Output), t)
		}
	case "event_msg":
		switch p.Type {
		case "token_count":
			if p.Info != nil && p.Info.Total != nil {
				s.legacy(*p.Info.Total, p.Info.Last, t, offset)
			}
		case "user_message":
			s.message("user", p.Message, false, "event", t)
		case "agent_message":
			s.message("assistant", p.Message, false, "event", t)
		case "task_started":
			s.busy = true
			s.started = t
			if p.Started > 0 {
				s.started = p.Started
			}
		case "task_complete", "turn_aborted":
			s.busy = false
			if p.Completed > 0 {
				s.last = max(s.last, p.Completed)
			}
		case "item_completed":
			var item struct {
				ID     string `json:"id"`
				CallID string `json:"call_id"`
			}
			_ = json.Unmarshal(p.Item, &item)
			id := item.CallID
			if id == "" {
				id = item.ID
			}
			if tool := s.tools[id]; tool != nil {
				if p.StartedMS > 0 {
					tool.start = p.StartedMS
				}
				if p.CompletedMS > 0 {
					tool.end = p.CompletedMS
				}
			}
		}
	}
}
func decodeValue(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	if str, ok := v.(string); ok {
		var obj map[string]any
		if json.Unmarshal([]byte(str), &obj) == nil && obj != nil {
			return obj
		}
	}
	return v
}
func agentName(role, nick, path string) string {
	if role != "" {
		return role
	}
	if nick != "" {
		return nick
	}
	if path != "" {
		return filepath.Base(path)
	}
	return ""
}
func realPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	for _, prefix := range []string{"<environment_context>", "# AGENTS.md instructions", "<INSTRUCTIONS>", "<instructions>"} {
		if strings.HasPrefix(t, prefix) {
			return false
		}
	}
	return true
}
func textPart(text string) provider.TriggerPartText {
	t, cut := provider.Clip(text)
	return provider.TriggerPartText{Kind: "text", Text: t, Cut: cut}
}
func (s *session) message(role, text string, image bool, source string, t int64) {
	if role != "user" && role != "assistant" {
		return
	}
	mark := s.lastMessage[role]
	signature := text
	if mark.text == signature && mark.source != source {
		delete(s.lastMessage, role)
		if role == "user" && image && s.pending != nil && s.pending.Case == "user" {
			s.hu = provider.TrigImage
			s.pending.Parts = append(s.pending.Parts, textPart("[Image attached]"))
		}
		return
	}
	s.lastMessage[role] = messageMark{signature, source}
	if role == "assistant" {
		if strings.TrimSpace(text) != "" {
			s.beginResponse()
			// Compatibility messages summarize the same visible response and
			// need not have exactly the same chunk boundaries.
			if source == "event" && s.responseTextSource == "response" {
				return
			}
			if source == "response" && s.responseTextSource == "event" {
				s.texts = nil
			}
			s.responseTextSource = source
			s.texts = append(s.texts, text)
		}
		return
	}
	if !realPrompt(text) && !image {
		return
	}
	parts := []any{}
	if realPrompt(text) {
		parts = append(parts, textPart(text))
		s.prompts = append(s.prompts, text)
	}
	if image {
		parts = append(parts, textPart("[Image attached]"))
	}
	s.pending = &provider.TriggerResp{Case: "user", Time: t, Parts: parts}
	s.hu = provider.TrigText
	if image {
		s.hu = provider.TrigImage
	}
}
func (s *session) beginResponse() {
	if s.response {
		return
	}
	s.response = true
	s.responseTrigger = s.pending
	s.responseHu = s.hu
	s.pending = nil
	s.hu = provider.TrigNone
}
func askTool(name string) bool {
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return provider.IsAskTool(name) || name == "request_user_input" || name == "request_user_input_async"
}
func toolPart(t *tool) provider.TriggerPartTool {
	i, ci := provider.Clip(t.input)
	o, co := provider.Clip(t.output)
	status := "running"
	if t.complete {
		status = "completed"
	}
	return provider.TriggerPartTool{Kind: "tool", Tool: t.name, Status: status, Input: i, CutI: ci, Output: o, CutO: co}
}
func (s *session) toolOutput(id string, output any, t int64) {
	tool := s.tools[id]
	if tool == nil {
		return
	}
	tool.output = output
	tool.end = max(tool.start, t)
	tool.complete = true
	if s.pending == nil || s.pending.Case != "assistant" {
		s.pending = &provider.TriggerResp{Case: "assistant", Time: t, Parts: []any{}}
		s.hu = provider.TrigNone
	}
	s.pending.Time = t
	s.pending.Parts = append(s.pending.Parts, toolPart(tool))
	if askTool(tool.name) {
		s.hu = provider.TrigQuestion
	}
}
func (s *session) rich(id string, u usage, total *usage, t, offset int64) {
	if id == "" {
		id = fmt.Sprintf("offset:%d", offset)
	}
	mid := "codex:" + s.id + ":" + id
	if _, ok := s.seen[mid]; ok {
		return
	}
	// A compatibility row may precede the richer record in older writers.
	if total != nil {
		if i, ok := s.snapshots[*total]; ok && strings.Contains(s.calls[i].Mid, ":legacy:") {
			old := s.calls[i]
			c := u.normalized()
			c.Mid = mid
			c.Time = t
			c.Model = old.Model
			c.Hu = old.Hu
			c.Ask = old.Ask
			s.calls[i] = c
			for _, tool := range s.tools {
				if tool.mid == old.Mid {
					tool.mid = mid
				}
			}
			s.triggers[mid] = s.triggers[old.Mid]
			delete(s.triggers, old.Mid)
			s.seen[mid] = i
			v := *total
			s.checkpoint = &v
			return
		}
	}
	i := s.emit(mid, u, t)
	s.seen[mid] = i
	if total != nil {
		v := *total
		s.checkpoint = &v
		s.snapshots[v] = i
		s.compatibility[v]++
	}
}
func (s *session) legacy(total usage, last *usage, t, offset int64) {
	if s.foreignCompatibility[total] > 0 {
		s.foreignCompatibility[total]--
		return
	}
	prev := s.checkpoint
	if s.compatibility[total] > 0 {
		s.compatibility[total]--
		// rich already installed this checkpoint. A delayed copy must not
		// move it behind a newer rich response and inflate a later delta.
		return
	}
	v := total
	s.checkpoint = &v
	if prev != nil && total == *prev {
		return
	}
	if prev != nil && total.decreased(*prev) {
		s.snapshots = map[usage]int{}
	}
	var u usage
	if last != nil {
		u = *last
	} else if prev != nil && !total.decreased(*prev) {
		u = total.delta(*prev)
	} else if prev == nil {
		u = total
	} else {
		return
	}
	i := s.emit(fmt.Sprintf("codex:%s:legacy:%d", s.id, offset), u, t)
	s.snapshots[total] = i
}
func (s *session) emit(mid string, u usage, t int64) int {
	if !s.response {
		s.beginResponse()
	}
	c := u.normalized()
	c.Mid = mid
	c.Time = t
	c.Model = s.model
	c.Hu = s.responseHu
	tr := s.responseTrigger
	if tr == nil {
		tr = &provider.TriggerResp{Case: "start", Parts: []any{}}
	} else {
		tr = cloneTrigger(tr)
	}
	for _, text := range s.texts {
		tr.Reply = append(tr.Reply, textPart(text))
	}
	for _, id := range s.responseTools {
		tool := s.tools[id]
		tool.mid = mid
		if askTool(tool.name) {
			c.Ask = true
			tr.Reply = append(tr.Reply, toolPart(tool))
		}
	}
	s.calls = append(s.calls, c)
	s.triggers[mid] = tr
	s.response = false
	s.responseTrigger = nil
	s.responseHu = provider.TrigNone
	s.texts = nil
	s.responseTextSource = ""
	s.responseTools = nil
	return len(s.calls) - 1
}
