package collect_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The two defects found live after the step-5A live corrections were deployed
// (specs/SPEC-test-budget-and-two-live-defects.md):
//
//   - L1: a lease pass counted as complete only when every active backend was read, so
//     a backend that cannot be read by design, or that fails on every pass, kept every
//     unresolved host-name lookup waiting for a pass that never came;
//   - L2: the ARP and NDP neighbour tables list this firewall's own interface entries,
//     and each became a `client` at an address the firewall itself holds.

// TestABackendThatCannotBeReadByDesignDoesNotHoldTheHostNameRetry is AC5's first point.
// The ISC plugin answers every read with ErrUnsupportedRead: it contributes no lease, and
// a pass that read the one readable backend beside it is complete, so the pending lookup
// is resolved by the first pass.
func TestABackendThatCannotBeReadByDesignDoesNotHoldTheHostNameRetry(t *testing.T) {
	t.Parallel()
	harness := collecttest.LeaseRetryHarness(t, "example-key-unsupported", "example-host-unsupported")
	ctx := context.Background()
	if err := harness.Store.SetActiveProviders(ctx, "dhcp_lease", "dnsmasq", "isc"); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}

	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("a backend that cannot be read by design failed the lease pass: %v", err)
	}
	if done, resolution := collecttest.Examined(t, harness.Store, "example-key-unsupported"); !done ||
		resolution != vocab.ClientResolutionLeased {
		t.Errorf("the first pass beside an unreadable backend left the lookup examined %t, %s",
			done, resolution)
	}
	// The backend that cannot be read keeps its own reason, as before.
	if detail := harness.DetailOf(t, "dhcp_lease", "isc"); !strings.Contains(detail,
		"field contract is unverified") {
		t.Errorf("the unreadable backend's detail is %q, which has lost its reason", detail)
	}
}

// TestTheLimitIsAValidatedSettingOfTheLeasePass: the limit reaches a running
// collector from the setting row, and a collector told a lower limit stops holding
// the retry sooner.
func TestTheLimitIsAValidatedSettingOfTheLeasePass(t *testing.T) {
	t.Parallel()
	harness := collecttest.LeaseRetryHarness(t, "example-key-configured", "example-host-configured")
	ctx := context.Background()
	if err := harness.Store.SetActiveProviders(ctx, "dhcp_lease", "dnsmasq", "kea"); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}
	harness.Fake.Answer(collecttest.Declared("kea", "leases"), http.StatusInternalServerError, []byte(`{}`))
	settings := config.Defaults()
	settings.LeaseBackendFailurePassLimit = 1
	harness.Collector.Configure(settings)

	if err := harness.Collector.CollectDHCPLease(ctx); err == nil {
		t.Fatal("a lease pass whose backend answered 500 reported no failure")
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-configured"); !done {
		t.Error("with a limit of one pass, the first failed pass still held the lookup back")
	}
}

// TestANeighbourAtAnAddressThisFirewallHoldsCreatesNoClient is AC6's first point, against the
// neighbours plugin (PA-AC20). The neighbour tables list the firewall's own interface entries; an
// entry at an address the firewall holds at that instant, in either family, is this firewall and
// never a client, while the other neighbours are still clients.
func TestANeighbourAtAnAddressThisFirewallHoldsCreatesNoClient(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeNeighbourCollection(t)
	ctx := context.Background()
	held := stringColumn(t, harness.Store, `SELECT DISTINCT a.address FROM interface_address AS a
		JOIN interface AS i ON i.id = a.interface_id
		WHERE a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6') AND NOT i.is_upstream
		ORDER BY a.address`)
	var heldV4, heldV6 string
	for _, address := range held {
		if containsColon(address) && heldV6 == "" {
			heldV6 = address
		}
		if !containsColon(address) && heldV4 == "" {
			heldV4 = address
		}
	}
	if heldV4 == "" || heldV6 == "" {
		t.Fatalf("discovery stored no inside address of both families to hold: %v", held)
	}
	device := stringColumn(t, harness.Store, `SELECT device FROM interface WHERE NOT is_upstream
		ORDER BY id LIMIT 1`)[0]

	harness.Fake.AnswerJSON(collecttest.Declared("neighbours", "arp"), []any{
		map[string]any{"mac": "0a:11:22:33:44:f1", "ip": heldV4, "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": true},
		map[string]any{"mac": "0a:11:22:33:44:f2", "ip": "198.51.100.77", "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": false},
	})
	harness.Fake.AnswerJSON(collecttest.Declared("neighbours", "ndp"), []any{
		map[string]any{"mac": "0a:11:22:33:44:f3", "ip": heldV6, "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": true},
	})
	if err := harness.Collector.CollectNeighbour(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	for _, address := range []string{heldV4, heldV6} {
		if clients := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM client WHERE last_address = $1",
			address); clients != 0 {
			t.Errorf("the neighbour entry at %s, an address this firewall holds, made %d clients",
				address, clients)
		}
	}
	if clients := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM client
		WHERE last_address = '198.51.100.77' AND identity_kind = $1`, vocab.IdentityMAC); clients != 1 {
		t.Errorf("the neighbour at an address the firewall does not hold made %d clients, want 1", clients)
	}
}
