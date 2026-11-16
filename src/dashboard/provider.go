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

// selectProviders returns the providers the dashboard serves: all of them by
// default, or only those named (comma-separated) by the PROVIDER env var or the
// --provider flag, which wins. explicit reports whether a filter was given.
func selectProviders(ps []provider.Provider, args []string) (sel []provider.Provider, explicit bool, err error) {
	if len(ps) == 0 {
		return nil, false, fmt.Errorf("no providers registered")
	}
	spec := os.Getenv("PROVIDER")
	if v, ok := parseProviderFlag(args); ok {
		spec = v
	}
	if strings.TrimSpace(spec) == "" {
		return ps, false, nil
	}
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p := findProvider(ps, name)
		if p == nil {
			return nil, true, fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(providerNamesOf(ps), ", "))
		}
		if findProvider(sel, name) == nil {
			sel = append(sel, p)
		}
	}
	if len(sel) == 0 {
		return ps, false, nil
	}
	return sel, true, nil
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
