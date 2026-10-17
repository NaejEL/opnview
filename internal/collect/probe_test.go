package collect_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"

	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The availability-probe tests.
//
// AN UNAVAILABLE SOURCE IS REPORTED, NEVER WORKED AROUND. For each source, a fake in the
// state the survey documents as "present but disabled" must move exactly one availability
// row to the state the survey names, with the probe and the instant recorded, and must
// write no data row. That is the whole of what these tests check, source by source,
// because the alternative — an empty screen with no explanation — is the failure this
// product exists to avoid.

// TestEachSourcePresentButDisabledMovesExactlyOneAvailabilityRowAndWritesNoData is the
// table AC16 asks for, one subtest per source.
func TestEachSourcePresentButDisabledMovesExactlyOneAvailabilityRowAndWritesNoData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// arrange puts the fake in the state the survey documents as present-but-disabled
		// for this source.
		arrange func(fake *providertest.Firewall)
		kind    string
		key     string
		// wantState is the state the survey names for that condition.
		wantState vocab.AvailabilityState
		// wantProbe is the path the survey says determines it.
		wantProbe string
		// wantDetailContains is the phrase the recorded detail has to carry, so the
		// condition is legible rather than merely coded.
		wantDetailContains string
		// wantMoved is EXACTLY the set of registry rows that may end the pass in
		// wantState by way of wantProbe, as "kind/provider_key". This is the "exactly
		// one" of the criterion: a pass that moved a second row would fail here even
		// though the row under test was correct.
		wantMoved []string
		// wantStateTotal is how many rows in the WHOLE registry may hold wantState,
		// whatever probe determined it. Zero means the case does not assert it, which is
		// only true of the case whose wantState is `unavailable` — nearly every row is
		// unavailable against a fake that answers 404 almost everywhere, so a total
		// there would assert nothing.
		wantStateTotal int
	}{
		{
			name: "the intrusion-detection engine is installed and stopped",
			arrange: func(fake *providertest.Firewall) {
				fake.AnswerFixture(collecttest.IDSStatus, "ids_status_stopped.json")
			},
			kind: core.KindSecurityEvent, key: collecttest.ProviderSuricata,
			wantState: vocab.StatePresentButDisabled,
			wantProbe: collecttest.IDSStatus.Path,
			// The survey's own words: 404, 401 or 403 is absent; a status of stopped,
			// disabled or unknown is present but disabled.
			wantDetailContains: "stopped",
			wantMoved:          []string{"security_event/suricata"},
			wantStateTotal:     1,
		},
		{
			name: "the firewall exports its flow data elsewhere and keeps none",
			arrange: func(fake *providertest.Firewall) {
				fake.AnswerFixture(collecttest.NetflowIsEnabled, "netflow_is_enabled_export_only.json")
			},
			kind: core.KindMeasurementSample, key: collecttest.ProviderInsight,
			wantState:          vocab.StatePresentButDisabled,
			wantProbe:          collecttest.NetflowIsEnabled.Path,
			wantDetailContains: "local collection is off",
			wantMoved:          []string{"measurement_sample/insight"},
			wantStateTotal:     1,
		},
		{
			name: "the DHCP backend runs and serves no range",
			arrange: func(fake *providertest.Firewall) {
				fake.AnswerFixture(collecttest.DnsmasqStatus, "dnsmasq_status_running.json")
				fake.AnswerFixture(collecttest.DnsmasqSettings, "dnsmasq_settings_no_ranges.json")
			},
			kind: core.KindDHCPLease, key: collecttest.ProviderDnsmasq,
			wantState:          vocab.StatePresentButDisabled,
			wantProbe:          collecttest.DnsmasqStatus.Path,
			wantDetailContains: "dhcp_ranges is empty",
			// TWO rows, and that is correct rather than a smear: one service status
			// endpoint informs two registry rows, because Dnsmasq is an implementation of
			// the dhcp_lease kind AND of the dns_lookup kind. The seam is what makes that
			// two rows instead of one conflated source, and the quantifier has to know it.
			wantMoved:      []string{"dhcp_lease/dnsmasq", "dns_lookup/dnsmasq"},
			wantStateTotal: 2,
		},
		{
			name: "the resolver runs and its query reporting is off",
			arrange: func(fake *providertest.Firewall) {
				fake.AnswerFixture(collecttest.UnboundStatus, "unbound_status_running.json")
				fake.AnswerFixture(collecttest.UnboundSettings, "unbound_settings.json")
				fake.AnswerFixture(collecttest.UnboundIsEnabled, "unbound_is_enabled_off.json")
			},
			kind: core.KindDNSLookup, key: collecttest.ProviderUnbound,
			wantState: vocab.StatePresentButDisabled,
			wantProbe: collecttest.UnboundStatus.Path,
			// The survey is explicit that this is a setting to turn on and never an
			// absence of lookups, and the detail has to say so.
			wantDetailContains: "query reporting is off",
			wantMoved:          []string{"dns_lookup/unbound"},
			wantStateTotal:     1,
		},
		{
			name: "the filter log answers with a body that is not an array",
			arrange: func(fake *providertest.Firewall) {
				// HTTP 200 with a body of the wrong shape. The survey names this as a
				// backend failure, distinct from an empty array, which is a healthy but
				// silent log.
				fake.Answer(opnsense.FirewallLog, http.StatusOK, []byte(`{"result":"failed"}`))
			},
			kind: core.KindFirewallLog, key: collecttest.ProviderPf,
			wantState:          vocab.StateUnavailable,
			wantProbe:          opnsense.FirewallLog.Path,
			wantDetailContains: "failed result",
			wantMoved:          []string{"firewall_log/pf"},
			// Not asserted: see wantStateTotal's own note.
			wantStateTotal: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := collecttest.NewProbeHarness(t)
			testCase.arrange(harness.Fake)

			// Every row starts in the not-yet-probed state the schema creates, so any row
			// that ends the pass in wantState by way of wantProbe moved during it.
			for _, provider := range everyProvider(t, harness.Store) {
				state, probe, checkedAt := harness.AvailabilityOf(t, provider.kind, provider.key)
				if state != vocab.StateUnavailable || probe != "not_yet_probed" || checkedAt != 0 {
					t.Fatalf("%s/%s did not start not-yet-probed (%q, %q, %d), so nothing below "+
						"can say that it moved", provider.kind, provider.key, state, probe, checkedAt)
				}
			}

			if err := harness.Collector.ProbeAll(context.Background()); err != nil {
				// A probe round against a fake that answers 404 almost everywhere reports
				// what it could not reach, which is the point; it is not a test failure.
				t.Logf("the probe round reported: %v", err)
			}
			harness.Fake.AssertEveryPathIsRegistered()

			state, probe, checkedAt := harness.AvailabilityOf(t, testCase.kind, testCase.key)
			if state != testCase.wantState {
				t.Errorf("the state is %q, want %q", state, testCase.wantState)
			}
			if !strings.Contains(probe, testCase.wantProbe) {
				t.Errorf("the probe is %q, which does not name %s", probe, testCase.wantProbe)
			}
			if checkedAt == 0 {
				t.Error("the row carries no instant, so nothing can say how stale the state is")
			}
			if detail := harness.DetailOf(t, testCase.kind, testCase.key); !strings.Contains(
				detail, testCase.wantDetailContains) {
				t.Errorf("the detail is %q, which does not carry %q",
					detail, testCase.wantDetailContains)
			}

			// The quantifier. EXACTLY these rows moved to that state by that probe.
			moved := harness.MovedRows(t, testCase.wantState, testCase.wantProbe)
			if strings.Join(moved, ",") != strings.Join(testCase.wantMoved, ",") {
				t.Errorf("the rows that moved to %q by way of %s are %v, want exactly %v",
					testCase.wantState, testCase.wantProbe, moved, testCase.wantMoved)
			}
			if testCase.wantStateTotal > 0 {
				if total := harness.RowsInState(t, testCase.wantState); total != testCase.wantStateTotal {
					t.Errorf("%d rows in the whole registry hold %q, want %d; a pass that put one "+
						"source's conclusion on another source's row would look like this",
						total, testCase.wantState, testCase.wantStateTotal)
				}
			}

			// And no data row exists anywhere. A probe reads a state; it never ingests.
			for _, table := range []string{"flow", "security_event", "dns_resolution",
				"dhcp_lease", "pair_volume_observation", "measurement_sample", "collection_gap",
				"state_snapshot", "state_item"} {
				if stored := collecttest.CountRows(t, harness.Store, table); stored != 0 {
					t.Errorf("the probe round wrote %d rows into %s", stored, table)
				}
			}
		})
	}
}

