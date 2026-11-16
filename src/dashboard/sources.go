package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// getDB lists every served provider for the settings "data sources" rows:
// its current and default data path, whether the data is there, and whether
// it is switched on.
func (s *server) getDB(w http.ResponseWriter) {
	sources := []map[string]any{}
	for _, p := range s.providers {
		found := true
		var errV any
		if err := p.Available(); err != nil {
			found = false
			errV = err.Error()
		}
		cands := []string{}
		if p.PathSwitchable() && p.Candidates() != nil {
			cands = p.Candidates()
		}
		def := ""
		if dp, ok := p.(provider.DefaultPather); ok {
			def = dp.DefaultPath()
		}
		sources = append(sources, map[string]any{
			"provider":   p.Name(),
			"path":       p.DataPath(),
			"default":    def,
			"found":      found,
			"error":      errV,
			"candidates": cands,
			"switchable": p.PathSwitchable(),
			"enabled":    s.enabled(p.Name()),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

// postDB changes one provider's settings: {"provider", "enabled"} switches it
// on or off, {"provider", "path"} picks its data path (validated read-only;
// the previous path stays on error), {"provider", "reset": true} goes back to
// the autodetected path.
func (s *server) postDB(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string  `json:"provider"`
		Path     *string `json:"path"`
		Reset    bool    `json:"reset"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	p := findProvider(s.providers, body.Provider)
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("unknown provider %q (available: %s)", body.Provider, strings.Join(sortedProviderNames(s.providers), ", ")),
		})
		return
	}
	out := map[string]any{"ok": true, "provider": p.Name()}
	if body.Enabled != nil {
		s.setEnabled(p.Name(), *body.Enabled)
		out["enabled"] = *body.Enabled
		if body.Path == nil && !body.Reset {
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	if body.Path == nil && !body.Reset {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "path": nil, "error": "nothing to change: give path, reset or enabled"})
		return
	}
	if !p.PathSwitchable() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "path switching is not supported for the " + p.Name() + " provider",
		})
		return
	}
	target := ""
	if body.Reset {
		if dp, ok := p.(provider.DefaultPather); ok {
			target = dp.DefaultPath()
		}
	} else {
		target = *body.Path
	}
	out["path"] = target
	if err := p.SetDataPath(target); err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	out["path"] = p.DataPath()
	writeJSON(w, http.StatusOK, out)
}
