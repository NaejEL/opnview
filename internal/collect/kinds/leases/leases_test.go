package leases_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestAnImplementationThatCannotBeReadReportsWhyRatherThanFailingThePass is the shape the two
// unreadable implementations take: a state with a reason, not a missing code path.
//
// The end-of-life ISC plugin and the Dnsmasq resolver are both registered and both probed, and
// neither can be read — the first because its endpoint answered 404 on the surveyed firmware
// and its contract is unverified, the second because its lookups exist only as free text under
// a grammar nobody has recorded. A pass that meets one records it and returns no error, because
// nothing failed: nothing was attempted.
func TestAnImplementationThatCannotBeReadReportsWhyRatherThanFailingThePass(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	for _, unreadable := range []struct {
		kind string
		key  string
		call func(context.Context) error
	}{
		{"dhcp_lease", "isc", func(ctx context.Context) error {
			if err := harness.Store.SetActiveProviders(ctx, "dhcp_lease", "isc"); err != nil {
				return err
			}
			var seen []string
			_, err := harness.Collector.CollectLeases(ctx, &seen)
			return err
		}},
		{"dns_lookup", "dnsmasq", func(ctx context.Context) error {
			if err := harness.Store.SetActiveProviders(ctx, "dns_lookup", "dnsmasq"); err != nil {
				return err
			}
			return harness.Collector.CollectDNSLookup(ctx)
		}},
	} {
		if err := unreadable.call(ctx); err != nil {
			t.Errorf("reading %s/%s returned %v; an unreadable implementation is a state and not "+
				"a failed pass", unreadable.kind, unreadable.key, err)
		}
		detail := harness.DetailOf(t, unreadable.kind, unreadable.key)
		if detail == "" {
			t.Errorf("%s/%s was not read and recorded no reason", unreadable.kind, unreadable.key)
		}
	}
}

// TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence keeps a registry row that
// somebody activated by hand, and that no code answers for, from looking like a quiet source.
func TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	// A registry row of a collected kind with no implementation registered for it. Inserting one
	// is how a future provider arrives, and until its file exists the collector has to say so.
	if _, err := harness.Store.DB().ExecContext(ctx,
		`INSERT INTO provider (kind, provider_key, display_name, concurrent, is_active, registered_at)
		 VALUES ($1, $2, $3, true, false, $4)`,
		"dhcp_lease", "an-unimplemented-backend", "An unimplemented backend",
		collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("registering a provider with no implementation: %v", err)
	}
	if _, err := harness.Store.DB().ExecContext(ctx,
		`INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
		 SELECT id, $1, 'not_yet_probed', NULL, 0 FROM provider WHERE provider_key = $2`,
		string(vocab.StateUnavailable), "an-unimplemented-backend"); err != nil {
		t.Fatalf("giving it an availability row: %v", err)
	}
	if err := harness.Store.SetActiveProviders(ctx, "dhcp_lease", "an-unimplemented-backend"); err != nil {
		t.Fatalf("activating it: %v", err)
	}

	var seen []string
	if _, err := harness.Collector.CollectLeases(ctx, &seen); err == nil {
		t.Fatal("collecting from an active provider with no implementation returned no error")
	}
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
		{"a machine that sends a DHCP client identifier", collecttest.StringPointer("example-client-identifier-shared")},
		{"a machine that sends none, so the cascade falls to its MAC", nil},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			harness := collecttest.NewDiscoveredProbeHarness(t)
			collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)

			first, err := harness.Store.ProviderID(ctx, "dhcp_lease", "kea")
			if err != nil {
				t.Fatalf("looking up the first server: %v", err)
			}
			second, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
			if err != nil {
				t.Fatalf("looking up the second server: %v", err)
			}

			// One machine: one MAC, one client identifier if it sends one. Two servers, two
			// VLANs, two addresses. The interface identifiers are read out of the discovery
			// fixture rather than assumed.
			snapshot := harness.Collector.Discovery()
			identifiers := make([]string, 0, len(snapshot.Identifiers))
			for _, name := range snapshot.Identifiers {
				identifiers = append(identifiers, string(name))
			}
			if len(identifiers) < 2 {
				t.Fatalf("discovery found %d interfaces, and this test needs two VLANs",
					len(identifiers))
			}

			mac := "0a:11:22:33:44:aa"
			expiry := collecttest.ReferenceEpoch() + 3600
			for index, lease := range []observation{
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
				if err := harness.Collector.IngestLease(ctx, lease.answer(), snapshot,
					providerID, backend, collecttest.ReferenceEpoch()); err != nil {
					t.Fatalf("ingesting the lease from server %d: %v", providerID, err)
				}
			}

			// ONE client. This is the whole claim.
			if clients := collecttest.CountRows(t, harness.Store, "client"); clients != 1 {
				t.Fatalf("one machine leased by two servers produced %d clients, want 1", clients)
			}
			// TWO leases, both attributed to that one machine.
			if leases := collecttest.CountRows(t, harness.Store, "dhcp_lease"); leases != 2 {
				t.Fatalf("one machine leased by two servers produced %d leases, want 2", leases)
			}
			if attributed := collecttest.ScalarCount(t, harness.Store,
				"SELECT count(*) FROM dhcp_lease WHERE client_id IS NULL"); attributed != 0 {
				t.Errorf("%d leases name no machine", attributed)
			}
			// Each naming a different server, which is what makes the two rows readable rather
			// than merely distinct.
			if servers := collecttest.ScalarCount(t, harness.Store,
				"SELECT count(DISTINCT provider_id) FROM dhcp_lease"); servers != 2 {
				t.Errorf("the two leases name %d servers, want 2", servers)
			}
			// And sitting on two different interfaces, which is the deployment rather than an
			// incidental detail: the machine is on two VLANs.
			if interfaces := collecttest.ScalarCount(t, harness.Store,
				"SELECT count(DISTINCT interface_id) FROM dhcp_lease"); interfaces != 2 {
				t.Errorf("the two leases sit on %d interfaces, want 2", interfaces)
			}
			// Re-polling both servers changes nothing: the generation key still carries the
			// idempotence, per server.
			for pass := 0; pass < 2; pass++ {
				for index, lease := range []observation{
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
					if err := harness.Collector.IngestLease(ctx, lease.answer(), snapshot,
						providerID, backend, collecttest.ReferenceEpoch()); err != nil {
						t.Fatalf("re-polling: %v", err)
					}
				}
			}
			if leases := collecttest.CountRows(t, harness.Store, "dhcp_lease"); leases != 2 {
				t.Fatalf("re-polling both servers produced %d leases, want the same 2", leases)
			}
			if clients := collecttest.CountRows(t, harness.Store, "client"); clients != 1 {
				t.Fatalf("re-polling both servers produced %d clients, want the same 1", clients)
			}
		})
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
// backends — and closing it means merging identities across cascade levels on a shared MAC, which
// is a change to the cascade. The reasoning in full, and what closing it would take, is in the doc
// comment on `leaseIdentity` in internal/collect/kinds/leases/dhcplease.go, under "THE ONE CASE
// THAT STILL SPLITS A MACHINE IN TWO". What making the kind concurrent changed is only that the
// disagreement is now simultaneous rather than sequential, which is why it needs an assertion under
// it.
//
// IF THIS TEST FAILS because the cascade was changed deliberately, the expectation below is what
// records the new answer — and the paragraph in internal/collect/kinds/leases/dhcplease.go has to
// move with it.
func TestAnAsymmetricReportOfOneMachineStillSplitsItInTwoClients(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := collecttest.NewDiscoveredProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)

	first, err := harness.Store.ProviderID(ctx, "dhcp_lease", "kea")
	if err != nil {
		t.Fatalf("looking up the first server: %v", err)
	}
	second, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the second server: %v", err)
	}

	snapshot := harness.Collector.Discovery()
	identifiers := make([]string, 0, len(snapshot.Identifiers))
	for _, name := range snapshot.Identifiers {
		identifiers = append(identifiers, string(name))
	}
	if len(identifiers) < 2 {
		t.Fatalf("discovery found %d interfaces, and this test needs two VLANs", len(identifiers))
	}

	mac := "0a:11:22:33:44:bb"
	identifier := "example-client-identifier-only-one-server-reports"
	expiry := collecttest.ReferenceEpoch() + 3600
	// The asymmetry: the same machine, reported WITH its DHCP client identifier by one server
	// and WITHOUT it by the other.
	leases := []struct {
		observation observation
		providerID  int64
		backend     string
	}{
		{observation{
			Address: "example-address-from-the-reporting-server", MAC: &mac,
			DHCPClientID: &identifier, LeaseState: "active",
			InterfaceIdentifier: identifiers[0], ExpiresAt: &expiry,
		}, first, "kea"},
		{observation{
			Address: "example-address-from-the-silent-server", MAC: &mac,
			DHCPClientID: nil, LeaseState: "active",
			InterfaceIdentifier: identifiers[1], ExpiresAt: &expiry,
		}, second, "dnsmasq"},
	}
	for _, lease := range leases {
		if err := harness.Collector.IngestLease(ctx, lease.observation.answer(), snapshot,
			lease.providerID, lease.backend, collecttest.ReferenceEpoch()); err != nil {
			t.Fatalf("ingesting the lease from %s: %v", lease.backend, err)
		}
	}

	// TODAY'S ANSWER, recorded: two clients, because the two leases resolved at two levels.
	if clients := collecttest.CountRows(t, harness.Store, "client"); clients != 2 {
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
		{vocab.IdentityDHCPClientID, "the server that reported the identifier"},
		{vocab.IdentityMAC, "the server that reported none"},
	} {
		if resolved := collecttest.ScalarCount(t, harness.Store,
			"SELECT count(*) FROM client WHERE identity_kind = $1", level.kind); resolved != 1 {
			t.Errorf("%s resolved %d clients at the %s level, want 1", level.label, resolved,
				level.kind)
		}
	}
	// Both leases are still stored and still attributed to a machine. The limit is that they
	// name two machines; it is not that either lease is lost or orphaned.
	if stored := collecttest.CountRows(t, harness.Store, "dhcp_lease"); stored != 2 {
		t.Errorf("the asymmetric report stored %d leases, want 2", stored)
	}
	if orphaned := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM dhcp_lease WHERE client_id IS NULL"); orphaned != 0 {
		t.Errorf("%d leases name no machine, which is a defect rather than the pinned limit",
			orphaned)
	}

	// And the split is stable across a re-poll: it neither heals nor compounds. A cascade that
	// produced a fresh client per pass would be a much worse defect hiding behind the same count.
	for pass := 0; pass < 2; pass++ {
		for _, lease := range leases {
			if err := harness.Collector.IngestLease(ctx, lease.observation.answer(), snapshot,
				lease.providerID, lease.backend, collecttest.ReferenceEpoch()); err != nil {
				t.Fatalf("re-polling %s: %v", lease.backend, err)
			}
		}
	}
	if clients := collecttest.CountRows(t, harness.Store, "client"); clients != 2 {
		t.Errorf("re-polling produced %d clients, want the same 2", clients)
	}
	if stored := collecttest.CountRows(t, harness.Store, "dhcp_lease"); stored != 2 {
		t.Errorf("re-polling produced %d leases, want the same 2", stored)
	}
}

