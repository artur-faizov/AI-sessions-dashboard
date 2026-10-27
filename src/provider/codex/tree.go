package codex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func mergeSessions(ss []*session) *session {
	// Stable sorting keeps path order as the tie breaker.
	sort.SliceStable(ss, func(i, j int) bool {
		if len(ss[i].calls) != len(ss[j].calls) {
			return len(ss[i].calls) > len(ss[j].calls)
		}
		ri, rj := 0, 0
		for _, c := range ss[i].calls {
			if !strings.Contains(c.Mid, ":legacy:") {
				ri++
			}
		}
		for _, c := range ss[j].calls {
			if !strings.Contains(c.Mid, ":legacy:") {
				rj++
			}
		}
		if ri != rj {
			return ri > rj
		}
		return ss[i].last > ss[j].last
	})
	base := ss[0]
	merged := *base
	merged.calls = append([]provider.CallRec{}, base.calls...)
	merged.triggers = map[string]*provider.TriggerResp{}
	merged.tools = map[string]*tool{}
	merged.toolOrder = append([]string{}, base.toolOrder...)
	for mid, t := range base.triggers {
		merged.triggers[mid] = t
	}
	for id, t := range base.tools {
		merged.tools[id] = t
	}
	mids := map[string]int{}
	snapshots := map[usage]int{}
	for i, c := range merged.calls {
		mids[c.Mid] = i
	}
	for u, i := range base.snapshots {
		snapshots[u] = i
	}
	for _, other := range ss[1:] {
		if other.last > merged.last {
			merged.last = other.last
			merged.busy = other.busy
			merged.started = other.started
		}
		if merged.first == 0 || other.first > 0 && other.first < merged.first {
			merged.first = other.first
		}
		if merged.parent == "" {
			merged.parent = other.parent
		}
		if merged.dir == "" {
			merged.dir = other.dir
		}
		if merged.agent == "" {
			merged.agent = other.agent
		}
		byIndex := map[int]usage{}
		for u, i := range other.snapshots {
			byIndex[i] = u
		}
		for i, c := range other.calls {
			if _, ok := mids[c.Mid]; ok {
				continue
			}
			if u, ok := byIndex[i]; ok {
				if oldIndex, exists := snapshots[u]; exists {
					old := merged.calls[oldIndex]
					oldLegacy := strings.Contains(old.Mid, ":legacy:")
					newLegacy := strings.Contains(c.Mid, ":legacy:")
					if newLegacy {
						continue
					}
					if oldLegacy {
						delete(mids, old.Mid)
						delete(merged.triggers, old.Mid)
						merged.calls[oldIndex] = c
						merged.triggers[c.Mid] = other.triggers[c.Mid]
						mids[c.Mid] = oldIndex
						continue
					}
				}
				snapshots[u] = len(merged.calls)
			}
			mids[c.Mid] = len(merged.calls)
			merged.calls = append(merged.calls, c)
			merged.triggers[c.Mid] = other.triggers[c.Mid]
		}
		for _, id := range other.toolOrder {
			if merged.tools[id] == nil {
				merged.tools[id] = other.tools[id]
				merged.toolOrder = append(merged.toolOrder, id)
			}
		}
	}
	sort.SliceStable(merged.calls, func(i, j int) bool {
		if merged.calls[i].Time != merged.calls[j].Time {
			return merged.calls[i].Time < merged.calls[j].Time
		}
		return merged.calls[i].Mid < merged.calls[j].Mid
	})
	return &merged
}

func resolvedParents(sessions map[string]*session) map[string]string {
	parents := map[string]string{}
	ids := []string{}
	for id, s := range sessions {
		ids = append(ids, id)
		if s.parent != id && sessions[s.parent] != nil {
			parents[id] = s.parent
		}
	}
	sort.Strings(ids)
	// Break one edge of each cycle before attaching children. All nodes survive.
	state := map[string]int{}
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		if state[id] == 1 {
			parents[id] = ""
			return
		}
		state[id] = 1
		if parent := parents[id]; parent != "" {
			visit(parent)
		}
		state[id] = 2
	}
	for _, id := range ids {
		visit(id)
	}
	return parents
}

