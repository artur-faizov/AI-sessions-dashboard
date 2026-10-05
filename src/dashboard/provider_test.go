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

func TestPickProviderDefaultIsFirst(t *testing.T) {
	t.Setenv("PROVIDER", "")
	a, _, ps := twoProviders()
	p, err := pickProvider(ps, nil)
	if err != nil || p != a {
		t.Fatalf("default %v (%v), want first provider", p, err)
	}
	// order is priority: reversing the slice changes the default
	p, _ = pickProvider([]provider.Provider{ps[1], ps[0]}, nil)
	if p.Name() != "beta" {
		t.Fatalf("default %s, want beta", p.Name())
	}
}

func TestPickProviderExplicitOverride(t *testing.T) {
	_, b, ps := twoProviders()
	t.Setenv("PROVIDER", "")
	if p, err := pickProvider(ps, []string{"--provider", "beta"}); err != nil || p != b {
		t.Fatalf("--provider beta: %v %v", p, err)
	}
	t.Setenv("PROVIDER", "beta")
	if p, err := pickProvider(ps, nil); err != nil || p != b {
		t.Fatalf("PROVIDER=beta: %v %v", p, err)
	}
	if p, err := pickProvider(ps, []string{"--provider=alpha"}); err != nil || p.Name() != "alpha" {
		t.Fatalf("flag must win over env: %v %v", p, err)
	}
}

func TestPickProviderUnknownRejected(t *testing.T) {
	t.Setenv("PROVIDER", "")
	_, _, ps := twoProviders()
	p, err := pickProvider(ps, []string{"--provider", "bogus"})
	if p != nil || err == nil {
		t.Fatalf("bogus resolved: %v %v", p, err)
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("error must name the selection and list providers: %v", err)
	}
	if _, err := pickProvider(nil, nil); err == nil {
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

func TestGetAPIProviderListsRegistry(t *testing.T) {
	a, _, ps := twoProviders()
	// registration order differs from sorted order; the response is sorted
	srv := apiServerWith(t, []provider.Provider{ps[1], ps[0]}, a)
	status, body := httpGet(t, srv.URL+"/api/provider")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body["provider"] != "alpha" {
		t.Fatalf("provider %v, want alpha", body["provider"])
	}
	names, _ := body["providers"].([]any)
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("providers %v, want [alpha beta]", body["providers"])
	}
}

func TestPostProviderSwitchesAndBack(t *testing.T) {
	a, b, ps := twoProviders()
	b.tree = []*provider.SessionNode{{ID: "b1", Source: "beta"}}
	srv := apiServerWith(t, ps, a)

	status, body := httpPost(t, srv.URL+"/api/provider", `{"name":"beta"}`)
	if status != http.StatusOK || body["ok"] != true || body["provider"] != "beta" {
		t.Fatalf("switch to beta: %d %v", status, body)
	}
	if _, body = httpGet(t, srv.URL+"/api/provider"); body["provider"] != "beta" {
		t.Fatalf("active provider after switch: %v", body)
	}
	if _, raw := httpGetRaw(t, srv.URL+"/api/tree"); !strings.Contains(raw, `"id":"b1"`) {
		t.Fatalf("tree must come from beta: %s", raw)
	}

	if status, body = httpPost(t, srv.URL+"/api/provider", `{"name":"alpha"}`); status != http.StatusOK || body["ok"] != true {
		t.Fatalf("switch back to alpha: %d %v", status, body)
	}
}

func TestPostProviderRejectsUnknownAndMissing(t *testing.T) {
	a := newLenient("alpha", "/data/alpha.db")
	b := newStrict("beta", "/no-such-projects", false)
	srv := apiServerWith(t, []provider.Provider{a, b}, a)

	status, body := httpPost(t, srv.URL+"/api/provider", `{"name":"bogus"}`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("unknown provider: %d %v", status, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("unknown provider must carry an error: %v", body)
	}

	status, body = httpPost(t, srv.URL+"/api/provider", `{"name":"beta"}`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("missing strict data: %d %v", status, body)
	}
	if !strings.Contains(body["error"].(string), "no-such-projects") {
		t.Fatalf("error must name the missing path: %v", body)
	}
	if _, body = httpGet(t, srv.URL+"/api/provider"); body["provider"] != "alpha" {
		t.Fatalf("failed switch must keep alpha active: %v", body)
	}
}

func TestPostProviderLenientSwitchesEvenWhenMissing(t *testing.T) {
	strict := newStrict("beta", "/data/beta", true)
	lenient := newLenient("alpha", "/data/alpha.db")
	lenient.validPaths = map[string]bool{}
	srv := apiServerWith(t, []provider.Provider{strict, lenient}, strict)
	status, body := httpPost(t, srv.URL+"/api/provider", `{"name":"alpha"}`)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("lenient provider must be selectable without data: %d %v", status, body)
	}
}

func TestPostProviderRejectsBadBody(t *testing.T) {
	srv := apiServer(t)
	status, body := httpPost(t, srv.URL+"/api/provider", `{not json`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("malformed body: %d %v", status, body)
	}
	status, body = httpPost(t, srv.URL+"/api/provider", `{"name":""}`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("empty name: %d %v", status, body)
	}
}