// TestASourceThatAnswers404IsUnavailableAndNotActivated is the absent case, which the
// survey separates from present-but-disabled: a 404 on a module endpoint is the normal
// signal that an optional component is not installed.
func TestASourceThatAnswers404IsUnavailableAndNotActivated(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	// Nothing is arranged, so every endpoint answers 404.
	if err := harness.Collector.ProbeAll(context.Background()); err == nil {
		t.Log("the probe round reported no failure, which is acceptable: every state was recorded")
	}
	harness.Fake.AssertEveryPathIsRegistered()

	for _, provider := range everyProvider(t, harness.Store) {
		if provider.kind == core.KindIPGeolocation {
			// Not probed here: the maxmind plugin records the dataset's availability, and
			// this package makes no call outside the firewall.
			continue
		}
		state, _, _ := harness.AvailabilityOf(t, provider.kind, provider.key)
		if state != vocab.StateUnavailable {
			t.Errorf("%s/%s reads %q against a firewall that answers 404 everywhere",
				provider.kind, provider.key, state)
		}
	}
	for _, kind := range []string{core.KindFirewallLog, core.KindSecurityEvent, core.KindMeasurementSample,
		core.KindDHCPLease, core.KindDNSLookup} {
		if key := harness.ActiveKeyOf(t, kind); key != "" {
			t.Errorf("the %s kind activated %q against a firewall that answers 404", kind, key)
		}
	}
}

