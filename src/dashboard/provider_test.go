package dashboard

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultProviderIsOpencode(t *testing.T) {
	if activeProvider == nil || activeProvider.name() != "opencode" {
		t.Fatalf("default provider %v, want opencode", activeProvider)
	}
	if activeProvider.fatalWhenMissing() {
		t.Fatal("opencode must serve even when the DB is missing")
	}
}

func TestSelectProviderKnownNames(t *testing.T) {
	if p := selectProvider("opencode"); p == nil || p.name() != "opencode" {
		t.Fatalf("opencode provider missing: %v", p)
	}
	t.Setenv("CLAUDE_PROJECTS_DIR", "/tmp/claude-select-test")
	if p := selectProvider("claude"); p == nil || p.name() != "claude" {
		t.Fatalf("claude provider missing: %v", p)
	}
}

func TestSelectProviderUnknownNameRejected(t *testing.T) {
	if p := selectProvider("bogus"); p != nil {
		t.Fatalf("bogus provider resolved: %v", p)
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

func TestClaudeProviderRootFromEnv(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude-projects")
	t.Setenv("CLAUDE_PROJECTS_DIR", root)
	p := newClaudeProvider()
	if p.dataPath() != root {
		t.Fatalf("root %s, want %s", p.dataPath(), root)
	}
	if !p.fatalWhenMissing() {
		t.Fatal("claude must exit when its data dir is missing")
	}
	err := p.available()
	if err == nil || !strings.Contains(err.Error(), root) {
		t.Fatalf("available() must name %s, got %v", root, err)
	}
}

func TestClaudeProviderDefaultRoot(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", "")
	p := newClaudeProvider()
	if !strings.HasSuffix(p.dataPath(), filepath.Join(".claude", "projects")) {
		t.Fatalf("default root %s, want ~/.claude/projects", p.dataPath())
	}
}

func TestClaudeProviderAvailableOnExistingDir(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	if err := newClaudeProvider().available(); err != nil {
		t.Fatalf("existing dir must be available, got %v", err)
	}
}

func TestOpencodeNodesCarrySource(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	seedSession(t, pool, "s1", nowMS(), nil, nil, nil, 1)
	activateDB(t, path)
	roots := mustTree(t)
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if roots[0].Source != "opencode" {
		t.Fatalf("source %q, want opencode", roots[0].Source)
	}
	s := jdump(roots[0])
	if !strings.HasSuffix(s, `"source":"opencode"}`) {
		t.Fatalf("source must be the last JSON field: %s", s)
	}
}

func restoreProvider(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { setActiveProvider(opencodeProvider{}) })
}

func TestGetAPIProviderListsRegistry(t *testing.T) {
	restoreProvider(t)
	srv := apiServer(t)
	status, body := httpGet(t, srv.URL+"/api/provider")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body["provider"] != "opencode" {
		t.Fatalf("provider %v, want opencode", body["provider"])
	}
	names, _ := body["providers"].([]any)
	if len(names) != 2 || names[0] != "claude" || names[1] != "opencode" {
		t.Fatalf("providers %v, want [claude opencode]", body["providers"])
	}
}

func TestPostProviderSwitchesToClaudeAndBack(t *testing.T) {
	restoreProvider(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_DIR", root)
	srv := apiServer(t)

	status, body := httpPost(t, srv.URL+"/api/provider", `{"name":"claude"}`)
	if status != http.StatusOK || body["ok"] != true || body["provider"] != "claude" {
		t.Fatalf("switch to claude: %d %v", status, body)
	}
	if status, body = httpGet(t, srv.URL+"/api/provider"); body["provider"] != "claude" {
		t.Fatalf("active provider after switch: %v", body)
	}
	if _, body = httpGet(t, srv.URL+"/api/tree"); len(body) != 0 {
		t.Fatalf("claude tree on empty root must be [], got %v", body)
	}

	if status, body = httpPost(t, srv.URL+"/api/provider", `{"name":"opencode"}`); status != http.StatusOK || body["ok"] != true {
		t.Fatalf("switch back to opencode: %d %v", status, body)
	}
}

func TestPostProviderRejectsUnknownAndMissing(t *testing.T) {
	restoreProvider(t)
	srv := apiServer(t)

	status, body := httpPost(t, srv.URL+"/api/provider", `{"name":"bogus"}`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("unknown provider: %d %v", status, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("unknown provider must carry an error: %v", body)
	}

	t.Setenv("CLAUDE_PROJECTS_DIR", filepath.Join(t.TempDir(), "no-such-projects"))
	status, body = httpPost(t, srv.URL+"/api/provider", `{"name":"claude"}`)
	if status != http.StatusBadRequest || body["ok"] == true {
		t.Fatalf("missing claude root: %d %v", status, body)
	}
	if !strings.Contains(body["error"].(string), "no-such-projects") {
		t.Fatalf("error must name the missing path: %v", body)
	}
	if _, body = httpGet(t, srv.URL+"/api/provider"); body["provider"] != "opencode" {
		t.Fatalf("failed switch must keep opencode active: %v", body)
	}
}

func TestPostProviderRejectsBadBody(t *testing.T) {
	restoreProvider(t)
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
