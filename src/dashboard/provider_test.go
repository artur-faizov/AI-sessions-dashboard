package dashboard

import (
	"net/http"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func twoProviders() (*fakeProvider, *fakeProvider, []provider.Provider) {
	a := newLenient("alpha", "/data/alpha.db")
	b := newStrict("beta", "/data/beta", true)
	return a, b, []provider.Provider{a, b}
}

func names(ps []provider.Provider) string { return strings.Join(providerNamesOf(ps), ",") }

func TestSelectProvidersDefaultIsAll(t *testing.T) {
	t.Setenv("PROVIDER", "")
	_, _, ps := twoProviders()
	sel, explicit, err := selectProviders(ps, nil)
	if err != nil || explicit || names(sel) != "alpha,beta" {
		t.Fatalf("default %s explicit=%v (%v), want every provider", names(sel), explicit, err)
	}
}

func TestSelectProvidersFilter(t *testing.T) {
	_, _, ps := twoProviders()
	t.Setenv("PROVIDER", "")
	if sel, explicit, err := selectProviders(ps, []string{"--provider", "beta"}); err != nil || !explicit || names(sel) != "beta" {
		t.Fatalf("--provider beta: %s %v %v", names(sel), explicit, err)
	}
	if sel, _, err := selectProviders(ps, []string{"--provider=beta, alpha,beta"}); err != nil || names(sel) != "beta,alpha" {
		t.Fatalf("list keeps the given order without duplicates: %s %v", names(sel), err)
	}
	t.Setenv("PROVIDER", "beta")
	if sel, _, err := selectProviders(ps, nil); err != nil || names(sel) != "beta" {
		t.Fatalf("PROVIDER=beta: %s %v", names(sel), err)
	}
	if sel, _, err := selectProviders(ps, []string{"--provider=alpha"}); err != nil || names(sel) != "alpha" {
		t.Fatalf("flag must win over env: %s %v", names(sel), err)
	}
}

func TestSelectProvidersUnknownRejected(t *testing.T) {
	t.Setenv("PROVIDER", "")
	_, _, ps := twoProviders()
	sel, _, err := selectProviders(ps, []string{"--provider", "alpha,bogus"})
	if sel != nil || err == nil {
		t.Fatalf("bogus resolved: %v %v", sel, err)
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("error must name the selection and list providers: %v", err)
	}
	if _, _, err := selectProviders(nil, nil); err == nil {
		t.Fatal("empty provider list must be rejected")
	}
}

func TestParseProviderFlag(t *testing.T) {
	if v, ok := parseProviderFlag([]string{"--provider", "claude"}); !ok || v != "claude" {
		t.Fatalf("--provider NAME: %q %v", v, ok)
	}
	if v, ok := parseProviderFlag([]string{"--provider=claude"}); !ok || v != "claude" {
		t.Fatalf("--provider=NAME: %q %v", v, ok)
	}
	if v, ok := parseProviderFlag([]string{"-provider=claude"}); !ok || v != "claude" {
		t.Fatalf("-provider=NAME: %q %v", v, ok)
	}
	if _, ok := parseProviderFlag([]string{"--db", "x"}); ok {
		t.Fatal("must not consume unrelated flags")
	}
}

// mergedFixture: two providers whose sessions share own ids — both have a
// subagent "kid" and a call "m1" — so only the provider prefix tells them
// apart. Their roots differ (alpha "s1", beta "t1"): a root id served twice is
// treated as a copy (see TestTreeDropsCopiesOfLiveSessions).
func mergedFixture() (*fakeProvider, *fakeProvider, []provider.Provider) {
	a, b, ps := twoProviders()
	for _, p := range []*fakeProvider{a, b} {
		root := map[string]string{"alpha": "s1", "beta": "t1"}[p.name]
		kid := &provider.SessionNode{ID: "kid", Parent: root, Title: p.name + " kid", Children: []*provider.SessionNode{}}
		p.tree = []*provider.SessionNode{{ID: root, Title: p.name + " root", Dir: "/proj", Children: []*provider.SessionNode{kid}}}
		p.calls = map[string][]provider.CallRec{root: {{Mid: "m1", In: int64(len(p.name))}}, "kid": {}}
		p.prompts = map[string][]string{root: {p.name + " prompt"}, "kid": {p.name + " kid prompt"}}
		p.triggers = map[string]*provider.TriggerResp{"m1": {Case: p.name, Parts: []any{}}}
	}
	return a, b, ps
}

func TestTreeDropsCopiesOfLiveSessions(t *testing.T) {
	a, _, _ := mergedFixture()
	copyOf := newLenient("folder", "/exports") // registered last, like the real folder provider
	kid := &provider.SessionNode{ID: "kid", Parent: "s1", Children: []*provider.SessionNode{}}
	copyOf.tree = []*provider.SessionNode{
		{ID: "s1", Title: "exported copy", Children: []*provider.SessionNode{kid}},
		{ID: "old", Title: "only in the export", Children: []*provider.SessionNode{}},
	}
	srv := apiServerWith(t, []provider.Provider{a, copyOf})
	_, raw := httpGetRaw(t, srv.URL+"/api/tree")
	if strings.Contains(raw, "folder:s1") || strings.Contains(raw, "exported copy") || strings.Contains(raw, "folder:kid") {
		t.Fatalf("a copy of a live session must not show: %s", raw)
	}
	if !strings.Contains(raw, `"id":"alpha:s1"`) || !strings.Contains(raw, `"id":"folder:old"`) {
		t.Fatalf("live session and export-only session must show: %s", raw)
	}
}

func TestTreeMergesProvidersWithQualifiedIDs(t *testing.T) {
	a, _, ps := mergedFixture()
	srv := apiServerWith(t, ps)
	r, raw := httpGetRaw(t, srv.URL+"/api/tree")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", r.StatusCode, raw)
	}
	for _, want := range []string{
		`"id":"alpha:s1"`, `"id":"beta:t1"`, `"id":"alpha:kid","parent":"alpha:s1"`, `"id":"beta:kid","parent":"beta:t1"`,
		`"prov":"alpha"`, `"prov":"beta"`, `"dir":"/proj"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("tree must contain %s: %s", want, raw)
		}
	}
	// the provider's own (possibly cached) tree is left untouched
	if a.tree[0].ID != "s1" || a.tree[0].Children[0].Parent != "s1" || a.tree[0].Prov != "" {
		t.Fatalf("provider tree modified: %+v", a.tree[0])
	}
	if _, raw2 := httpGetRaw(t, srv.URL+"/api/tree"); raw2 != raw {
		t.Fatalf("second build differs (ids qualified twice?): %s", raw2)
	}
}

func TestTreeLeavesOutMissingProviders(t *testing.T) {
	a, b, _ := mergedFixture()
	b.validPaths = map[string]bool{} // beta's data is gone
	srv := apiServerWith(t, []provider.Provider{a, b})
	_, raw := httpGetRaw(t, srv.URL+"/api/tree")
	if !strings.Contains(raw, `"id":"alpha:s1"`) || strings.Contains(raw, `beta:`) {
		t.Fatalf("tree: %s", raw)
	}
}

func TestPerSessionEndpointsRouteByPrefix(t *testing.T) {
	_, _, ps := mergedFixture()
	srv := apiServerWith(t, ps)
	for prov, root := range map[string]string{"alpha": "s1", "beta": "t1"} {
		_, body := httpGet(t, srv.URL+"/api/prompt/"+prov+":kid")
		if texts, _ := body["texts"].([]any); len(texts) != 1 || texts[0] != prov+" kid prompt" {
			t.Fatalf("%s kid prompt: %v", prov, body)
		}
		_, body = httpGet(t, srv.URL+"/api/calls/"+prov+":"+root)
		calls, _ := body["calls"].([]any)
		if len(calls) != 1 {
			t.Fatalf("%s calls: %v", prov, body)
		}
		c := calls[0].(map[string]any)
		if c["mid"] != prov+":m1" || c["in"].(float64) != float64(len(prov)) {
			t.Fatalf("%s call must carry a qualified mid: %v", prov, c)
		}
		status, body := httpGet(t, srv.URL+"/api/trigger/"+prov+":m1")
		if status != http.StatusOK || body["case"] != prov {
			t.Fatalf("%s trigger: %d %v", prov, status, body)
		}
	}
	for _, id := range []string{"s1", "bogus:s1", "alpha:"} {
		if status, _ := httpGet(t, srv.URL+"/api/calls/"+id); status != http.StatusNotFound {
			t.Fatalf("%q: status %d, want 404", id, status)
		}
	}
}

func TestProviderEndpointRemoved(t *testing.T) {
	_, _, ps := twoProviders()
	srv := apiServerWith(t, ps)
	if status, _ := httpGet(t, srv.URL+"/api/provider"); status != http.StatusNotFound {
		t.Fatalf("GET /api/provider: %d, want 404", status)
	}
	if status, _ := httpPost(t, srv.URL+"/api/provider", `{"name":"beta"}`); status != http.StatusNotFound {
		t.Fatalf("POST /api/provider: %d, want 404", status)
	}
}

// segFake adds subagent spans and call context to a fakeProvider.
type segFake struct{ *fakeProvider }

func (segFake) SessionSegments(string) ([]provider.Seg, error) {
	return []provider.Seg{{Kind: provider.SegSub, Mid: "m1", Sub: "kid"}, {Kind: provider.SegTool}}, nil
}

func (segFake) CallContext(mid string) (*provider.ContextResp, error) {
	if mid != "m1" {
		return nil, nil
	}
	return &provider.ContextResp{Mid: mid, Ctx: 5, Items: []provider.ContextItem{}}, nil
}

func TestSegsAndContextCarryQualifiedIDs(t *testing.T) {
	a, b, _ := mergedFixture()
	srv := apiServerWith(t, []provider.Provider{segFake{a}, b})
	_, raw := httpGetRaw(t, srv.URL+"/api/calls/alpha:s1")
	if !strings.Contains(raw, `"mid":"alpha:m1","sub":"alpha:kid"`) || !strings.Contains(raw, `{"kind":"tool","start":0,"end":0}`) {
		t.Fatalf("segs must qualify mid and sub, and leave empty ones empty: %s", raw)
	}
	status, body := httpGet(t, srv.URL+"/api/context/alpha:m1")
	if status != http.StatusOK || body["mid"] != "alpha:m1" {
		t.Fatalf("context: %d %v", status, body)
	}
	if status, body = httpGet(t, srv.URL+"/api/context/beta:m1"); status != http.StatusNotFound || body["error"] != "not_supported" {
		t.Fatalf("context of a provider without it: %d %v", status, body)
	}
}
