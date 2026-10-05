package copilot

import (
	"bufio"
	"os"
	"strings"
)

// workspace holds the workspace.yaml fields the dashboard uses.
type workspace struct {
	id, cwd, name        string
	createdMS, updatedMS int64
}

// readWorkspace parses the flat `key: value` workspace.yaml. Indented lines,
// block scalars, and unknown keys are ignored. ok is false when the file is
// missing or yields none of the known keys.
func readWorkspace(path string) (ws workspace, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return ws, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		val = unquoteYAML(strings.TrimSpace(val))
		if strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">") {
			continue
		}
		switch strings.TrimSpace(key) {
		case "id":
			ws.id, ok = val, true
		case "cwd":
			ws.cwd, ok = val, true
		case "name":
			ws.name, ok = val, true
		case "created_at":
			if ms := parseMS(val); ms > 0 {
				ws.createdMS, ok = ms, true
			}
		case "updated_at":
			if ms := parseMS(val); ms > 0 {
				ws.updatedMS, ok = ms, true
			}
		}
	}
	return ws, ok
}

func unquoteYAML(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
