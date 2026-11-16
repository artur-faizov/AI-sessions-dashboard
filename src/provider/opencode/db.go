package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"

	_ "modernc.org/sqlite"
)

var errDBNotOpen = errors.New("db_not_found")

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

// ParseDBFlag extracts the --db PATH / --db=PATH CLI override ("" when absent).
func ParseDBFlag(argv []string) string {
	for i, a := range argv {
		if a == "--db" || a == "-db" {
			if i+1 < len(argv) {
				return argv[i+1]
			}
		}
		if strings.HasPrefix(a, "--db=") {
			return strings.TrimPrefix(a, "--db=")
		}
		if strings.HasPrefix(a, "-db=") {
			return strings.TrimPrefix(a, "-db=")
		}
	}
	return ""
}

// dbCandidates returns candidate DB paths in priority order (cli, env, platform defaults), deduplicated.
func dbCandidates(cliPath, envPath string) []string {
	var out []string
	if cliPath != "" {
		out = append(out, provider.ExpandUser(cliPath))
	}
	if envPath != "" {
		out = append(out, provider.ExpandUser(envPath))
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
