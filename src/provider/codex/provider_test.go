package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func row(kind string, p any, t int64) string {
	b, _ := json.Marshal(map[string]any{"timestamp": t, "type": kind, "payload": p})
	return string(b) + "\n"
}
func meta(id, parent string) string {
	return row("session_meta", map[string]any{"id": id, "parent_thread_id": parent, "cwd": "/project/" + id}, 1000)
}
func user(text string) string {
	return row("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}, 1100)
}
func assistant(text string) string {
	return row("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}, 1200)
}
func context(model string) string { return row("turn_context", map[string]any{"model": model}, 1150) }
func rich(sid, id string, u usage, total *usage, t int64) string {
	return row("token_usage_record", map[string]any{"thread_id": sid, "response_id": id, "usage": u, "thread_token_usage": total}, t)
}
func legacy(u usage, last *usage, t int64) string {
	return row("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": u, "last_token_usage": last, "model_context_window": 999999}}, t)
}
func makeProvider(t *testing.T, files map[string]string) *Provider {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	p := New()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, contents := range files {
		writeFile(t, filepath.Join(home, path), contents)
	}
	return p
}
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func mustCalls(t *testing.T, p *Provider, id string) []provider.CallRec {
	t.Helper()
	calls, err := p.SessionCalls(id)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func TestNormalizeInclusiveBuckets(t *testing.T) {
	for _, tc := range []struct {
		u    usage
		want provider.CallRec
	}{
		{usage{Input: 21365, Cached: 18560, Output: 86, Reason: 10}, provider.CallRec{In: 2805, Cr: 18560, Out: 76, Reason: 10, Ctx: 21365}},
		{usage{Input: 100, Cached: 30, Write: 20, Output: 10, Reason: 4}, provider.CallRec{In: 50, Cr: 30, Cw: 20, Out: 6, Reason: 4, Ctx: 100}},
		{usage{Input: 5, Cached: 7, Write: 9, Output: 2, Reason: 4}, provider.CallRec{Cr: 5, Reason: 2, Ctx: 5}},
		{usage{Input: -5, Cached: -1, Write: -2, Output: -6, Reason: -2}, provider.CallRec{}},
	} {
		got := tc.u.normalized()
		if got != tc.want {
			t.Fatalf("normalized %+v = %+v, want %+v", tc.u, got, tc.want)
		}
		if got.In+got.Cr+got.Cw+got.Out+got.Reason != max(0, tc.u.Input)+max(0, tc.u.Output) {
			t.Fatal("inclusive accounting changed total")
		}
	}
}

func TestRichCompatibilityKeepsNextToolTrigger(t *testing.T) {
	u := usage{Input: 100, Cached: 60, Output: 20, Reason: 5, Total: 120}
	total2 := usage{Input: 200, Cached: 120, Output: 40, Reason: 10, Total: 240}
	data := meta("s", "") + context("model-a") + user("Build a sample") + assistant("First chunk") + assistant("Second chunk")
	data += row("event_msg", map[string]any{"type": "agent_message", "message": "First chunk\nSecond chunk"}, 1220)
	data += row("response_item", map[string]any{"type": "custom_tool_call", "call_id": "tool-1", "name": "exec_command", "namespace": "functions", "input": `{"command":"echo sample"}`}, 1250)
	data += rich("s", "r1", u, &u, 1300)
	data += row("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "tool-1", "output": "sample"}, 1400)
	data += legacy(u, &u, 1500) + row("event_msg", map[string]any{"type": "token_count", "info": nil}, 1510)
	data += context("model-b") + assistant("Finished") + rich("s", "r2", u, &total2, 1600) + legacy(total2, &u, 1700)
	data += rich("s", "r2", u, &total2, 1800)
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": data})
	calls := mustCalls(t, p, "s")
	if len(calls) != 2 || calls[0].Hu != provider.TrigText || calls[1].Hu != provider.TrigNone {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].Model != "model-a" || calls[1].Model != "model-b" || calls[0].Mid == calls[1].Mid {
		t.Fatalf("model/id mismatch: %+v", calls)
	}
	tr, _ := p.CallTrigger(calls[1].Mid)
	if tr.Case != "assistant" || len(tr.Parts) != 1 {
		t.Fatalf("lost next tool trigger: %+v", tr)
	}
	part := tr.Parts[0].(provider.TriggerPartTool)
	if part.Output != "sample" || part.Tool != "functions.exec_command" {
		t.Fatalf("tool part: %+v", part)
	}
	first, _ := p.CallTrigger(calls[0].Mid)
	if len(first.Reply) != 2 {
		t.Fatalf("response chunks split or lost: %+v", first)
	}
	segs, _ := p.SessionSegments("s")
	if len(segs) != 1 || segs[0].Mid != calls[0].Mid || segs[0].Start != 1250 || segs[0].End != 1400 || segs[0].Run {
		t.Fatalf("segments: %+v", segs)
	}
	tree, _ := p.BuildTree(2000)
	n := tree[0]
	if n.In != 80 || n.Cr != 120 || n.Out != 30 || n.Reason != 10 || n.MaxCtx != 100 {
		t.Fatalf("tree tokens: %+v", n)
	}
	if len(n.ByModel) != 2 || n.Subtree.In != n.In {
		t.Fatalf("tree sums: %+v", n)
	}
	for _, beat := range n.Beats {
		if beat[0] != beat[1] {
			t.Fatal("invented request duration")
		}
	}
}

func TestLegacyCheckpointsAndMixedHistory(t *testing.T) {
	u := usage{Input: 10, Output: 2, Total: 12}
	twice := usage{Input: 20, Output: 4, Total: 24}
	thrice := usage{Input: 30, Output: 6, Total: 36}
	tests := []struct {
		name, data string
		want       int
		inputs     []int64
	}{
		{"repeated", legacy(u, &u, 1300) + legacy(u, &u, 1400) + legacy(twice, &u, 1500), 2, []int64{10, 10}},
		{"delta", legacy(u, nil, 1300) + legacy(thrice, nil, 1500), 2, []int64{10, 20}},
		{"reset-reuses-old-snapshot", legacy(u, &u, 1300) + legacy(twice, &u, 1400) + legacy(u, &u, 1500) + legacy(twice, &u, 1600), 4, []int64{10, 10, 10, 10}},
		{"reset-without-last", legacy(twice, &u, 1300) + legacy(u, nil, 1400) + legacy(twice, &u, 1500), 2, []int64{10, 10}},
		{"mixed", legacy(u, &u, 1300) + rich("s", "r2", u, &twice, 1400) + legacy(twice, &u, 1500) + legacy(thrice, &u, 1600), 3, []int64{10, 10, 10}},
		{"delayed-compatibility-delta", rich("s", "r1", u, &u, 1300) + rich("s", "r2", u, &twice, 1400) + legacy(u, &u, 1450) + legacy(thrice, nil, 1500), 3, []int64{10, 10, 10}},
		{"reverse-compat", legacy(u, &u, 1300) + rich("s", "r1", u, &u, 1400), 1, []int64{10}},
		{"distinct-same-usage", rich("s", "r1", u, &u, 1300) + rich("s", "r2", u, &twice, 1400), 2, []int64{10, 10}},
		{"inherited-other-thread", rich("parent", "r1", u, &u, 1300) + legacy(u, &u, 1350) + rich("s", "r2", u, &u, 1400) + legacy(u, &u, 1500), 1, []int64{10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := makeProvider(t, map[string]string{"sessions/s.jsonl": meta("s", "") + tc.data})
			calls := mustCalls(t, p, "s")
			if len(calls) != tc.want {
				t.Fatalf("got %d calls, want %d: %+v", len(calls), tc.want, calls)
			}
			for i, c := range calls {
				if c.In != tc.inputs[i] || c.Time == 0 || c.End != 0 || c.Dur != 0 {
					t.Fatalf("call %d: %+v", i, c)
				}
			}
			if tc.name == "reverse-compat" && calls[0].Mid != "codex:s:r1" {
				t.Fatal("did not prefer rich response ID")
			}
		})
	}
}

func TestMessagesImagesQuestionsAndUnknownIDs(t *testing.T) {
	u := usage{Input: 1, Output: 1, Total: 2}
	two := usage{Input: 2, Output: 2, Total: 4}
	data := meta("s", "") + user("<environment_context>ignored</environment_context>") + user("# AGENTS.md instructions\nignored")
	data += row("event_msg", map[string]any{"type": "user_message", "message": "Use this picture"}, 1099)
	data += row("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Use this picture"}, map[string]any{"type": "input_image", "image_url": "hidden"}}}, 1100)
	data += row("event_msg", map[string]any{"type": "agent_message", "message": "Pick an option"}, 1199) + assistant("Pick an option")
	data += row("response_item", map[string]any{"type": "function_call", "call_id": "q", "name": "request_user_input_async", "arguments": `{"question":"Option?"}`}, 1250) + rich("s", "r1", u, &u, 1300)
	data += row("response_item", map[string]any{"type": "function_call_output", "call_id": "q", "output": "A"}, 1400) + assistant("Done") + rich("s", "r2", u, &two, 1500)
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": data})
	calls := mustCalls(t, p, "s")
	if calls[0].Hu != provider.TrigImage || !calls[0].Ask || calls[1].Hu != provider.TrigQuestion {
		t.Fatalf("human types: %+v", calls)
	}
	prompts, _ := p.SessionPrompt("s")
	if !reflect.DeepEqual(prompts, []string{"Use this picture"}) {
		t.Fatalf("prompts: %#v", prompts)
	}
	tr, _ := p.CallTrigger(calls[0].Mid)
	if len(tr.Reply) != 2 {
		t.Fatalf("duplicate replies: %+v", tr)
	}
	if tr, _ := p.CallTrigger("absent"); tr != nil {
		t.Fatal("unknown trigger should be nil")
	}
	if _, err := p.SessionCalls("absent"); err == nil {
		t.Fatal("missing session should fail")
	}
	if _, err := p.SessionPrompt("absent"); err == nil {
		t.Fatal("missing prompt session should fail")
	}
	if _, err := p.SessionSegments("absent"); err == nil {
		t.Fatal("missing segment session should fail")
	}
	if _, err := p.SessionTriggers("absent", calls); err == nil {
		t.Fatal("missing trigger session should fail")
	}
	trs, err := p.SessionTriggers("s", append(calls, provider.CallRec{Mid: "absent"}))
	if err != nil || len(trs) != 2 {
		t.Fatalf("batch triggers: %v %v", trs, err)
	}
}

