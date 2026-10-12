package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// apiServerWith serves ps with active selected.
func apiServerWith(t *testing.T, ps []provider.Provider, active provider.Provider) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newServer(false, ps, active))
	t.Cleanup(srv.Close)
	return srv
}

// apiServer serves a single lenient, switchable provider with data present.
func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	p := newLenient("alpha", "/data/alpha.db")
	return apiServerWith(t, []provider.Provider{p}, p)
}

func httpGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	return r.StatusCode, body
}

func httpGetRaw(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r, string(b)
}

func httpPost(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	r, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	return r.StatusCode, out
}

// missingLenient is a switchable provider whose current path does not exist;
// /data/good.db is the one valid switch target.
func missingLenient() *fakeProvider {
	p := newLenient("alpha", "/no-such-db/api-test.db")
	p.validPaths = map[string]bool{"/data/good.db": true}
	return p
}

func serveOne(t *testing.T, p *fakeProvider) *httptest.Server {
	t.Helper()
	return apiServerWith(t, []provider.Provider{p}, p)
}

func TestAPIDbReportsNotFoundState(t *testing.T) {
	srv := serveOne(t, missingLenient())
	status, body := httpGet(t, srv.URL+"/api/db")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if body["found"] != false {
		t.Fatal("found must be false")
	}
	if body["path"] != "/no-such-db/api-test.db" {
		t.Fatalf("path %v", body["path"])
	}
	if e, _ := body["error"].(string); !strings.Contains(e, "/no-such-db/api-test.db") {
		t.Fatalf("error must name the path: %v", body["error"])
	}
	if body["switchable"] != true {
		t.Fatalf("switchable %v, want true even when data is missing", body["switchable"])
	}
	if cands, ok := body["candidates"].([]any); !ok || len(cands) == 0 {
		t.Fatal("candidates missing")
	}
}

func TestAPIDbSwitchableProviderReportsCandidates(t *testing.T) {
	srv := serveOne(t, newLenient("alpha", "/data/alpha.db"))
	_, body := httpGet(t, srv.URL+"/api/db")
	if body["switchable"] != true || body["found"] != true || body["error"] != nil {
		t.Fatalf("body %v", body)
	}
	if body["path"] != "/data/alpha.db" {
		t.Fatalf("path %v", body["path"])
	}
	cands, _ := body["candidates"].([]any)
	if len(cands) != 2 || cands[0] != "/data/alpha.db" || cands[1] != "/default/alpha.db" {
		t.Fatalf("candidates %v", body["candidates"])
	}
}

func TestAPIDbNonSwitchableProviderReportsNoCandidates(t *testing.T) {
	for _, present := range []bool{true, false} {
		p := newStrict("beta", "/data/beta", present)
		p.cands = []string{"/must/not/leak"}
		srv := serveOne(t, p)
		r, raw := httpGetRaw(t, srv.URL+"/api/db")
		if r.StatusCode != 200 {
			t.Fatalf("status %d", r.StatusCode)
		}
		if !strings.Contains(raw, `"candidates":[]`) || !strings.Contains(raw, `"switchable":false`) {
			t.Fatalf("present=%v: %s", present, raw)
		}
		if !strings.Contains(raw, `"path":"/data/beta"`) || !strings.Contains(raw, fmt.Sprintf(`"found":%v`, present)) {
			t.Fatalf("present=%v: %s", present, raw)
		}
	}
}

func TestAPIDbNilCandidatesSerializeAsEmptyList(t *testing.T) {
	p := newLenient("alpha", "/data/alpha.db")
	p.cands = nil
	srv := serveOne(t, p)
	if _, raw := httpGetRaw(t, srv.URL+"/api/db"); !strings.Contains(raw, `"candidates":[]`) {
		t.Fatalf("nil candidates must be []: %s", raw)
	}
}

func TestTreeReturnsDBNotFoundWithoutDB(t *testing.T) {
	srv := serveOne(t, missingLenient())
	status, body := httpGet(t, srv.URL+"/api/tree")
	if status != 503 {
		t.Fatalf("status %d", status)
	}
	if body["error"] != "db_not_found" || body["path"] != "/no-such-db/api-test.db" {
		t.Fatalf("body %v", body)
	}
}

func TestTreeReturnsEmptyArrayWithValidDB(t *testing.T) {
	srv := apiServer(t)
	r, body := httpGetRaw(t, srv.URL+"/api/tree")
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Fatalf("body %q", body)
	}
}

func TestPostDBSwitchesToValidPath(t *testing.T) {
	p := missingLenient()
	srv := serveOne(t, p)
	status, body := httpPost(t, srv.URL+"/api/db", `{"path": "/data/good.db"}`)
	if status != 200 || body["ok"] != true {
		t.Fatalf("status %d body %v", status, body)
	}
	if body["path"] != "/data/good.db" {
		t.Fatalf("path %v", body["path"])
	}
	_, body = httpGet(t, srv.URL+"/api/db")
	if body["found"] != true || body["path"] != "/data/good.db" {
		t.Fatalf("found %v path %v", body["found"], body["path"])
	}
	if status, _ = httpGet(t, srv.URL+"/api/tree"); status != 200 {
		t.Fatalf("tree status %d", status)
	}
}

func TestPostDBRejectsBadPathWith400(t *testing.T) {
	p := missingLenient()
	srv := serveOne(t, p)
	status, body := httpPost(t, srv.URL+"/api/db", `{"path": "/nope/bad.db"}`)
	if status != 400 || body["ok"] != false {
		t.Fatalf("status %d body %v", status, body)
	}
	if e, _ := body["error"].(string); !strings.Contains(e, "/nope/bad.db") {
		t.Fatalf("error must name the path: %v", body["error"])
	}
	if p.DataPath() != "/no-such-db/api-test.db" {
		t.Fatalf("previous path must stay active, got %s", p.DataPath())
	}
}