// TestAReachableSourceWithNothingToSayStaysReachable is the case the verified section
// measured and the one a naive collector gets wrong.
//
// A narrow ruleset that fires only on contact with known-malicious infrastructure reports
// nothing for weeks. That is a true reading of a correctly working source, and calling it
// a fault would be the defect.
func TestAReachableSourceWithNothingToSayStaysReachable(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	harness.Fake.AnswerFixture(collecttest.IDSStatus, "ids_status_running.json")
	// An empty array, written here rather than as a fixture because the value is the
	// whole of what it says: the engine answered and had nothing to report.
	harness.Fake.AnswerJSON(opnsense.FirewallLog, []any{})

	if err := harness.Collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	state, _, _ := harness.AvailabilityOf(t, core.KindSecurityEvent, collecttest.ProviderSuricata)
	if state != vocab.StateReachable {
		t.Errorf("a running engine reads %q", state)
	}
	logState, _, _ := harness.AvailabilityOf(t, core.KindFirewallLog, collecttest.ProviderPf)
	if logState != vocab.StateReachable {
		t.Errorf("a log that returned an empty array reads %q, not reachable", logState)
	}
	detail := harness.DetailOf(t, core.KindFirewallLog, collecttest.ProviderPf)
	if !strings.Contains(detail, "not an absence of traffic") {
		t.Errorf("the detail is %q, which does not say that silence is not an absence of traffic",
			detail)
	}
}

// TestTwoProvidersOfAnExclusiveKindTheFirewallDoesNotSeparateActivateNeitherAndRecordTheAmbiguity
// is decision 6, restated against an EXCLUSIVE kind.
//
// The rule has not changed: provider selection reads the firewall's own configuration and never
// a preference order invented here, so when two implementations of one kind are both running and
// both configured, opnview reads NEITHER and says why rather than choosing for the user.
//
// What changed is the kind it used to be demonstrated on. This test drove it through the two
// DHCP backends, and dhcp_lease is now CONCURRENT — one server issuing on one VLAN and another
// on a second is an ordinary deployment, and every lease says which server issued it, so there
// is nothing for opnview to choose. That case is now asserted as the positive one, in
// TestTwoDHCPServersBothServingAreBothActivated.
//
// So the ambiguity rule is driven here at the seam where it lives, with two probeables of an
// exclusive kind, rather than through whichever product happens to be ambiguously configured.
// That is stricter as well as still true: it no longer depends on a fixture's configuration, and
// it keeps working when no shipped implementation can present the ambiguity at all.
func TestTwoProvidersOfAnExclusiveKindTheFirewallDoesNotSeparateActivateNeitherAndRecordTheAmbiguity(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	// dns_lookup is exclusive, and for a reason about the rows rather than the deployment:
	// dns_resolution.lookup_key carries no provider, so a lookup that transited two resolvers
	// would be two records nothing could tell apart from two lookups.
	separable := func(key string) core.Probeable {
		return core.Probeable{ProviderKey: key, Run: func(context.Context, provider.Session) (provider.Result, error) {
			return provider.Result{
				State:     vocab.StateReachable,
				Outcome:   vocab.OutcomeHealthy,
				Probe:     collecttest.UnboundIsEnabled,
				Separable: true,
			}, nil
		}}
	}
	if err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup,
		[]core.Probeable{separable(collecttest.ProviderUnbound), separable(collecttest.ProviderDnsmasq)}); err != nil {
		t.Fatalf("resolving an ambiguous exclusive kind: %v", err)
	}

	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != "" {
		t.Fatalf("an exclusive kind activated %q although the firewall separates neither provider",
			key)
	}
	for _, key := range []string{collecttest.ProviderUnbound, collecttest.ProviderDnsmasq} {
		state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, key)
		if state != vocab.StateReachable {
			t.Errorf("%s reads %q, and both are running", key, state)
		}
		detail := harness.DetailOf(t, core.KindDNSLookup, key)
		if !strings.Contains(detail, "does not say") {
			t.Errorf("the detail for %s is %q, which does not record the ambiguity", key, detail)
		}
	}
}

