package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
	"github.com/artur-faizov/token-monitor/src/provider/folder"
)

type exportErr struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// postExport writes one file per requested top-level session (subagents
// included) into dir. Existing files are never overwritten: they are reported
// as skipped. Content-Type must be application/json, which a cross-site form
// cannot send without a CORS preflight.
func (s *server) postExport(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"ok": false, "error": "content type must be application/json"})
		return
	}
	var body struct {
		IDs   []string          `json:"ids"`
		Dir   string            `json:"dir"`
		Names map[string]string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	dir := provider.ExpandUser(strings.TrimSpace(body.Dir))
	if dir == "" || !filepath.IsAbs(dir) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "dir must be an absolute path"})
		return
	}
	if len(body.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "no sessions selected"})
		return
	}
	active := s.current()
	if err := active.Available(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	now := time.Now().UnixMilli()
	roots, err := active.BuildTree(now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	byID := map[string]*provider.SessionNode{}
	for _, n := range roots {
		byID[n.ID] = n
	}
	written, skipped, failed := []string{}, []string{}, []exportErr{}
	for _, id := range body.IDs {
		root := byID[id]
		if root == nil {
			failed = append(failed, exportErr{id, "not a top-level session"})
			continue
		}
		f, err := folder.Collect(active, root, body.Names, now)
		if err != nil {
			failed = append(failed, exportErr{id, err.Error()})
			continue
		}
		switch _, err := folder.Write(dir, f); {
		case errors.Is(err, folder.ErrExists):
			skipped = append(skipped, id)
		case err != nil:
			failed = append(failed, exportErr{id, err.Error()})
		default:
			written = append(written, id)
		}
	}
	if fp, ok := findProvider(s.providers, folder.SourceName).(interface{ Remember(string) }); ok {
		fp.Remember(dir)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(failed) == 0, "dir": dir, "written": written, "skipped": skipped, "errors": failed})
}

// getBrowse lists the sub-directories of ?path= (home when empty) for the
// folder picker. Hidden directories are left out.
func (s *server) getBrowse(w http.ResponseWriter, r *http.Request) {
	path := provider.ExpandUser(strings.TrimSpace(r.URL.Query().Get("path")))
	if path == "" {
		path, _ = os.UserHomeDir()
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "path": path})
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "parent": parent, "dirs": dirs})
}
