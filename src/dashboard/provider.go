package dashboard

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// providerNamesOf lists the names of the given providers, in slice order.
func providerNamesOf(ps []provider.Provider) []string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name())
	}
	return names
}

// sortedProviderNames lists the names of the given providers, sorted.
func sortedProviderNames(ps []provider.Provider) []string {
	names := providerNamesOf(ps)
	sort.Strings(names)
	return names
}

// findProvider returns the provider registered under name, or nil when unknown.
func findProvider(ps []provider.Provider, name string) provider.Provider {
	for _, p := range ps {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// pickProvider selects the startup provider: the first one by default,
// overridden by the PROVIDER env var and then the --provider flag.
func pickProvider(ps []provider.Provider, args []string) (provider.Provider, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no providers registered")
	}
	name := ps[0].Name()
	if v := os.Getenv("PROVIDER"); v != "" {
		name = v
	}
	if v, ok := parseProviderFlag(args); ok {
		name = v
	}
	p := findProvider(ps, name)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(providerNamesOf(ps), ", "))
	}
	return p, nil
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
