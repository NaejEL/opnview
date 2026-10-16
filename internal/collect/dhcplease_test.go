package collect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/store/vocab"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// The lease and neighbour collector tests.

// TestALeasePassStoresEachGenerationAndResolvesTheIdentityCascade is the ingest, and the
// cascade choosing the most stable level each lease supports.
func TestALeasePassStoresEachGenerationAndResolvesTheIdentityCascade(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()

	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	harness.Fake.AssertEveryPathIsRegistered()

	leases := len(fixtureRecords(t, "dnsmasq_leases.json"))
	if stored := collecttest.CountRows(t, harness.Store, "dhcp_lease"); stored != leases {
		t.Fatalf("stored %d of the %d leases", stored, leases)
	}

	// The lease that reports a client identifier uses the most stable level; the one that
	// reports only a MAC uses the second.
	if byIdentifier := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM client WHERE identity_kind = ?",
		vocab.IdentityDHCPClientID); byIdentifier == 0 {
		t.Error("no client was identified by its DHCP client identifier")
	}
	if byMAC := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM client WHERE identity_kind = ?", vocab.IdentityMAC); byMAC == 0 {
		t.Error("no client was identified by its MAC")
	}

	// The IEEE locally-administered bit is read from the second hex digit, and the fixture
	// carries both a randomised MAC and a burned-in one, so both directions are exercised.
	if unstable := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM client WHERE unstable_identity = 1"); unstable == 0 {
		t.Error("no client reads as an unstable identity, so the randomised-MAC test is untested")
	}
	if stable := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM client WHERE mac IS NOT NULL AND unstable_identity = 0"); stable == 0 {
		t.Error("no client with a MAC reads as a stable identity, so the negative direction is " +
			"untested — and marking a burned-in address unstable is the defect that matters")
	}

	// A reserved lease reports no expiry, and the state is normalised to the vocabulary
	// rather than being folded into active.
	if reserved := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE lease_state = 'reserved'"); reserved == 0 {
		t.Error("the reserved lease was not stored as reserved")
	}
	if withoutExpiry := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NULL"); withoutExpiry == 0 {
		t.Error("no lease was stored without an expiry, so the standing-configuration case is " +
			"untested")
	}
}

// TestALeasePassRepeatedStoresNothingNew is idempotence on the lease table. A generation is
// keyed by (address, generation_key, backend), so a re-poll is a no-op and a renewal is a new
// row. The key is a column of its own rather than the validity start, because the start is now
// null on every backend that cannot report one — and a null in a uniqueness constraint is
// distinct from every other null, so keying on it would have made every one of those leases
// insert a fresh row on every pass. This test is what would catch that.
func TestALeasePassRepeatedStoresNothingNew(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()

	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	leases := collecttest.CountRows(t, harness.Store, "dhcp_lease")
	clients := collecttest.CountRows(t, harness.Store, "client")
	for pass := 0; pass < 3; pass++ {
		if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := collecttest.CountRows(t, harness.Store, "dhcp_lease"); repeated != leases {
		t.Fatalf("four passes stored %d leases, up from %d", repeated, leases)
	}
	if repeated := collecttest.CountRows(t, harness.Store, "client"); repeated != clients {
		t.Fatalf("four passes stored %d clients, up from %d", repeated, clients)
	}
}

// TestTheKeaBackendValidityStartIsTheRealFigureAndTheOtherBackendsStoreNone is the restatement
// of the substitute this collector used to make, against the columns that replaced it.
//
// The finding has not changed: Kea reports valid_lifetime, so the start is the expiry minus it, a
// real figure. What changed is what the other backends do with the fact that they report none.
// starts_at is nullable now, so a backend that cannot know stores NULL instead of writing the
// expiry into a column named for a start; and what keeps a re-poll idempotent is
// generation_key, which says in its own prefix what it rests on. Both directions are asserted,
// because storing a null where a figure belongs and storing a figure where a null belongs are
// both defects and only one of them used to be visible.
func TestTheKeaBackendValidityStartIsTheRealFigureAndTheOtherBackendsStoreNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Kea, where the figure is real.
	keaHarness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, keaHarness.Fake, keaHarness.Collector)
	keaHarness.Fake.AnswerFixture(opnsense.KeaStatus, "kea_status_running.json")
	keaHarness.Fake.AnswerFixture(opnsense.KeaDHCPv4, "kea_dhcpv4_get.json")
	keaHarness.Fake.AnswerFixture(opnsense.KeaLeases, "kea_leases.json")
	// The neighbour tables are read on every lease pass, whichever backend is active.
	keaHarness.Fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	keaHarness.Fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")
	if err := keaHarness.Collector.ProbeDHCPLease(ctx); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if err := keaHarness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the Kea leases: %v", err)
	}
	if realStarts := collecttest.ScalarCount(t, keaHarness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at - starts_at = 3600"); realStarts == 0 {
		t.Error("no Kea lease carries a start computed from its valid_lifetime")
	}
	// And the key of such a lease says that it rests on a real start.
	if keyed := collecttest.ScalarCount(t, keaHarness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE generation_key = 'start:' || starts_at"); keyed == 0 {
		t.Error("no Kea lease is keyed on the validity start it actually knows")
	}

	// Dnsmasq, which reports no start at all and now says so.
	harness := collecttest.ArrangeLeaseCollection(t)
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the Dnsmasq leases: %v", err)
	}
	unknownStarts := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NOT NULL AND starts_at IS NULL")
	if unknownStarts == 0 {
		t.Error("no Dnsmasq lease records its validity start as unknown, so the substitute this " +
			"schema change removed is still being written")
	}
	// The expiry still discriminates the generation — a renewal is still a new row — but the
	// column that says so no longer pretends to be a start.
	keyedOnExpiry := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE generation_key = 'expiry:' || expires_at")
	if keyedOnExpiry == 0 {
		t.Error("no Dnsmasq lease is keyed on its expiry, so the discriminator that keeps a " +
			"re-poll idempotent is not being applied")
	}
	// A standing reservation reports neither, and is one row per day rather than one per poll.
	if reserved := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NULL"); reserved > 0 {
		if keyedOnDay := collecttest.ScalarCount(t, harness.Store,
			`SELECT count(*) FROM dhcp_lease
			 WHERE expires_at IS NULL AND generation_key LIKE 'observed_day:%'`); keyedOnDay != reserved {
			t.Errorf("%d leases report no expiry and %d of them are keyed on the day they were "+
				"observed", reserved, keyedOnDay)
		}
	}
	// Nothing anywhere stores a start it did not read: no lease carries a start equal to its
	// expiry, which is precisely what the removed substitute produced.
	if substituted := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE starts_at IS NOT NULL AND starts_at = expires_at"); substituted != 0 {
		t.Errorf("%d leases carry their expiry as their validity start", substituted)
	}
}