// TestTwoProvidersOfAConcurrentKindAreBothActivatedAndRecordNoAmbiguity is the same seam, the
// other rule, and the reason the test above had to move off dhcp_lease.
//
// Where a kind's rows carry the provider that reported them, two qualifying providers are not an
// ambiguity: both are read, and neither row carries the sentence about opnview reading neither,
// because that sentence would be false.
func TestTwoProvidersOfAConcurrentKindAreBothActivatedAndRecordNoAmbiguity(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	separable := func(key string) core.Probeable {
		return core.Probeable{ProviderKey: key, Run: func(context.Context, provider.Session) (provider.Result, error) {
			return provider.Result{
				State:     vocab.StateReachable,
				Outcome:   vocab.OutcomeHealthy,
				Probe:     collecttest.KeaStatus,
				Separable: true,
			}, nil
		}}
	}
	if err := harness.Collector.ResolveKind(ctx, core.KindDHCPLease,
		[]core.Probeable{separable(collecttest.ProviderKea), separable(collecttest.ProviderDnsmasq)}); err != nil {
		t.Fatalf("resolving a concurrent kind: %v", err)
	}

	keys := harness.ActiveKeysOf(t, core.KindDHCPLease)
	if len(keys) != 2 {
		t.Fatalf("a concurrent kind with two qualifying providers activated %d: %v", len(keys), keys)
	}
	for _, key := range keys {
		if detail := harness.DetailOf(t, core.KindDHCPLease, key); strings.Contains(detail, "reads neither") {
			t.Errorf("%s records an ambiguity that does not apply to a concurrent kind: %q",
				key, detail)
		}
	}
}

