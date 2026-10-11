package collect

import (
	"context"
	"errors"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
)

// The provider-seam tests.
//
// The seam exists because the schema's registry already holds more than one implementation of
// two kinds, and because the firewall the survey probed runs Dnsmasq for DHCP with Kea
// disabled while another firewall is the other way round. These tests are what keep it a seam
// rather than a comment: the registrations and the schema agree, the multi-implementation
// kinds really have several, and nothing is chosen by code order.

// TestEveryRegisteredImplementationHasARegistryRow keeps the code and the schema from drifting
// apart in the direction that would fail at runtime: an implementation with no registry row
// cannot have its availability recorded against anything.
func TestEveryRegisteredImplementationHasARegistryRow(t *testing.T) {
	t.Parallel()
	database := newTestStore(t)
	ctx := context.Background()

	for kind, keys := range registeredKeysByKind() {
		for _, key := range keys {
			if _, err := database.ProviderID(ctx, kind, key); err != nil {
				t.Errorf("the %s implementation %q is registered in code and has no row in the "+
					"schema's registry: %v", kind, key, err)
			}
		}
	}
}

// TestEveryRegistryRowOfACollectedKindHasAnImplementation is the other direction, and it is the
// one that would otherwise fail silently: a registry row with no implementation would be
// probed by nothing, so its availability would stay at "not yet probed" for ever and a screen
// would show a source nobody is looking at.
//
// Three kinds are excluded deliberately, each for a recorded reason, and the exclusions are
// named here rather than in a comment on the registry so that adding a fourth has to be argued
// for in a test file somebody reads.
//
//   - geo_asn: the MaxMind dataset is acquired and read by internal/maxmind, not by a
//     connector behind this package's port, whose only outbound capability is the firewall.
//   - flow_volume: its destination, pair_volume_observation, is DERIVED from `flow` by step 5 —
//     the maintainer's ruling — so nothing collects the kind. Its registry row records that the
//     implementation exists on the firewall; the netflow probe that used to answer for it moved
//     with the sampler to the measurement_sample row, which is the row whose material it
//     governs.
//   - reconciled_state: the kind exists so the ten surveyed state-shaped sources have a
//     destination, and it has NO registry row at all, precisely so that this test does not have
//     to be told to ignore one. It is listed here only to say that the absence is deliberate.
func TestEveryRegistryRowOfACollectedKindHasAnImplementation(t *testing.T) {
	t.Parallel()
	database := newTestStore(t)
	registered := registeredKeysByKind()

	for _, provider := range everyProvider(t, database) {
		if provider.kind == KindIPGeolocation || provider.kind == KindFlowVolume || provider.kind == KindRegistrableDomain {
			continue
		}
		found := false
		for _, key := range registered[provider.kind] {
			if key == provider.key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the registry holds %s/%s and no implementation is registered for it, so "+
				"nothing will ever probe it", provider.kind, provider.key)
		}
	}
}

// TestTheKindsWithSeveralImplementationsReallyHaveSeveral is what makes the seam structure
// rather than scaffolding: two implementations of each of these kinds exist today, and on the
// maintainer's own hardware both are running.
func TestTheKindsWithSeveralImplementationsReallyHaveSeveral(t *testing.T) {
	t.Parallel()
	registered := registeredKeysByKind()
	for kind, minimum := range map[string]int{
		KindDHCPLease: 3,
		KindDNSLookup: 2,
	} {
		if got := len(registered[kind]); got < minimum {
			t.Errorf("the %s kind has %d implementations, want at least %d", kind, got, minimum)
		}
	}
	// And the kinds with one are not pretending otherwise.
	for _, kind := range []string{KindFirewallLog, KindSecurityEvent, KindMeasurementSample} {
		if got := len(registered[kind]); got != 1 {
			t.Errorf("the %s kind has %d implementations, want exactly the one that exists",
				kind, got)
		}
	}
}

