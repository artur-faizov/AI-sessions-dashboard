package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/artur-faizov/token-monitor/src/provider"
)

func TestReadUsageGroupsRowsAndPrefersFreshInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-store.db")
	makeStoreDB(t, path, true, fixtureRows...)
	idx, err := readUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	root := idx[usageKey{"rows-0005", ""}]
	sub := idx[usageKey{"rows-0005", "call-R"}]
	if len(root) != 2 || len(sub) != 1 {
		t.Fatalf("groups = %d/%d", len(root), len(sub))
	}
	if root[0].in != 10 || root[1].in != 12 {
		t.Fatalf("input = %d,%d (token_details input must win, else input_tokens)", root[0].in, root[1].in)
	}
	if root[0].timeMS != parseMS("2026-01-01T10:00:02Z") {
		t.Fatalf("time = %d", root[0].timeMS)
	}
}

func TestReadUsageMissingDB(t *testing.T) {
	idx, err := readUsage(filepath.Join(t.TempDir(), "session-store.db"))
	if err != nil || len(idx) != 0 {
		t.Fatalf("idx=%v err=%v", idx, err)
	}
}

func TestReadUsageNoTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-store.db")
	makeStoreDB(t, path, false)
	idx, err := readUsage(path)
	if err != nil || len(idx) != 0 {
		t.Fatalf("idx=%v err=%v", idx, err)
	}
}

func TestReadUsageCorruptDBServesWithoutRows(t *testing.T) {
	p, root := fixtureRoot(t)
	os.WriteFile(filepath.Join(root, "session-store.db"), []byte("not a sqlite file at all, just junk bytes"), 0o644)
	if idx := p.loadUsage(); len(idx) != 0 {
		t.Fatalf("idx = %v", idx)
	}
	roots, err := p.BuildTree(fixtureNow)
	if err != nil || findNode(roots, "plain-0001") == nil {
		t.Fatalf("tree not served: %v", err)
	}
}

// --- token source selection, one test per spec scenario ---

func sums(n *provider.SessionNode) [5]int64 { return [5]int64{n.In, n.Out, n.Cr, n.Cw, n.Reason} }

func TestTokensRowsOnly(t *testing.T) {
	p := fixtureWithDB(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "rows-0005")
	if got := sums(root); got != [5]int64{22, 50, 300, 5, 2} {
		t.Fatalf("root = %v", got)
	}
	if root.MaxCtx != 212 {
		t.Fatalf("maxctx = %d", root.MaxCtx)
	}
	sub := findNode(roots, "rows-0005/call-R")
	if got := sums(sub); got != [5]int64{7, 9, 50, 1, 3} || sub.MaxCtx != 58 {
		t.Fatalf("sub = %v maxctx=%d", got, sub.MaxCtx)
	}
}

func TestTokensNoDoubleCountingWithRowsAndShutdown(t *testing.T) {
	p := fixtureWithDB(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "rows-0005")
	// Neither shutdown metrics (5 each) nor outputTokens (999) may leak in.
	if st := root.Subtree; *st != (provider.SubtreeSums{In: 29, Out: 59, Cr: 350, Cw: 6, Reason: 5}) {
		t.Fatalf("subtree = %+v", *st)
	}
}

func TestTokensShutdownOnly(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "shutdown-0003")
	sub := findNode(roots, "shutdown-0003/call-D")
	if got := sums(sub); got != [5]int64{0, 25, 0, 0, 0} || sub.MaxCtx != 0 {
		t.Fatalf("sub = %v", got)
	}
	if got := sums(root); got != [5]int64{50, 175, 1000, 300, 10} || root.MaxCtx != 0 {
		t.Fatalf("root = %v maxctx=%d", got, root.MaxCtx)
	}
	if st := *root.Subtree; st != (provider.SubtreeSums{In: 50, Out: 200, Cr: 1000, Cw: 300, Reason: 10}) {
		t.Fatalf("subtree must equal shutdown totals: %+v", st)
	}
}

func TestTokensOutputOnly(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "plain-0001")
	if got := sums(root); got != [5]int64{0, 163, 0, 0, 0} || root.MaxCtx != 0 {
		t.Fatalf("root = %v", got)
	}
}

