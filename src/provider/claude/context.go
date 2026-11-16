package claude

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// imageTokens is a rough token cost of one attached image (Claude bills by
// pixels, which the transcript does not record).
const imageTokens = 1600

// claudeAttachLine is a transcript "attachment" line: context Claude Code
// injected into the conversation. Rendered is the exact text sent to the
// model; prompt_snapshot attachments carry the system prompt instead.
type claudeAttachLine struct {
	Attachment struct {
		Type         string   `json:"type"`
		HookName     string   `json:"hookName"`
		Content      any      `json:"content"`
		SystemPrompt []string `json:"systemPrompt"`
		Files        []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
		Filename string `json:"filename"`
		Path     string `json:"path"`
	} `json:"attachment"`
	Rendered []struct {
		Content string `json:"content"`
	} `json:"rendered"`
}

// claudeAttachKinds maps attachment types to context item kinds; other types
// are "reminders".
var claudeAttachKinds = map[string]string{
	"instructions":            "instructions",
	"nested_memory":           "instructions",
	"skill_listing":           "skills",
	"agent_listing_delta":     "agents",
	"hook_success":            "hooks",
	"hook_additional_context": "hooks",
	"file":                    "files",
	"deferred_tools_delta":    "tools",
}

// attachItems turns one attachment line into context items.
func attachItems(a *claudeAttachLine) []provider.ContextItem {
	typ := a.Attachment.Type
	if typ == "prompt_snapshot" {
		if len(a.Attachment.SystemPrompt) == 0 {
			return nil
		}
		return []provider.ContextItem{provider.TextItem("system", "", strings.Join(a.Attachment.SystemPrompt, "\n\n"))}
	}
	var parts []string
	for _, r := range a.Rendered {
		parts = append(parts, r.Content)
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		if s, ok := a.Attachment.Content.(string); ok {
			text = s
		}
	}
	if text == "" {
		return nil
	}
	kind := claudeAttachKinds[typ]
	if kind == "" {
		kind = "reminders"
	}
	label := typ
	switch {
	case typ == "instructions" && len(a.Attachment.Files) > 0:
		paths := make([]string, len(a.Attachment.Files))
		for i, f := range a.Attachment.Files {
			paths[i] = f.Path
		}
		label = strings.Join(paths, ", ")
	case a.Attachment.HookName != "":
		label = a.Attachment.HookName
	case a.Attachment.Path != "":
		label = a.Attachment.Path
	case a.Attachment.Filename != "":
		label = a.Attachment.Filename
	}
	return []provider.ContextItem{provider.TextItem(kind, label, text)}
}

// userItems turns the content of a user line sent before the first call into
// context items: human text, injected noise (command output, reminders),
// tool results and images.
func userItems(raw *claudeRawLine) []provider.ContextItem {
	textItem := func(s string) provider.ContextItem {
		if !raw.IsMeta && claudeHumanText(s) != "" {
			return provider.TextItem("user", "", s)
		}
		return provider.TextItem("reminders", "", s)
	}
	var s string
	if json.Unmarshal(raw.Message.Content, &s) == nil {
		if s == "" {
			return nil
		}
		return []provider.ContextItem{textItem(s)}
	}
	var blocks []claudeRawBlock
	if json.Unmarshal(raw.Message.Content, &blocks) != nil {
		return nil
	}
	var out []provider.ContextItem
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, textItem(b.Text))
			}
		case "image":
			out = append(out, provider.ContextItem{Kind: "files", Label: "image", Tokens: imageTokens, Est: provider.EstText})
		case "tool_result":
			out = append(out, provider.TextItem("tools", "", claudeBlockContentString(b.Content)))
		}
	}
	return out
}

// claudeRequestLine holds the fields that tie an AI call to the request Claude
// Code stored for it (2.1.304+). An "api-request-shape" line carries the
// system prompt and tool schemas exactly as sent, once per shapeHash; an
// "api-request" line (one per call) names its shape; assistant lines name
// their request in requestRef.
type claudeRequestLine struct {
	ID         string       `json:"id"`
	ShapeHash  string       `json:"shapeHash"`
	RequestRef string       `json:"requestRef"`
	Shape      *claudeShape `json:"shape"`
}

type claudeShape struct {
	System []struct {
		Text string `json:"text"`
	} `json:"system"`
	Tools []json.RawMessage `json:"tools"`
}

