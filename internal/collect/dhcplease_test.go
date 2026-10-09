package collect

import (
	"context"
	"strings"
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
	t.Parallel()
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
// keyed by (address, generation_key, backend), so a re-poll is a no-op and a renewal is a new
// row. The key is a column of its own rather than the validity start, because the start is now
// null on every backend that cannot report one — and a null in a uniqueness constraint is
// distinct from every other null, so keying on it would have made every one of those leases
// insert a fresh row on every pass. This test is what would catch that.
func TestALeasePassRepeatedStoresNothingNew(t *testing.T) {
	t.Parallel()
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
	// And the key of such a lease says that it rests on a real start.
	if keyed := scalarCount(t, keaHarness.store,
		"SELECT count(*) FROM dhcp_lease WHERE generation_key = 'start:' || starts_at"); keyed == 0 {
		t.Error("no Kea lease is keyed on the validity start it actually knows")
	}

	// Dnsmasq, which reports no start at all and now says so.
	harness := arrangeLeaseCollection(t)
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the Dnsmasq leases: %v", err)
	}
	unknownStarts := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NOT NULL AND starts_at IS NULL")
	if unknownStarts == 0 {
		t.Error("no Dnsmasq lease records its validity start as unknown, so the substitute this " +
			"schema change removed is still being written")
	}
	// The expiry still discriminates the generation — a renewal is still a new row — but the
	// column that says so no longer pretends to be a start.
	keyedOnExpiry := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE generation_key = 'expiry:' || expires_at")
	if keyedOnExpiry == 0 {
		t.Error("no Dnsmasq lease is keyed on its expiry, so the discriminator that keeps a " +
			"re-poll idempotent is not being applied")
	}
	// A standing reservation reports neither, and is one row per day rather than one per poll.
	if reserved := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE expires_at IS NULL"); reserved > 0 {
		if keyedOnDay := scalarCount(t, harness.store,
			`SELECT count(*) FROM dhcp_lease
			 WHERE expires_at IS NULL AND generation_key LIKE 'observed_day:%'`); keyedOnDay != reserved {
			t.Errorf("%d leases report no expiry and %d of them are keyed on the day they were "+
				"observed", reserved, keyedOnDay)
		}
	}
	// Nothing anywhere stores a start it did not read: no lease carries a start equal to its
	// expiry, which is precisely what the removed substitute produced.
	if substituted := scalarCount(t, harness.store,
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
	t.Parallel()
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

// TestOneMachineLeasedByTwoServersIsOneClientWithTwoLeases is the claim the dhcp_lease kind's
// concurrency rests on, and it is asserted here because this is where the identity cascade runs.
//
// THE DEPLOYMENT: one DHCP server issuing on one VLAN, another on a second, two scopes with no
// overlap. THE OBJECTION it had to answer: two active lease providers would put one machine on
// the screen twice under two names. THE ANSWER: the cascade keys on the DHCP client identifier
// first and on the MAC second, and NEITHER is scoped to an interface, so the same machine
// reported by two servers on two VLANs resolves to ONE client holding TWO leases — which is the
// truth rather than a collapse. The duplication the objection feared needs two servers issuing
// on the SAME scope, and that is a misconfiguration of the firewall rather than a shape this
// model absorbs.
//
// Both routes through the cascade are exercised, because a machine may or may not send a DHCP
// client identifier and the answer has to hold either way.
func TestOneMachineLeasedByTwoServersIsOneClientWithTwoLeases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, shape := range []struct {
		name     string
		clientID *string
	}{
		{"a machine that sends a DHCP client identifier", stringPointer("example-client-identifier-shared")},
		{"a machine that sends none, so the cascade falls to its MAC", nil},
	} {
		t.Run(shape.name, func(t *testing.T) {
			harness := newProbeHarness(t)
			arrangeDiscoverableFirewall(t, harness.fake, harness.collector)

			first, err := harness.store.ProviderID(ctx, KindDHCPLease, ProviderKea)
			if err != nil {
				t.Fatalf("looking up the first server: %v", err)
			}
			second, err := harness.store.ProviderID(ctx, KindDHCPLease, ProviderDnsmasq)
			if err != nil {
				t.Fatalf("looking up the second server: %v", err)
			}

			// One machine: one MAC, one client identifier if it sends one. Two servers, two
			// VLANs, two addresses. The interface identifiers are read out of the discovery
			// fixture rather than assumed.
			snapshot := harness.collector.Discovery()
			identifiers := make([]string, 0, len(snapshot.Identifiers))
			for _, name := range snapshot.Identifiers {
				identifiers = append(identifiers, string(name))
			}
			if len(identifiers) < 2 {
				t.Fatalf("discovery found %d interfaces, and this test needs two VLANs",
					len(identifiers))
			}

			mac := "0a:11:22:33:44:aa"
			expiry := referenceEpoch() + 3600
			for index, lease := range []leaseObservation{
				{
					Address: "example-address-on-the-first-vlan", MAC: &mac,
					DHCPClientID: shape.clientID, LeaseState: "active",
					InterfaceIdentifier: identifiers[0], ExpiresAt: &expiry,
				},
				{
					Address: "example-address-on-the-second-vlan", MAC: &mac,
					DHCPClientID: shape.clientID, LeaseState: "active",
					InterfaceIdentifier: identifiers[1], ExpiresAt: &expiry,
				},
			} {
				providerID, backend := first, "kea"
				if index == 1 {
					providerID, backend = second, "dnsmasq"
				}
				if err := harness.collector.ingestLease(ctx, lease, snapshot,
					providerID, backend, referenceEpoch()); err != nil {
					t.Fatalf("ingesting the lease from server %d: %v", providerID, err)
				}
			}

			// ONE client. This is the whole claim.
			if clients := countRows(t, harness.store, "client"); clients != 1 {
				t.Fatalf("one machine leased by two servers produced %d clients, want 1", clients)
			}
			// TWO leases, both attributed to that one machine.
			if leases := countRows(t, harness.store, "dhcp_lease"); leases != 2 {
				t.Fatalf("one machine leased by two servers produced %d leases, want 2", leases)
			}
			if attributed := scalarCount(t, harness.store,
				"SELECT count(*) FROM dhcp_lease WHERE client_id IS NULL"); attributed != 0 {
				t.Errorf("%d leases name no machine", attributed)
			}
			// Each naming a different server, which is what makes the two rows readable rather
			// than merely distinct.
			if servers := scalarCount(t, harness.store,
				"SELECT count(DISTINCT provider_id) FROM dhcp_lease"); servers != 2 {
				t.Errorf("the two leases name %d servers, want 2", servers)
			}
			// And sitting on two different interfaces, which is the deployment rather than an
			// incidental detail: the machine is on two VLANs.
			if interfaces := scalarCount(t, harness.store,
				"SELECT count(DISTINCT interface_id) FROM dhcp_lease"); interfaces != 2 {
				t.Errorf("the two leases sit on %d interfaces, want 2", interfaces)
			}
			// Re-polling both servers changes nothing: the generation key still carries the
			// idempotence, per server.
			for pass := 0; pass < 2; pass++ {
				for index, lease := range []leaseObservation{
					{
						Address: "example-address-on-the-first-vlan", MAC: &mac,
						DHCPClientID: shape.clientID, LeaseState: "active",
						InterfaceIdentifier: identifiers[0], ExpiresAt: &expiry,
					},
					{
						Address: "example-address-on-the-second-vlan", MAC: &mac,
						DHCPClientID: shape.clientID, LeaseState: "active",
						InterfaceIdentifier: identifiers[1], ExpiresAt: &expiry,
					},
				} {
					providerID, backend := first, "kea"
					if index == 1 {
						providerID, backend = second, "dnsmasq"
					}
					if err := harness.collector.ingestLease(ctx, lease, snapshot,
						providerID, backend, referenceEpoch()); err != nil {
						t.Fatalf("re-polling: %v", err)
					}
				}
			}
			if leases := countRows(t, harness.store, "dhcp_lease"); leases != 2 {
				t.Fatalf("re-polling both servers produced %d leases, want the same 2", leases)
			}
			if clients := countRows(t, harness.store, "client"); clients != 1 {
				t.Fatalf("re-polling both servers produced %d clients, want the same 1", clients)
			}
		})
	}
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
	harness := newProbeHarness(t)
	ctx := context.Background()
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)

	// Both servers running and serving a range.
	harness.fake.answerFixture(opnsense.KeaStatus, "kea_status_running.json")
	harness.fake.answerFixture(opnsense.KeaDHCPv4, "kea_dhcpv4_get.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.probeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}

	keys := harness.activeKeysOf(t, KindDHCPLease)
	if len(keys) != 2 {
		t.Fatalf("a firewall running two DHCP servers activated %d of them: %v", len(keys), keys)
	}
	// And neither row carries the ambiguity detail, because there is no ambiguity to record:
	// that sentence belongs to a kind where opnview would have to choose.
	for _, key := range keys {
		if detail := harness.detailOf(t, KindDHCPLease, key); strings.Contains(detail, "reads neither") {
			t.Errorf("%s records an ambiguity that no longer applies: %q", key, detail)
		}
	}
}

