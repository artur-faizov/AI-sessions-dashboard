package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// imageTokens is a rough token cost of one attached image (billed by pixels,
// which the rollout does not record).
const imageTokens = 1600

// contextRow is the payload of a rollout row that feeds the model's input:
// the session's base instructions, a message, a message from another agent or
// the tool schemas.
type contextRow struct {
	Type   string `json:"type"`
	Role   string `json:"role"`
	Author string `json:"author"`
	Base   struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
	Content []struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Encrypted string `json:"encrypted_content"`
	} `json:"content"`
	Tools []json.RawMessage `json:"tools"`
}

// tagKinds maps the opening tags of injected blocks to context item kinds;
// other tags are "reminders".
var tagKinds = map[string]string{
	"skills_instructions": "skills",
	"instructions":        "instructions",
}

// blockItem turns one text block of a developer or user message into a
// context item. Codex marks the blocks it injects with a fixed heading or an
// opening tag; an unmarked block is the system prompt (developer) or the
// human's prompt (user).
func blockItem(role, text string) provider.ContextItem {
	if role == "user" && realPrompt(text) {
		return provider.TextItem("user", "", text)
	}
	t := strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(t, "# AGENTS.md instructions"); ok {
		dir, _, _ := strings.Cut(rest, "\n")
		dir = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(dir), "for "))
		if dir == "" {
			dir = "AGENTS.md"
		}
		return provider.TextItem("instructions", dir, text)
	}
	if strings.HasPrefix(t, "## Memory") {
		return provider.TextItem("instructions", "memory", text)
	}
	if i := strings.IndexAny(t, ">\n"); i > 1 && t[0] == '<' && t[i] == '>' {
		kind := tagKinds[strings.ToLower(t[1:i])]
		if kind == "" {
			kind = "reminders"
		}
		return provider.TextItem(kind, strings.ReplaceAll(t[1:i], "_", " "), text)
	}
	return provider.TextItem("system", "", text)
}

// toolItems turns the tool schemas of an additional_tools row into one item
// per tool. Tools come grouped in namespaces; an MCP server is a namespace
// named mcp__<server>.
func toolItems(tools []json.RawMessage) []provider.ContextItem {
	var out []provider.ContextItem
	for _, raw := range tools {
		var ns struct {
			Name      string            `json:"name"`
			Namespace string            `json:"namespace"`
			Tools     []json.RawMessage `json:"tools"`
		}
		if json.Unmarshal(raw, &ns) != nil {
			continue
		}
		if ns.Tools == nil {
			// a tool outside any namespace
			out = append(out, provider.TextItem("tools", ns.Name, string(raw)))
			continue
		}
		if ns.Name == "" {
			ns.Name = ns.Namespace
		}
		for _, schema := range ns.Tools {
			var tool struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(schema, &tool)
			if server, ok := strings.CutPrefix(ns.Name, "mcp__"); ok {
				out = append(out, provider.TextItem("mcp", server+": "+tool.Name, string(schema)))
			} else {
				out = append(out, provider.TextItem("tools", ns.Name+"."+tool.Name, string(schema)))
			}
		}
	}
	return out
}

// firstCallContext tells what the input context of a rollout's first AI call
// was made of: everything the rollout stored before the call's usage row, or
// nil when that cannot be told. The system prompt is the unmarked developer
// block (a subagent gets a variant of the session's base instructions) and
// falls back to the base instructions for writers that do not store it.
// Writers that store the tool schemas (additional_tools) store the whole
// request, so no hidden rest is left; without them the schemas are the
// inferred rest.
func firstCallContext(path string, first provider.CallRec) *provider.ContextResp {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var base string
	var system, items []provider.ContextItem
	tools := false
	// a scratch parse tells which row completes the first call
	s := newSession()
	r := bufio.NewReaderSize(fh, 256*1024)
	for len(s.calls) == 0 {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return nil
		}
		s.parse(line, 0)
		var raw rawRecord
		var row contextRow
		if json.Unmarshal(line, &raw) != nil || json.Unmarshal(raw.Payload, &row) != nil {
			continue
		}
		switch raw.Type {
		case "session_meta":
			base = row.Base.Text
		case "token_usage_record":
			// a forked rollout starts with its parent's history, whose replies
			// and tool results are part of the context too
			if len(s.calls) == 0 {
				return nil
			}
		case "response_item":
			switch row.Type {
			case "additional_tools":
				tools = true
				items = append(items, toolItems(row.Tools)...)
			case "agent_message":
				// a task from another agent: a readable header and an encrypted
				// payload, sized by its base64 length (4 characters per 3 bytes)
				var text string
				var hidden int64
				for _, c := range row.Content {
					text += c.Text
					hidden += int64(len(c.Encrypted)) * 3 / 16
				}
				it := provider.TextItem("user", "from "+row.Author, text)
				it.Tokens += hidden
				items = append(items, it)
			case "message":
				if row.Role != "developer" && row.Role != "user" {
					continue
				}
				for _, c := range row.Content {
					if c.Type == "input_image" {
						items = append(items, provider.ContextItem{Kind: "files", Label: "image", Tokens: imageTokens, Est: provider.EstText})
					}
					if c.Type != "input_text" || strings.TrimSpace(c.Text) == "" {
						continue
					}
					if it := blockItem(row.Role, c.Text); it.Kind == "system" {
						system = append(system, it)
					} else {
						items = append(items, it)
					}
				}
			}
		}
	}
	if system == nil && base != "" {
		system = []provider.ContextItem{provider.TextItem("system", "", base)}
	}
	items = append(system, items...)
	if tools {
		items = provider.FitRecorded(first.Ctx, items)
	} else {
		items = provider.FitContext(first.Ctx, items)
	}
	return &provider.ContextResp{Mid: first.Mid, Ctx: first.Ctx, Items: items}
}

// CallContext tells what the input context of a session's first AI call was
// made of.
func (p *Provider) CallContext(mid string) (*provider.ContextResp, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.scan() {
		if len(s.calls) == 0 || s.calls[0].Mid != mid {
			continue
		}
		// any copy of the rollout that starts with this call tells its context
		for path, f := range p.files {
			if f.s.id == s.id && len(f.s.calls) > 0 && f.s.calls[0].Mid == mid {
				return firstCallContext(path, s.calls[0]), nil
			}
		}
	}
	return nil, nil
}