// TestOneBackendTheFirewallSeparatesIsActivated is the other half: when the firewall's
// own configuration does name one, opnview reads it.
func TestOneBackendTheFirewallSeparatesIsActivated(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	harness.Fake.AnswerFixture(collecttest.KeaStatus, "kea_status_disabled.json")
	harness.Fake.AnswerFixture(collecttest.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(collecttest.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.Collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	if key := harness.ActiveKeyOf(t, core.KindDHCPLease); key != collecttest.ProviderDnsmasq {
		t.Fatalf("the DHCP kind activated %q, want the one backend the firewall separates", key)
	}
	state, _, _ := harness.AvailabilityOf(t, core.KindDHCPLease, collecttest.ProviderKea)
	if state != vocab.StatePresentButDisabled {
		t.Errorf("the disabled backend reads %q", state)
	}
}

// TestTheDnsmasqResolverIsDetectedAndReportedAndNeverActivated is decision 8's other half.
//
// Dnsmasq offers no structured query API. The only path to its per-client lookups is
// parsing the free-text line of the generic log endpoint, and that grammar is dnsmasq's
// own rather than an OPNsense contract and remains unseen. Writing a parser against it
// would be guessing, so the provider is detected, reported, and never read — which also
// means the two resolvers can never present the ambiguity the DHCP kind can.
func TestTheDnsmasqResolverIsDetectedAndReportedAndNeverActivated(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	harness.Fake.AnswerFixture(collecttest.UnboundStatus, "unbound_status_running.json")
	harness.Fake.AnswerFixture(collecttest.UnboundSettings, "unbound_settings.json")
	harness.Fake.AnswerFixture(collecttest.UnboundIsEnabled, "unbound_is_enabled_on.json")
	harness.Fake.AnswerFixture(collecttest.DnsmasqStatus, "dnsmasq_status_running.json")
	// log_queries is on in this fixture, so the state is detected rather than inferred.
	harness.Fake.AnswerFixture(collecttest.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.Collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderUnbound {
		t.Fatalf("the resolver kind activated %q, want the one with a structured query API", key)
	}
	state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderDnsmasq)
	if state != vocab.StatePresentButDisabled {
		t.Errorf("the Dnsmasq resolver reads %q", state)
	}
	detail := harness.DetailOf(t, core.KindDNSLookup, collecttest.ProviderDnsmasq)
	if !strings.Contains(detail, "not recorded") || !strings.Contains(detail, "does not parse") {
		t.Errorf("the detail is %q, which does not say that the grammar is unrecorded and "+
			"therefore unparsed", detail)
	}
}

// TestTheEndOfLifeISCPluginIsRecordedAsAbsentRatherThanRead is the 404 the survey
// measured. A 404 says the plugin is not installed here, which is the unavailable state
// working — not a reason to attempt its lease endpoint.
func TestTheEndOfLifeISCPluginIsRecordedAsAbsentRatherThanRead(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	if err := harness.Collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	state, probe, _ := harness.AvailabilityOf(t, core.KindDHCPLease, collecttest.ProviderISC)
	if state != vocab.StateUnavailable {
		t.Errorf("the end-of-life plugin reads %q", state)
	}
	if !strings.Contains(probe, collecttest.ISCStatus.Path) {
		t.Errorf("the probe is %q, which does not name the presence check", probe)
	}
	if detail := harness.DetailOf(t, core.KindDHCPLease, collecttest.ProviderISC); !strings.Contains(
		detail, "not installed") {
		t.Errorf("the detail is %q", detail)
	}
}

// registryRow is one provider, for iteration.
type registryRow struct {
	kind string
	key  string
}

// everyProvider lists the registry.
func everyProvider(t *testing.T, database *store.Store) []registryRow {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(),
		"SELECT kind, provider_key FROM provider ORDER BY kind, provider_key")
	if err != nil {
		t.Fatalf("listing the registry: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var providers []registryRow
	for rows.Next() {
		var row registryRow
		if err := rows.Scan(&row.kind, &row.key); err != nil {
			t.Fatalf("listing the registry: %v", err)
		}
		providers = append(providers, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the registry: %v", err)
	}
	if len(providers) == 0 {
		t.Fatal("the registry is empty")
	}
	return providers
}

// The operator's selection. Rebuilt after the loss of 2 October 2026: the build of that
// evening recorded the four test names below, not their bodies, so the bodies are rewritten
// from what the names state and from the compiled behaviour of resolveKind.

// reachableAndSeparable is a probeable of an exclusive kind the firewall marks as serving.
func reachableAndSeparable(key string) core.Probeable {
	return core.Probeable{ProviderKey: key, Run: func(context.Context, provider.Session) (provider.Result, error) {
		return provider.Result{State: vocab.StateReachable, Outcome: vocab.OutcomeHealthy,
			Probe: collecttest.UnboundIsEnabled, Separable: true}, nil
	}}
}

// TestASourceTheOperatorTurnedOffIsNotCollectedAlthoughItIsReachable: off is never read,
// whatever the probe concluded, and availability is still recorded as the firewall reported
// it, because a selection is a decision and not a claim about the firewall.
func TestASourceTheOperatorTurnedOffIsNotCollectedAlthoughItIsReachable(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	harness.SelectSource(t, core.KindFirewallLog, collecttest.ProviderPf, string(config.SelectionOff))

	if err := harness.Collector.ProbeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindFirewallLog); key != "" {
		t.Fatalf("a source turned off was activated: %q", key)
	}
	if state, _, _ := harness.AvailabilityOf(t, core.KindFirewallLog, collecttest.ProviderPf); state != vocab.StateReachable {
		t.Errorf("the reachable filter log reads %q; the selection must not change what was reported", state)
	}

	probes := len(harness.Fake.RequestsTo(opnsense.FirewallLog))
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("a collector pass over a source turned off: %v", err)
	}
	if read := len(harness.Fake.RequestsTo(opnsense.FirewallLog)) - probes; read != 0 {
		t.Errorf("the pass read a source turned off %d times", read)
	}
	if stored := collecttest.CountRows(t, harness.Store, "flow"); stored != 0 {
		t.Errorf("the pass stored %d flows from a source turned off", stored)
	}
}

// TestASourceTheOperatorTurnedOnIsCollectedAlthoughTheProbeWouldHaveDroppedIt: one failed
// probe used to drop a working source until the next round. Turned on, the source is read
// whatever the probe concluded, and the availability row says what the probe saw.
func TestASourceTheOperatorTurnedOnIsCollectedAlthoughTheProbeWouldHaveDroppedIt(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.Fake.Answer(opnsense.FirewallLog, http.StatusInternalServerError, []byte(`{}`))
	harness.SelectSource(t, core.KindFirewallLog, collecttest.ProviderPf, string(config.SelectionOn))

	if err := harness.Collector.ProbeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindFirewallLog); key != collecttest.ProviderPf {
		t.Fatalf("the source turned on is not active (active: %q)", key)
	}
	if state, _, _ := harness.AvailabilityOf(t, core.KindFirewallLog, collecttest.ProviderPf); state != vocab.StateUnavailable {
		t.Errorf("a probe answered 500 reads %q; it must be recorded as unavailable", state)
	}

	// The next pass reads it, at the page size in force, and reports what the read returned.
	// It reads it at its interval, past the back-off the failed probe began (A12 of the broker
	// amendment), so the clock moves on as it would.
	harness.Clock.Advance(time.Minute)
	probes := len(harness.Fake.RequestsTo(opnsense.FirewallLog))
	if err := harness.Collector.CollectFirewallLog(ctx); err == nil {
		t.Error("a pass over a source answering 500 reported no failure")
	}
	requests := harness.Fake.RequestsTo(opnsense.FirewallLog)
	if len(requests)-probes != 1 {
		t.Fatalf("the pass sent %d requests to the filter log, want 1", len(requests)-probes)
	}
	if limit := requests[len(requests)-1].Query.Get("limit"); limit != "500" {
		t.Errorf("the pass asked for %q records, want the default page of 500", limit)
	}
}

// TestASelectionThatCannotBeReadIsReportedRatherThanTreatedAsAuto: a selection row whose
// value is not auto, on or off is an error naming the row, returned by the probe round so
// the operator can find it, rather than a silent fallback.
//
// What the compiled build of 2 October did with the implementation itself is recorded here
// as found: it gave the unreadable row no selection, so the implementation was activated or
// not on the probe's word alone, as with no row. The assertions below hold to that build.
func TestASelectionThatCannotBeReadIsReportedRatherThanTreatedAsAuto(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, "yes")

	err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup, []core.Probeable{reachableAndSeparable(collecttest.ProviderUnbound)})
	if err == nil {
		t.Fatal("an unreadable selection was not reported")
	}
	key := config.KeySourceSelection(core.KindDNSLookup, collecttest.ProviderUnbound)
	if !strings.Contains(err.Error(), key) {
		t.Errorf("the report %q does not name the setting %s", err, key)
	}
	if state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); state != vocab.StateReachable {
		t.Errorf("the availability reads %q; an unreadable selection must not stop it being recorded", state)
	}
}