func TestUserMessageFiltersIndividualContextBlocks(t *testing.T) {
	u := usage{Input: 10, Output: 2, Total: 12}
	const prompt = "Summarize this project\nFocus on the backend."
	for _, image := range []bool{false, true} {
		t.Run(fmt.Sprintf("image=%v", image), func(t *testing.T) {
			content := []any{
				map[string]any{"type": "input_text", "text": "<environment_context>\n<cwd>/sample</cwd>\n</environment_context>"},
				map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /sample\n<INSTRUCTIONS>Internal guidance</INSTRUCTIONS>"},
				map[string]any{"type": "input_text", "text": "Summarize this project"},
				map[string]any{"type": "input_text", "text": "Focus on the backend."},
			}
			if image {
				content = append(content, map[string]any{"type": "input_image", "image_url": "synthetic-image"})
			}
			data := meta("s", "") + row("response_item", map[string]any{"type": "message", "role": "user", "content": content}, 1100)
			// The compatibility event must not expose the same prompt twice.
			data += row("event_msg", map[string]any{"type": "user_message", "message": prompt}, 1110)
			data += assistant("Project summary") + rich("s", "r1", u, &u, 1300)
			instructionOnly := meta("instructions", "") + user("<environment_context>Only context</environment_context>") + user("# AGENTS.md instructions\nOnly guidance") + rich("instructions", "r1", u, &u, 1300)
			p := makeProvider(t, map[string]string{"sessions/s.jsonl": data, "sessions/instructions.jsonl": instructionOnly})
			prompts, err := p.SessionPrompt("s")
			if err != nil || !reflect.DeepEqual(prompts, []string{prompt}) {
				t.Fatalf("prompt blocks: %#v, %v", prompts, err)
			}
			calls := mustCalls(t, p, "s")
			wantHu := provider.TrigText
			if image {
				wantHu = provider.TrigImage
			}
			if len(calls) != 1 || calls[0].Hu != wantHu {
				t.Fatalf("first human trigger: %+v", calls)
			}
			tr, err := p.CallTrigger(calls[0].Mid)
			wantParts := 1
			if image {
				wantParts++
			}
			if err != nil || tr == nil || tr.Case != "user" || len(tr.Parts) != wantParts || tr.Parts[0].(provider.TriggerPartText).Text != prompt {
				t.Fatalf("trigger content: %+v, %v", tr, err)
			}
			tree, err := p.BuildTree(1400)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range tree {
				if n.ID == "s" && n.Title != "Summarize this project" {
					t.Fatalf("title: %q", n.Title)
				}
				if n.ID == "instructions" && n.Title != "(untitled)" {
					t.Fatalf("instruction title: %q", n.Title)
				}
			}
			if prompts, err := p.SessionPrompt("instructions"); err != nil || len(prompts) != 0 {
				t.Fatalf("instruction-only prompts: %#v, %v", prompts, err)
			}
			if calls := mustCalls(t, p, "instructions"); len(calls) != 1 || calls[0].Hu != provider.TrigNone {
				t.Fatalf("instruction-only human trigger: %+v", calls)
			}
		})
	}
}

