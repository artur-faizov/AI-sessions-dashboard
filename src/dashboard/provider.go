package dashboard

import (
	"sort"
	"strings"
	"sync"
)

// provider abstracts the data source behind the API (opencode sqlite DB,
// claude projects dir, ...).
type provider interface {
	name() string
	available() error       // nil if the data source is present; the error text names the missing path
	dataPath() string       // the source root (db file path / claude projects dir) for errors and UI
	fatalWhenMissing() bool // true => startup exits when available() fails; false => serve anyway
	buildTree(nowMS int64) ([]*sessionNode, error)
	sessionCalls(sid string) ([]callRec, error)
	sessionPrompt(sid string) ([]string, error)
	callTrigger(mid string) (*triggerResp, error)
}

// providers maps provider names to constructors.
var providers = map[string]func() provider{
	"opencode": func() provider { return opencodeProvider{} },
	"claude":   func() provider { return newClaudeProvider() },
}

// activeProvider serves the API; it defaults to opencode so code paths that
// never pick a provider (tests) keep working. Swapped at runtime via POST /api/provider.
var activeProvider provider = opencodeProvider{}

var providerMu sync.RWMutex

// currentProvider returns the provider serving the API right now.
func currentProvider() provider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	return activeProvider
}

// setActiveProvider swaps the provider serving the API.
func setActiveProvider(p provider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	activeProvider = p
}

// providerNames lists registered provider names, sorted.
func providerNames() []string {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// selectProvider returns the provider registered under name, or nil when unknown.
func selectProvider(name string) provider {
	if f, ok := providers[name]; ok {
		return f()
	}
	return nil
}

// parseProviderFlag extracts the --provider NAME / --provider=NAME CLI override.
func parseProviderFlag(argv []string) (string, bool) {
	for i, a := range argv {
		if a == "--provider" || a == "-provider" {
			if i+1 < len(argv) {
				return argv[i+1], true
			}
		}
		if strings.HasPrefix(a, "--provider=") {
			return strings.TrimPrefix(a, "--provider="), true
		}
		if strings.HasPrefix(a, "-provider=") {
			return strings.TrimPrefix(a, "-provider="), true
		}
	}
	return "", false
}
