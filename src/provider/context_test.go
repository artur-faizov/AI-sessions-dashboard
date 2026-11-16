package provider

import "testing"

func TestEstTokens(t *testing.T) {
	if n := EstTokens("abcdefgh"); n != 2 {
		t.Fatalf("ascii: %d, want 2", n)
	}
	if n := EstTokens("привет"); n != 3 { // 6 runes / 2.5, rounded up
		t.Fatalf("cyrillic: %d, want 3", n)
	}
}

func TestScaleTokensKeepsTotal(t *testing.T) {
	items := []ContextItem{{Tokens: 1}, {Tokens: 2}, {Tokens: 7}}
	ScaleTokens(items, 33)
	if SumTokens(items) != 33 || items[0].Tokens != 3 || items[1].Tokens != 6 || items[2].Tokens != 24 {
		t.Fatalf("scaled %+v", items)
	}
}

func TestFitContext(t *testing.T) {
	items := FitContext(100, []ContextItem{{Kind: "user", Tokens: 30}})
	if len(items) != 2 || items[1].Kind != "hidden" || items[1].Tokens != 70 || items[1].Est != EstInferred {
		t.Fatalf("fit %+v", items)
	}
	items = FitContext(10, []ContextItem{{Kind: "user", Tokens: 30}})
	if len(items) != 1 || items[0].Tokens != 10 {
		t.Fatalf("over-estimate must scale down: %+v", items)
	}
}

func TestFitRecorded(t *testing.T) {
	items := FitRecorded(100, []ContextItem{{Kind: "system", Tokens: 20}, {Kind: "tools", Tokens: 40}})
	if len(items) != 2 || SumTokens(items) != 100 {
		t.Fatalf("plausible estimate must scale up without a hidden item: %+v", items)
	}
	items = FitRecorded(100, []ContextItem{{Kind: "system", Tokens: 30}})
	if len(items) != 2 || items[1].Kind != "hidden" || items[1].Tokens != 70 {
		t.Fatalf("a too small estimate must keep the hidden rest: %+v", items)
	}
}
