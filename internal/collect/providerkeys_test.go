package collect_test

import (
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/plugin"
)

// unboundPageBound is how many pages the Unbound lookup implementation walks, read from the
// registry rather than from the implementation's package.
func unboundPageBound(t *testing.T) int {
	t.Helper()
	source, found := plugin.Builtins().DNSLookup.Lookup(collecttest.ProviderUnbound)
	if !found {
		t.Fatal("the Unbound lookup implementation is not registered")
	}
	return source.PageBound()
}

// mustLeaseSource returns a registered dhcp_lease implementation.
func mustLeaseSource(t *testing.T, key string) dhcplease.Source {
	t.Helper()
	source, found := plugin.Builtins().DHCPLease.Lookup(key)
	if !found {
		t.Fatalf("the dhcp_lease implementation %q is not registered", key)
	}
	return source
}

// mustLookupSource returns a registered dns_lookup implementation.
func mustLookupSource(t *testing.T, key string) dnslookup.Source {
	t.Helper()
	source, found := plugin.Builtins().DNSLookup.Lookup(key)
	if !found {
		t.Fatalf("the dns_lookup implementation %q is not registered", key)
	}
	return source
}
