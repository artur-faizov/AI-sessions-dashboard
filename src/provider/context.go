package provider

// EstTokens estimates the token count of s: about 4 characters per token for
// ASCII text and 2.5 for other scripts (cyrillic tokenizes much denser).
func EstTokens(s string) int64 {
	var ascii, other int64
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii*5 + other*8 + 19) / 20
}

// TextItem builds a context item whose tokens are estimated from its text.
func TextItem(kind, label, text string) ContextItem {
	t, cut := Clip(text)
	return ContextItem{Kind: kind, Label: label, Tokens: EstTokens(text), Est: EstText, Text: t, Cut: cut}
}

// ScaleTokens rescales the items' tokens in place so they add up to target,
// keeping their proportions; the rounding rest goes to the largest item.
func ScaleTokens(items []ContextItem, target int64) {
	var sum int64
	big := -1
	for i, it := range items {
		sum += it.Tokens
		if big < 0 || it.Tokens > items[big].Tokens {
			big = i
		}
	}
	if sum <= 0 || target < 0 {
		return
	}
	var got int64
	for i := range items {
		items[i].Tokens = items[i].Tokens * target / sum
		got += items[i].Tokens
	}
	items[big].Tokens += target - got
}

// SumTokens adds up the items' tokens.
func SumTokens(items []ContextItem) int64 {
	var n int64
	for _, it := range items {
		n += it.Tokens
	}
	return n
}

// FitContext completes the stored items of an AI call with the part of its
// context nothing records: a "hidden" item worth ctx minus the items. When the
// estimates exceed ctx they are scaled down to it and no hidden item is added.
func FitContext(ctx int64, items []ContextItem) []ContextItem {
	sum := SumTokens(items)
	if sum >= ctx {
		ScaleTokens(items, ctx)
		return items
	}
	return append(items, ContextItem{Kind: "hidden", Tokens: ctx - sum, Est: EstInferred})
}

// FitRecorded fits items that cover the whole context of an AI call (the tool
// stored its full request): estimates at least half of ctx are scaled to it,
// so no hidden item is left. Smaller estimates mean something is missing
// after all and fall back to FitContext.
func FitRecorded(ctx int64, items []ContextItem) []ContextItem {
	if SumTokens(items)*2 < ctx {
		return FitContext(ctx, items)
	}
	ScaleTokens(items, ctx)
	return items
}
