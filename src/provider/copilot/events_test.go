package copilot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func parseFixture(t *testing.T, sid string) *session {
	t.Helper()
	s := parseSession(sid, filepath.Join("testdata", "home", "session-state", sid))
	s.resolveParents()
	return s
}

func TestParseSkipsMalformedAndUnknownLines(t *testing.T) {
	s := parseFixture(t, "plain-0001")
	root := s.streams[""]
	var kinds []itemKind
	for _, it := range root.items {
		kinds = append(kinds, it.kind)
	}
	want := []itemKind{kindUser, kindAssistant, kindTool, kindAssistant, kindUser, kindAssistant, kindUser, kindAssistant}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if root.assistantCount != 4 || s.startCwd != "/work/proj" || s.startModel != "model-a" {
		t.Fatalf("session = %+v", s)
	}
	tool := root.items[2]
	if tool.toolName != "view" || tool.status != "completed" || tool.toolOut != "package main" {
		t.Fatalf("tool = %+v", tool)
	}
	if root.items[4].human {
		t.Fatal("skill-injected user message must not count as human")
	}
}

func TestParseSubagentsAttributionAndNesting(t *testing.T) {
	s := parseFixture(t, "sub-0002")
	if got := []string{"call-A", "call-B", "call-C"}; len(s.order) != 3 || s.order[0] != got[0] || s.order[1] != got[1] || s.order[2] != got[2] {
		t.Fatalf("order = %v", s.order)
	}
	a, b, c := s.subagents["call-A"], s.subagents["call-B"], s.subagents["call-C"]
	if a.parent != "" || b.parent != "call-A" || c.parent != "" {
		t.Fatalf("parents = %q %q %q", a.parent, b.parent, c.parent)
	}
	if a.name != "explore" || a.model != "model-b" || !a.completed || a.durationMS != 11000 {
		t.Fatalf("a = %+v", a)
	}
	if c.completed || c.endMS != 0 {
		t.Fatalf("c should be unfinished: %+v", c)
	}
	if n := s.streams[""].assistantCount; n != 2 {
		t.Fatalf("root assistant count = %d", n)
	}
	if n := s.streams["call-B"].assistantCount; n != 2 {
		t.Fatalf("call-B assistant count = %d", n)
	}
	if s.tasks["call-A"].prompt != "Find the parser entry points" || s.tasks["call-B"].owner != "call-A" {
		t.Fatalf("tasks = %+v %+v", s.tasks["call-A"], s.tasks["call-B"])
	}
	if st := s.streams["call-B"].items[1]; st.status != "error" || st.toolOut != "exit status 1" {
		t.Fatalf("failed tool = %+v", st)
	}
}

func TestParseIndexesAPICallIDs(t *testing.T) {
	s := parseFixture(t, "sub-0002")
	want := map[string]string{
		"api-s1": "", "api-s2": "",
		"api-a1": "call-A", "api-a2": "call-A",
		"api-b1": "call-B", "api-b2": "call-B",
		"api-c1": "call-C",
	}
	if len(s.apiIDs) != len(want) {
		t.Fatalf("apiIDs = %v", s.apiIDs)
	}
	for api, agent := range want {
		ref, ok := s.apiIDs[api]
		if !ok || ref.agent != agent {
			t.Fatalf("apiIDs[%s] = %+v, want agent %q", api, ref, agent)
		}
		it := s.streams[agent].items[ref.index]
		if it.kind != kindAssistant || it.apiCallID != api || it.mid != "m-"+api[4:] {
			t.Fatalf("apiIDs[%s] points at %+v", api, it)
		}
	}
}

func TestParseShutdownUsesFreshInput(t *testing.T) {
	s := parseFixture(t, "shutdown-0003")
	if len(s.shutdowns) != 1 {
		t.Fatalf("shutdowns = %d", len(s.shutdowns))
	}
	if u := s.shutdowns[0]; u != (shutdownUsage{in: 50, out: 200, cr: 1000, cw: 300, reason: 10}) {
		t.Fatalf("usage = %+v", u)
	}
}

func TestLoadSessionCache(t *testing.T) {
	p, root := fixtureRoot(t)
	first := p.loadSession("plain-0001")
	if first == nil || p.loadSession("plain-0001") != first {
		t.Fatal("expected cache hit for unchanged file")
	}
	path := filepath.Join(root, "session-state", "plain-0001", "events.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"assistant.message","id":"x","timestamp":"2026-01-01T10:00:30.000Z","data":{"messageId":"m-new","outputTokens":1}}` + "\n")
	f.Close()
	later := time.Now().Add(time.Second)
	os.Chtimes(path, later, later)
	second := p.loadSession("plain-0001")
	if second == first || second.streams[""].assistantCount != 5 {
		t.Fatal("expected re-parse after file change")
	}
}

func TestLookupNodeRejectsUnknownAndTraversal(t *testing.T) {
	p, _ := fixtureRoot(t)
	for _, id := range []string{"nope", "sub-0002/call-Z", "..", "", "../session-state"} {
		if _, _, ok := p.lookupNode(id); ok {
			t.Errorf("lookupNode(%q) ok", id)
		}
	}
	if _, agent, ok := p.lookupNode("sub-0002/call-B"); !ok || agent != "call-B" {
		t.Fatal("subagent lookup failed")
	}
}