// TestAnImplementationTheFirewallDoesNotSeparateIsNeverActivatedWhateverItsPosition is the
// guarantee that activation reads the firewall and not the code.
//
// Kea sorts before Dnsmasq, so if anything preferred the first implementation it visited, a
// firewall with Kea disabled and Dnsmasq serving would activate the wrong one. The ordering in
// probe.go exists for reproducibility of the request sequence and for nothing else, and this
// is what says so in a way that can fail.
func TestAnImplementationTheFirewallDoesNotSeparateIsNeverActivatedWhateverItsPosition(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	ctx := context.Background()

	// Kea is the first key in sort order and is disabled; Dnsmasq is second and is serving.
	harness.fake.AnswerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	harness.fake.AnswerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.AnswerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.probeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}
	if key := harness.activeKeyOf(t, KindDHCPLease); key != ProviderDnsmasq {
		t.Fatalf("the active backend is %q; activation followed something other than the "+
			"firewall's own configuration", key)
	}

	// The probe order is the sorted one, which is what makes a request sequence comparable.
	keys := make([]string, 0, len(probeablesOf(plugin.Builtins().DHCPLease, leaseProbe)))
	for _, candidate := range probeablesOf(plugin.Builtins().DHCPLease, leaseProbe) {
		keys = append(keys, candidate.providerKey)
	}
	for index := 1; index < len(keys); index++ {
		if keys[index-1] > keys[index] {
			t.Fatalf("the probe order is %v, which is not sorted, so a request sequence is not "+
				"reproducible between passes", keys)
		}
	}
}

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
	harness := newProbeHarness(t)
	ctx := context.Background()

	for _, unreadable := range []struct {
		kind string
		key  string
		call func(context.Context) error
	}{
		{KindDHCPLease, ProviderISC, func(ctx context.Context) error {
			if err := harness.store.SetActiveProviders(ctx, KindDHCPLease, ProviderISC); err != nil {
				return err
			}
			var seen []string
			_, err := harness.collector.collectLeases(ctx, &seen)
			return err
		}},
		{KindDNSLookup, ProviderDnsmasq, func(ctx context.Context) error {
			if err := harness.store.SetActiveProviders(ctx, KindDNSLookup, ProviderDnsmasq); err != nil {
				return err
			}
			return harness.collector.CollectDNSLookup(ctx)
		}},
	} {
		if err := unreadable.call(ctx); err != nil {
			t.Errorf("reading %s/%s returned %v; an unreadable implementation is a state and not "+
				"a failed pass", unreadable.kind, unreadable.key, err)
		}
		detail := harness.detailOf(t, unreadable.kind, unreadable.key)
		if detail == "" {
			t.Errorf("%s/%s was not read and recorded no reason", unreadable.kind, unreadable.key)
		}
	}
}

// TestTheUnreadableImplementationsReturnTheUnsupportedSentinel pins the sentinel itself, so a
// caller can tell "cannot be read" from "the call failed" without matching on a message.
func TestTheUnreadableImplementationsReturnTheUnsupportedSentinel(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	ctx := context.Background()

	if _, _, err := mustLeaseSource(t, ProviderISC).Leases(ctx, harness.collector, 1); !errors.Is(
		err, provider.ErrUnsupportedRead) {
		t.Errorf("the ISC lease read returned %v, not the unsupported sentinel", err)
	}
	if _, _, err := mustLookupSource(t, ProviderDnsmasq).Lookups(ctx, harness.collector, 1); !errors.Is(
		err, provider.ErrUnsupportedRead) {
		t.Errorf("the Dnsmasq lookup read returned %v, not the unsupported sentinel", err)
	}
}

// TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence keeps a registry row that
// somebody activated by hand, and that no code answers for, from looking like a quiet source.
func TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	ctx := context.Background()

	// A registry row of a collected kind with no implementation registered for it. Inserting one
	// is how a future provider arrives, and until its file exists the collector has to say so.
	if _, err := harness.store.DB().ExecContext(ctx,
		`INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
		 VALUES (?, ?, ?, 0, ?)`,
		KindDHCPLease, "an-unimplemented-backend", "An unimplemented backend",
		referenceEpoch()); err != nil {
		t.Fatalf("registering a provider with no implementation: %v", err)
	}
	if _, err := harness.store.DB().ExecContext(ctx,
		`INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
		 SELECT id, ?, 'not_yet_probed', NULL, 0 FROM provider WHERE provider_key = ?`,
		string(store.StateUnavailable), "an-unimplemented-backend"); err != nil {
		t.Fatalf("giving it an availability row: %v", err)
	}
	if err := harness.store.SetActiveProviders(ctx, KindDHCPLease, "an-unimplemented-backend"); err != nil {
		t.Fatalf("activating it: %v", err)
	}

	var seen []string
	if _, err := harness.collector.collectLeases(ctx, &seen); err == nil {
		t.Fatal("collecting from an active provider with no implementation returned no error")
	}
}

// registeredKeysByKind lists what each kind's registry in this package holds.
func registeredKeysByKind() map[string][]string {
	registered := map[string][]string{}
	for key := range plugin.Builtins().FirewallLog.Sources() {
		registered[KindFirewallLog] = append(registered[KindFirewallLog], key)
	}
	for key := range plugin.Builtins().SecurityEvent.Sources() {
		registered[KindSecurityEvent] = append(registered[KindSecurityEvent], key)
	}
	for key := range plugin.Builtins().Measurement.Sources() {
		registered[KindMeasurementSample] = append(registered[KindMeasurementSample], key)
	}
	for key := range plugin.Builtins().DHCPLease.Sources() {
		registered[KindDHCPLease] = append(registered[KindDHCPLease], key)
	}
	for key := range plugin.Builtins().DNSLookup.Sources() {
		registered[KindDNSLookup] = append(registered[KindDNSLookup], key)
	}
	for key := range plugin.Builtins().ResolverCache.Sources() {
		registered[KindResolverCache] = append(registered[KindResolverCache], key)
	}
	for key := range plugin.Builtins().ResolverLocalData.Sources() {
		registered[KindResolverLocalData] = append(registered[KindResolverLocalData], key)
	}
	return registered
}