// shapeItems turns a stored request shape into context items: the system
// prompt and one item per tool schema, MCP tools (mcp__<server>__<tool>)
// apart from the built-in ones.
func shapeItems(s *claudeShape) []provider.ContextItem {
	var out []provider.ContextItem
	var parts []string
	for _, b := range s.System {
		parts = append(parts, b.Text)
	}
	if text := strings.Join(parts, "\n\n"); text != "" {
		out = append(out, provider.TextItem("system", "", text))
	}
	for _, raw := range s.Tools {
		var t struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		kind, label := "tools", t.Name
		if rest, ok := strings.CutPrefix(t.Name, "mcp__"); ok {
			if server, tool, ok := strings.Cut(rest, "__"); ok {
				kind, label = "mcp", server+": "+tool
			}
		}
		out = append(out, provider.TextItem(kind, label, string(raw)))
	}
	return out
}

// firstCallContext tells what the input context of a transcript's first AI
// call was made of, or nil when mid is not that call. Everything the
// transcript recorded before the call (attachments, user lines) went into its
// messages; the system prompt and tool schemas form the cached prefix. When
// the transcript stores the call's request shape, the prefix is itemized from
// it and nothing is left hidden. Older transcripts record no tool schemas:
// there the uncached tail is measured by input_tokens and the schemas are the
// inferred rest.
func firstCallContext(path, mid string) *provider.ContextResp {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tail []provider.ContextItem
	var system *provider.ContextItem
	shapes := map[string]*claudeShape{} // by shapeHash
	reqShape := map[string]string{}     // api-request id -> shapeHash
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw claudeRawLine
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		switch raw.Type {
		case "attachment":
			var a claudeAttachLine
			if json.Unmarshal([]byte(line), &a) != nil {
				continue
			}
			for _, it := range attachItems(&a) {
				if it.Kind == "system" {
					system = &it
				} else {
					tail = append(tail, it)
				}
			}
		case "user":
			if raw.Message != nil {
				tail = append(tail, userItems(&raw)...)
			}
		case "api-request-shape", "api-request":
			var r claudeRequestLine
			if json.Unmarshal([]byte(line), &r) != nil {
				continue
			}
			if r.Shape != nil {
				shapes[r.ShapeHash] = r.Shape
			} else {
				reqShape[r.ID] = r.ShapeHash
			}
		case "assistant":
			if raw.Message == nil || raw.Message.Usage == nil || raw.Message.Model == "<synthetic>" {
				continue
			}
			u := raw.Message.Usage
			ctx := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			if ctx == 0 && u.OutputTokens == 0 {
				continue
			}
			if raw.UUID != mid {
				return nil
			}
			// a failed request may be retried with another shape, so the
			// one that produced this answer is looked up by requestRef
			var r claudeRequestLine
			_ = json.Unmarshal([]byte(line), &r)
			if shape := shapes[reqShape[r.RequestRef]]; shape != nil {
				items := append(tail, shapeItems(shape)...)
				return &provider.ContextResp{Mid: mid, Ctx: ctx, Items: provider.FitRecorded(ctx, items)}
			}
			return &provider.ContextResp{Mid: mid, Ctx: ctx, Items: fitClaude(ctx, u.InputTokens, tail, system)}
		}
	}
	return nil
}

// fitClaude scales the tail items to the call's uncached input when the
// estimate is plausible, then splits the rest of the context into the system
// prompt (estimated from its text) and the inferred hidden part.
func fitClaude(ctx, in int64, tail []provider.ContextItem, system *provider.ContextItem) []provider.ContextItem {
	if est := provider.SumTokens(tail); in > 0 && est > in/2 && est < in*2 {
		provider.ScaleTokens(tail, in)
	}
	if provider.SumTokens(tail) >= ctx {
		provider.ScaleTokens(tail, ctx)
		return tail
	}
	if system != nil {
		if rest := ctx - provider.SumTokens(tail); system.Tokens > rest {
			system.Tokens = rest
		}
		tail = append(tail, *system)
	}
	return provider.FitContext(ctx, tail)
}

// CallContext tells what the input context of a session's first AI call was
// made of.
func (c *Provider) CallContext(mid string) (*provider.ContextResp, error) {
	for _, s := range c.scanIndex().all() {
		for _, m := range s.file.messages {
			if m.role == "assistant" && m.hasUsage {
				if m.mid == mid {
					return firstCallContext(s.file.path, mid), nil
				}
				break
			}
		}
	}
	return nil, nil
}
