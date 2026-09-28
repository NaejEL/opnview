package collect

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
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
//   - geo_asn: acquiring the MaxMind dataset is cycle 4C, and this cycle neither probes nor
//     reads it. An implementation for it now would be a seam justified by a step nobody has
//     specified.
//   - flow_volume: its destination, pair_volume_observation, is DERIVED from `flow` by step 5 —
//     the maintainer's ruling — so nothing collects the kind. Its registry row records that the
//     implementation exists on the firewall; the netflow probe that used to answer for it moved
//     with the sampler to the measurement_sample row, which is the row whose material it
//     governs.
//   - reconciled_state: the kind exists so the ten surveyed state-shaped sources have a
//     destination, and it has NO registry row at all, precisely so that this test does not have
//     to be told to ignore one. It is listed here only to say that the absence is deliberate.
func TestEveryRegistryRowOfACollectedKindHasAnImplementation(t *testing.T) {
	database := newTestStore(t)
	registered := registeredKeysByKind()

	for _, provider := range everyProvider(t, database) {
		if provider.kind == KindGeoASN || provider.kind == KindFlowVolume {
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
	harness := newProbeHarness(t)
	ctx := context.Background()

	// Kea is the first key in sort order and is disabled; Dnsmasq is second and is serving.
	harness.fake.answerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.probeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}
	if key := harness.activeKeyOf(t, KindDHCPLease); key != ProviderDnsmasq {
		t.Fatalf("the active backend is %q; activation followed something other than the "+
			"firewall's own configuration", key)
	}

	// The probe order is the sorted one, which is what makes a request sequence comparable.
	keys := make([]string, 0, len(leaseProbeables()))
	for _, candidate := range leaseProbeables() {
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
			return harness.collector.collectLeases(ctx)
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
	harness := newProbeHarness(t)
	ctx := context.Background()

	if _, _, err := leaseSources[ProviderISC].leases(ctx, harness.collector); !errors.Is(
		err, ErrUnsupportedRead) {
		t.Errorf("the ISC lease read returned %v, not the unsupported sentinel", err)
	}
	if _, _, err := lookupSources[ProviderDnsmasq].lookups(ctx, harness.collector, 1); !errors.Is(
		err, ErrUnsupportedRead) {
		t.Errorf("the Dnsmasq lookup read returned %v, not the unsupported sentinel", err)
	}
}

// TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence keeps a registry row that
// somebody activated by hand, and that no code answers for, from looking like a quiet source.
func TestAnActiveProviderWithNoImplementationIsAnErrorRatherThanSilence(t *testing.T) {
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

	if err := harness.collector.collectLeases(ctx); err == nil {
		t.Fatal("collecting from an active provider with no implementation returned no error")
	}
}

// registeredKeysByKind lists what each kind's registry in this package holds.
func registeredKeysByKind() map[string][]string {
	registered := map[string][]string{}
	for key := range firewallLogSources {
		registered[KindFirewallLog] = append(registered[KindFirewallLog], key)
	}
	for key := range securityEventSources {
		registered[KindSecurityEvent] = append(registered[KindSecurityEvent], key)
	}
	for key := range measurementSources {
		registered[KindMeasurementSample] = append(registered[KindMeasurementSample], key)
	}
	for key := range leaseSources {
		registered[KindDHCPLease] = append(registered[KindDHCPLease], key)
	}
	for key := range lookupSources {
		registered[KindDNSLookup] = append(registered[KindDNSLookup], key)
	}
	return registered
}

// TestThePortGrantsExactlyTheEnumeratedCapabilities is what keeps the seam narrow after this
// file stops being read.
//
// The port is the half of the contract that will become a protocol when connectors move out of
// process, so a method added to it is a verb added to that protocol and removing one later is a
// compatibility break. This test fails in BOTH directions on purpose: a capability granted that
// nothing needs is as much a defect as a missing one, because the argument for the port is that
// an implementation cannot reach the store, discovery, availability or activation — and that
// argument is only true while the list below is the whole list.
func TestThePortGrantsExactlyTheEnumeratedCapabilities(t *testing.T) {
	granted := map[string]bool{}
	port := reflect.TypeOf((*session)(nil)).Elem()
	for index := 0; index < port.NumMethod(); index++ {
		granted[port.Method(index).Name] = true
	}

	for _, capability := range []string{
		"call", "serviceState", "readObject", "readCollection", "normaliser", "now",
	} {
		if !granted[capability] {
			t.Errorf("the port no longer grants %q, which an implementation needs", capability)
		}
		delete(granted, capability)
	}
	for surplus := range granted {
		t.Errorf("the port grants %q, which is not in the enumerated boundary: either an "+
			"implementation now needs it and the enumeration should say so, or the port was "+
			"widened to make a call site compile", surplus)
	}
}

// TestNoImplementationReachesPastThePort is the other half of the boundary, and it has to be
// checked in the source rather than at runtime.
//
// A port narrows the STATIC type an implementation is handed; it cannot narrow the dynamic one.
// The value behind `session` is a *Collector, so `host.(*Collector)` compiles and succeeds, and
// an implementation that wrote it would have the store, the identity cascade and every
// coordinator back. No reflection can detect that from outside — what the assertion recovers is
// exactly what the type system was asked to hide. So the guarantee is stated where it is
// visible: no implementation file names the host type at all.
//
// The implementation files are the ones with a product in their name, which is the layout rule
// collect.go states, so this also fails if an implementation is added that reaches past the
// port on its first day.
func TestNoImplementationReachesPastThePort(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			!strings.Contains(strings.TrimSuffix(name, ".go"), "_") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		if strings.Contains(string(source), "Collector") {
			t.Errorf("%s names Collector; an implementation is handed the port and the host type "+
				"is not its business — reaching it back, by assertion or by parameter, undoes "+
				"every absence the port is for", name)
		}
	}
	if checked == 0 {
		t.Fatal("no implementation file was checked, so this test proves nothing")
	}
}