// TestTurningOneOnBreaksTheTieThatUsedToReadNeither: where two implementations of an
// exclusive kind both answer and the firewall's configuration does not separate them, the
// round reads neither — and no person could break the tie. Turning one on is that person
// saying which one: it is read, the other is not, and the ambiguity is recorded nowhere.
func TestTurningOneOnBreaksTheTieThatUsedToReadNeither(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, string(config.SelectionOn))

	if err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup,
		[]core.Probeable{reachableAndSeparable(collecttest.ProviderUnbound), reachableAndSeparable(collecttest.ProviderDnsmasq)}); err != nil {
		t.Fatalf("resolving the kind: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderUnbound {
		t.Fatalf("the active resolver is %q, want the one turned on", key)
	}
	for _, provider := range []string{collecttest.ProviderUnbound, collecttest.ProviderDnsmasq} {
		if state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, provider); state != vocab.StateReachable {
			t.Errorf("%s reads %q, and both answered", provider, state)
		}
		if detail := harness.DetailOf(t, core.KindDNSLookup, provider); strings.Contains(detail, "reads neither") {
			t.Errorf("%s records the ambiguity although it was settled: %q", provider, detail)
		}
	}
}

// countingProbeable is reachableAndSeparable, counting how often its probe runs.
func countingProbeable(key string, runs *int) core.Probeable {
	inner := reachableAndSeparable(key)
	return core.Probeable{ProviderKey: key, Run: func(ctx context.Context, host provider.Session) (provider.Result, error) {
		*runs++
		return inner.Run(ctx, host)
	}}
}

