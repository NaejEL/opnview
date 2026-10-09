package collect

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The two defects found live after the step-5A live corrections were deployed
// (specs/SPEC-test-budget-and-two-live-defects.md):
//
//   - L1: a lease pass counted as complete only when every active backend was read, so
//     a backend that cannot be read by design, or that fails on every pass, kept every
//     unresolved host-name lookup waiting for a pass that never came;
//   - L2: the ARP and NDP neighbour tables list this firewall's own interface entries,
//     and each became a `client` at an address the firewall itself holds.

// leaseRetryHarness is the lease harness with a pending lookup whose host name a lease
// names: the lookup is resolved by the first lease pass that examines it.
func leaseRetryHarness(t *testing.T, key, hostname string) *probeHarness {
	t.Helper()
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()
	unresolvedLookup(t, harness.store, key, hostname+".example.invalid",
		referenceEpoch()-120, referenceEpoch()-100)
	provider, err := harness.store.ProviderID(ctx, KindDHCPLease, ProviderDnsmasq)
	if err != nil {
		t.Fatalf("looking up the readable backend: %v", err)
	}
	label := hostname
	if err := harness.store.InsertDHCPLease(ctx, provider, store.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.51", Hostname: &label, LeaseState: "reserved",
		GenerationKey: store.GenerationKeyOf(nil, nil, referenceEpoch()), ObservedAt: referenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	return harness
}

// TestABackendThatCannotBeReadByDesignDoesNotHoldTheHostNameRetry is AC5's first point.
// The ISC plugin answers every read with ErrUnsupportedRead: it contributes no lease, and
// a pass that read the one readable backend beside it is complete, so the pending lookup
// is resolved by the first pass.
func TestABackendThatCannotBeReadByDesignDoesNotHoldTheHostNameRetry(t *testing.T) {
	t.Parallel()
	harness := leaseRetryHarness(t, "example-key-unsupported", "example-host-unsupported")
	ctx := context.Background()
	if err := harness.store.SetActiveProviders(ctx, KindDHCPLease, ProviderDnsmasq, ProviderISC); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}

	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("a backend that cannot be read by design failed the lease pass: %v", err)
	}
	if done, resolution := examined(t, harness.store, "example-key-unsupported"); !done ||
		resolution != store.ClientResolutionLeased {
		t.Errorf("the first pass beside an unreadable backend left the lookup examined %t, %s",
			done, resolution)
	}
	// The backend that cannot be read keeps its own reason, as before.
	if detail := harness.detailOf(t, KindDHCPLease, ProviderISC); !strings.Contains(detail,
		"field contract is unverified") {
		t.Errorf("the unreadable backend's detail is %q, which has lost its reason", detail)
	}
}

// TestABackendFailingOnEveryPassHoldsTheRetryForTheLimitAndNoLonger is AC5's second
// point. A backend that answers 500 on every pass is reported on each, and holds the
// pending lookup back for exactly the number of passes the setting allows: not one
// pass fewer, which would use the lookup's one examination up against an incomplete
// table after a transient failure, and not for ever.
func TestABackendFailingOnEveryPassHoldsTheRetryForTheLimitAndNoLonger(t *testing.T) {
	t.Parallel()
	harness := leaseRetryHarness(t, "example-key-failing", "example-host-failing")
	ctx := context.Background()
	if err := harness.store.SetActiveProviders(ctx, KindDHCPLease, ProviderDnsmasq, ProviderKea); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}
	harness.fake.answer(opnsense.KeaLeases, http.StatusInternalServerError, []byte(`{}`))

	limit := int(config.DefaultLeaseBackendFailurePassLimit)
	if limit < 2 {
		t.Fatalf("the default limit is %d, and this test needs a pass before it", limit)
	}
	for pass := 1; pass <= limit; pass++ {
		setClock(harness, referenceInstant().Add(time.Duration(pass)*5*time.Minute))
		err := harness.collector.CollectDHCPLease(ctx)
		if err == nil {
			t.Fatalf("pass %d: a lease pass whose backend answered 500 reported no failure", pass)
		}
		done, resolution := examined(t, harness.store, "example-key-failing")
		if pass < limit && done {
			t.Fatalf("pass %d of %d: the lookup was examined while the failing backend still "+
				"held it back", pass, limit)
		}
		if pass == limit && (!done || resolution != store.ClientResolutionLeased) {
			t.Fatalf("pass %d, the limit: the lookup is examined %t, %s; the failing backend "+
				"still holds it back", pass, done, resolution)
		}
		detail := harness.detailOf(t, KindDHCPLease, ProviderKea)
		if !strings.Contains(detail, "lease passes in a row") {
			t.Errorf("pass %d: the failing backend's detail %q does not state the limit", pass, detail)
		}
	}
	if detail := harness.detailOf(t, KindDHCPLease, ProviderKea); !strings.Contains(detail,
		"re-examined without this backend") {
		t.Errorf("at the limit the detail %q does not say the retry goes on without the backend", detail)
	}

	// A read that succeeds again ends the count: the next failure holds the retry back
	// from the start.
	harness.fake.answerFixture(opnsense.KeaLeases, "kea_leases.json")
	setClock(harness, referenceInstant().Add(time.Duration(limit+1)*5*time.Minute))
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the pass whose backends both answered: %v", err)
	}
	if failures := harness.collector.leaseFailurePasses(mustProviderID(t, harness, ProviderKea)); failures != 0 {
		t.Errorf("a backend read again still counts %d failed passes", failures)
	}
}

