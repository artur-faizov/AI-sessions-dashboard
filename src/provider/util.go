package provider

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ActiveWindowMS is how recently a session must have been updated to count as "active".
const ActiveWindowMS = 15 * 60 * 1000

// TriggerMax is the per-value character cap on trigger part text.
const TriggerMax = 60000

// Clip truncates a trigger part value at TriggerMax characters; non-string
// values are rendered as indented JSON (unicode kept raw).
func Clip(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		if err := enc.Encode(v); err != nil {
			return "", false
		}
		s = strings.TrimRight(buf.String(), "\n")
	}
	r := []rune(s)
	if len(r) > TriggerMax {
		return string(r[:TriggerMax]), true
	}
	return s, false
}

// ExpandUser expands a leading "~" to the user's home directory.
func ExpandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// ByModelAdd accumulates the sums s into m under model, copying s.
func ByModelAdd(m map[string]*SubtreeSums, model string, s *SubtreeSums) {
	if model == "" || s == nil {
		return
	}
	if cur, ok := m[model]; ok {
		cur.In += s.In
		cur.Out += s.Out
		cur.Reason += s.Reason
		cur.Cr += s.Cr
		cur.Cw += s.Cw
		return
	}
	c := *s
	m[model] = &c
}

// ByModelMerge merges every entry of src into dst.
func ByModelMerge(dst, src map[string]*SubtreeSums) {
	for k, v := range src {
		ByModelAdd(dst, k, v)
	}
}
