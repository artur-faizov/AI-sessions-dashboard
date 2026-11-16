package copilot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// otelTokens are the usage attributes of one exported model-call span.
// input is gen_ai.usage.input_tokens as reported (cache read and write included).
type otelTokens struct {
	input, cr, cw, out, reason int64
}

func (t otelTokens) add(o otelTokens) otelTokens {
	return otelTokens{t.input + o.input, t.cr + o.cr, t.cw + o.cw, t.out + o.out, t.reason + o.reason}
}

// fresh is the new (uncached) input: input minus cache read minus cache write, floored at 0.
func (t otelTokens) fresh() int64 {
	return max(0, t.input-t.cr-t.cw)
}

// otelKey joins a span to an assistant message: conversation id = session id,
// response id = the message's apiCallId.
type otelKey struct{ conv, resp string }

// otelIndex holds the summed tokens of distinct spans per key.
type otelIndex map[otelKey]otelTokens

type otelSpan struct {
	spanID string
	key    otelKey
	tok    otelTokens
}

type rawOTelLine struct {
	Type       string                     `json:"type"`
	SpanID     string                     `json:"spanId"`
	Name       string                     `json:"name"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// parseOTelLine returns the model-call span on one export line. Anything else
// (invalid JSON, metrics, other spans, spans missing ids) is reported as !ok.
func parseOTelLine(line []byte) (otelSpan, bool) {
	if !bytes.Contains(line, []byte("gen_ai.response.id")) {
		return otelSpan{}, false
	}
	var raw rawOTelLine
	if json.Unmarshal(line, &raw) != nil || raw.Type != "span" {
		return otelSpan{}, false
	}
	if raw.Name != "chat" && !strings.HasPrefix(raw.Name, "chat ") {
		return otelSpan{}, false
	}
	a := raw.Attributes
	if attrString(a["gen_ai.operation.name"]) != "chat" {
		return otelSpan{}, false
	}
	key := otelKey{conv: attrString(a["gen_ai.conversation.id"]), resp: attrString(a["gen_ai.response.id"])}
	if key.conv == "" || key.resp == "" {
		return otelSpan{}, false
	}
	return otelSpan{
		spanID: raw.SpanID,
		key:    key,
		tok: otelTokens{
			input:  attrInt(a["gen_ai.usage.input_tokens"]),
			cr:     attrInt(a["gen_ai.usage.cache_read_input_tokens"]),
			cw:     attrInt(a["gen_ai.usage.cache_creation_input_tokens"]),
			out:    attrInt(a["gen_ai.usage.output_tokens"]),
			reason: attrInt(a["gen_ai.usage.reasoning_output_tokens"]),
		},
	}, true
}

func attrString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

// attrInt reads a JSON number or a numeric string; anything else is 0.
func attrInt(v json.RawMessage) int64 {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return 0
	}
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(v, &s) != nil {
			return 0
		}
		s = strings.TrimSpace(s)
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// --- incremental reading ---

type otelFile struct {
	info   os.FileInfo // identity of the file read so far (inode)
	offset int64       // bytes consumed: always just after a newline
	spans  []otelSpan
}

// otelReader keeps parsed spans per export file and reads only appended data.
type otelReader struct {
	mu    sync.Mutex
	files map[string]*otelFile
	index otelIndex
}

// load refreshes every path and returns the merged index. The returned index
// is shared and must not be modified.
func (r *otelReader) load(paths []string) otelIndex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files == nil {
		r.files = map[string]*otelFile{}
	}
	changed := r.index == nil
	want := map[string]bool{}
	for _, path := range paths {
		want[path] = true
		if r.refresh(path) {
			changed = true
		}
	}
	for path := range r.files {
		if !want[path] {
			delete(r.files, path)
			changed = true
		}
	}
	if changed {
		r.index = r.merge()
	}
	return r.index
}

// refresh brings one file up to date and reports whether its spans changed.
func (r *otelReader) refresh(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		if _, ok := r.files[path]; ok {
			delete(r.files, path)
			return true
		}
		return false
	}
	f := r.files[path]
	changed := false
	if f == nil || !os.SameFile(f.info, info) || info.Size() < f.offset {
		changed = f != nil && len(f.spans) > 0
		f = &otelFile{}
		r.files[path] = f
	}
	f.info = info
	if info.Size() == f.offset {
		return changed
	}
	fh, err := os.Open(path)
	if err != nil {
		return changed
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return changed
	}
	br := bufio.NewReaderSize(fh, 256*1024)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			// A line without its newline is still being written: leave it for the next pass.
			break
		}
		f.offset += int64(len(line))
		if sp, ok := parseOTelLine(line); ok {
			f.spans = append(f.spans, sp)
			changed = true
		}
	}
	return changed
}

// merge sums spans per key, counting each span id once across all files.
func (r *otelReader) merge() otelIndex {
	paths := make([]string, 0, len(r.files))
	for p := range r.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	idx := otelIndex{}
	seen := map[string]bool{}
	for _, p := range paths {
		for _, sp := range r.files[p].spans {
			if sp.spanID != "" {
				if seen[sp.spanID] {
					continue
				}
				seen[sp.spanID] = true
			}
			idx[sp.key] = idx[sp.key].add(sp.tok)
		}
	}
	return idx
}

// --- discovery ---

// otelPaths lists the export files: COPILOT_OTEL_FILE_EXPORTER_PATH (captured
// by New) plus <root>/otel/*.jsonl, de-duplicated by cleaned absolute path.
func (p *Provider) otelPaths() []string {
	var out []string
	seen := map[string]bool{}
	addPath := func(path string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	addPath(p.otelEnv)
	matches, _ := filepath.Glob(filepath.Join(p.root.Get(), "otel", "*.jsonl"))
	sort.Strings(matches)
	for _, m := range matches {
		addPath(m)
	}
	return out
}

func (p *Provider) loadOTel() otelIndex {
	return p.otel.load(p.otelPaths())
}