// TestTheLimitIsAValidatedSettingOfTheLeasePass: the limit reaches a running
// collector from the setting row, and a collector told a lower limit stops holding
// the retry sooner.
func TestTheLimitIsAValidatedSettingOfTheLeasePass(t *testing.T) {
	t.Parallel()
	harness := leaseRetryHarness(t, "example-key-configured", "example-host-configured")
	ctx := context.Background()
	if err := harness.store.SetActiveProviders(ctx, KindDHCPLease, ProviderDnsmasq, ProviderKea); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}
	harness.fake.answer(opnsense.KeaLeases, http.StatusInternalServerError, []byte(`{}`))
	settings := config.Defaults()
	settings.LeaseBackendFailurePassLimit = 1
	harness.collector.Configure(settings)

	if err := harness.collector.CollectDHCPLease(ctx); err == nil {
		t.Fatal("a lease pass whose backend answered 500 reported no failure")
	}
	if done, _ := examined(t, harness.store, "example-key-configured"); !done {
		t.Error("with a limit of one pass, the first failed pass still held the lookup back")
	}
}

// mustProviderID looks one lease provider's registry id up.
func mustProviderID(t *testing.T, harness *probeHarness, key string) int64 {
	t.Helper()
	id, err := harness.store.ProviderID(context.Background(), KindDHCPLease, key)
	if err != nil {
		t.Fatalf("looking up %s: %v", key, err)
	}
	return id
}

// TestANeighbourAtAnAddressThisFirewallHoldsCreatesNoClient is AC6's first point. The
// neighbour tables list the firewall's own interface entries; an entry at an address
// the firewall holds at that instant, in either family, is this firewall and never a
// client, while the other neighbours are still clients.
func TestANeighbourAtAnAddressThisFirewallHoldsCreatesNoClient(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	ctx := context.Background()
	held := stringColumn(t, harness.store, `SELECT DISTINCT a.address FROM interface_address AS a
		JOIN interface AS i ON i.id = a.interface_id
		WHERE a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6') AND i.is_upstream = 0
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
	device := stringColumn(t, harness.store, `SELECT device FROM interface WHERE is_upstream = 0
		ORDER BY id LIMIT 1`)[0]

	harness.fake.answerJSON(opnsense.ARPTable, []any{
		map[string]any{"mac": "0a:11:22:33:44:f1", "ip": heldV4, "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": true},
		map[string]any{"mac": "0a:11:22:33:44:f2", "ip": "198.51.100.77", "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": false},
	})
	harness.fake.answerJSON(opnsense.NDPTable, []any{
		map[string]any{"mac": "0a:11:22:33:44:f3", "ip": heldV6, "intf": device,
			"manufacturer": "example vendor string", "expired": false, "permanent": true},
	})
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	for _, address := range []string{heldV4, heldV6} {
		if clients := scalarCount(t, harness.store, "SELECT count(*) FROM client WHERE last_address = ?",
			address); clients != 0 {
			t.Errorf("the neighbour entry at %s, an address this firewall holds, made %d clients",
				address, clients)
		}
	}
	if clients := scalarCount(t, harness.store, `SELECT count(*) FROM client
		WHERE last_address = '198.51.100.77' AND identity_kind = ?`, store.IdentityMAC); clients != 1 {
		t.Errorf("the neighbour at an address the firewall does not hold made %d clients, want 1", clients)
	}
}
