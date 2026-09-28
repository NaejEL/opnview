package collect

import (
	"context"
	"net/http"
	"strings"
	"testing"

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

// probeHarness is a collector, a fake and a store, wired together.
type probeHarness struct {
	fake      *fakeFirewall
	store     *store.Store
	collector *Collector
	clock     *fixedClock
}

// newProbeHarness returns a harness with a fake that answers 404 everywhere, which is the
// honest starting point: an endpoint the test did not arrange is a component that is not
// installed.
func newProbeHarness(t *testing.T) *probeHarness {
	t.Helper()
	fake := newFakeFirewall(t)
	database := newTestStore(t)
	clock := newFixedClock(referenceInstant())
	return &probeHarness{
		fake:      fake,
		store:     database,
		collector: New(newFakeClient(t, fake), database, clock),
		clock:     clock,
	}
}

// availabilityOf reads one provider's recorded state.
func (h *probeHarness) availabilityOf(t *testing.T, kind, providerKey string) (
	store.AvailabilityState, string, int64) {
	t.Helper()
	providerID, err := h.store.ProviderID(context.Background(), kind, providerKey)
	if err != nil {
		t.Fatalf("looking up %s/%s: %v", kind, providerKey, err)
	}
	state, probe, checkedAt, err := h.store.Availability(context.Background(), providerID)
	if err != nil {
		t.Fatalf("reading availability for %s/%s: %v", kind, providerKey, err)
	}
	return state, probe, checkedAt
}

// detailOf reads one provider's recorded detail.
func (h *probeHarness) detailOf(t *testing.T, kind, providerKey string) string {
	t.Helper()
	ctx := context.Background()
	providerID, err := h.store.ProviderID(ctx, kind, providerKey)
	if err != nil {
		t.Fatalf("looking up %s/%s: %v", kind, providerKey, err)
	}
	var detail *string
	if err := h.store.DB().QueryRowContext(ctx,
		"SELECT detail FROM source_availability WHERE provider_id = ?", providerID).
		Scan(&detail); err != nil {
		t.Fatalf("reading the detail for %s/%s: %v", kind, providerKey, err)
	}
	if detail == nil {
		return ""
	}
	return *detail
}

// movedRows returns the registry rows whose availability now reads exactly one state AND
// names one probe endpoint, rendered as sorted "kind/provider_key".
//
// It is the quantifier. Asserting that the row under test moved says nothing about the
// other eight: ProbeAll rewrites every provider's row on every pass, so a collector that
// smeared one source's conclusion across a kind would leave the target row correct and
// still be wrong. Scoping to (state, probe) is what makes "exactly one row moved to the
// state the survey names" a thing that can fail.
func (h *probeHarness) movedRows(t *testing.T,
	state store.AvailabilityState, probePath string) []string {
	t.Helper()
	rows, err := h.store.DB().QueryContext(context.Background(),
		`SELECT p.kind, p.provider_key FROM source_availability a
		 JOIN provider p ON p.id = a.provider_id
		 WHERE a.state = ? AND a.probe LIKE '%' || ? || '%'
		 ORDER BY p.kind, p.provider_key`,
		string(state), probePath)
	if err != nil {
		t.Fatalf("reading the availability table: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var moved []string
	for rows.Next() {
		var kind, key string
		if err := rows.Scan(&kind, &key); err != nil {
			t.Fatalf("reading the availability table: %v", err)
		}
		moved = append(moved, kind+"/"+key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the availability table: %v", err)
	}
	return moved
}

// rowsInState counts the registry rows holding one state, whatever probe determined it.
func (h *probeHarness) rowsInState(t *testing.T, state store.AvailabilityState) int {
	t.Helper()
	var count int
	if err := h.store.DB().QueryRowContext(context.Background(),
		"SELECT count(*) FROM source_availability WHERE state = ?", string(state)).
		Scan(&count); err != nil {
		t.Fatalf("counting the rows in state %q: %v", state, err)
	}
	return count
}

// activeKeyOf returns the provider key opnview reads for a kind, or the empty string.
func (h *probeHarness) activeKeyOf(t *testing.T, kind string) string {
	t.Helper()
	ctx := context.Background()
	id, active, err := h.store.ActiveProviderID(ctx, kind)
	if err != nil {
		t.Fatalf("reading the active %s provider: %v", kind, err)
	}
	if !active {
		return ""
	}
	key, err := h.store.ProviderKey(ctx, id)
	if err != nil {
		t.Fatalf("reading the active %s provider's key: %v", kind, err)
	}
	return key
}

// TestEachSourcePresentButDisabledMovesExactlyOneAvailabilityRowAndWritesNoData is the
// table AC16 asks for, one subtest per source.
func TestEachSourcePresentButDisabledMovesExactlyOneAvailabilityRowAndWritesNoData(t *testing.T) {
	cases := []struct {
		name string
		// arrange puts the fake in the state the survey documents as present-but-disabled
		// for this source.
		arrange func(fake *fakeFirewall)
		kind    string
		key     string
		// wantState is the state the survey names for that condition.
		wantState store.AvailabilityState
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
			arrange: func(fake *fakeFirewall) {
				fake.answerFixture(opnsense.IDSStatus, "ids_status_stopped.json")
			},
			kind: KindSecurityEvent, key: ProviderSuricata,
			wantState: store.StatePresentButDisabled,
			wantProbe: opnsense.IDSStatus.Path,
			// The survey's own words: 404, 401 or 403 is absent; a status of stopped,
			// disabled or unknown is present but disabled.
			wantDetailContains: "stopped",
			wantMoved:          []string{"security_event/suricata"},
			wantStateTotal:     1,
		},
		{
			name: "the firewall exports its flow data elsewhere and keeps none",
			arrange: func(fake *fakeFirewall) {
				fake.answerFixture(opnsense.NetflowIsEnabled, "netflow_is_enabled_export_only.json")
			},
			kind: KindFlowVolume, key: ProviderInsight,
			wantState:          store.StatePresentButDisabled,
			wantProbe:          opnsense.NetflowIsEnabled.Path,
			wantDetailContains: "local collection is off",
			wantMoved:          []string{"flow_volume/insight"},
			wantStateTotal:     1,
		},
		{
			name: "the DHCP backend runs and serves no range",
			arrange: func(fake *fakeFirewall) {
				fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
				fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings_no_ranges.json")
			},
			kind: KindDHCPLease, key: ProviderDnsmasq,
			wantState:          store.StatePresentButDisabled,
			wantProbe:          opnsense.DnsmasqStatus.Path,
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
			arrange: func(fake *fakeFirewall) {
				fake.answerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
				fake.answerFixture(opnsense.UnboundSettings, "unbound_settings.json")
				fake.answerFixture(opnsense.UnboundIsEnabled, "unbound_is_enabled_off.json")
			},
			kind: KindDNSLookup, key: ProviderUnbound,
			wantState: store.StatePresentButDisabled,
			wantProbe: opnsense.UnboundStatus.Path,
			// The survey is explicit that this is a setting to turn on and never an
			// absence of lookups, and the detail has to say so.
			wantDetailContains: "query reporting is off",
			wantMoved:          []string{"dns_lookup/unbound"},
			wantStateTotal:     1,
		},
		{
			name: "the filter log answers with a body that is not an array",
			arrange: func(fake *fakeFirewall) {
				// HTTP 200 with a body of the wrong shape. The survey names this as a
				// backend failure, distinct from an empty array, which is a healthy but
				// silent log.
				fake.answer(opnsense.FirewallLog, http.StatusOK, []byte(`{"result":"failed"}`))
			},
			kind: KindFirewallLog, key: ProviderPf,
			wantState:          store.StateUnavailable,
			wantProbe:          opnsense.FirewallLog.Path,
			wantDetailContains: "failed result",
			wantMoved:          []string{"firewall_log/pf"},
			// Not asserted: see wantStateTotal's own note.
			wantStateTotal: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newProbeHarness(t)
			testCase.arrange(harness.fake)

			// Every row starts in the not-yet-probed state the schema creates, so any row
			// that ends the pass in wantState by way of wantProbe moved during it.
			for _, provider := range everyProvider(t, harness.store) {
				state, probe, checkedAt := harness.availabilityOf(t, provider.kind, provider.key)
				if state != store.StateUnavailable || probe != "not_yet_probed" || checkedAt != 0 {
					t.Fatalf("%s/%s did not start not-yet-probed (%q, %q, %d), so nothing below "+
						"can say that it moved", provider.kind, provider.key, state, probe, checkedAt)
				}
			}

			if err := harness.collector.ProbeAll(context.Background()); err != nil {
				// A probe round against a fake that answers 404 almost everywhere reports
				// what it could not reach, which is the point; it is not a test failure.
				t.Logf("the probe round reported: %v", err)
			}
			harness.fake.assertEveryPathIsRegistered()

			state, probe, checkedAt := harness.availabilityOf(t, testCase.kind, testCase.key)
			if state != testCase.wantState {
				t.Errorf("the state is %q, want %q", state, testCase.wantState)
			}
			if !strings.Contains(probe, testCase.wantProbe) {
				t.Errorf("the probe is %q, which does not name %s", probe, testCase.wantProbe)
			}
			if checkedAt == 0 {
				t.Error("the row carries no instant, so nothing can say how stale the state is")
			}
			if detail := harness.detailOf(t, testCase.kind, testCase.key); !strings.Contains(
				detail, testCase.wantDetailContains) {
				t.Errorf("the detail is %q, which does not carry %q",
					detail, testCase.wantDetailContains)
			}

			// The quantifier. EXACTLY these rows moved to that state by that probe.
			moved := harness.movedRows(t, testCase.wantState, testCase.wantProbe)
			if strings.Join(moved, ",") != strings.Join(testCase.wantMoved, ",") {
				t.Errorf("the rows that moved to %q by way of %s are %v, want exactly %v",
					testCase.wantState, testCase.wantProbe, moved, testCase.wantMoved)
			}
			if testCase.wantStateTotal > 0 {
				if total := harness.rowsInState(t, testCase.wantState); total != testCase.wantStateTotal {
					t.Errorf("%d rows in the whole registry hold %q, want %d; a pass that put one "+
						"source's conclusion on another source's row would look like this",
						total, testCase.wantState, testCase.wantStateTotal)
				}
			}

			// And no data row exists anywhere. A probe reads a state; it never ingests.
			for _, table := range []string{"flow", "security_event", "dns_resolution",
				"dhcp_lease", "pair_volume_observation", "measurement_sample", "collection_gap"} {
				if stored := countRows(t, harness.store, table); stored != 0 {
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
	harness := newProbeHarness(t)
	// Nothing is arranged, so every endpoint answers 404.
	if err := harness.collector.ProbeAll(context.Background()); err == nil {
		t.Log("the probe round reported no failure, which is acceptable: every state was recorded")
	}
	harness.fake.assertEveryPathIsRegistered()

	for _, provider := range everyProvider(t, harness.store) {
		if provider.kind == KindGeoASN {
			// Not probed in this cycle: acquiring the dataset is 4C, and no second
			// outbound destination exists here.
			continue
		}
		state, _, _ := harness.availabilityOf(t, provider.kind, provider.key)
		if state != store.StateUnavailable {
			t.Errorf("%s/%s reads %q against a firewall that answers 404 everywhere",
				provider.kind, provider.key, state)
		}
	}
	for _, kind := range []string{KindFirewallLog, KindSecurityEvent, KindFlowVolume,
		KindDHCPLease, KindDNSLookup} {
		if key := harness.activeKeyOf(t, kind); key != "" {
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
	harness := newProbeHarness(t)
	harness.fake.answerFixture(opnsense.IDSStatus, "ids_status_running.json")
	// An empty array, written here rather than as a fixture because the value is the
	// whole of what it says: the engine answered and had nothing to report.
	harness.fake.answerJSON(opnsense.FirewallLog, []any{})

	if err := harness.collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	state, _, _ := harness.availabilityOf(t, KindSecurityEvent, ProviderSuricata)
	if state != store.StateReachable {
		t.Errorf("a running engine reads %q", state)
	}
	logState, _, _ := harness.availabilityOf(t, KindFirewallLog, ProviderPf)
	if logState != store.StateReachable {
		t.Errorf("a log that returned an empty array reads %q, not reachable", logState)
	}
	detail := harness.detailOf(t, KindFirewallLog, ProviderPf)
	if !strings.Contains(detail, "not an absence of traffic") {
		t.Errorf("the detail is %q, which does not say that silence is not an absence of traffic",
			detail)
	}
}

// TestTwoBackendsTheFirewallDoesNotSeparateActivateNeitherAndRecordTheAmbiguity is
// decision 6.
//
// Provider selection reads the firewall's own configuration and never a preference order
// invented here. When two implementations of one kind are both running and both
// configured, the firewall has not said which serves clients — so opnview reads neither
// and says why, rather than choosing for the user.
func TestTwoBackendsTheFirewallDoesNotSeparateActivateNeitherAndRecordTheAmbiguity(t *testing.T) {
	harness := newProbeHarness(t)
	harness.fake.answerFixture(opnsense.KeaStatus, "kea_status_running.json")
	harness.fake.answerFixture(opnsense.KeaDHCPv4, "kea_dhcpv4_get.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	if key := harness.activeKeyOf(t, KindDHCPLease); key != "" {
		t.Fatalf("the DHCP kind activated %q although the firewall separates neither backend", key)
	}
	for _, key := range []string{ProviderKea, ProviderDnsmasq} {
		state, _, _ := harness.availabilityOf(t, KindDHCPLease, key)
		if state != store.StateReachable {
			t.Errorf("%s reads %q, and both backends are running", key, state)
		}
		detail := harness.detailOf(t, KindDHCPLease, key)
		if !strings.Contains(detail, "does not say") {
			t.Errorf("the detail for %s is %q, which does not record the ambiguity", key, detail)
		}
	}
}

// TestOneBackendTheFirewallSeparatesIsActivated is the other half: when the firewall's
// own configuration does name one, opnview reads it.
func TestOneBackendTheFirewallSeparatesIsActivated(t *testing.T) {
	harness := newProbeHarness(t)
	harness.fake.answerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	if key := harness.activeKeyOf(t, KindDHCPLease); key != ProviderDnsmasq {
		t.Fatalf("the DHCP kind activated %q, want the one backend the firewall separates", key)
	}
	state, _, _ := harness.availabilityOf(t, KindDHCPLease, ProviderKea)
	if state != store.StatePresentButDisabled {
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
	harness := newProbeHarness(t)
	harness.fake.answerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
	harness.fake.answerFixture(opnsense.UnboundSettings, "unbound_settings.json")
	harness.fake.answerFixture(opnsense.UnboundIsEnabled, "unbound_is_enabled_on.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	// log_queries is on in this fixture, so the state is detected rather than inferred.
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")

	if err := harness.collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	if key := harness.activeKeyOf(t, KindDNSLookup); key != ProviderUnbound {
		t.Fatalf("the resolver kind activated %q, want the one with a structured query API", key)
	}
	state, _, _ := harness.availabilityOf(t, KindDNSLookup, ProviderDnsmasq)
	if state != store.StatePresentButDisabled {
		t.Errorf("the Dnsmasq resolver reads %q", state)
	}
	detail := harness.detailOf(t, KindDNSLookup, ProviderDnsmasq)
	if !strings.Contains(detail, "not recorded") || !strings.Contains(detail, "does not parse") {
		t.Errorf("the detail is %q, which does not say that the grammar is unrecorded and "+
			"therefore unparsed", detail)
	}
}

// TestTheEndOfLifeISCPluginIsRecordedAsAbsentRatherThanRead is the 404 the survey
// measured. A 404 says the plugin is not installed here, which is the unavailable state
// working — not a reason to attempt its lease endpoint.
func TestTheEndOfLifeISCPluginIsRecordedAsAbsentRatherThanRead(t *testing.T) {
	harness := newProbeHarness(t)
	if err := harness.collector.ProbeAll(context.Background()); err != nil {
		t.Logf("the probe round reported: %v", err)
	}

	state, probe, _ := harness.availabilityOf(t, KindDHCPLease, ProviderISC)
	if state != store.StateUnavailable {
		t.Errorf("the end-of-life plugin reads %q", state)
	}
	if !strings.Contains(probe, opnsense.ISCStatus.Path) {
		t.Errorf("the probe is %q, which does not name the presence check", probe)
	}
	if detail := harness.detailOf(t, KindDHCPLease, ProviderISC); !strings.Contains(
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
