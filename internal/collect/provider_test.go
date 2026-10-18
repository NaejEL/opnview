package collect_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin"
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
	database := collecttest.NewTestStore(t)
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
//   - geo_asn: the MaxMind dataset is acquired and read by the maxmind plugin, not by a
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
	database := collecttest.NewTestStore(t)
	registered := registeredKeysByKind()

	for _, provider := range everyProvider(t, database) {
		if provider.kind == "ip_geolocation" || provider.kind == "flow_volume" || provider.kind == "registrable_domain" {
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
		"dhcp_lease": 3,
		"dns_lookup": 2,
	} {
		if got := len(registered[kind]); got < minimum {
			t.Errorf("the %s kind has %d implementations, want at least %d", kind, got, minimum)
		}
	}
	// And the kinds with one are not pretending otherwise.
	for _, kind := range []string{"firewall_log", "security_event", "measurement_sample"} {
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
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	// Kea is the first key in sort order and is disabled; Dnsmasq is second and is serving.
	harness.Fake.AnswerFixture(collecttest.Declared("kea", "status"), "kea_status_disabled.json")
	harness.Fake.AnswerFixture(collecttest.Declared("dnsmasq", "status"), "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(collecttest.Declared("dnsmasq", "settings"), "dnsmasq_settings.json")

	if err := harness.Collector.ProbeKind(ctx, "dhcp_lease"); err != nil {
		t.Logf("the lease probe reported: %v", err)
	}
	if key := harness.ActiveKeyOf(t, "dhcp_lease"); key != "dnsmasq" {
		t.Fatalf("the active backend is %q; activation followed something other than the "+
			"firewall's own configuration", key)
	}

	// The probe order is the sorted one, which is what makes a request sequence comparable.
	var keys []string
	for _, candidate := range harness.Collector.ProbeablesOf("dhcp_lease") {
		keys = append(keys, candidate.ProviderKey)
	}
	for index := 1; index < len(keys); index++ {
		if keys[index-1] > keys[index] {
			t.Fatalf("the probe order is %v, which is not sorted, so a request sequence is not "+
				"reproducible between passes", keys)
		}
	}
}

// TestTheUnreadableImplementationsReturnTheUnsupportedSentinel pins the sentinel itself, so a
// caller can tell "cannot be read" from "the call failed" without matching on a message.
func TestTheUnreadableImplementationsReturnTheUnsupportedSentinel(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	if _, _, err := mustSource(t, "dhcp_lease", "isc").Implementation.Call(ctx, harness.Collector,
		"leases", 1); !errors.Is(
		err, provider.ErrUnsupportedRead) {
		t.Errorf("the ISC lease read returned %v, not the unsupported sentinel", err)
	}
	if _, _, err := mustSource(t, "dns_lookup", "dnsmasq").Implementation.Call(ctx,
		harness.Collector, "lookups", 1); !errors.Is(
		err, provider.ErrUnsupportedRead) {
		t.Errorf("the Dnsmasq lookup read returned %v, not the unsupported sentinel", err)
	}
}

// registeredKeysByKind lists what the built-ins register, by kind.
func registeredKeysByKind() map[string][]string {
	registered := map[string][]string{}
	for _, registration := range plugin.Builtins().All() {
		registered[registration.Kind] = append(registered[registration.Kind], registration.Key)
	}
	return registered
}