func TestPostDBMissingPathRejected(t *testing.T) {
	srv := serveOne(t, missingLenient())
	status, body := httpPost(t, srv.URL+"/api/db", `{}`)
	if status != 400 || body["ok"] != false || body["path"] != nil {
		t.Fatalf("status %d body %v", status, body)
	}
}

func TestPostDBRejectedForNonSwitchableProvider(t *testing.T) {
	p := newStrict("beta", "/data/beta", true)
	p.validPaths["/data/other"] = true
	srv := serveOne(t, p)
	status, body := httpPost(t, srv.URL+"/api/db", `{"path": "/data/other"}`)
	if status != 400 || body["ok"] != false {
		t.Fatalf("status %d body %v", status, body)
	}
	if body["error"] != "path switching is not supported for the active provider" {
		t.Fatalf("error %v", body["error"])
	}
	if p.DataPath() != "/data/beta" {
		t.Fatalf("path changed to %s", p.DataPath())
	}
}

func TestPostDBMalformedJSON400(t *testing.T) {
	srv := apiServer(t)
	status, body := httpPost(t, srv.URL+"/api/db", `{not json`)
	if status != 400 || body["error"] != "invalid JSON body" {
		t.Fatalf("status %d body %v", status, body)
	}
}

func TestPostOtherPaths404(t *testing.T) {
	srv := apiServer(t)
	status, _ := httpPost(t, srv.URL+"/api/nope", `{"x":1}`)
	if status != 404 {
		t.Fatalf("status %d", status)
	}
}

func TestRootPageServesHTMLWithVersion(t *testing.T) {
	srv := apiServer(t)
	r, body := httpGetRaw(t, srv.URL+"/")
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	if strings.Contains(body, "{{VERSION}}") {
		t.Fatal("version placeholder not substituted")
	}
	if !strings.Contains(body, Version) {
		t.Fatal("version missing from page")
	}
}

func TestI18nServedWithJSContentType(t *testing.T) {
	srv := apiServer(t)
	r, body := httpGetRaw(t, srv.URL+"/i18n.js")
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if !strings.Contains(body, "I18N") || !strings.Contains(body, "en") || !strings.Contains(body, "ru") {
		t.Fatal("i18n content missing")
	}
}

func TestAlertSoundsServed(t *testing.T) {
	srv := apiServer(t)
	for _, name := range []string{"ask", "perm", "done"} {
		r, body := httpGetRaw(t, srv.URL+"/sounds/"+name+".oga")
		if r.StatusCode != 200 {
			t.Fatalf("%s: status %d", name, r.StatusCode)
		}
		if ct := r.Header.Get("Content-Type"); ct != "audio/ogg" {
			t.Fatalf("%s: content type %q", name, ct)
		}
		if !strings.HasPrefix(body, "OggS") {
			t.Fatalf("%s: not an ogg file", name)
		}
	}
	for _, p := range []string{"/sounds/nope.oga", "/sounds/../index.html", "/sounds/CREDITS"} {
		if r, _ := httpGetRaw(t, srv.URL+p); r.StatusCode != 404 {
			t.Fatalf("%s: status %d", p, r.StatusCode)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	srv := apiServer(t)
	r, _ := httpGetRaw(t, srv.URL+"/nope.html")
	if r.StatusCode != 404 {
		t.Fatalf("status %d", r.StatusCode)
	}
}

func TestAPICallsAndPromptWithSession(t *testing.T) {
	p := newLenient("alpha", "/data/alpha.db")
	p.prompts = map[string][]string{"s1": {"hello"}}
	p.calls = map[string][]provider.CallRec{"s1": {{Mid: "a1", Time: 100, In: 10, Cr: 1, Cw: 2, Out: 5, Reason: 3, Ctx: 13, Hu: provider.TrigText}}}
	p.triggers = map[string]*provider.TriggerResp{"a1": {Case: "user", Time: int64(50), Parts: []any{}}}
	srv := serveOne(t, p)

	status, body := httpGet(t, srv.URL+"/api/prompt/s1")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	texts, _ := body["texts"].([]any)
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("texts %v", texts)
	}

	status, body = httpGet(t, srv.URL+"/api/calls/s1")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	calls, _ := body["calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("calls %v", calls)
	}
	c := calls[0].(map[string]any)
	if c["ctx"].(float64) != 13 || c["hu"] != "text" {
		t.Fatalf("call %v", c)
	}

	status, body = httpGet(t, srv.URL+"/api/trigger/a1")
	if status != 200 || body["case"] != "user" {
		t.Fatalf("trigger status %d body %v", status, body)
	}

	status, body = httpGet(t, srv.URL+"/api/trigger/nope")
	if status != 404 || body["error"] != "message not found" {
		t.Fatalf("trigger 404: status %d body %v", status, body)
	}

	status, body = httpGet(t, srv.URL+"/api/calls/nope")
	if status != 500 || body["error"] == nil {
		t.Fatalf("calls error: status %d body %v", status, body)
	}
}

func TestDevModeServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.html"), []byte("<html>dev {{VERSION}}</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "i18n.js"), []byte("dev i18n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(old) })

	if got := loadPage(true); got != "<html>dev "+Version+"</html>" {
		t.Fatalf("page %q", got)
	}
	if got := loadI18n(true); got != "dev i18n" {
		t.Fatalf("i18n %q", got)
	}
}
