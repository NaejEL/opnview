package collection_test

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/collection"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The collection surface's JSON routes (C-AC9): every figure of the collection in the JSON answer,
// and every save made, and refused, with nothing stored and nothing handed to the running service
// by a refusal.

// settingRows renders every setting row.
func settingRows(t *testing.T, h *webtest.Harness) string {
	t.Helper()
	rows, err := h.Store.DB().Query("SELECT key, value FROM setting ORDER BY key")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var builder strings.Builder
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("reading the settings: %v", err)
		}
		builder.WriteString(key + "=" + value + "\n")
	}
	return builder.String()
}

// kindPath is the save of one kind's edit.
func kindPath(kind string) string {
	return strings.Replace(collection.PathAPICollectionKind, "{kind}", kind, 1)
}

// cardPath is the save of one group of settings.
func cardPath(card string) string {
	return strings.Replace(collection.PathAPICollectionSettings, "{card}", card, 1)
}

// suggestedHarness is a signed-in harness whose filter log is reachable and holds enough
// records, spread over an hour, for a suggestion.
func suggestedHarness(t *testing.T) *webtest.Harness {
	t.Helper()
	h := collectiontest.SignedInHarness(t)
	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateReachable)
	now := h.Clock.Now().Unix()
	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60, now-minute*60+7)
	}
	collectiontest.StoreFlows(h, instants...)
	return h
}

// kindOf is one sized kind of the collection answer, failing when it is absent.
func kindOf(t *testing.T, answer map[string]any, kind string) any {
	t.Helper()
	for _, raw := range settingstest.Path(answer, "kinds").([]any) {
		if settingstest.Path(raw, "kind") == kind {
			return raw
		}
	}
	t.Fatalf("the collection answer has no kind %s", kind)
	return nil
}

// hasKind reports whether the answer lists a sized or polled kind.
func hasKind(answer map[string]any, list, kind string) bool {
	for _, raw := range settingstest.Path(answer, list).([]any) {
		if settingstest.Path(raw, "kind") == kind {
			return true
		}
	}
	return false
}

// refusal is one input refused with its code and its field.
type refusal struct {
	what      string
	path      string
	body      map[string]any
	condition string
	field     string
}