// TestABackendFailingOnEveryPassHoldsTheRetryForTheLimitAndNoLonger is AC5's second
// point. A backend that answers 500 on every pass is reported on each, and holds the
// pending lookup back for exactly the number of passes the setting allows: not one
// pass fewer, which would use the lookup's one examination up against an incomplete
// table after a transient failure, and not for ever.
func TestABackendFailingOnEveryPassHoldsTheRetryForTheLimitAndNoLonger(t *testing.T) {
	t.Parallel()
	harness := collecttest.LeaseRetryHarness(t, "example-key-failing", "example-host-failing")
	ctx := context.Background()
	if err := harness.Store.SetActiveProviders(ctx, "dhcp_lease", "dnsmasq", "kea"); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}
	harness.Fake.Answer(collecttest.Declared("kea", "leases"), http.StatusInternalServerError, []byte(`{}`))

	limit := int(config.DefaultLeaseBackendFailurePassLimit)
	if limit < 2 {
		t.Fatalf("the default limit is %d, and this test needs a pass before it", limit)
	}
	for pass := 1; pass <= limit; pass++ {
		collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Duration(pass)*5*time.Minute))
		err := harness.Collector.CollectDHCPLease(ctx)
		if err == nil {
			t.Fatalf("pass %d: a lease pass whose backend answered 500 reported no failure", pass)
		}
		done, resolution := collecttest.Examined(t, harness.Store, "example-key-failing")
		if pass < limit && done {
			t.Fatalf("pass %d of %d: the lookup was examined while the failing backend still "+
				"held it back", pass, limit)
		}
		if pass == limit && (!done || resolution != vocab.ClientResolutionLeased) {
			t.Fatalf("pass %d, the limit: the lookup is examined %t, %s; the failing backend "+
				"still holds it back", pass, done, resolution)
		}
		detail := harness.DetailOf(t, "dhcp_lease", "kea")
		if !strings.Contains(detail, "lease passes in a row") {
			t.Errorf("pass %d: the failing backend's detail %q does not state the limit", pass, detail)
		}
	}
	if detail := harness.DetailOf(t, "dhcp_lease", "kea"); !strings.Contains(detail,
		"re-examined without this backend") {
		t.Errorf("at the limit the detail %q does not say the retry goes on without the backend", detail)
	}

	// A read that succeeds again ends the count: the next failure holds the retry back
	// from the start.
	harness.Fake.AnswerFixture(collecttest.Declared("kea", "leases"), "kea_leases.json")
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Duration(limit+1)*5*time.Minute))
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the pass whose backends both answered: %v", err)
	}
	if failures := harness.Collector.LeaseFailurePasses(collecttest.MustProviderID(t, harness, "kea")); failures != 0 {
		t.Errorf("a backend read again still counts %d failed passes", failures)
	}
}

// observation is one lease as a test writes it: the dhcp_lease_record fields these tests vary.
type observation struct {
	Address             string
	MAC                 *string
	DHCPClientID        *string
	LeaseState          string
	InterfaceIdentifier string
	ExpiresAt           *int64
}

// answer is the observation as a checked dhcp_lease_record, absent fields absent.
func (o observation) answer() provider.Answer {
	record := provider.Answer{"address": o.Address, "lease_state": o.LeaseState}
	if o.MAC != nil {
		record["mac"] = *o.MAC
	}
	if o.DHCPClientID != nil {
		record["dhcp_client_id"] = *o.DHCPClientID
	}
	if o.InterfaceIdentifier != "" {
		record["interface_identifier"] = o.InterfaceIdentifier
	}
	if o.ExpiresAt != nil {
		record["expires_at"] = *o.ExpiresAt
	}
	return record
}