func TestTreeNestingCyclesOrphansAndArchiveCopies(t *testing.T) {
	u := usage{Input: 100, Cached: 40, Write: 10, Output: 20, Reason: 5, Total: 120}
	files := map[string]string{}
	for _, spec := range []struct{ id, parent string }{{"root", ""}, {"child", "root"}, {"grandchild", "child"}, {"orphan", "missing"}, {"self", "self"}, {"cycle-a", "cycle-b"}, {"cycle-b", "cycle-a"}} {
		files["sessions/"+spec.id+".jsonl"] = meta(spec.id, spec.parent) + context("model") + rich(spec.id, "r", u, &u, 1300)
	}
	files["archived_sessions/root.jsonl"] = files["sessions/root.jsonl"]
	files["sessions/child.jsonl"] = row("session_meta", map[string]any{"id": "child", "session_id": "broader-not-thread", "cwd": "/different", "source": map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "root", "agent_role": "reviewer"}}}}, 1000) + context("model") + rich("child", "r", u, &u, 1300)
	p := makeProvider(t, files)
	tree, err := p.BuildTree(1400)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var root *provider.SessionNode
	var visit func(*provider.SessionNode)
	visit = func(n *provider.SessionNode) {
		if seen[n.ID] {
			t.Fatal("duplicate/cyclic node")
		}
		seen[n.ID] = true
		if n.ID == "root" {
			root = n
		}
		for _, ch := range n.Children {
			visit(ch)
		}
	}
	for _, n := range tree {
		visit(n)
	}
	if len(seen) != 7 {
		t.Fatalf("lost nodes: %v", seen)
	}
	if root == nil || len(root.Children) != 1 || len(root.Children[0].Children) != 1 {
		t.Fatalf("missing nesting: %+v", root)
	}
	if root.Children[0].Agent != "reviewer" || root.Children[0].Dir != root.Dir || root.Children[0].Children[0].Dir != root.Dir {
		t.Fatal("child metadata/grouping")
	}
	if root.In != 50 || root.Subtree.In != 150 || root.Subtree.Cr != 120 || root.ByModel["model"].In != 150 {
		t.Fatalf("subtree accounting: %+v", root)
	}
	if calls := mustCalls(t, p, "root"); len(calls) != 1 {
		t.Fatal("archived copy duplicated calls")
	}
	segs, _ := p.SessionSegments("root")
	if len(segs) != 1 || segs[0].Sub != "child" || segs[0].Mid != "" {
		t.Fatalf("child spans: %+v", segs)
	}
}

