package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	webui "github.com/artur-faizov/token-monitor/src"
	"github.com/artur-faizov/token-monitor/src/provider"
)

type server struct {
	dev       bool
	providers []provider.Provider // served together, in priority order

	mu  sync.RWMutex
	off map[string]bool // providers switched off in the settings
}

func newServer(dev bool, providers []provider.Provider) *server {
	return &server{dev: dev, providers: providers, off: map[string]bool{}}
}

// enabled reports whether the provider named name is part of the merged tree.
func (s *server) enabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.off[name]
}

func (s *server) setEnabled(name string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		delete(s.off, name)
	} else {
		s.off[name] = true
	}
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
	if path == "/api/browse" {
		s.getBrowse(w, r)
		return
	}
	if path == "/api/db" {
		s.getDB(w)
		return
	}
	if path == "/api/tree" {
		if len(s.available()) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_not_found"})
			return
		}
		nodes, err := s.mergedTree(time.Now().UnixMilli())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, nodes)
		return
	}
	if strings.HasPrefix(path, "/api/") {
		s.getByID(w, path)
		return
	}
	switch {
	case path == "/i18n.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(loadI18n(s.dev)))
	case strings.HasPrefix(path, "/sounds/") && !strings.Contains(path[len("/sounds/"):], "/"):
		b, ok := webui.Sound(path[len("/sounds/"):])
		if !ok {
			http.Error(w, "404 Not Found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "audio/ogg")
		w.Header().Set("Cache-Control", "max-age=86400")
		// ServeContent: Content-Length and Range requests, which media elements use
		http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(b))
	case strings.HasPrefix(path, "/icons/") && !strings.Contains(path[len("/icons/"):], "/"):
		b, ok := webui.Icon(path[len("/icons/"):])
		if !ok {
			http.Error(w, "404 Not Found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	case path == "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(loadPage(s.dev)))
	default:
		http.Error(w, "404 Not Found", http.StatusNotFound)
	}
}

// getByID serves the per-session and per-call endpoints. Their id is
// qualified ("<provider>:<own id>"); the provider that owns it answers.
func (s *server) getByID(w http.ResponseWriter, path string) {
	var kind, id string
	for _, k := range []string{"prompt", "calls", "trigger", "context"} {
		if strings.HasPrefix(path, "/api/"+k+"/") {
			kind, id = k, strings.TrimPrefix(path, "/api/"+k+"/")
			break
		}
	}
	if kind == "" {
		http.Error(w, "404 Not Found", http.StatusNotFound)
		return
	}
	p, raw := s.route(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown provider in id " + id})
		return
	}
	if err := p.Available(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_not_found", "path": p.DataPath()})
		return
	}
	name := p.Name()
	switch kind {
	case "prompt":
		texts, err := p.SessionPrompt(raw)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"texts": texts})
	case "calls":
		calls, err := p.SessionCalls(raw)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		resp := map[string]any{"calls": qualifiedCalls(name, calls)}
		if sp, ok := p.(provider.SegmentProvider); ok {
			if segs, err := sp.SessionSegments(raw); err == nil && segs != nil {
				resp["segs"] = qualifiedSegs(name, segs)
			}
		}
		writeJSON(w, http.StatusOK, resp)
	case "trigger":
		res, err := p.CallTrigger(raw)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if res == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "message not found"})
			return
		}
		writeJSON(w, http.StatusOK, res)
	case "context":
		cp, ok := p.(provider.ContextProvider)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_supported"})
			return
		}
		res, err := cp.CallContext(raw)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if res == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_available"})
			return
		}
		out := *res
		out.Mid = qualify(name, res.Mid)
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *server) handlePost(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/export":
		s.postExport(w, r)
	case "/api/db":
		s.postDB(w, r)
	default:
		http.Error(w, "404 Not Found", http.StatusNotFound)
	}
}

func statusOf(ok bool) int {
	if ok {
		return http.StatusOK
	}
	return http.StatusBadRequest
}