// TestASavedSelectionIsAppliedWithoutProbingTheFirewallAgain: the collection surface saves a
// selection between two probe rounds, and it is applied at once, from what the last round
// found, with no probe run and no availability row rewritten.
func TestASavedSelectionIsAppliedWithoutProbingTheFirewallAgain(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	runs := 0
	candidates := []core.Probeable{
		countingProbeable(collecttest.ProviderDnsmasq, &runs), countingProbeable(collecttest.ProviderUnbound, &runs),
	}
	if err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup, candidates); err != nil {
		t.Fatalf("resolving the kind: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != "" {
		t.Fatalf("two resolvers the firewall does not separate activated %q", key)
	}
	_, _, checkedBefore := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound)
	runsBefore := runs

	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, string(config.SelectionOn))
	if err := harness.Collector.Reselect(ctx, core.KindDNSLookup); err != nil {
		t.Fatalf("applying the selection: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderUnbound {
		t.Errorf("the active resolver is %q after turning Unbound on, want it", key)
	}

	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, string(config.SelectionOff))
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderDnsmasq, string(config.SelectionOn))
	if err := harness.Collector.Reselect(ctx, core.KindDNSLookup); err != nil {
		t.Fatalf("applying the second selection: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderDnsmasq {
		t.Errorf("the active resolver is %q after switching, want dnsmasq", key)
	}

	if runs != runsBefore {
		t.Errorf("applying a selection ran %d probes; it must run none", runs-runsBefore)
	}
	harness.Clock.Advance(time.Hour)
	if err := harness.Collector.Reselect(ctx, core.KindDNSLookup); err != nil {
		t.Fatalf("applying the selection again: %v", err)
	}
	if _, _, checked := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); checked != checkedBefore {
		t.Error("applying a selection rewrote an availability row, which records a check that did not happen")
	}
}

// TestASelectionSavedBeforeAnyProbeRoundWaitsForTheFirst: with nothing probed yet there is
// nothing to decide from, so nothing is activated until the first round, which applies it.
func TestASelectionSavedBeforeAnyProbeRoundWaitsForTheFirst(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, string(config.SelectionOn))
	if err := harness.Collector.Reselect(ctx, core.KindDNSLookup); err != nil {
		t.Fatalf("applying a selection before any round: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != "" {
		t.Errorf("a selection activated %q with nothing probed", key)
	}
	if err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup,
		[]core.Probeable{reachableAndSeparable(collecttest.ProviderDnsmasq), reachableAndSeparable(collecttest.ProviderUnbound)}); err != nil {
		t.Fatalf("resolving the kind: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderUnbound {
		t.Errorf("the first round activated %q, want the resolver turned on", key)
	}
}

// TestASelectionThatCannotBeReadLeavesTheActiveSourcesAsTheyWere: applying a selection that
// cannot be read is an error, and changes nothing.
func TestASelectionThatCannotBeReadLeavesTheActiveSourcesAsTheyWere(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, string(config.SelectionOn))
	if err := harness.Collector.ResolveKind(ctx, core.KindDNSLookup,
		[]core.Probeable{reachableAndSeparable(collecttest.ProviderDnsmasq), reachableAndSeparable(collecttest.ProviderUnbound)}); err != nil {
		t.Fatalf("resolving the kind: %v", err)
	}
	harness.SelectSource(t, core.KindDNSLookup, collecttest.ProviderUnbound, "maybe")
	if err := harness.Collector.Reselect(ctx, core.KindDNSLookup); err == nil {
		t.Error("a selection that cannot be read was applied without an error")
	}
	if key := harness.ActiveKeyOf(t, core.KindDNSLookup); key != collecttest.ProviderUnbound {
		t.Errorf("the active resolver is %q after a failed application, want it unchanged", key)
	}
}
