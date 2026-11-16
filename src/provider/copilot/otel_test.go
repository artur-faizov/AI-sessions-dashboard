package copilot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// Sample lines trimmed from a real Copilot 1.0.63 export (ids replaced).
const (
	lineChatCLI = `{"type":"span","traceId":"t1","spanId":"s-cli","parentSpanId":"p1","name":"chat claude-opus-5.5","kind":2,"attributes":{"gen_ai.operation.name":"chat","gen_ai.provider.name":"github","gen_ai.request.model":"claude-opus-5.5","gen_ai.conversation.id":"conv-1","gen_ai.request.stream":true,"gen_ai.response.finish_reasons":["tool_calls"],"gen_ai.usage.input_tokens":24000,"gen_ai.usage.output_tokens":86,"gen_ai.usage.cache_read_input_tokens":23545,"gen_ai.usage.cache_creation_input_tokens":448,"gen_ai.response.model":"claude-opus-5.5","gen_ai.response.id":"msg_1","github.copilot.cost":1,"github.copilot.nano_aiu":12.5},"status":{"code":0},"events":[],"resource":{"attributes":{"service.name":"github-copilot","service.version":"1.0.63"}}}`
	lineChatIDE = `{"type":"span","traceId":"t2","spanId":"s-ide","name":"chat","attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"conv-2","gen_ai.usage.input_tokens":"137294","gen_ai.usage.output_tokens":"203","gen_ai.usage.cache_read_input_tokens":"136620","gen_ai.usage.cache_creation_input_tokens":"672","gen_ai.usage.reasoning_output_tokens":"35","gen_ai.response.id":"msg_2"}}`
	lineNoCache = `{"type":"span","spanId":"s-nc","name":"chat gpt-5","attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"conv-3","gen_ai.usage.input_tokens":23523,"gen_ai.response.id":"msg_3"}}`
	lineMetric  = `{"type":"metric","name":"gen_ai.client.token.usage","unit":"{token}","dataPoints":[{"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.id":"msg_1","gen_ai.conversation.id":"conv-1"},"value":{"sum":24000}}]}`
	lineInvoke  = `{"type":"span","spanId":"s-inv","name":"invoke_agent explore","attributes":{"gen_ai.operation.name":"invoke_agent","gen_ai.conversation.id":"conv-1","gen_ai.response.id":"msg_1","gen_ai.usage.input_tokens":5}}`
	linePerm    = `{"type":"span","spanId":"s-perm","name":"permission","attributes":{"github.copilot.permission.kind":"shell"}}`
	lineNoResp  = `{"type":"span","spanId":"s-nr","name":"chat","attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"conv-1","gen_ai.response.id":""}}`
	lineNoConv  = `{"type":"span","spanId":"s-nconv","name":"chat","attributes":{"gen_ai.operation.name":"chat","gen_ai.response.id":"msg_9"}}`
	lineGarbage = `{"type":"span","spanId":"s-bad","name":"chat","attributes":{"gen_ai.response.id":`
)

func TestParseOTelLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
		want otelSpan
	}{
		{"cli numbers", lineChatCLI, true, otelSpan{"s-cli", otelKey{"conv-1", "msg_1"}, otelTokens{24000, 23545, 448, 86, 0}}},
		{"ide strings", lineChatIDE, true, otelSpan{"s-ide", otelKey{"conv-2", "msg_2"}, otelTokens{137294, 136620, 672, 203, 35}}},
		{"missing cache", lineNoCache, true, otelSpan{"s-nc", otelKey{"conv-3", "msg_3"}, otelTokens{input: 23523}}},
		{"metric", lineMetric, false, otelSpan{}},
		{"other span", lineInvoke, false, otelSpan{}},
		{"no response id attr", linePerm, false, otelSpan{}},
		{"empty response id", lineNoResp, false, otelSpan{}},
		{"no conversation id", lineNoConv, false, otelSpan{}},
		{"garbage", lineGarbage, false, otelSpan{}},
		{"not json", `not json gen_ai.response.id`, false, otelSpan{}},
		{"empty", ``, false, otelSpan{}},
	}
	for _, c := range cases {
		got, ok := parseOTelLine([]byte(c.line))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v ok=%v, want %+v ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestOTelTokensFresh(t *testing.T) {
	if f := (otelTokens{input: 24000, cr: 23545, cw: 448}).fresh(); f != 7 {
		t.Fatalf("fresh = %d", f)
	}
	if f := (otelTokens{input: 10, cr: 20}).fresh(); f != 0 {
		t.Fatalf("fresh must floor at 0: %d", f)
	}
}

func TestAttrInt(t *testing.T) {
	for in, want := range map[string]int64{`12`: 12, `"34"`: 34, `5.0`: 5, `"x"`: 0, `null`: 0, `true`: 0, ``: 0, `" 7 "`: 7} {
		if got := attrInt([]byte(in)); got != want {
			t.Errorf("attrInt(%s) = %d, want %d", in, got, want)
		}
	}
}

func TestOTelPathsEnvAndDirDeduplicated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "otel")
	os.MkdirAll(dir, 0o755)
	shared := filepath.Join(dir, "copilot-otel.jsonl")
	archived := filepath.Join(dir, "old.jsonl")
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	os.WriteFile(shared, []byte(lineChatCLI+"\n"), 0o644)
	os.WriteFile(archived, []byte(lineChatIDE+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(lineNoCache+"\n"), 0o644)
	os.WriteFile(outside, []byte(lineNoCache+"\n"), 0o644)

	t.Setenv("COPILOT_HOME", root)
	t.Setenv("COPILOT_OTEL_FILE_EXPORTER_PATH", filepath.Join(dir, ".", "copilot-otel.jsonl"))
	p := New()
	if got, want := p.otelPaths(), []string{shared, archived}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	idx := p.loadOTel()
	if len(idx) != 2 || idx[otelKey{"conv-1", "msg_1"}].input != 24000 || idx[otelKey{"conv-2", "msg_2"}].input != 137294 {
		t.Fatalf("idx = %+v", idx)
	}
	if n := len(p.otel.files); n != 2 {
		t.Fatalf("files read = %d", n)
	}

	t.Setenv("COPILOT_OTEL_FILE_EXPORTER_PATH", outside)
	p = New()
	if got, want := p.otelPaths(), []string{outside, shared, archived}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if idx := p.loadOTel(); len(idx) != 3 {
		t.Fatalf("idx = %+v", idx)
	}
}

