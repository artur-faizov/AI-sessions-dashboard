package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

type server struct {
	dev       bool
	providers []provider.Provider

	mu     sync.RWMutex
	active provider.Provider // swapped at runtime via POST /api/provider
}

func newServer(dev bool, providers []provider.Provider, active provider.Provider) *server {
	return &server{dev: dev, providers: providers, active: active}
}

// current returns the provider serving the API right now.
func (s *server) current() provider.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func (s *server) setActive(p provider.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = p
}

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
	active := s.current()
	if path == "/api/provider" {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":  active.Name(),
			"providers": sortedProviderNames(s.providers),
		})
		return
	}
	if path == "/api/browse" {
		s.getBrowse(w, r)
		return
	}
	if path == "/api/db" {
		found := true
		var errV any
		if err := active.Available(); err != nil {
			found = false
			errV = err.Error()
		}
		cands := []string{}
		if active.PathSwitchable() && active.Candidates() != nil {
			cands = active.Candidates()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":       active.DataPath(),
			"found":      found,
			"error":      errV,
			"candidates": cands,
			"switchable": active.PathSwitchable(),
		})
		return
	}
	if strings.HasPrefix(path, "/api/") {
		if err := active.Available(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_not_found", "path": active.DataPath()})
			return
		}
		switch {
		case path == "/api/tree":
			nodes, err := active.BuildTree(time.Now().UnixMilli())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, nodes)
		case strings.HasPrefix(path, "/api/prompt/"):
			sid := strings.TrimPrefix(path, "/api/prompt/")
			texts, err := active.SessionPrompt(sid)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"texts": texts})
		case strings.HasPrefix(path, "/api/calls/"):
			sid := strings.TrimPrefix(path, "/api/calls/")
			calls, err := active.SessionCalls(sid)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
		case strings.HasPrefix(path, "/api/trigger/"):
			mid := strings.TrimPrefix(path, "/api/trigger/")
			res, err := active.CallTrigger(mid)
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
	if r.URL.Path == "/api/export" {
		s.postExport(w, r)
		return
	}
	if r.URL.Path != "/api/db" {
		http.Error(w, "404 Not Found", http.StatusNotFound)
		return
	}
	active := s.current()
	if !active.PathSwitchable() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "path switching is not supported for the active provider",
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
	target := ""
	if body.Path != nil {
		reqPath = *body.Path
		target = *body.Path
	}
	err := active.SetDataPath(target)
	out := map[string]any{"ok": err == nil, "path": reqPath}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, statusOf(err == nil), out)
}

func statusOf(ok bool) int {
	if ok {
		return http.StatusOK
	}
	return http.StatusBadRequest
}

// postProvider switches the active data source. Lenient providers may be
// selected even when their data is missing; strict ones (FatalWhenMissing)
// must be available first.
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
	p := findProvider(s.providers, body.Name)
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("unknown provider %q (available: %s)", body.Name, strings.Join(sortedProviderNames(s.providers), ", ")),
		})
		return
	}
	if p.FatalWhenMissing() {
		if err := p.Available(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	s.setActive(p)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": p.Name()})
}