func TestTokensSubagentTotalNeverRelabelled(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	a := findNode(roots, "sub-0002/call-A")
	b := findNode(roots, "sub-0002/call-B")
	if got := sums(a); got != [5]int64{0, 50, 0, 0, 0} {
		t.Fatalf("call-A = %v", got)
	}
	if got := sums(b); got != [5]int64{0, 10, 0, 0, 0} {
		t.Fatalf("call-B = %v", got)
	}
	var walk func(n *provider.SessionNode)
	walk = func(n *provider.SessionNode) {
		for _, v := range []int64{n.In, n.Out, n.Cr, n.Cw, n.Reason, n.MaxCtx,
			n.Subtree.In, n.Subtree.Out, n.Subtree.Cr, n.Subtree.Cw, n.Subtree.Reason} {
			if v == 88888 || v == 99999 {
				t.Fatalf("totalTokens leaked into %s", n.ID)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(findNode(roots, "sub-0002"))
}

// --- export data precedence (otel-0006 + testdata/home/otel/export.jsonl) ---

func TestTokensOTelSession(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	root := findNode(roots, "otel-0006")
	// api-o1 (duplicated span counted once) + api-o2 + output of unmatched api-o3.
	if got := sums(root); got != [5]int64{7 + 16, 86 + 120 + 30, 23545 + 24000, 448 + 600, 12} {
		t.Fatalf("root = %v", got)
	}
	if root.MaxCtx != 24616 {
		t.Fatalf("maxctx = %d", root.MaxCtx)
	}
}

func TestTokensOTelSubagent(t *testing.T) {
	p, _ := fixtureRoot(t)
	roots, _ := p.BuildTree(fixtureNow)
	sub := findNode(roots, "otel-0006/call-O")
	if got := sums(sub); got != [5]int64{308, 574, 40000, 15000, 0} || sub.MaxCtx != 0 {
		t.Fatalf("sub = %v maxctx=%d", got, sub.MaxCtx)
	}
}

func TestTokensOTelNotDoubleCountedWithShutdownOrRows(t *testing.T) {
	p, root := fixtureRoot(t)
	makeStoreDB(t, filepath.Join(root, "session-store.db"), true,
		fixtureRow{session: "otel-0006", in: 5000, out: 5000, cr: 5000, cw: 5000, reason: 5000, createdAt: "2026-01-01 11:00:02"},
		fixtureRow{session: "otel-0006", agent: "call-O", in: 6000, out: 6000, cr: 6000, cw: 6000, reason: 6000, createdAt: "2026-01-01 11:00:05"})
	roots, _ := p.BuildTree(fixtureNow)
	n := findNode(roots, "otel-0006")
	want := provider.SubtreeSums{In: 23 + 308, Out: 236 + 574, Cr: 47545 + 40000, Cw: 1048 + 15000, Reason: 12}
	if *n.Subtree != want {
		t.Fatalf("subtree = %+v, want %+v", *n.Subtree, want)
	}
	if got := sums(n); got != [5]int64{23, 236, 47545, 1048, 12} {
		t.Fatalf("root own = %v (shutdown subtraction must not apply)", got)
	}
}

func TestTokensOTelMissingCacheAttributes(t *testing.T) {
	s := parseFixture(t, "otel-0006")
	td := tokenData{otel: otelIndex{{"otel-0006", "api-o1"}: {input: 23523}}}
	got := s.nodeTokens("", td)
	if got != (nodeTokens{in: 23523, out: 110 + 30, maxctx: 23523, source: srcOTel}) {
		t.Fatalf("tokens = %+v", got)
	}
	if calls := s.calls("", td); calls[0].In != 23523 || calls[0].Cr != 0 || calls[0].Cw != 0 || calls[0].Ctx != 23523 {
		t.Fatalf("call = %+v", calls[0])
	}
}

func TestTokensOTelOnlyForOwningItem(t *testing.T) {
	// A span for another session, or for a response id no item owns, changes nothing.
	s := parseFixture(t, "sub-0002")
	td := tokenData{otel: otelIndex{{"otel-0006", "api-s1"}: {input: 1, out: 1}, {"sub-0002", "api-zz"}: {input: 1}}}
	if got := s.nodeTokens("", td); got.source != srcOutputOnly || got.out != 100 {
		t.Fatalf("tokens = %+v", got)
	}
}