// TestAnAsymmetricReportOfOneMachineStillSplitsItInTwoClients PINS A KNOWN LIMIT. It records
// today's answer so that a change to the identity cascade cannot flip it unnoticed; it does not
// endorse the answer, and two clients is not the outcome anybody wants.
//
// THE CASE: one machine, one MAC, leased by two servers, and only one of the two supplies the
// DHCP client identifier the machine sent. The first lease resolves at the `dhcp_client_id`
// level of the cascade and the second falls to `mac`, so the two identities are different rows
// and one machine reads as two clients.
//
// WHY IT IS PINNED RATHER THAN FIXED: the split is a property of what a source reported, not of
// running two sources — the same thing already happened in sequence when a firewall switched
// backends — and closing it means merging identities across cascade levels on a shared MAC,
// which is a change to the cascade. The reasoning in full, and what closing it would take, is
// in the doc comment on `leaseIdentity` in dhcplease.go, under "THE ONE CASE THAT STILL SPLITS A
// MACHINE IN TWO". What making the kind concurrent changed is only that the disagreement is now
// simultaneous rather than sequential, which is why it needs an assertion under it.
//
// IF THIS TEST FAILS because the cascade was changed deliberately, the expectation below is what
// records the new answer — and the paragraph in dhcplease.go has to move with it.
func TestAnAsymmetricReportOfOneMachineStillSplitsItInTwoClients(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)

	first, err := harness.store.ProviderID(ctx, KindDHCPLease, ProviderKea)
	if err != nil {
		t.Fatalf("looking up the first server: %v", err)
	}
	second, err := harness.store.ProviderID(ctx, KindDHCPLease, ProviderDnsmasq)
	if err != nil {
		t.Fatalf("looking up the second server: %v", err)
	}

	snapshot := harness.collector.Discovery()
	identifiers := make([]string, 0, len(snapshot.Identifiers))
	for _, name := range snapshot.Identifiers {
		identifiers = append(identifiers, string(name))
	}
	if len(identifiers) < 2 {
		t.Fatalf("discovery found %d interfaces, and this test needs two VLANs", len(identifiers))
	}

	mac := "0a:11:22:33:44:bb"
	identifier := "example-client-identifier-only-one-server-reports"
	expiry := referenceEpoch() + 3600
	// The asymmetry: the same machine, reported WITH its DHCP client identifier by one server
	// and WITHOUT it by the other.
	leases := []struct {
		observation leaseObservation
		providerID  int64
		backend     string
	}{
		{leaseObservation{
			Address: "example-address-from-the-reporting-server", MAC: &mac,
			DHCPClientID: &identifier, LeaseState: "active",
			InterfaceIdentifier: identifiers[0], ExpiresAt: &expiry,
		}, first, "kea"},
		{leaseObservation{
			Address: "example-address-from-the-silent-server", MAC: &mac,
			DHCPClientID: nil, LeaseState: "active",
			InterfaceIdentifier: identifiers[1], ExpiresAt: &expiry,
		}, second, "dnsmasq"},
	}
	for _, lease := range leases {
		if err := harness.collector.ingestLease(ctx, lease.observation, snapshot,
			lease.providerID, lease.backend, referenceEpoch()); err != nil {
			t.Fatalf("ingesting the lease from %s: %v", lease.backend, err)
		}
	}

	// TODAY'S ANSWER, recorded: two clients, because the two leases resolved at two levels.
	if clients := countRows(t, harness.store, "client"); clients != 2 {
		t.Fatalf("the asymmetric report produced %d clients; this test pins the known limit at 2, "+
			"so a change here is a change to the identity cascade and has to be deliberate",
			clients)
	}
	// One at each level, which is the mechanism rather than the symptom: asserting only the
	// count would keep passing if both leases fell to the same wrong level.
	for _, level := range []struct {
		kind  string
		label string
	}{
		{store.IdentityDHCPClientID, "the server that reported the identifier"},
		{store.IdentityMAC, "the server that reported none"},
	} {
		if resolved := scalarCount(t, harness.store,
			"SELECT count(*) FROM client WHERE identity_kind = ?", level.kind); resolved != 1 {
			t.Errorf("%s resolved %d clients at the %s level, want 1", level.label, resolved,
				level.kind)
		}
	}
	// Both leases are still stored and still attributed to a machine. The limit is that they
	// name two machines; it is not that either lease is lost or orphaned.
	if stored := countRows(t, harness.store, "dhcp_lease"); stored != 2 {
		t.Errorf("the asymmetric report stored %d leases, want 2", stored)
	}
	if orphaned := scalarCount(t, harness.store,
		"SELECT count(*) FROM dhcp_lease WHERE client_id IS NULL"); orphaned != 0 {
		t.Errorf("%d leases name no machine, which is a defect rather than the pinned limit",
			orphaned)
	}

	// And the split is stable across a re-poll: it neither heals nor compounds. A cascade that
	// produced a fresh client per pass would be a much worse defect hiding behind the same count.
	for pass := 0; pass < 2; pass++ {
		for _, lease := range leases {
			if err := harness.collector.ingestLease(ctx, lease.observation, snapshot,
				lease.providerID, lease.backend, referenceEpoch()); err != nil {
				t.Fatalf("re-polling %s: %v", lease.backend, err)
			}
		}
	}
	if clients := countRows(t, harness.store, "client"); clients != 2 {
		t.Errorf("re-polling produced %d clients, want the same 2", clients)
	}
	if stored := countRows(t, harness.store, "dhcp_lease"); stored != 2 {
		t.Errorf("re-polling produced %d leases, want the same 2", stored)
	}
}

// stringPointer returns a pointer to a value, so a test can express "the backend reported this
// field" and "the backend did not report it" as two different values rather than as a zero.
func stringPointer(value string) *string { return &value }
