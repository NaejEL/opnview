package collect

import (
	"testing"

	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/plugin"
)

// The provider keys the coordinator tests arrange and assert against.
//
// They are fixture values and nothing more: the shipped implementations declare their own keys in
// their plugin manifest (plugins/<name>/plugin.json), no non-test code in this package names one, and a test reads the key it
// needs from this list the way it reads a fixture file name. A test that needs the key of an
// implementation that does not ship declares its own.
const (
	ProviderPf       = "pf"
	ProviderSuricata = "suricata"
	ProviderInsight  = "insight"
	ProviderKea      = "kea"
	ProviderDnsmasq  = "dnsmasq"
	ProviderISC      = "isc"
	ProviderUnbound  = "unbound"
)

// unboundPageBound is how many pages the Unbound lookup implementation walks, read from the
// registry rather than from the implementation's package.
func unboundPageBound(t *testing.T) int {
	t.Helper()
	source, found := plugin.Builtins().DNSLookup.Lookup(ProviderUnbound)
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

// mustFirewallLogSource returns a registered firewall_log implementation.
func mustFirewallLogSource(t *testing.T, key string) firewalllog.Source {
	t.Helper()
	source, found := plugin.Builtins().FirewallLog.Lookup(key)
	if !found {
		t.Fatalf("the firewall_log implementation %q is not registered", key)
	}
	return source
}