func TestOTelPathsMissing(t *testing.T) {
	p := &Provider{root: provider.NewRoot(t.TempDir()), otelEnv: filepath.Join(t.TempDir(), "none.jsonl")}
	if idx := p.loadOTel(); len(idx) != 0 {
		t.Fatalf("idx = %+v", idx)
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestOTelReaderIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	var r otelReader
	k1, k2 := otelKey{"conv-1", "msg_1"}, otelKey{"conv-2", "msg_2"}

	appendFile(t, path, lineMetric+"\n"+lineChatCLI+"\n")
	if idx := r.load([]string{path}); len(idx) != 1 || idx[k1].out != 86 {
		t.Fatalf("first read = %+v", idx)
	}
	off := r.files[path].offset

	// Append between reads: only the new bytes are read.
	appendFile(t, path, lineGarbage+"\n"+lineChatIDE[:40])
	idx := r.load([]string{path})
	if len(idx) != 1 {
		t.Fatalf("half line must wait: %+v", idx)
	}
	if got := r.files[path].offset; got != off+int64(len(lineGarbage)+1) {
		t.Fatalf("offset = %d, want up to last newline", got)
	}
	appendFile(t, path, lineChatIDE[40:]+"\n")
	if idx := r.load([]string{path}); len(idx) != 2 || idx[k2].reason != 35 {
		t.Fatalf("completed line = %+v", idx)
	}

	// Truncate (copy-truncate rotation): re-read from 0.
	os.WriteFile(path, []byte(lineNoCache+"\n"), 0o644)
	if idx := r.load([]string{path}); len(idx) != 1 || idx[otelKey{"conv-3", "msg_3"}].input != 23523 {
		t.Fatalf("after truncate = %+v", idx)
	}

	// Replaced by a new inode of larger size: re-read from 0.
	tmp := path + ".new"
	os.WriteFile(tmp, []byte(lineChatCLI+"\n"+lineChatIDE+"\n"+lineMetric+"\n"), 0o644)
	os.Rename(tmp, path)
	if idx := r.load([]string{path}); len(idx) != 2 || idx[k1].out != 86 || idx[k2].out != 203 {
		t.Fatalf("after replace = %+v", idx)
	}

	// File removed: its spans go away.
	os.Remove(path)
	if idx := r.load([]string{path}); len(idx) != 0 {
		t.Fatalf("after remove = %+v", idx)
	}
}

func TestOTelReaderDedupAndSum(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")
	// The same span twice in a and once in b; a distinct span with the same key is summed.
	other := `{"type":"span","spanId":"s-cli-2","name":"chat","attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"conv-1","gen_ai.response.id":"msg_1","gen_ai.usage.input_tokens":100,"gen_ai.usage.output_tokens":4}}`
	appendFile(t, a, lineChatCLI+"\n"+lineChatCLI+"\n")
	appendFile(t, b, lineChatCLI+"\n"+other+"\n")
	var r otelReader
	idx := r.load([]string{a, b})
	if got := idx[otelKey{"conv-1", "msg_1"}]; got != (otelTokens{24100, 23545, 448, 90, 0}) {
		t.Fatalf("summed = %+v", got)
	}
}