// TestEveryRefusedSaveNamesItsCodeAndFieldAndChangesNothing is C-AC9's refusals, each against the
// member that caused it.
func TestEveryRefusedSaveNamesItsCodeAndFieldAndChangesNothing(t *testing.T) {
	t.Parallel()
	for _, input := range []refusal{
		{"an interval of nought", kindPath("firewall_log"), map[string]any{"page_size": 250, "interval_seconds": 0},
			"collection_value_invalid", "interval_seconds"},
		{"an interval that is no number", kindPath("firewall_log"),
			map[string]any{"page_size": 250, "interval_seconds": "soon"}, "collection_value_invalid", "interval_seconds"},
		{"an interval of one and a half", kindPath("firewall_log"),
			map[string]any{"page_size": 800, "interval_seconds": 1.5}, "collection_value_invalid", "interval_seconds"},
		{"an interval no duration holds", kindPath("dns_lookup"),
			map[string]any{"interval_seconds": config.MaxIntervalSeconds + 1}, "collection_interval_too_large",
			"interval_seconds"},
		{"a page of nought", kindPath("firewall_log"), map[string]any{"page_size": 0, "interval_seconds": 30},
			"collection_value_invalid", "page_size"},
		{"a page that is no number", kindPath("firewall_log"), map[string]any{"page_size": "lots", "interval_seconds": 30},
			"collection_value_invalid", "page_size"},
		{"a negative page", kindPath("firewall_log"), map[string]any{"page_size": -3, "interval_seconds": 20},
			"collection_value_invalid", "page_size"},
		{"a selection that is no selection", kindPath("dhcp_lease"), map[string]any{"page_size": 600,
			"interval_seconds": 120, "selections": map[string]any{"kea": "maybe"}}, "collection_value_invalid",
			"selections.kea"},
		{"a polled kind's interval of nought", kindPath("resolver_cache"), map[string]any{"interval_seconds": 0},
			"collection_value_invalid", "interval_seconds"},
		{"a polled kind's selection that is no selection", kindPath("resolver_cache"),
			map[string]any{"interval_seconds": 60, "selections": map[string]any{"unbound": "maybe"}},
			"collection_value_invalid", "selections.unbound"},
		{"a lease failure limit of nought", cardPath("lease_backends"),
			map[string]any{config.KeyLeaseBackendFailurePassLimit: 0}, "collection_value_invalid",
			config.KeyLeaseBackendFailurePassLimit},
		{"an unknown site-name mode", cardPath("storage"), map[string]any{config.KeyRetentionSeconds: 0,
			config.KeyAggregateMode: "partial"}, "aggregate_mode_unknown", config.KeyAggregateMode},
		{"a negative pairing window", cardPath("pairing"), map[string]any{config.KeyLegPairingWindow: -1},
			"value_out_of_range", config.KeyLegPairingWindow},
		{"an unknown kind", kindPath("no_such_kind"), map[string]any{"interval_seconds": 30},
			"collection_kind_unknown", "kind"},
		{"an unknown card", cardPath("no_such_card"), map[string]any{}, "collection_kind_unknown", "card"},
	} {
		t.Run(input.what, func(t *testing.T) {
			t.Parallel()
			h := collectiontest.SignedInHarness(t)
			before := settingRows(t, h)
			status, answer := settingstest.SendJSON(h, input.path, input.body)
			if status != http.StatusBadRequest || answer["condition"] != input.condition {
				t.Errorf("answered %d %v rather than refusing %s", status, answer, input.condition)
			}
			if field, _ := answer["field"].(string); field != input.field {
				t.Errorf("the refusal names %q rather than %q", field, input.field)
			}
			if settingRows(t, h) != before {
				t.Error("a refused input changed a row")
			}
			if len(h.Collector.Received()) != 0 || len(h.Collector.ReselectedKinds()) != 0 {
				t.Error("a refused input reached the running collector")
			}
		})
	}
}

