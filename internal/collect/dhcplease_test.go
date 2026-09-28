package collect

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The lease and neighbour collector tests.

// arrangeLeaseCollection stands up a harness whose Dnsmasq backend is the active one, which
// is what the surveyed firewall reported: Kea disabled, Dnsmasq running.
func arrangeLeaseCollection(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	harness.fake.answerFixture(opnsense.DnsmasqLeases, "dnsmasq_leases.json")
	harness.fake.answerFixture(opnsense.ARPTable, "get_arp.json")
	harness.fake.answerFixture(opnsense.NDPTable, "get_ndp.json")
	if err := harness.collector.probeDHCPLease(context.Background()); err != nil {
		t.Fatalf("probing the lease backends: %v", err)
	}
	if key := harness.activeKeyOf(t, KindDHCPLease); key != ProviderDnsmasq {
		t.Fatalf("the lease kind activated %q", key)
	}
	return harness
}

// TestALeasePassStoresEachGenerationAndResolvesTheIdentityCascade is the ingest, and the
// cascade choosing the most stable level each lease supports.
func TestALeasePassStoresEachGenerationAndResolvesTheIdentityCascade(t *testing.T) {
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	harness.fake.assertEveryPathIsRegistered()

	leases := len(fixtureRecords(t, "dnsmasq_leases.json"))
	if stored := countRows(t, harness.store, "dhcp_lease"); stored != leases {
		t.Fatalf("stored %d of the %d leases", stored, leases)
	}

	// The lease that reports a client identifier uses the most stable level; the one that
	// reports only a MAC uses the second.
	if byIdentifier := scalarCount(t, harness.store,
		"SELECT count(*) FROM client WHERE identity_kind = ?",
		store.IdentityDHCPClientID); byIdentifier == 0 {
		t.Error("no client was identified by its DHCP client identifier")
	}
	if byMAC := scalarCount(t, harness.store,
		"SELECT count(*) FROM client WHERE identity_kind = ?", store.IdentityMAC); byMAC == 0 {
		t.Error("no client was identified by its MAC")
	}

	// The IEEE locally-administered bit is read from the second hex digit, and the fixture
	// carries both a randomised MAC and a burned-in one, so both directions are exercised.
	if unstable := scalarCount(t, harness.store,
		"SELECT count(*) FROM client WHERE unstable_identity = 1"); unstable == 0 {
		t.Error("no client reads as an unstable identity, so the randomised-MAC test is untested")
	}
	if stable := scalarCount(t, harness.store,
		"SELECT count(*) FROM client WHERE mac IS NOT NULL AND unstable_identity = 0"); stable == 0 {
		t.Error("no client with a MAC reads as a stable identity, so the negative direction is " +
			"untested — and marking a burned-in address unstable is the defect that matters")
	}

	// A reserved lease reports no expiry, and the state is normalised to the vocabulary
	// rather than being folded into active.
	if reserved := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE lease_state = 'reserved'"); reserved == 0 {
		t.Error("the reserved lease was not stored as reserved")
	}
	if withoutExpiry := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NULL"); withoutExpiry == 0 {
		t.Error("no lease was stored without an expiry, so the standing-configuration case is " +
			"untested")
	}
}

// TestALeasePassRepeatedStoresNothingNew is idempotence on the lease table. A generation is
// keyed by (address, start, backend), so a re-poll is a no-op and a renewal is a new row.
func TestALeasePassRepeatedStoresNothingNew(t *testing.T) {
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	leases := countRows(t, harness.store, "dhcp_lease")
	clients := countRows(t, harness.store, "client")
	for pass := 0; pass < 3; pass++ {
		if err := harness.collector.CollectDHCPLease(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := countRows(t, harness.store, "dhcp_lease"); repeated != leases {
		t.Fatalf("four passes stored %d leases, up from %d", repeated, leases)
	}
	if repeated := countRows(t, harness.store, "client"); repeated != clients {
		t.Fatalf("four passes stored %d clients, up from %d", repeated, clients)
	}
}

// TestTheKeaBackendValidityStartIsTheRealFigureAndTheOtherBackendsUseASubstitute is the
// substitute this collector has to make, and the reason it is a reported finding.
//
// dhcp_lease.starts_at is NOT NULL and is half of the key that makes a reissue a second
// row. Kea reports valid_lifetime, so the start is the expiry minus it — a real figure.
// Dnsmasq reports only the expiry, so there is no start to read: the expiry is used as the
// generation discriminator, which keeps a re-poll idempotent and is NOT a claim about when
// the lease began.
func TestTheKeaBackendValidityStartIsTheRealFigureAndTheOtherBackendsUseASubstitute(t *testing.T) {
	ctx := context.Background()

	// Kea, where the figure is real.
	keaHarness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, keaHarness.fake, keaHarness.collector)
	keaHarness.fake.answerFixture(opnsense.KeaStatus, "kea_status_running.json")
	keaHarness.fake.answerFixture(opnsense.KeaDHCPv4, "kea_dhcpv4_get.json")
	keaHarness.fake.answerFixture(opnsense.KeaLeases, "kea_leases.json")
	// The neighbour tables are read on every lease pass, whichever backend is active.
	keaHarness.fake.answerFixture(opnsense.ARPTable, "get_arp.json")
	keaHarness.fake.answerFixture(opnsense.NDPTable, "get_ndp.json")
	if err := keaHarness.collector.probeDHCPLease(ctx); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if err := keaHarness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the Kea leases: %v", err)
	}
	if realStarts := scalarCount(t, keaHarness.store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at - starts_at = 3600"); realStarts == 0 {
		t.Error("no Kea lease carries a start computed from its valid_lifetime")
	}

	// Dnsmasq, where it is the substitute.
	harness := arrangeLeaseCollection(t)
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the Dnsmasq leases: %v", err)
	}
	substituted := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NOT NULL AND starts_at = expires_at")
	if substituted == 0 {
		t.Error("no Dnsmasq lease uses its expiry as the generation discriminator, so the " +
			"substitute this backend needs is not being applied")
	}
}