func TestAgentTitleFallbackWithoutVisibleUserPrompt(t *testing.T) {
	files := map[string]string{"sessions/root.jsonl": meta("root", "")}
	wantTitles := map[string]string{"root": "(untitled)", "role": "reviewer", "nickname": "Ada", "path": "backend", "generic": "Subagent", "named-root": "Helper", "prompt": "A visible task"}
	for _, tc := range []struct {
		id, parent, role, nick, path, prompt string
	}{
		{id: "role", parent: "root", role: "reviewer", nick: "Ada"},
		{id: "nickname", parent: "root", nick: "Ada"},
		{id: "path", parent: "root", path: "/root/backend"},
		{id: "generic", parent: "root"},
		{id: "named-root", nick: "Helper"},
		{id: "prompt", parent: "root", role: "reviewer", prompt: "A visible task"},
	} {
		data := row("session_meta", map[string]any{"id": tc.id, "parent_thread_id": tc.parent, "agent_role": tc.role, "agent_nickname": tc.nick, "agent_path": tc.path}, 1000)
		data += user("<environment_context>Only context</environment_context>")
		// Developer task content is not a visible user prompt or a title source.
		data += row("response_item", map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "Private task instructions"}}}, 1100)
		if tc.prompt != "" {
			data += user(tc.prompt)
		}
		files["sessions/"+tc.id+".jsonl"] = data
	}
	p := makeProvider(t, files)
	tree, err := p.BuildTree(1400)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	var check func(*provider.SessionNode)
	check = func(n *provider.SessionNode) {
		seen++
		if n.Title != wantTitles[n.ID] {
			t.Errorf("%s title = %q, want %q", n.ID, n.Title, wantTitles[n.ID])
		}
		prompts, err := p.SessionPrompt(n.ID)
		if err != nil || n.ID != "prompt" && len(prompts) != 0 {
			t.Errorf("%s prompt content: %#v, %v", n.ID, prompts, err)
		}
		for _, child := range n.Children {
			check(child)
		}
	}
	for _, n := range tree {
		check(n)
	}
	if seen != len(wantTitles) {
		t.Fatalf("checked %d nodes, want %d", seen, len(wantTitles))
	}
}