// TestASavedPairIsStoredAndReachesTheRunningServiceWithoutARestart: a save writes the two rows,
// and the live holder and the running collector have the new pair as soon as it answers.
func TestASavedPairIsStoredAndReachesTheRunningServiceWithoutARestart(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	status, answer := settingstest.SendJSON(h, kindPath("security_event"),
		map[string]any{"page_size": 1200, "interval_seconds": 45})
	if status != http.StatusOK {
		t.Fatalf("saving a pair answered %d %v", status, answer)
	}
	if collectiontest.Setting(h, "page_size_security_event") != "1200" ||
		collectiontest.Setting(h, "poll_interval_security_event_seconds") != "45" {
		t.Error("the pair was not stored under its two rows")
	}
	live := h.Live.Get()
	if live.PageSize("security_event") != 1200 || live.Interval("security_event", "poll") != 45*time.Second {
		t.Errorf("the live configuration holds %d records every %s after the save",
			live.PageSize("security_event"), live.Interval("security_event", "poll"))
	}
	received := h.Collector.Received()
	if len(received) != 1 || received[0].PageSize("security_event") != 1200 ||
		received[0].Interval("security_event", "poll") != 45*time.Second {
		t.Errorf("the running collector was handed %d configurations without the saved pair", len(received))
	}
	// The answer is every figure as it now stands.
	inForce := settingstest.Path(kindOf(t, answer, "security_event"), "in_force")
	if settingstest.Path(inForce, "page_size") != float64(1200) || settingstest.Path(inForce, "interval_seconds") != float64(45) {
		t.Errorf("the save's answer reports %v in force", inForce)
	}

	// The largest interval a duration holds is accepted.
	largest := config.MaxIntervalSeconds
	if status, answer := settingstest.SendJSON(h, kindPath("dns_lookup"), map[string]any{"interval_seconds": largest}); status != http.StatusOK {
		t.Fatalf("the largest interval a duration holds answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, "poll_interval_dns_lookup_seconds"); got != strconv.FormatInt(largest, 10) {
		t.Errorf("the stored interval is %q, want %d", got, largest)
	}
}

// TestAPageSizeAboveTheCeilingIsStoredAndReported: a page above the firewall's ceiling is stored,
// and answered as above it.
func TestAPageSizeAboveTheCeilingIsStoredAndReported(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	above := ceilingOf("security_event") + 1
	status, answer := settingstest.SendJSON(h, kindPath("security_event"),
		map[string]any{"page_size": above, "interval_seconds": 60})
	if status != http.StatusOK {
		t.Fatalf("a page above the ceiling answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, "page_size_security_event"); got != strconv.Itoa(above) {
		t.Errorf("the stored page size is %q, want %d", got, above)
	}
	inForce := settingstest.Path(kindOf(t, answer, "security_event"), "in_force")
	if settingstest.Path(inForce, "page_size") != float64(above) ||
		settingstest.Path(inForce, "page_state") != web.ConditionOf(msgPageAboveCeiling) {
		t.Errorf("the page in force is answered %v, not above the ceiling", inForce)
	}
}

// TestASavedSelectionIsStoredAndAppliedAtOnce: a save stores each selection sent and hands the
// kind to the running collector; a save that sends none leaves them as they are.
func TestASavedSelectionIsStoredAndAppliedAtOnce(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	status, answer := settingstest.SendJSON(h, kindPath("dhcp_lease"), map[string]any{"page_size": 500,
		"interval_seconds": 300, "selections": map[string]any{"kea": "on", "isc": "off", "dnsmasq": "auto"}})
	if status != http.StatusOK {
		t.Fatalf("saving a selection answered %d %v", status, answer)
	}
	for key, want := range map[string]string{"kea": "on", "isc": "off", "dnsmasq": "auto"} {
		if got := collectiontest.Setting(h, config.KeySourceSelection("dhcp_lease", key)); got != want {
			t.Errorf("the stored selection of %s is %q, want %q", key, got, want)
		}
	}
	if got := h.Collector.ReselectedKinds(); len(got) != 1 || got[0] != "dhcp_lease" {
		t.Errorf("the running collector applied the selections of %v, want [dhcp_lease]", got)
	}
	lease := kindOf(t, answer, "dhcp_lease")
	if settingstest.Path(lease, "source_state", "selection") != "on" {
		t.Errorf("the kind's selection is answered %v, not on", settingstest.Path(lease, "source_state"))
	}
	for _, implementation := range settingstest.Path(lease, "implementations").([]any) {
		if settingstest.Path(implementation, "provider_key") == "kea" && settingstest.Path(implementation, "selection") != "on" {
			t.Errorf("kea's selection is answered %v", implementation)
		}
	}

	if status, answer := settingstest.SendJSON(h, kindPath("dhcp_lease"),
		map[string]any{"page_size": 500, "interval_seconds": 300}); status != http.StatusOK {
		t.Fatalf("saving with no selection answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, config.KeySourceSelection("dhcp_lease", "kea")); got != "on" {
		t.Errorf("a save that sent no selection changed one to %q", got)
	}
}

// TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout: the next purge deletes what is older
// than the new horizon, so a shorter one stores nothing until the save confirms that very horizon;
// the confirmation states both horizons and every other change the save applies.
func TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	path := cardPath("storage")
	inForce := collectiontest.Setting(h, config.KeyRetentionSeconds)
	before := settingRows(t, h)
	status, answer := settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 86400,
		config.KeyAggregateMode: "no_domains"})
	if status != http.StatusConflict || answer["condition"] != collection.ConditionRetentionConfirmationRequired ||
		answer["requested_retention_seconds"] != float64(86400) ||
		strconv.FormatFloat(answer["in_force_retention_seconds"].(float64), 'f', 0, 64) != inForce {
		t.Fatalf("a shorter horizon answered %d %v rather than asking for its confirmation", status, answer)
	}
	changes, _ := answer["changes"].([]any)
	if len(changes) != 1 || settingstest.Path(changes[0], "key") != config.KeyAggregateMode ||
		settingstest.Path(changes[0], "in_force") != "full" || settingstest.Path(changes[0], "requested") != "no_domains" {
		t.Errorf("the confirmation lists %v, not the site-name mode the same save changes", changes)
	}
	if settingRows(t, h) != before {
		t.Fatal("a horizon waiting for its confirmation was stored")
	}
	// THE CONFIRMATION IS FOR THE HORIZON IT SHOWED. Another one is asked about again.
	status, answer = settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 7000000,
		config.KeyAggregateMode: "full", "confirm": 86400})
	if status != http.StatusConflict || answer["requested_retention_seconds"] != float64(7000000) {
		t.Errorf("a confirmation of another horizon answered %d %v rather than asking again", status, answer)
	}
	status, answer = settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 86400,
		config.KeyAggregateMode: "full", "confirm": 86400})
	if status != http.StatusOK || collectiontest.Setting(h, config.KeyRetentionSeconds) != "86400" {
		t.Errorf("the confirmed horizon answered %d %v and stored %q", status, answer,
			collectiontest.Setting(h, config.KeyRetentionSeconds))
	}
	// A longer one, or unlimited, purges nothing more and needs no confirmation.
	status, answer = settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 0,
		config.KeyAggregateMode: "full"})
	if status != http.StatusOK || collectiontest.Setting(h, config.KeyRetentionSeconds) != "0" {
		t.Errorf("an unlimited retention answered %d %v and was not stored", status, answer)
	}
}

