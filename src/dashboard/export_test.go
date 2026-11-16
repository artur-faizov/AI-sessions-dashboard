package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
	"github.com/artur-faizov/token-monitor/src/provider/folder"
)

func exportFixture(t *testing.T) (*fakeProvider, *folder.Provider) {
	t.Helper()
	p := newLenient("alpha", "/data/alpha.db")
	kid := &provider.SessionNode{ID: "kid", Parent: "s1", Children: []*provider.SessionNode{}}
	p.tree = []*provider.SessionNode{{ID: "s1", Title: "one", Children: []*provider.SessionNode{kid}}, {ID: "s2", Children: []*provider.SessionNode{}}}
	p.calls = map[string][]provider.CallRec{"s1": {{Mid: "m1"}}, "kid": {}, "s2": {}}
	p.prompts = map[string][]string{"s1": {"hi"}, "kid": {}, "s2": {}}
	p.triggers = map[string]*provider.TriggerResp{"m1": {Case: "text", Parts: []any{}}}
	return p, folder.New()
}

func TestExportWritesAndSkips(t *testing.T) {
	p, fp := exportFixture(t)
	srv := apiServerWith(t, []provider.Provider{p, fp})
	dir := filepath.Join(t.TempDir(), "out")
	body, _ := json.Marshal(map[string]any{"ids": []string{"alpha:s1", "alpha:kid", "alpha:nope", "s2"}, "dir": dir, "names": map[string]string{"alpha:s1": "renamed"}})

	code, res := httpPost(t, srv.URL+"/api/export", string(body))
	if code != 200 {
		t.Fatalf("status %d: %v", code, res)
	}
	if w := res["written"].([]any); len(w) != 1 || w[0] != "alpha:s1" {
		t.Fatalf("written: %v", res)
	}
	if e := res["errors"].([]any); len(e) != 3 { // kid is not top-level, nope is unknown, s2 has no provider prefix
		t.Fatalf("errors: %v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.json")); err != nil {
		t.Fatal(err)
	}

	_, res = httpPost(t, srv.URL+"/api/export", string(body))
	if s := res["skipped"].([]any); len(s) != 1 || s[0] != "alpha:s1" {
		t.Fatalf("second export must skip: %v", res)
	}

	// the export is readable through the folder provider, subagent included
	if err := fp.SetDataPath(dir); err != nil {
		t.Fatal(err)
	}
	nodes, _ := fp.BuildTree(0)
	if len(nodes) != 1 || nodes[0].Title != "renamed" || len(nodes[0].Children) != 1 {
		t.Fatalf("imported tree: %+v", nodes)
	}
	if got := fp.Candidates(); len(got) != 1 || got[0] != dir {
		t.Fatalf("candidates: %v", got)
	}
}

func TestExportValidation(t *testing.T) {
	p, fp := exportFixture(t)
	srv := apiServerWith(t, []provider.Provider{p, fp})
	for name, body := range map[string]string{
		"bad json":     `{`,
		"relative dir": `{"ids":["alpha:s1"],"dir":"rel/dir"}`,
		"no ids":       `{"ids":[],"dir":"/tmp/x"}`,
	} {
		if code, _ := httpPost(t, srv.URL+"/api/export", body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, code)
		}
	}
	r, err := http.Post(srv.URL+"/api/export", "text/plain", strings.NewReader(`{"ids":["s1"],"dir":"/tmp/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON content type: want 415, got %d", r.StatusCode)
	}
}

func TestBrowse(t *testing.T) {
	srv := apiServer(t)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "b"), 0o755)
	os.Mkdir(filepath.Join(root, "a"), 0o755)
	os.Mkdir(filepath.Join(root, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(root, "file"), nil, 0o644)

	code, res := httpGet(t, srv.URL+"/api/browse?path="+root)
	if code != 200 {
		t.Fatalf("status %d: %v", code, res)
	}
	d := res["dirs"].([]any)
	if len(d) != 2 || d[0] != "a" || d[1] != "b" {
		t.Fatalf("dirs: %v", d)
	}
	if res["parent"] != filepath.Dir(root) {
		t.Fatalf("parent: %v", res["parent"])
	}
	if code, _ := httpGet(t, srv.URL+"/api/browse?path="+filepath.Join(root, "missing")); code != http.StatusBadRequest {
		t.Fatalf("missing dir: want 400, got %d", code)
	}
}
