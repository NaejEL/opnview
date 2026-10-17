package widgets

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// AC7 and AC8: the four conditions, state unknown, and per-series degradation.

// conditionCase sets every provider of a kind to one condition.
type conditionCase struct {
	condition string
	apply     func(t *testing.T, f *fixture, kind, key string)
}

var conditionCases = []conditionCase{
	{envelope.ConditionNotYetProbed, func(t *testing.T, f *fixture, kind, key string) {
		f.setAvailability(t, kind, key, vocab.StateUnavailable, vocab.OutcomeNotYetProbed, request.ProbeNotYetRun)
	}},
	{envelope.ConditionUnavailable, func(t *testing.T, f *fixture, kind, key string) {
		f.setAvailability(t, kind, key, vocab.StateUnavailable, vocab.OutcomeAuthenticationFailed, "example probe")
	}},
	{envelope.ConditionPresentButDisabled, func(t *testing.T, f *fixture, kind, key string) {
		f.setAvailability(t, kind, key, vocab.StatePresentButDisabled, vocab.OutcomePresentButDisabled, "example probe")
	}},
	{envelope.ConditionReachable, func(t *testing.T, f *fixture, kind, key string) {
		f.setAvailability(t, kind, key, vocab.StateReachable, vocab.OutcomeHealthy, "example probe")
	}},
	{envelope.ConditionStateUnknown, func(t *testing.T, f *fixture, kind, key string) {
		id, err := f.db.ProviderID(context.Background(), kind, key)
		check(t, err)
		f.exec(t, "DELETE FROM source_availability WHERE provider_id = $1", id)
	}},
}

// emptyFixture is an installation with its discovery and no record at all.
func emptyFixture(t *testing.T) *fixture {
	t.Helper()
	database, err := store.Open(context.Background(), storetest.Database(t))
	check(t, err)
	t.Cleanup(func() { _ = database.Close() })
	f := &fixture{db: database, now: fixtureNow}
	// One person, so Per-person activity reads the source's conditions rather than its own
	// fifth one, which TestNobodyCreatedYetIsPerPersonActivitysOwnCondition covers.
	f.exec(t, "INSERT INTO owner (display_name, created_at, updated_at) VALUES ('Example Person', $1, $2)",
		f.now, f.now)
	f.allReachable(t)
	f.service, err = widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) }})
	check(t, err)
	return f
}

// TestEachConditionOfEachBoundKindYieldsItsOwnCodeAndNoZero is AC7: for every served type
// and every provider kind it binds, each of the four conditions and state unknown gives
// that kind's providers their own condition code; when the kind is the type's primary one,
// the widget's condition is that code, a degraded source with nothing stored gives no data
// at all -- never a zero -- and a reachable source with nothing stored gives reachable_no_rows
// with its measured zeros.
func TestEachConditionOfEachBoundKindYieldsItsOwnCodeAndNoZero(t *testing.T) {
	t.Parallel()
	f := emptyFixture(t)
	registered, err := request.ReadProviders(context.Background(), f.db)
	check(t, err)
	for _, widgetType := range f.service.Types() {
		preset, _ := f.service.Preset(widgetType)
		for _, kind := range preset.Providers {
			for _, item := range conditionCases {
				f.allReachable(t)
				for _, provider := range registered {
					if provider.Kind == kind {
						item.apply(t, f, kind, provider.ProviderKey)
					}
				}
				response := f.respond(t, widgetType, defaultParameters(f, widgetType))
				for _, provider := range response.Availability {
					if provider.Kind == kind && provider.Condition != item.condition {
						t.Errorf("%s, %s %s: the provider reads %q", widgetType, kind, item.condition, provider.Condition)
					}
				}
				if kind != preset.Primary || widgetType == "client_traffic_detail" {
					continue
				}
				expected := item.condition
				if expected == envelope.ConditionReachable {
					expected = envelope.ConditionReachableNoRows
				}
				if response.Condition != expected {
					t.Errorf("%s with %s %s: the widget reads %q", widgetType, kind, item.condition, response.Condition)
				}
				switch {
				case expected == envelope.ConditionReachableNoRows && response.Data == nil:
					t.Errorf("%s: reachable with no rows drew no measured zeros", widgetType)
				case expected != envelope.ConditionReachableNoRows && response.Data != nil:
					t.Errorf("%s with %s %s: a degraded source with nothing stored returned data", widgetType, kind,
						item.condition)
				}
			}
		}
	}
	f.allReachable(t)
}

// TestNobodyCreatedYetIsPerPersonActivitysOwnCondition is the preset's fifth condition:
// with no person created, the widget says so and draws nothing, whatever the traffic.
func TestNobodyCreatedYetIsPerPersonActivitysOwnCondition(t *testing.T) {
	t.Parallel()
	f := emptyFixture(t)
	f.exec(t, "DELETE FROM owner")
	response := f.respond(t, "owner_activity", nil)
	if response.Condition != envelope.ConditionNoOwnerExists || response.Data != nil {
		t.Errorf("with nobody created the widget reads %q with data %v", response.Condition, response.Data)
	}
}

// TestOneUnavailableSeriesDegradesItsOwnLineOnly is AC8: a custom chart of a traffic series
// and a DNS series, with the resolver unavailable, returns the DNS series with its own
// unavailable condition and the traffic series with data; and the traffic-over-time series
// carry their availability, coverage and gaps each.
func TestOneUnavailableSeriesDegradesItsOwnLineOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	f.setAvailability(t, "dns_lookup", "unbound", vocab.StateUnavailable, vocab.OutcomeTransportFailure, "example probe")
	f.setAvailability(t, "dns_lookup", "dnsmasq", vocab.StateUnavailable, vocab.OutcomeTransportFailure, "example probe")
	f.exec(t, "DELETE FROM dns_resolution")
	response := f.respond(t, "custom_chart", map[string]any{"series": []any{
		map[string]any{"source": "traffic", "measure": "bytes"},
		map[string]any{"source": "dns", "measure": "lookups"},
	}})
	chart := decodeData[customChartData](t, response)
	if chart.Series[0].Condition != envelope.ConditionReachable || chart.Series[0].Buckets == nil {
		t.Errorf("the traffic series reads %q", chart.Series[0].Condition)
	}
	if chart.Series[1].Condition != envelope.ConditionUnavailable || chart.Series[1].Buckets != nil {
		t.Errorf("the DNS series reads %q with buckets %v", chart.Series[1].Condition, chart.Series[1].Buckets != nil)
	}
	for _, series := range chart.Series {
		if series.Window == nil || series.Gaps == nil || len(series.Availability) == 0 {
			t.Errorf("%s carries no availability, coverage or gaps of its own", series.Key)
		}
	}
	scopes := decodeData[timeSeriesData](t, f.respond(t, "traffic_over_time_by_scope", nil))
	if len(scopes.Series) != 3 {
		t.Fatalf("%d scope series", len(scopes.Series))
	}
	for _, series := range scopes.Series {
		if series.Window == nil || series.Gaps == nil || len(series.Availability) == 0 {
			t.Errorf("the %s series carries no availability, coverage or gaps of its own", series.Key)
		}
	}
}