// TestTheCollectionAnswerReportsEveryFigureItMeasures: every figure the measurement carries is in
// the answer, in the form the measurement gives it, and a figure with no answer is null beside its
// named state.
func TestTheCollectionAnswerReportsEveryFigureItMeasures(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	now := h.Clock.Now().Unix()
	collectiontest.RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, now-900, now-840)
	collectiontest.RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, now-600, now-570)

	kind, _ := collection.KindNamed("firewall_log")
	measured, err := h.Store.MeasureRecordRate(context.Background(), kind.Spec,
		sizing.Window(h.Clock.Now(), config.DefaultRetentionSeconds), sizing.BucketSeconds)
	if err != nil {
		t.Fatalf("measuring the filter log: %v", err)
	}
	if !measured.HasRecords || !measured.MeanRate.Known || !measured.IngestedMeanRate.Known {
		t.Fatal("the stored records do not make a measurement with every figure, so this assertion is vacuous")
	}

	answer := settingstest.GetJSON(h, collection.PathAPICollection)
	log := kindOf(t, answer, "firewall_log")
	rate := func(r attribution.Rate) float64 { return r.PerSecond }
	for _, row := range []struct {
		path []any
		want any
	}{
		{[]any{"measurement", "window_start_at"}, float64(now - sizing.NominalWindowSeconds)},
		{[]any{"measurement", "window_end_at"}, float64(now)},
		{[]any{"measurement", "covered_from_at"}, float64(now - 3600)},
		{[]any{"measurement", "covered_to_at"}, float64(now - 60 + 7)},
		{[]any{"measurement", "record_count"}, float64(120)},
		{[]any{"measurement", "mean_rate", "per_second"}, rate(measured.MeanRate)},
		{[]any{"measurement", "ingested_mean_rate", "per_second"}, rate(measured.IngestedMeanRate)},
		{[]any{"measurement", "peak_rate", "per_second"}, rate(measured.PeakRate)},
		{[]any{"measurement", "gap_count"}, float64(2)},
		{[]any{"measurement", "missed_seconds"}, float64(90)},
		{[]any{"measurement", "retention_seconds"}, float64(config.DefaultRetentionSeconds)},
		{[]any{"in_force", "page_size"}, float64(config.Defaults().PageSize("firewall_log"))},
		{[]any{"in_force", "interval_seconds"}, float64(config.Defaults().Interval("firewall_log", "poll") / time.Second)},
		{[]any{"in_force", "page_ceiling"}, float64(ceilingOf("firewall_log"))},
		{[]any{"in_force", "page_state"}, web.ConditionOf(msgPageWithinCeiling)},
		{[]any{"suggestion", "state"}, web.ConditionOf(msgSuggestionAvailable)},
		{[]any{"source_state", "availability"}, web.ConditionOf(msgAvailabilityHealthy)},
		{[]any{"source_state", "selection"}, "auto"},
	} {
		if got := settingstest.Path(log, row.path...); got != row.want {
			t.Errorf("%v is %#v, want %#v", row.path, got, row.want)
		}
	}
	gaps, _ := settingstest.Path(log, "measurement", "gaps").([]any)
	if len(gaps) != 1 || settingstest.Path(gaps[0], "reason") != string(vocab.GapDigestOutsideWindow) ||
		settingstest.Path(gaps[0], "count") != float64(2) || settingstest.Path(gaps[0], "missed_seconds") != float64(90) {
		t.Errorf("the gaps of the window are answered %v", gaps)
	}
	if settingstest.Path(log, "suggestion", "interval_seconds") == nil {
		t.Error("a suggestion was made and no suggested interval is answered")
	}

	// A table with no ingested clock, and a window with no record, are states, never noughts.
	lease := kindOf(t, answer, "dhcp_lease")
	for _, row := range []struct {
		path []any
		want any
	}{
		{[]any{"measurement", "ingested_mean_rate", "state"}, web.ConditionOf(msgNoIngestedClock)},
		{[]any{"measurement", "ingested_mean_rate", "per_second"}, nil},
		{[]any{"measurement", "mean_rate", "state"}, web.ConditionOf(msgRateNotMeasured)},
		{[]any{"measurement", "covered_from_at"}, nil},
		{[]any{"measurement", "covered_to_at"}, nil},
		{[]any{"in_force", "page_ceiling"}, nil},
		{[]any{"in_force", "page_state"}, nil},
	} {
		if got := settingstest.Path(lease, row.path...); got != row.want {
			t.Errorf("the lease kind's %v is %#v, want %#v", row.path, got, row.want)
		}
	}
}

// TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion: records on the table do not make
// a suggestion for a kind nobody reads.
func TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateUnavailable)
	log := kindOf(t, settingstest.GetJSON(h, collection.PathAPICollection), "firewall_log")
	if settingstest.Path(log, "source_state", "availability") != web.ConditionOf(msgAvailabilityUnavailable) ||
		settingstest.Path(log, "suggestion", "state") != web.ConditionOf(msgSuggestionSourceNotCollected) ||
		settingstest.Path(log, "suggestion", "interval_seconds") != nil {
		t.Errorf("an unreachable source is answered %v", log)
	}

	// Reachable but turned off by the operator: the same outcome, and the selection is a fact of
	// its own.
	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateReachable)
	collectiontest.SetSelection(h, "firewall_log", "pf", config.SelectionOff)
	log = kindOf(t, settingstest.GetJSON(h, collection.PathAPICollection), "firewall_log")
	if settingstest.Path(log, "source_state", "availability") != web.ConditionOf(msgAvailabilityHealthy) ||
		settingstest.Path(log, "source_state", "selection") != "off" ||
		settingstest.Path(log, "suggestion", "state") != web.ConditionOf(msgSuggestionSourceNotCollected) {
		t.Errorf("a source turned off is answered %v", log)
	}
}

// TestAKindWithNoProviderRowIsNotAnswered: the kinds are those the provider table holds, read at
// run time, and a kind the registry does not hold takes no save.
func TestAKindWithNoProviderRowIsNotAnswered(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	db := h.Store.DB()
	for _, kind := range []string{"security_event", "resolver_cache"} {
		for _, statement := range []string{
			`DELETE FROM source_availability WHERE provider_id IN (SELECT id FROM provider WHERE kind = '` + kind + `')`,
			`DELETE FROM provider WHERE kind = '` + kind + `'`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("removing the %s registry rows: %v", kind, err)
			}
		}
	}
	answer := settingstest.GetJSON(h, collection.PathAPICollection)
	if hasKind(answer, "kinds", "security_event") || hasKind(answer, "polled_kinds", "resolver_cache") {
		t.Error("a kind with no provider row is still answered")
	}
	for _, kind := range []string{"firewall_log", "dhcp_lease", "dns_lookup"} {
		kindOf(t, answer, kind)
	}
	if !hasKind(answer, "polled_kinds", "resolver_local_data") {
		t.Error("removing one kind's rows removed the other polled kind")
	}
	if status, refused := settingstest.SendJSON(h, kindPath("resolver_cache"), map[string]any{"interval_seconds": 90}); status != http.StatusBadRequest {
		t.Errorf("saving a kind the registry does not hold answered %d %v", status, refused)
	}
}

