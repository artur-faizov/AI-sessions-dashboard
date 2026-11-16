package dashboard

import (
	"errors"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// The dashboard shows every available provider at once. Session and message
// ids are only unique within one provider, so on the wire they are qualified
// as "<provider name>:<own id>"; per-session endpoints split the prefix off to
// route the request back to the provider that owns it.
const idSep = ":"

// qualify prefixes a provider's own id with its name; empty ids stay empty.
func qualify(prov, id string) string {
	if id == "" {
		return ""
	}
	return prov + idSep + id
}

// route resolves a qualified id to its provider and that provider's own id;
// nil when the prefix is missing or names no served provider.
func (s *server) route(id string) (provider.Provider, string) {
	name, raw, ok := strings.Cut(id, idSep)
	if !ok || raw == "" {
		return nil, ""
	}
	p := findProvider(s.providers, name)
	if p == nil {
		return nil, ""
	}
	return p, raw
}

// available lists the served providers that are switched on in the settings
// and whose data is present, in order.
func (s *server) available() []provider.Provider {
	var out []provider.Provider
	for _, p := range s.providers {
		if s.enabled(p.Name()) && p.Available() == nil {
			out = append(out, p)
		}
	}
	return out
}

// qualifiedTree copies nodes with every id qualified by prov and Prov set.
// Providers may cache their trees, so the originals are never modified.
func qualifiedTree(prov string, nodes []*provider.SessionNode) []*provider.SessionNode {
	out := make([]*provider.SessionNode, 0, len(nodes))
	for _, n := range nodes {
		c := *n
		c.ID = qualify(prov, n.ID)
		if ps, ok := n.Parent.(string); ok && ps != "" {
			c.Parent = qualify(prov, ps)
		}
		c.Prov = prov
		c.Children = qualifiedTree(prov, n.Children)
		out = append(out, &c)
	}
	return out
}

// mergedTree builds the tree of every available provider. A provider that
// fails is left out; the error is returned only when none of them succeeded.
// A root session whose own id an earlier provider already served is dropped,
// so a copy (an exported session read back by the folder provider, which is
// registered last) never shows next to the live session.
func (s *server) mergedTree(nowMS int64) ([]*provider.SessionNode, error) {
	out := []*provider.SessionNode{}
	seen := map[string]bool{}
	var errs []error
	ok := 0
	for _, p := range s.available() {
		nodes, err := p.BuildTree(nowMS)
		if err != nil {
			errs = append(errs, errors.New(p.Name()+": "+err.Error()))
			continue
		}
		ok++
		var kept []*provider.SessionNode
		for _, n := range nodes {
			if !seen[n.ID] {
				kept = append(kept, n)
			}
		}
		markIDs(seen, kept)
		out = append(out, qualifiedTree(p.Name(), kept)...)
	}
	if ok == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// markIDs records the own ids of nodes and all their descendants.
func markIDs(seen map[string]bool, nodes []*provider.SessionNode) {
	for _, n := range nodes {
		seen[n.ID] = true
		markIDs(seen, n.Children)
	}
}

func qualifiedCalls(prov string, calls []provider.CallRec) []provider.CallRec {
	out := make([]provider.CallRec, len(calls))
	for i, c := range calls {
		c.Mid = qualify(prov, c.Mid)
		out[i] = c
	}
	return out
}

func qualifiedSegs(prov string, segs []provider.Seg) []provider.Seg {
	out := make([]provider.Seg, len(segs))
	for i, g := range segs {
		g.Mid = qualify(prov, g.Mid)
		g.Sub = qualify(prov, g.Sub)
		out[i] = g
	}
	return out
}