func TestIncrementalCompleteRowsLargeMalformedAndReplacement(t *testing.T) {
	u := usage{Input: 10, Output: 2, Total: 12}
	two := usage{Input: 20, Output: 4, Total: 24}
	complete := meta("s", "") + "{malformed complete row}\n" + user(strings.Repeat("x", 128*1024)) + rich("s", "r1", u, &u, 1300)
	partial := strings.TrimSuffix(rich("s", "r2", u, &two, 1400), "\n")
	p := makeProvider(t, map[string]string{"sessions/s.jsonl": complete + partial})
	path := filepath.Join(p.root, "sessions/s.jsonl")
	if calls := mustCalls(t, p, "s"); len(calls) != 1 {
		t.Fatalf("partial row counted: %v", calls)
	}
	if got := p.files[path].offset; got != int64(len(complete)) {
		t.Fatalf("offset %d want %d", got, len(complete))
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if calls := mustCalls(t, p, "s"); len(calls) != 2 {
		t.Fatal("completed append missing")
	}
	writeFile(t, path, meta("s", "")+rich("s", "new", u, &u, 1500))
	if calls := mustCalls(t, p, "s"); len(calls) != 1 || calls[0].Mid != "codex:s:new" {
		t.Fatalf("truncation did not reset: %+v", calls)
	}
	replacement := path + ".replacement"
	writeFile(t, replacement, meta("s", "")+rich("s", "replacement", u, &u, 1600))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if calls := mustCalls(t, p, "s"); len(calls) != 1 || calls[0].Mid != "codex:s:replacement" {
		t.Fatalf("replacement did not reset: %+v", calls)
	}
	// Same-size rewrites must not leave old normalized data cached.
	data := meta("s", "") + rich("s", "same-length", u, &u, 1700)
	writeFile(t, path, data)
	_ = mustCalls(t, p, "s")
	writeFile(t, path, strings.Replace(data, "same-length", "diff-length", 1))
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if calls := mustCalls(t, p, "s"); calls[0].Mid != "codex:s:diff-length" {
		t.Fatalf("same-size rewrite stale: %+v", calls)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	tree, _ := p.BuildTree(2000)
	if len(tree) != 0 || len(p.files) != 0 {
		t.Fatal("removed file not pruned")
	}
}

func TestAvailabilityLivenessAndConcurrentReads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	p := New()
	if p.Available() == nil {
		t.Fatal("missing directories available")
	}
	if !strings.Contains(p.Available().Error(), home) {
		t.Fatal("missing path in error")
	}
	if p.Name() != "codex" || !p.FatalWhenMissing() || p.PathSwitchable() || p.Candidates() != nil || p.SetDataPath(home) == nil {
		t.Fatal("provider contract")
	}
	if err := os.Mkdir(filepath.Join(home, "archived_sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := p.Available(); err != nil {
		t.Fatal(err)
	}
	tree, _ := p.BuildTree(2000)
	if tree == nil || len(tree) != 0 {
		t.Fatal("empty tree must be []")
	}
	start := row("event_msg", map[string]any{"type": "task_started", "started_at": 1200}, 1200)
	writeFile(t, filepath.Join(home, "archived_sessions/s.jsonl"), meta("s", "")+start)
	for _, tc := range []struct {
		now    int64
		active bool
	}{{1300, true}, {1200 + provider.ActiveWindowMS + 1, false}, {1000, false}} {
		tree, _ := p.BuildTree(tc.now)
		if tree[0].Active != tc.active {
			t.Fatalf("now %d active %v", tc.now, tree[0].Active)
		}
	}
	writeFile(t, filepath.Join(home, "archived_sessions/s.jsonl"), meta("s", "")+start+row("event_msg", map[string]any{"type": "task_complete", "completed_at": 1500}, 1500))
	tree, _ = p.BuildTree(1600)
	if tree[0].Active || tree[0].State != "" {
		t.Fatal("completed turn active")
	}
	calls := mustCalls(t, p, "s")
	if calls == nil {
		t.Fatal("empty calls must be []")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if _, err := p.BuildTree(1600); err != nil {
					t.Error(err)
				}
				if _, err := p.SessionCalls("s"); err != nil {
					t.Error(err)
				}
				if _, err := p.SessionPrompt("s"); err != nil {
					t.Error(err)
				}
				if _, err := p.CallTrigger(fmt.Sprintf("missing-%d", j)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