// TestTheNeighbourTablesAreReadWhetherOrNotDHCPIs is decision 12.
//
// get_arp and get_ndp carry a MAC, an address, the interface and a vendor string, and the
// survey's inferred text named neither. On a firewall whose DHCP is unreadable they are the
// only client identity there is, so they are read unconditionally — and this test proves it
// by reading them with no lease backend active at all.
func TestTheNeighbourTablesAreReadWhetherOrNotDHCPIs(t *testing.T) {
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.ARPTable, "get_arp.json")
	harness.fake.answerFixture(opnsense.NDPTable, "get_ndp.json")
	// No lease backend is arranged, so every lease probe answers 404 and no provider of the
	// kind is active.
	if err := harness.collector.probeDHCPLease(context.Background()); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}
	if key := harness.activeKeyOf(t, KindDHCPLease); key != "" {
		t.Fatalf("a lease backend was activated against a firewall with none: %q", key)
	}

	if err := harness.collector.CollectDHCPLease(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	neighbours := len(fixtureRecords(t, "get_arp.json")) + len(fixtureRecords(t, "get_ndp.json"))
	if stored := countRows(t, harness.store, "client"); stored != neighbours {
		t.Fatalf("the neighbour tables produced %d clients, want %d", stored, neighbours)
	}
	if leases := countRows(t, harness.store, "dhcp_lease"); leases != 0 {
		t.Fatalf("%d leases were stored with no backend active", leases)
	}
	// Both families reach the client table, because the two tables are the v4 and v6 views
	// of the same question.
	addresses := stringColumn(t, harness.store,
		"SELECT last_address FROM client WHERE last_address IS NOT NULL")
	var families int
	for _, address := range addresses {
		if containsColon(address) {
			families |= 2
		} else {
			families |= 1
		}
	}
	if families != 3 {
		t.Error("the neighbour tables did not produce clients of both address families")
	}
	// Every neighbour is identified by its MAC, which is the level the tables supply.
	if byMAC := scalarCount(t, harness.store,
		"SELECT count(*) FROM client WHERE identity_kind = ?", store.IdentityMAC); byMAC != neighbours {
		t.Errorf("%d of the %d neighbours were identified by MAC", byMAC, neighbours)
	}
}

// TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong is the reason the MAC
// column is constrained to the seventeen-character form.
//
// client.mac_is_randomised tests the SECOND HEX DIGIT for the IEEE locally-administered
// bit, and that test is only total on a value of known shape. A MAC opnview cannot
// normalise has to be absent rather than wrong, because a wrong second digit would mark a
// real burned-in address as an unstable identity.
func TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong(t *testing.T) {
	for _, malformed := range []string{
		"", "not a mac", "0a:11:22:33:44", "0a-11-22-33-44-55", "0a:11:22:33:44:5g",
		"0A:11:22:33:44:55:66",
	} {
		if normalised, ok := normaliseMAC(malformed); ok {
			t.Errorf("%q was accepted as the MAC %q", malformed, normalised)
		}
	}
	// And an upper-case one of the right shape is lower-cased rather than refused, because
	// the digit test is on a lower-case value.
	normalised, ok := normaliseMAC("0A:11:22:33:44:55")
	if !ok || normalised != "0a:11:22:33:44:55" {
		t.Fatalf("an upper-case MAC normalised to %q (accepted: %v)", normalised, ok)
	}
}

// containsColon reports whether an address carries a colon, which is how the two families
// are told apart in these assertions. It is a test helper and no production code classifies
// an address this way — the family comes from the filter log's own ipversion field.
func containsColon(address string) bool {
	for index := 0; index < len(address); index++ {
		if address[index] == ':' {
			return true
		}
	}
	return false
}
