package collect_test

import (
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin"
)

// unboundPageBound is how many pages the Unbound lookup implementation walks, read from the
// registry rather than from the implementation's plugin.
func unboundPageBound(t *testing.T) int {
	t.Helper()
	return int(provider.Integer(mustSource(t, "dns_lookup", "unbound").Values, "page_bound"))
}

// mustSource returns a registered implementation of a kind.
func mustSource(t *testing.T, kind, key string) provider.Registration {
	t.Helper()
	source, found := plugin.Builtins().Lookup(kind, key)
	if !found {
		t.Fatalf("the %s implementation %q is not registered", kind, key)
	}
	return source
}
