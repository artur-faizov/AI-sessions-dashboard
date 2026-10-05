package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newServer(false))
	t.Cleanup(srv.Close)
	return srv
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

func setNoDB(t *testing.T) {
	t.Helper()
	activateDB(t, "/no-such-db/api-test.db")
}

func TestAPIDbReportsNotFoundState(t *testing.T) {
	setNoDB(t)
	srv := apiServer(t)
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
	if cands, ok := body["candidates"].([]any); !ok || len(cands) == 0 {
		t.Fatal("candidates missing")
	}
}

func TestTreeReturnsDBNotFoundWithoutDB(t *testing.T) {
	setNoDB(t)
	srv := apiServer(t)
	status, body := httpGet(t, srv.URL+"/api/tree")
	if status != 503 {
		t.Fatalf("status %d", status)
	}
	if body["error"] != "db_not_found" {
		t.Fatalf("error %v", body["error"])
	}
}

func TestTreeReturnsEmptyArrayWithValidDB(t *testing.T) {
	activateDB(t, makeDB(t))
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
	setNoDB(t)
	srv := apiServer(t)
	path := makeDB(t)
	status, body := httpPost(t, srv.URL+"/api/db", `{"path": "`+path+`"}`)
	if status != 200 || body["ok"] != true {
		t.Fatalf("status %d body %v", status, body)
	}
	if body["path"] != path {
		t.Fatalf("path %v", body["path"])
	}
	status, body = httpGet(t, srv.URL+"/api/db")
	if body["found"] != true || body["path"] != path {
		t.Fatalf("found %v path %v", body["found"], body["path"])
	}
	status, _ = httpGet(t, srv.URL+"/api/tree")
	if status != 200 {
		t.Fatalf("tree status %d", status)
	}
}

func TestPostDBRejectsBadPathWith400(t *testing.T) {
	setNoDB(t)
	srv := apiServer(t)
	status, body := httpPost(t, srv.URL+"/api/db", `{"path": "/nope/bad.db"}`)
	if status != 400 || body["ok"] != false {
		t.Fatalf("status %d body %v", status, body)
	}
	if body["error"] == nil {
		t.Fatal("error message missing")
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

func TestUnknownPath404(t *testing.T) {
	srv := apiServer(t)
	r, _ := httpGetRaw(t, srv.URL+"/nope.html")
	if r.StatusCode != 404 {
		t.Fatalf("status %d", r.StatusCode)
	}
}

func TestAPICallsAndPromptWithSession(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	if _, err := pool.Exec(`INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"s1", nil, "title", "agent", nil, "/d", 1, 2, 3, 4, 5, 100, 200, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "u1", "s1", 50, 50,
		`{"role":"user","time":{"created":50}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO part VALUES (?,?,?,?,?,?)`, "p1", "u1", "s1", 60, 60,
		`{"type":"text","text":"hello"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO message VALUES (?,?,?,?,?)`, "a1", "s1", 100, 100,
		`{"role":"assistant","tokens":{"input":10,"output":5,"cache":{"read":1,"write":2},"reasoning":3}}`); err != nil {
		t.Fatal(err)
	}
	activateDB(t, path)
	srv := apiServer(t)

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
	if c["ctx"].(float64) != 13 || c["hu"] != true {
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
