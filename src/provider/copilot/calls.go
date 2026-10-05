package copilot

import (
	"fmt"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// promptCap mirrors the claude provider: stop collecting user texts once their
// combined length passes this many characters.
const promptCap = 20000

func (st *stream) humanBefore(i int) bool {
	return i > 0 && st.items[i-1].kind == kindUser && st.items[i-1].human
}

// calls returns one record per assistant API call of a node, in time order,
// with token fields from the node's token source only.
func (s *session) calls(agentID string, td tokenData) []provider.CallRec {
	st := s.streams[agentID]
	if st == nil {
		st = &stream{}
	}
	type msgPos struct {
		it    *item
		index int
	}
	var msgs []msgPos
	for i, it := range st.items {
		if it.kind == kindAssistant {
			msgs = append(msgs, msgPos{it, i})
		}
	}
	calls := []provider.CallRec{}
	if _, ok := s.otelNodeTokens(agentID, td.otel); ok {
		// Root calls take their matched span; subagent spans are per run, so
		// subagent calls (and unmatched root calls) keep output only.
		for _, m := range msgs {
			rec := provider.CallRec{
				Mid: m.it.mid, Time: m.it.timeMS, Out: m.it.outTokens,
				Hu: st.humanBefore(m.index),
			}
			if agentID == "" {
				if o, ok := s.otelFor(agentID, m.index, m.it, td.otel); ok {
					rec.In, rec.Cr, rec.Cw, rec.Out, rec.Reason, rec.Ctx = o.fresh(), o.cr, o.cw, o.out, o.reason, o.input
				}
			}
			calls = append(calls, rec)
		}
		return calls
	}
	if rows := td.usage[usageKey{session: s.id, agent: agentID}]; len(rows) > 0 {
		// Rows carry no message id: pair them with assistant messages by order.
		for k, r := range rows {
			rec := provider.CallRec{
				Mid: fmt.Sprintf("row:%d", r.id), Time: r.timeMS,
				In: r.in, Cr: r.cr, Cw: r.cw, Out: r.out, Reason: r.reason,
				Ctx: r.in + r.cr + r.cw,
			}
			if k < len(msgs) {
				rec.Mid = msgs[k].it.mid
				if rec.Time == 0 {
					rec.Time = msgs[k].it.timeMS
				}
				rec.Hu = st.humanBefore(msgs[k].index)
			}
			calls = append(calls, rec)
		}
		return calls
	}
	for _, m := range msgs {
		calls = append(calls, provider.CallRec{
			Mid: m.it.mid, Time: m.it.timeMS, Out: m.it.outTokens,
			Hu: st.humanBefore(m.index),
		})
	}
	return calls
}

// prompt returns the root's user messages, or the task prompt that started a subagent.
func (s *session) prompt(agentID string) []string {
	texts := []string{}
	if agentID != "" {
		if t := s.tasks[agentID]; t != nil && t.prompt != "" {
			texts = append(texts, t.prompt)
		}
		return texts
	}
	total := 0
	for _, it := range s.streams[""].items {
		if it.kind != kindUser || !it.human {
			continue
		}
		texts = append(texts, it.text)
		if total += len([]rune(it.text)); total > promptCap {
			break
		}
	}
	return texts
}

// trigger reports what preceded the assistant call mid: the user message, or
// the tool executions since the previous assistant message. ok is false when
// mid is not in this session.
func (s *session) trigger(mid string) (*provider.TriggerResp, bool) {
	ref, ok := s.mids[mid]
	if !ok {
		return nil, false
	}
	st := s.streams[ref.agent]
	j := ref.index - 1
	var tools []*item
	for ; j >= 0 && st.items[j].kind == kindTool; j-- {
		tools = append([]*item{st.items[j]}, tools...)
	}
	switch {
	case len(tools) > 0:
		parts := []any{}
		for _, t := range tools {
			in, cutI := provider.Clip(t.toolIn)
			out, cutO := provider.Clip(t.toolOut)
			var name any = "tool"
			if t.toolName != "" {
				name = t.toolName
			}
			parts = append(parts, provider.TriggerPartTool{
				Kind: "tool", Tool: name, Status: t.status,
				Input: in, CutI: cutI, Output: out, CutO: cutO,
			})
		}
		return &provider.TriggerResp{Case: "assistant", Time: tools[len(tools)-1].timeMS, Parts: parts}, true
	case j >= 0 && st.items[j].kind == kindUser:
		u := st.items[j]
		parts := []any{}
		if u.text != "" {
			t, cut := provider.Clip(u.text)
			parts = append(parts, provider.TriggerPartText{Kind: "text", Text: t, Cut: cut})
		}
		return &provider.TriggerResp{Case: "user", Time: u.timeMS, Parts: parts}, true
	case j >= 0:
		return &provider.TriggerResp{Case: "assistant", Time: st.items[j].timeMS, Parts: []any{}}, true
	}
	return &provider.TriggerResp{Case: "start", Time: nil, Parts: []any{}}, true
}