func (p *Provider) BuildTree(nowMS int64) ([]*provider.SessionNode, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sessions := p.scan()
	parents := resolvedParents(sessions)
	nodes := map[string]*provider.SessionNode{}
	for id, s := range sessions {
		title := "(untitled)"
		if s.agent != "" {
			title = s.agent
		} else if s.parent != "" {
			title = "Subagent"
		}
		if len(s.prompts) > 0 {
			title = strings.TrimSpace(strings.SplitN(s.prompts[0], "\n", 2)[0])
			r := []rune(title)
			if len(r) > 80 {
				title = string(r[:80]) + "…"
			}
		}
		n := &provider.SessionNode{ID: id, Title: title, Dir: s.dir, Created: s.first, Updated: s.last, Beats: []provider.Beat{}, Children: []*provider.SessionNode{}, ByModel: map[string]*provider.SubtreeSums{}, Source: "codex"}
		if s.model != "" {
			n.Model = s.model
		}
		if s.parent != "" || s.agent != "" {
			n.Agent = s.agent
			if s.agent == "" {
				n.Agent = "subagent"
			}
		}
		// A start row is a turn boundary, never an internal model request start.
		n.Active = s.busy && s.last > 0 && nowMS >= s.last && nowMS-s.last < provider.ActiveWindowMS
		if n.Active {
			n.State = provider.StateBusy
		}
		if s.first > 0 && s.last > s.first {
			n.Dur = provider.ActiveDur(s.last-s.first, s.calls)
		}
		for _, c := range s.calls {
			n.In += c.In
			n.Out += c.Out
			n.Cr += c.Cr
			n.Cw += c.Cw
			n.Reason += c.Reason
			n.MaxCtx = max(n.MaxCtx, c.Ctx)
			provider.ByModelAdd(n.ByModel, c.Model, &provider.SubtreeSums{In: c.In, Out: c.Out, Cr: c.Cr, Cw: c.Cw, Reason: c.Reason})
			if c.Time > 0 && (n.Active || nowMS-n.Updated < provider.BeatWindowMS) {
				n.Beats = append(n.Beats, provider.Beat{c.Time, c.Time})
			}
		}
		nodes[id] = n
	}
	roots := []*provider.SessionNode{}
	for id, n := range nodes {
		if parent := parents[id]; parent != "" {
			n.Parent = parent
			nodes[parent].Children = append(nodes[parent].Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	var fill func(*provider.SessionNode, string) provider.SubtreeSums
	fill = func(n *provider.SessionNode, dir string) provider.SubtreeSums {
		if dir != "" {
			n.Dir = dir
		}
		total := provider.SubtreeSums{In: n.In, Out: n.Out, Cr: n.Cr, Cw: n.Cw, Reason: n.Reason}
		for _, child := range n.Children {
			sub := fill(child, n.Dir)
			provider.ByModelMerge(n.ByModel, child.ByModel)
			total.In += sub.In
			total.Out += sub.Out
			total.Cr += sub.Cr
			total.Cw += sub.Cw
			total.Reason += sub.Reason
		}
		sortNodes(n.Children)
		n.Subtree = &total
		return total
	}
	for _, n := range roots {
		fill(n, "")
	}
	sortNodes(roots)
	return roots, nil
}
func sortNodes(nodes []*provider.SessionNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Created != nodes[j].Created {
			return nodes[i].Created > nodes[j].Created
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func (p *Provider) SessionSegments(sid string) ([]provider.Seg, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sessions := p.scan()
	s := sessions[sid]
	if s == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}
	segs := []provider.Seg{}
	for _, id := range s.toolOrder {
		t := s.tools[id]
		if t.start == 0 {
			continue
		}
		end := t.end
		if end < t.start {
			end = t.start
		}
		kind := provider.SegTool
		if askTool(t.name) {
			kind = provider.SegHuman
		}
		segs = append(segs, provider.Seg{Kind: kind, Start: t.start, End: end, Mid: t.mid, Name: t.name, Input: provider.ToolInput(t.input), Output: provider.ToolOutput(t.output)})
	}
	parents := resolvedParents(sessions)
	for id, child := range sessions {
		if parents[id] == sid && child.first > 0 && child.last > child.first {
			name := child.agent
			if name == "" {
				name = "subagent"
			}
			segs = append(segs, provider.Seg{Kind: provider.SegSub, Start: child.first, End: child.last, Name: name, Sub: id})
		}
	}
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].Start != segs[j].Start {
			return segs[i].Start < segs[j].Start
		}
		if segs[i].Name != segs[j].Name {
			return segs[i].Name < segs[j].Name
		}
		return segs[i].Sub < segs[j].Sub
	})
	return segs, nil
}