// TestEachImplementationIsAnsweredWithItsSelection: every implementation of a kind is answered
// with the selection stored, and no implementation of another kind.
func TestEachImplementationIsAnsweredWithItsSelection(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	collectiontest.SetSelection(h, "dhcp_lease", "kea", config.SelectionOn)
	lease := kindOf(t, settingstest.GetJSON(h, collection.PathAPICollection), "dhcp_lease")
	seen := map[string]any{}
	for _, implementation := range settingstest.Path(lease, "implementations").([]any) {
		seen[settingstest.Path(implementation, "provider_key").(string)] = settingstest.Path(implementation, "selection")
	}
	want := map[string]any{"dnsmasq": "auto", "isc": "auto", "kea": "on"}
	if len(seen) != len(want) {
		t.Errorf("the lease kind answers the implementations %v", seen)
	}
	for key, selection := range want {
		if seen[key] != selection {
			t.Errorf("the selection of %s is answered %v, want %v", key, seen[key], selection)
		}
	}
}

// TestAnsweringAndMeasuringWriteNothingAndContactNothing: the collection is answered and measured
// from the database without changing a byte of it, and nothing calls the firewall. The session row
// is left out: it records the reader's activity, which every signed-in request moves.
func TestAnsweringAndMeasuringWriteNothingAndContactNothing(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	before := webtest.DatabaseBytesExcept(t, h, "session")
	calls := len(h.Fake.Requests())
	settingstest.GetJSON(h, collection.PathAPICollection)
	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("answering or measuring the collection wrote to the database")
	}
	settingstest.SendJSON(h, kindPath("firewall_log"), map[string]any{"page_size": 600, "interval_seconds": 15})
	settingstest.SendJSON(h, kindPath("firewall_log"), map[string]any{"page_size": "x", "interval_seconds": 15})
	if after := len(h.Fake.Requests()); after != calls {
		t.Errorf("the collection made %d calls to the firewall", after-calls)
	}
}

// TestTheLowerBoundCaveatIsDocumented: the record rate counts what opnview stored, so it is a
// lower bound of what the source produced. That is written in docs/data-model.md; the front prints
// it nowhere.
func TestTheLowerBoundCaveatIsDocumented(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../../docs/data-model.md")
	if err != nil {
		t.Fatalf("reading docs/data-model.md: %v", err)
	}
	document := string(raw)
	start := strings.Index(document, "\n## Record rate\n")
	if start < 0 {
		t.Fatal("docs/data-model.md has no Record rate section")
	}
	section := document[start+1:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	if !strings.Contains(strings.ToLower(section), "lower bound") {
		t.Error("the Record rate section does not say the rate is a lower bound")
	}
}

// ceilingOf is the page ceiling a sized kind's file declares.
func ceilingOf(kind string) int {
	sized, _ := sizing.Named(kind)
	return sized.PageCeiling
}
