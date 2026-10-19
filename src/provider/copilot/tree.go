package copilot

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// activeWindowMS bounds how old the last event of a locked session may be for
// it to count as active; lock files can outlive a crashed CLI process.
const activeWindowMS = 10 * 60 * 1000

func (p *Provider) buildTree(nowMS int64) []*provider.SessionNode {
	td := p.loadTokens()
	roots := []*provider.SessionNode{}
	for _, s := range p.scan() {
		roots = append(roots, s.tree(nowMS, td))
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].Created > roots[j].Created })
	return roots
}

func hasLock(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "inuse.") && strings.HasSuffix(n, ".lock") {
			return true
		}
	}
	return false
}

func nodeID(sid, agentID string) string {
	if agentID == "" {
		return sid
	}
	return sid + "/" + agentID
}

func optString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (st *stream) beats() []int64 {
	beats := []int64{}
	if st == nil {
		return beats
	}
	for _, it := range st.items {
		if it.kind == kindAssistant && it.timeMS > 0 {
			beats = append(beats, it.timeMS)
		}
	}
	sort.Slice(beats, func(i, j int) bool { return beats[i] < beats[j] })
	return beats
}

func setTokens(n *provider.SessionNode, t nodeTokens) {
	n.In, n.Out, n.Cr, n.Cw, n.Reason, n.MaxCtx = t.in, t.out, t.cr, t.cw, t.reason, t.maxctx
}

// tree builds the root node of one session with its subagent subtree.
func (s *session) tree(nowMS int64, td tokenData) *provider.SessionNode {
	locked := hasLock(s.dir)
	root := s.rootNode(nowMS, locked)
	root.Dur = provider.ActiveDur(root.Dur, s.calls("", td))
	rootTokens := s.nodeTokens("", td)
	setTokens(root, rootTokens)

	nodes := map[string]*provider.SessionNode{"": root}
	for _, id := range s.order {
		n := s.subagentNode(s.subagents[id], root.Dir, nowMS, locked)
		setTokens(n, s.nodeTokens(id, td))
		nodes[id] = n
	}
	for _, id := range s.order {
		parent := nodes[s.subagents[id].parent]
		parent.Children = append(parent.Children, nodes[id])
	}
	for _, n := range nodes {
		if n.Active || nowMS-n.Updated < provider.BeatWindowMS {
			agent := ""
			if n != root {
				_, agent, _ = strings.Cut(n.ID, "/")
			}
			n.Beats = s.streams[agent].beats()
		}
	}

	fillSubtree(root)
	if rootTokens.source == srcShutdown && len(root.Children) > 0 {
		// Shutdown totals already include subagent spend: keep the root's own
		// share so that the subtree sum equals the shutdown totals.
		d := *root.Subtree
		root.In = clampSub(rootTokens.in, d.In-rootTokens.in)
		root.Out = clampSub(rootTokens.out, d.Out-rootTokens.out)
		root.Cr = clampSub(rootTokens.cr, d.Cr-rootTokens.cr)
		root.Cw = clampSub(rootTokens.cw, d.Cw-rootTokens.cw)
		root.Reason = clampSub(rootTokens.reason, d.Reason-rootTokens.reason)
		fillSubtree(root)
	}
	return root
}

func clampSub(a, b int64) int64 {
	if a-b < 0 {
		return 0
	}
	return a - b
}

func fillSubtree(n *provider.SessionNode) provider.SubtreeSums {
	t := provider.SubtreeSums{In: n.In, Out: n.Out, Reason: n.Reason, Cr: n.Cr, Cw: n.Cw}
	for _, ch := range n.Children {
		st := fillSubtree(ch)
		t.In += st.In
		t.Out += st.Out
		t.Reason += st.Reason
		t.Cr += st.Cr
		t.Cw += st.Cw
	}
	n.Subtree = &t
	sort.SliceStable(n.Children, func(i, j int) bool {
		return n.Children[i].Created > n.Children[j].Created
	})
	return t
}

func (s *session) rootNode(nowMS int64, locked bool) *provider.SessionNode {
	st := s.streams[""]
	model := st.lastModel
	if model == "" {
		model = s.startModel
	}
	dir := s.ws.cwd
	if dir == "" {
		dir = s.startCwd
	}
	if dir == "" {
		dir = filepath.Base(s.dir)
	}
	created := s.ws.createdMS
	if created == 0 {
		created = s.startMS
	}
	if created == 0 {
		created = s.firstMS
	}
	// workspace.yaml updated_at lags behind the event log, so take the later one.
	updated := max(s.ws.updatedMS, s.lastMS)
	title := s.ws.name
	if title == "" {
		title = "(untitled)"
	}
	n := &provider.SessionNode{
		ID:       s.id,
		Parent:   nil,
		Title:    title,
		Agent:    nil,
		Model:    optString(model),
		Dir:      dir,
		Created:  created,
		Updated:  updated,
		Active:   locked && s.lastMS > 0 && nowMS-s.lastMS < activeWindowMS,
		Beats:    []int64{},
		Children: []*provider.SessionNode{},
		Source:   "copilot",
	}
	if n.Dur = updated - created; n.Dur < 0 || created == 0 {
		n.Dur = 0
	}
	return n
}

func (s *session) subagentNode(sa *subagent, dir string, nowMS int64, locked bool) *provider.SessionNode {
	st := s.streams[sa.id]
	if st == nil {
		st = &stream{}
	}
	agent := sa.name
	if agent == "" {
		agent = "task"
	}
	title := ""
	if t := s.tasks[sa.id]; t != nil {
		title = t.description
	}
	if title == "" {
		title = sa.displayName
	}
	if title == "" {
		title = agent
	}
	model := sa.model
	if model == "" {
		model = st.lastModel
	}
	created := sa.startMS
	if created == 0 {
		created = st.firstMS
	}
	updated := sa.endMS
	if updated == 0 {
		updated = st.lastMS
	}
	n := &provider.SessionNode{
		ID:       nodeID(s.id, sa.id),
		Parent:   nodeID(s.id, sa.parent),
		Title:    title,
		Agent:    agent,
		Model:    optString(model),
		Dir:      dir,
		Created:  created,
		Updated:  updated,
		Active:   locked && !sa.completed && st.lastMS > 0 && nowMS-st.lastMS < activeWindowMS,
		Beats:    []int64{},
		Children: []*provider.SessionNode{},
		Source:   "copilot",
	}
	switch {
	case sa.completed && sa.durationMS > 0:
		n.Dur = sa.durationMS
	case created > 0 && updated > created:
		n.Dur = updated - created
	}
	return n
}
