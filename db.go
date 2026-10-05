package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

var errDBNotOpen = errors.New("db_not_found")

// dbx is the switchable read-only connection pool (one *sql.DB per generation).
var dbx = newDBState()

type dbState struct {
	mu   sync.RWMutex
	path string
	gen  int
	pool *sql.DB
}

func newDBState() *dbState { return &dbState{} }

// reset swaps the active database; validPath non-empty opens the pool.
func (d *dbState) reset(path, validPath string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.path = path
	d.gen++
	old := d.pool
	d.pool = nil
	if old != nil {
		old.Close()
	}
	if validPath != "" {
		pool, _ := sql.Open("sqlite", roDSN(validPath))
		d.pool = pool
	}
}

func (d *dbState) set(path string) (bool, string) {
	ok, errStr := validateDB(path)
	if !ok {
		return false, errStr
	}
	d.mu.Lock()
	old := d.pool
	pool, err := sql.Open("sqlite", roDSN(path))
	if err != nil {
		d.mu.Unlock()
		return false, err.Error()
	}
	d.path = path
	d.gen++
	d.pool = pool
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return true, ""
}

func (d *dbState) current() (string, *sql.DB, int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.path, d.pool, d.gen
}

// poolFor returns the current pool for a valid path, or nil.
func poolFor() (string, *sql.DB) {
	p, pool, _ := dbx.current()
	if p == "" {
		return p, nil
	}
	if pool == nil {
		var err error
		pool, err = sql.Open("sqlite", roDSN(p))
		if err != nil {
			return p, nil
		}
		dbx.mu.Lock()
		if dbx.pool == nil {
			dbx.pool = pool
		} else {
			pool.Close()
			pool = dbx.pool
		}
		dbx.mu.Unlock()
	}
	return p, pool
}

// quotePath percent-encodes a filesystem path for a sqlite file: URI,
// mirroring urllib.parse.quote (safe chars kept: alnum, "-._~/" and ":").
func quotePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if isURIUnreserved(r) || r == '/' || r == ':' {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isURIUnreserved(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		strings.ContainsRune("-._~", r)
}

func roDSN(path string) string {
	return "file:" + quotePath(path) + "?mode=ro"
}

// validateDB reports whether path exists, opens read-only, and has the session table.
func validateDB(path string) (bool, string) {
	if path == "" {
		return false, "empty path"
	}
	if _, err := os.Stat(path); err != nil {
		return false, fmt.Sprintf("file not found: %s", path)
	}
	pool, err := sql.Open("sqlite", roDSN(path))
	if err != nil {
		return false, err.Error()
	}
	defer pool.Close()
	if err := pool.Ping(); err != nil {
		return false, err.Error()
	}
	rows, err := pool.Query("SELECT name FROM sqlite_master WHERE type='table' AND name='session' LIMIT 1")
	if err != nil {
		return false, err.Error()
	}
	defer rows.Close()
	if !rows.Next() {
		return false, "no session table"
	}
	return true, ""
}

// parseDBFlag extracts the --db PATH / --db=PATH CLI override.
func parseDBFlag(argv []string) (string, bool) {
	for i, a := range argv {
		if a == "--db" || a == "-db" {
			if i+1 < len(argv) {
				return argv[i+1], true
			}
		}
		if strings.HasPrefix(a, "--db=") {
			return strings.TrimPrefix(a, "--db="), true
		}
		if strings.HasPrefix(a, "-db=") {
			return strings.TrimPrefix(a, "-db="), true
		}
	}
	return "", false
}

// dbCandidates returns candidate DB paths in priority order (cli, env, platform defaults), deduplicated.
func dbCandidates(cliPath, envPath string) []string {
	var out []string
	if cliPath != "" {
		out = append(out, expandUser(cliPath))
	}
	if envPath != "" {
		out = append(out, expandUser(envPath))
	}
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".local", "share")
	}
	out = append(out, filepath.Join(xdg, "opencode", "opencode.db"))
	switch runtime.GOOS {
	case "darwin":
		out = append(out, filepath.Join(home, "Library", "Application Support", "opencode", "opencode.db"))
	case "windows":
		for _, v := range []string{"APPDATA", "LOCALAPPDATA"} {
			if base := os.Getenv(v); base != "" {
				out = append(out, filepath.Join(base, "opencode", "opencode.db"))
			}
		}
	}
	seen := map[string]bool{}
	var uniq []string
	for _, p := range out {
		if !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	return uniq
}

// resolveDB picks the first valid candidate; else reports the first candidate with found=false.
func resolveDB(cliPath string) (string, bool) {
	cands := dbCandidates(cliPath, os.Getenv("DB_PATH"))
	for _, p := range cands {
		if ok, _ := validateDB(p); ok {
			return p, true
		}
	}
	if len(cands) == 0 {
		return "", false
	}
	return cands[0], false
}

func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