// TestTheNeighbourTablesAreReadWhetherOrNotDHCPIs is decision 12.
//
// get_arp and get_ndp carry a MAC, an address, the interface and a vendor string, and the
// survey's inferred text named neither. On a firewall whose DHCP is unreadable they are the
// only client identity there is, so they are read unconditionally — and this test proves it
// by reading them with no lease backend active at all.
func TestTheNeighbourTablesAreReadWhetherOrNotDHCPIs(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	harness.Fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")
	// No lease backend is arranged, so every lease probe answers 404 and no provider of the
	// kind is active.
	if err := harness.Collector.ProbeDHCPLease(context.Background()); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDHCPLease); key != "" {
		t.Fatalf("a lease backend was activated against a firewall with none: %q", key)
	}

	if err := harness.Collector.CollectDHCPLease(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	neighbours := len(fixtureRecords(t, "get_arp.json")) + len(fixtureRecords(t, "get_ndp.json"))
	if stored := collecttest.CountRows(t, harness.Store, "client"); stored != neighbours {
		t.Fatalf("the neighbour tables produced %d clients, want %d", stored, neighbours)
	}
	if leases := collecttest.CountRows(t, harness.Store, "dhcp_lease"); leases != 0 {
		t.Fatalf("%d leases were stored with no backend active", leases)
	}
	// Both families reach the client table, because the two tables are the v4 and v6 views
	// of the same question.
	addresses := stringColumn(t, harness.Store,
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
	if byMAC := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM client WHERE identity_kind = ?", vocab.IdentityMAC); byMAC != neighbours {
		t.Errorf("%d of the %d neighbours were identified by MAC", byMAC, neighbours)
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

// TestTwoDHCPServersBothServingAreBothActivated is the behavioural consequence of the kind
// becoming concurrent, at the probe round rather than in the schema.
//
// A firewall running both servers used to activate NEITHER, because two candidates the
// firewall's own configuration did not separate was an ambiguity opnview refused to resolve.
// For a concurrent kind there is nothing to resolve: every lease says which server issued it,
// so both are read.
func TestTwoDHCPServersBothServingAreBothActivated(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)

	// Both servers running and serving a range.
	harness.Fake.AnswerFixture(opnsense.KeaStatus, "kea_status_running.json")
	harness.Fake.AnswerFixture(opnsense.KeaDHCPv4, "kea_dhcpv4_get.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.Collector.ProbeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}

	keys := harness.ActiveKeysOf(t, core.KindDHCPLease)
	if len(keys) != 2 {
		t.Fatalf("a firewall running two DHCP servers activated %d of them: %v", len(keys), keys)
	}
	// And neither row carries the ambiguity detail, because there is no ambiguity to record:
	// that sentence belongs to a kind where opnview would have to choose.
	for _, key := range keys {
		if detail := harness.DetailOf(t, core.KindDHCPLease, key); strings.Contains(detail, "reads neither") {
			t.Errorf("%s records an ambiguity that no longer applies: %q", key, detail)
		}
	}
}
