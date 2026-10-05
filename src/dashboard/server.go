package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type server struct{ dev bool }

func newServer(dev bool) http.Handler { return &server{dev: dev} }

func writeJSON(w http.ResponseWriter, code int, obj any) {
	body, err := json.Marshal(obj)
	if err != nil {
		http.Error(w, "json error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	w.Write(body)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGet(w, r)
	case http.MethodPost:
		s.handlePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/provider" {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":  currentProvider().name(),
			"providers": providerNames(),
		})
		return
	}
	if path == "/api/db" {
		if currentProvider().name() == "claude" {
			found := true
			var errV any
			if err := currentProvider().available(); err != nil {
				found = false
				errV = err.Error()
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"path":       currentProvider().dataPath(),
				"found":      found,
				"error":      errV,
				"candidates": []string{},
			})
			return
		}
		p, _, _ := dbx.current()
		ok, errStr := validateDB(p)
		var errV any
		if !ok {
			errV = errStr
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":       p,
			"found":      ok,
			"error":      errV,
			"candidates": dbCandidates(cliDBFlag, os.Getenv("DB_PATH")),
		})
		return
	}
	if strings.HasPrefix(path, "/api/") {
		if err := currentProvider().available(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_not_found", "path": currentProvider().dataPath()})
			return
		}
		switch {
		case path == "/api/tree":
			nodes, err := currentProvider().buildTree(time.Now().UnixMilli())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, nodes)
		case strings.HasPrefix(path, "/api/prompt/"):
			sid := strings.TrimPrefix(path, "/api/prompt/")
			texts, err := currentProvider().sessionPrompt(sid)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"texts": texts})
		case strings.HasPrefix(path, "/api/calls/"):
			sid := strings.TrimPrefix(path, "/api/calls/")
			calls, err := currentProvider().sessionCalls(sid)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
		case strings.HasPrefix(path, "/api/trigger/"):
			mid := strings.TrimPrefix(path, "/api/trigger/")
			res, err := currentProvider().callTrigger(mid)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			if res == nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "message not found"})
				return
			}
			writeJSON(w, http.StatusOK, res)
		default:
			http.Error(w, "404 Not Found", http.StatusNotFound)
		}
		return
	}
	switch {
	case path == "/i18n.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(loadI18n(s.dev)))
	case path == "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(loadPage(s.dev)))
	default:
		http.Error(w, "404 Not Found", http.StatusNotFound)
	}
}

func (s *server) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/provider" {
		s.postProvider(w, r)
		return
	}
	if r.URL.Path != "/api/db" {
		http.Error(w, "404 Not Found", http.StatusNotFound)
		return
	}
	if currentProvider().name() == "claude" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "path switching is only supported for the opencode provider",
		})
		return
	}
	var body struct {
		Path *string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	var reqPath any
	if body.Path != nil {
		reqPath = *body.Path
	}
	ok, errStr := validateDB("")
	if body.Path != nil {
		ok, errStr = dbx.set(*body.Path)
	}
	out := map[string]any{"ok": ok, "path": reqPath}
	if errStr != "" {
		out["error"] = errStr
	}
	writeJSON(w, statusOf(ok), out)
}

func statusOf(ok bool) int {
	if ok {
		return http.StatusOK
	}
	return http.StatusBadRequest
}

// postProvider switches the active data source (opencode sqlite DB / claude
// projects dir). Lenient providers (opencode) may switch even when their data
// is missing; strict ones (claude) must be available first.
func (s *server) postProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing provider name"})
		return
	}
	p := selectProvider(body.Name)
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("unknown provider %q (available: %s)", body.Name, strings.Join(providerNames(), ", ")),
		})
		return
	}
	if p.fatalWhenMissing() {
		if err := p.available(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	setActiveProvider(p)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": p.name()})
}
