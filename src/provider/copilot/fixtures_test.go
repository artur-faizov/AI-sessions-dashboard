package copilot

import (
	"database/sql"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureNow is after every fixture event and outside the active window.
var fixtureNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()

// fixtureRoot copies testdata/home into a temp dir and returns a provider on it.
func fixtureRoot(t *testing.T) (*Provider, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "home")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Provider{root: provider.NewRoot(root)}, root
}

type fixtureRow struct {
	session, agent          string
	in, out, cr, cw, reason int64
	details, createdAt      string
}

const usageTableSQL = `CREATE TABLE assistant_usage_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	turn_index INTEGER,
	agent_id TEXT,
	parent_tool_call_id TEXT,
	model TEXT NOT NULL,
	input_tokens INTEGER,
	output_tokens INTEGER,
	cache_read_tokens INTEGER,
	cache_write_tokens INTEGER,
	reasoning_tokens INTEGER,
	total_nano_aiu INTEGER,
	duration_ms INTEGER,
	token_details_json TEXT,
	created_at TEXT DEFAULT (datetime('now'))
)`

// makeStoreDB writes a session-store.db at path. withTable=false creates a DB
// without assistant_usage_events.
func makeStoreDB(t *testing.T, path string, withTable bool, rows ...fixtureRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if !withTable {
		return
	}
	if _, err := db.Exec(usageTableSQL); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		var agent, details any
		if r.agent != "" {
			agent = r.agent
		}
		if r.details != "" {
			details = r.details
		}
		if _, err := db.Exec(`INSERT INTO assistant_usage_events
			(session_id, agent_id, model, input_tokens, output_tokens, cache_read_tokens,
			 cache_write_tokens, reasoning_tokens, token_details_json, created_at)
			VALUES (?, ?, 'model-a', ?, ?, ?, ?, ?, ?, ?)`,
			r.session, agent, r.in, r.out, r.cr, r.cw, r.reason, details, r.createdAt); err != nil {
			t.Fatal(err)
		}
	}
}

// fixtureRows are the usage rows for testdata session rows-0005. The first
// row reports cache-inclusive input_tokens plus token_details_json; the
// fresh input (10) must win.
var fixtureRows = []fixtureRow{
	{session: "rows-0005", in: 115, out: 20, cr: 100, cw: 5, reason: 2,
		details: `{"input":{"tokenCount":10},"cache_read":{"tokenCount":100}}`, createdAt: "2026-01-01 10:00:02"},
	{session: "rows-0005", in: 12, out: 30, cr: 200, cw: 0, reason: 0, createdAt: "2026-01-01 10:00:03"},
	{session: "rows-0005", agent: "call-R", in: 7, out: 9, cr: 50, cw: 1, reason: 3, createdAt: "2026-01-01 10:00:06"},
}

func fixtureWithDB(t *testing.T) *Provider {
	t.Helper()
	p, root := fixtureRoot(t)
	makeStoreDB(t, filepath.Join(root, "session-store.db"), true, fixtureRows...)
	return p
}

func findNode(nodes []*provider.SessionNode, id string) *provider.SessionNode {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
		if f := findNode(n.Children, id); f != nil {
			return f
		}
	}
	return nil
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s mismatch\n got: %s\nwant: %s", name, got, want)
	}
}

func TestFixturesExist(t *testing.T) {
	for _, sid := range []string{"plain-0001", "sub-0002", "shutdown-0003", "garbled-0004", "rows-0005", "otel-0006"} {
		for _, f := range []string{"events.jsonl", "workspace.yaml"} {
			if _, err := os.Stat(filepath.Join("testdata", "home", "session-state", sid, f)); err != nil {
				t.Errorf("fixture missing: %v", err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join("testdata", "home", "otel", "export.jsonl")); err != nil {
		t.Errorf("fixture missing: %v", err)
	}
}

func TestMakeStoreDBCreatesUsageRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-store.db")
	makeStoreDB(t, path, true, fixtureRows...)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assistant_usage_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(fixtureRows) {
		t.Fatalf("rows = %d, want %d", n, len(fixtureRows))
	}
}
