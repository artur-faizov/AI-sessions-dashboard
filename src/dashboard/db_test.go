package dashboard

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
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

// activateDB points the global db state at path (valid opens the read-only pool).
func activateDB(t *testing.T, path string) {
	t.Helper()
	oldPath, oldGen, oldPool := dbx.path, dbx.gen, dbx.pool
	t.Cleanup(func() {
		dbx.mu.Lock()
		defer dbx.mu.Unlock()
		dbx.path, dbx.gen, dbx.pool = oldPath, oldGen, oldPool
	})
	dbx.reset(path, path)
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

func TestSetDBSwitchesToValidPath(t *testing.T) {
	path := makeDB(t)
	oldPath, oldGen := dbx.path, dbx.gen
	t.Cleanup(func() { dbx.reset(oldPath, "") })
	ok, err := dbx.set(path)
	if !ok || err != "" {
		t.Fatalf("ok=%v err=%q", ok, err)
	}
	if dbx.path != path {
		t.Fatalf("path %s, want %s", dbx.path, path)
	}
	if dbx.gen != oldGen+1 {
		t.Fatalf("gen %d, want %d", dbx.gen, oldGen+1)
	}
	_, pool := poolFor()
	var n int
	if err := pool.QueryRow("SELECT count(*) FROM session").Scan(&n); err != nil {
		t.Fatal(err)
	}
}

func TestSetDBRejectsInvalidAndKeepsState(t *testing.T) {
	activateDB(t, makeDB(t))
	oldPath, oldGen := dbx.path, dbx.gen
	ok, err := dbx.set("/nope/does-not-exist.db")
	if ok || err == "" {
		t.Fatalf("expected failure with error, got ok=%v err=%q", ok, err)
	}
	if dbx.path != oldPath || dbx.gen != oldGen {
		t.Fatal("state changed on rejected switch")
	}
}

func TestSetDBReconnectsAfterGenerationBump(t *testing.T) {
	p1 := makeDB(t)
	p2 := makeDB(t)
	activateDB(t, p1)
	_, pool := poolFor()
	var n1 int
	pool.QueryRow("SELECT count(*) FROM session").Scan(&n1)
	if ok, err := dbx.set(p2); !ok {
		t.Fatalf("switch failed: %s", err)
	}
	p, pool2 := poolFor()
	if p != p2 || pool2 == pool {
		t.Fatal("pool did not reconnect after generation bump")
	}
	var n2 int
	if err := pool2.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n2); err != nil {
		t.Fatal(err)
	}
}
