package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE message (
  id text PRIMARY KEY,
  session_id text NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  data text NOT NULL
);
CREATE TABLE part (
  id text PRIMARY KEY,
  message_id text NOT NULL,
  session_id text NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  data text NOT NULL
);
CREATE TABLE session (
  id text PRIMARY KEY,
  parent_id text,
  title text,
  agent text,
  model text,
  directory text NOT NULL,
  tokens_input integer DEFAULT 0 NOT NULL,
  tokens_output integer DEFAULT 0 NOT NULL,
  tokens_reasoning integer DEFAULT 0 NOT NULL,
  tokens_cache_read integer DEFAULT 0 NOT NULL,
  tokens_cache_write integer DEFAULT 0 NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  time_archived integer
);
`

func openRW(t *testing.T, path string) *sql.DB {
	t.Helper()
	pool, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return pool
}

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

func makeDB(t *testing.T) string {
	t.Helper()
	path := tempDBPath(t)
	pool := openRW(t, path)
	pool.Close()
	return path
}

func jdump(o any) string {
	b, _ := json.Marshal(o)
	return string(b)
}

// activateDB returns a provider pointed at path (a valid path opens the read-only pool).
func activateDB(t *testing.T, path string) *Provider {
	t.Helper()
	p := &Provider{}
	p.reset(path, path)
	t.Cleanup(func() { p.reset("", "") })
	return p
}

// poolFor returns the current path and (lazily opened) pool.
func (p *Provider) poolFor() (string, *sql.DB) {
	p.ensurePool()
	path, pool, _ := p.current()
	return path, pool
}

func TestValidateDBWithSessionTablePasses(t *testing.T) {
	path := makeDB(t)
	ok, err := validateDB(path)
	if !ok {
		t.Fatalf("expected ok, got error: %s", err)
	}
	if err != "" {
		t.Fatalf("expected nil error, got %q", err)
	}
}

func TestValidateDBMissingFileFails(t *testing.T) {
	ok, err := validateDB(filepath.Join(t.TempDir(), "no-such-opencode.db"))
	if ok {
		t.Fatal("expected failure")
	}
	if err == "" {
		t.Fatal("expected an error message")
	}
}

func TestValidateDBNonSqliteFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.db")
	os.WriteFile(path, []byte("this is not a sqlite database at all"), 0o644)
	ok, err := validateDB(path)
	if ok || err == "" {
		t.Fatalf("expected failure with error, got ok=%v err=%q", ok, err)
	}
}

func TestValidateDBWithoutSessionTableFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	pool, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec("CREATE TABLE other (x integer)"); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	ok, errStr := validateDB(path)
	if ok || errStr == "" {
		t.Fatalf("expected failure with error, got ok=%v err=%q", ok, errStr)
	}
}

func TestDBCandidatesPriorityOrder(t *testing.T) {
	cands := dbCandidates("/flag/x.db", "/env/x.db")
	if len(cands) < 2 || cands[0] != "/flag/x.db" || cands[1] != "/env/x.db" {
		t.Fatalf("bad order: %v", cands[:2])
	}
}

func TestDBCandidatesXDGOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg-home")
	cands := dbCandidates("", "")
	want := filepath.Join("/xdg-home", "opencode", "opencode.db")
	found := false
	for _, c := range cands {
		if c == want {
			found = true
		}
		if c == filepath.Join(homeDir(), ".local", "share", "opencode", "opencode.db") {
			t.Fatal("default XDG path leaked through override")
		}
	}
	if !found {
		t.Fatalf("%v missing %s", cands, want)
	}
}

func TestDBCandidatesNoDuplicates(t *testing.T) {
	cands := dbCandidates("/same.db", "/same.db")
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c] {
			t.Fatalf("duplicate candidate: %s", c)
		}
		seen[c] = true
	}
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func TestResolveDBFirstValidCandidatePicked(t *testing.T) {
	path := makeDB(t)
	t.Setenv("DB_PATH", path)
	resolved, found := resolveDB("/nope/flag.db")
	if !found {
		t.Fatal("expected found")
	}
	if resolved != path {
		t.Fatalf("resolved %s, want %s", resolved, path)
	}
}

func TestResolveDBNotFoundReportsFirstCandidate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("DB_PATH", "")
	resolved, found := resolveDB("/nope/flag.db")
	if found {
		t.Fatal("expected not found")
	}
	if resolved != "/nope/flag.db" {
		t.Fatalf("resolved %s, want first candidate", resolved)
	}
}

func TestSetDataPathSwitchesToValidPath(t *testing.T) {
	path := makeDB(t)
	p := activateDB(t, "/no-such-db/start.db")
	_, _, oldGen := p.current()
	if err := p.SetDataPath(path); err != nil {
		t.Fatalf("switch failed: %v", err)
	}
	cur, _, gen := p.current()
	if cur != path || p.DataPath() != path {
		t.Fatalf("path %s, want %s", cur, path)
	}
	if gen != oldGen+1 {
		t.Fatalf("gen %d, want %d", gen, oldGen+1)
	}
	if err := p.Available(); err != nil {
		t.Fatalf("switched DB must be available: %v", err)
	}
	_, pool := p.poolFor()
	var n int
	if err := pool.QueryRow("SELECT count(*) FROM session").Scan(&n); err != nil {
		t.Fatal(err)
	}
}

func TestSetDataPathRejectsInvalidAndKeepsState(t *testing.T) {
	good := makeDB(t)
	p := activateDB(t, good)
	oldPath, _, oldGen := p.current()
	err := p.SetDataPath("/nope/does-not-exist.db")
	if err == nil || !strings.Contains(err.Error(), "/nope/does-not-exist.db") {
		t.Fatalf("expected error naming the path, got %v", err)
	}
	if path, _, gen := p.current(); path != oldPath || gen != oldGen {
		t.Fatal("state changed on rejected switch")
	}
	if _, err := p.BuildTree(nowMS()); err != nil {
		t.Fatalf("previous DB must keep serving: %v", err)
	}
}

func TestSetDataPathErrorNamesPathForNonOpencodeDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	pool, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec("CREATE TABLE other (x integer)"); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	p := activateDB(t, makeDB(t))
	if err := p.SetDataPath(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("expected error naming %s, got %v", path, err)
	}
}

func TestSetDataPathReconnectsAfterGenerationBump(t *testing.T) {
	p1 := makeDB(t)
	p2 := makeDB(t)
	p := activateDB(t, p1)
	_, pool := p.poolFor()
	var n1 int
	pool.QueryRow("SELECT count(*) FROM session").Scan(&n1)
	if err := p.SetDataPath(p2); err != nil {
		t.Fatalf("switch failed: %v", err)
	}
	path, pool2 := p.poolFor()
	if path != p2 || pool2 == pool {
		t.Fatal("pool did not reconnect after generation bump")
	}
	var n2 int
	if err := pool2.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n2); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSetDataPathAndReads(t *testing.T) {
	paths := make([]string, 2)
	for i := range paths {
		pool, path := fixturePool(t)
		seedSession(t, pool, "s1", nowMS(), nil, nil, nil, 1)
		seedMsg(t, pool, "u1", "s1", 1000, "user", false)
		seedMsg(t, pool, "a1", "s1", 2000, "assistant", true)
		pool.Close()
		paths[i] = path
	}
	p := activateDB(t, paths[0])
	stop := make(chan struct{})
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				roots, err := p.BuildTree(nowMS())
				if err == nil && len(roots) != 1 {
					err = fmt.Errorf("tree has %d roots", len(roots))
				}
				if err != nil {
					errs <- err
					return
				}
				calls, err := p.SessionCalls("s1")
				if err == nil && len(calls) != 1 {
					err = fmt.Errorf("got %d calls", len(calls))
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if err := p.SetDataPath(paths[i%2]); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("read failed during switch: %v", err)
	}
}

func TestNewResolvesCLIThenEnvThenDefault(t *testing.T) {
	cli := makeDB(t)
	env := makeDB(t)
	xdg := t.TempDir()
	def := filepath.Join(xdg, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(def), 0o755); err != nil {
		t.Fatal(err)
	}
	openRW(t, def).Close()
	t.Setenv("XDG_DATA_HOME", xdg)

	t.Setenv("DB_PATH", env)
	if got := New(cli).DataPath(); got != cli {
		t.Fatalf("cli must win: %s", got)
	}
	if got := New("").DataPath(); got != env {
		t.Fatalf("env must win over default: %s", got)
	}
	t.Setenv("DB_PATH", "")
	if got := New("").DataPath(); got != def {
		t.Fatalf("default must be used last: %s", got)
	}
	if got := New("/nope/flag.db").DataPath(); got != def {
		t.Fatalf("invalid cli must fall through to a valid default: %s", got)
	}
}

func TestNewMissingDBStaysServable(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("DB_PATH", "")
	p := New("/nope/flag.db")
	if p.DataPath() != "/nope/flag.db" {
		t.Fatalf("missing DB must report the first candidate, got %s", p.DataPath())
	}
	if err := p.Available(); err == nil || !strings.Contains(err.Error(), "/nope/flag.db") {
		t.Fatalf("Available must name the missing path, got %v", err)
	}
	if p.FatalWhenMissing() {
		t.Fatal("opencode must serve even when the DB is missing")
	}
	if _, err := p.BuildTree(nowMS()); err == nil {
		t.Fatal("BuildTree must fail without a DB")
	}
}

func TestCapabilities(t *testing.T) {
	t.Setenv("DB_PATH", "/env/x.db")
	p := &Provider{cliPath: "/flag/x.db"}
	if p.Name() != "opencode" || !p.PathSwitchable() {
		t.Fatalf("name=%s switchable=%v", p.Name(), p.PathSwitchable())
	}
	c := p.Candidates()
	if len(c) < 2 || c[0] != "/flag/x.db" || c[1] != "/env/x.db" {
		t.Fatalf("candidates %v", c)
	}
}

func TestParseDBFlag(t *testing.T) {
	cases := map[string][]string{
		"/a.db": {"--db", "/a.db"},
		"/b.db": {"--db=/b.db"},
		"/c.db": {"-db=/c.db"},
		"/d.db": {"--dev", "-db", "/d.db"},
		"":      {"--provider", "claude"},
	}
	for want, argv := range cases {
		if got := ParseDBFlag(argv); got != want {
			t.Fatalf("%v -> %q, want %q", argv, got, want)
		}
	}
}

func TestOpencodeNodesCarrySource(t *testing.T) {
	pool, path := fixturePool(t)
	defer pool.Close()
	seedSession(t, pool, "s1", nowMS(), nil, nil, nil, 1)
	p := activateDB(t, path)
	roots := mustTree(t, p)
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
