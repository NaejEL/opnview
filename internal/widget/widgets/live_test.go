package widgets

import (
	"encoding/json"
	"testing"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
)

// The live figures of the widget answers (C3 of the backend-completion cycle, C-AC13): every one
// carries its measure, the instant of its newest reading, the span one reading covers, its cadence
// and its bound; one older than twice its cadence reads stale_sample, its figures beside it; and an
// answer carrying one renews the live-interest lease, which the widget API does without calling the
// firewall.

// rateObjects walks a decoded answer for every chip rate: an object whose measure is sampled and
// whose state is a rate state.
func rateObjects(node any, found *[]map[string]any) {
	switch typed := node.(type) {
	case map[string]any:
		if typed["measure"] == envelope.MeasureSampled {
			if _, hasState := typed["state"]; hasState {
				*found = append(*found, typed)
			}
		}
		for _, child := range typed {
			rateObjects(child, found)
		}
	case []any:
		for _, child := range typed {
			rateObjects(child, found)
		}
	}
}

func TestLiveFieldsEveryLiveFigureCarriesItsMeasureSpanCadenceAndBound(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	interest := live.New(nil)
	f.service.SetLiveInterest(interest)
	client := f.clients[0]
	cadence := int64(10)
	// A fine sample 25 s old at a 10 s cadence: older than its cadence allows.
	for _, measure := range []vocab.Measure{vocab.MeasureRateBitsIn, vocab.MeasureRateBitsOut} {
		if err := f.db.InsertMeasurementSample(t.Context(), withParts(vocab.MeasurementSample{
			SubjectKind: vocab.SubjectInterfaceEndpoint,
			SubjectKey:  endpointKey(client.iface.device, client.address), Measure: measure,
			Unit: "bit_per_second", Value: 4000, SampledAt: f.now - 25, LiveCadenceSeconds: &cadence})); err != nil {
			t.Fatal(err)
		}
	}
	requests := []struct {
		widgetType string
		parameters any
	}{
		{"traffic_composition", map[string]any{"group_by": "client"}},
		{"owner_activity", nil},
		{"client_traffic_detail", map[string]any{"client": map[string]any{"kind": "client", "by": "mac",
			"value": "00:00:5e:00:53:00"}}},
	}
	staleSeen := false
	for _, request := range requests {
		renewals := interest.Renewals()
		response := f.respond(t, request.widgetType, request.parameters)
		if interest.Renewals() == renewals {
			t.Errorf("%s: an answer carrying a live figure did not renew the live-interest lease", request.widgetType)
		}
		encoded, err := json.Marshal(response.Data)
		check(t, err)
		var decoded any
		check(t, json.Unmarshal(encoded, &decoded))
		var rates []map[string]any
		rateObjects(decoded, &rates)
		if len(rates) == 0 {
			t.Errorf("%s: the answer carries no live figure", request.widgetType)
		}
		for _, rate := range rates {
			for _, key := range []string{"measure", "sampled_at", "span_seconds", "cadence_seconds", "lower_bound"} {
				if _, present := rate[key]; !present {
					t.Errorf("%s: a live figure lacks %s: %v", request.widgetType, key, rate)
				}
			}
			if rate["lower_bound"] != true {
				t.Errorf("%s: a live figure is not a lower bound: %v", request.widgetType, rate)
			}
			switch rate["state"] {
			case string(samples.RateNoCurrentSample):
				if rate["in_bits_per_second"] != nil || rate["cadence_seconds"] != nil {
					t.Errorf("%s: a figure with no current sample carries a value: %v", request.widgetType, rate)
				}
			case string(samples.RateStaleSample):
				staleSeen = true
				if rate["cadence_seconds"] != float64(10) || rate["span_seconds"] != float64(vocab.SampleSeconds) ||
					rate["in_bits_per_second"] == nil {
					t.Errorf("%s: a stale figure reads %v", request.widgetType, rate)
				}
			case string(samples.RateCurrent):
				if rate["cadence_seconds"] == nil || rate["span_seconds"] == nil || rate["sampled_at"] == nil {
					t.Errorf("%s: a current figure lacks its cadence, span or instant: %v", request.widgetType, rate)
				}
			default:
				t.Errorf("%s: a live figure in the state %v", request.widgetType, rate["state"])
			}
		}
	}
	if !staleSeen {
		t.Error("the fine sample older than twice its cadence was never read as stale_sample")
	}
}

func TestLiveFieldsAnAnswerWithNoLiveFigureRenewsNoLease(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	interest := live.New(nil)
	f.service.SetLiveInterest(interest)
	f.respond(t, "interface_volume_ranking", nil)
	if interest.Renewals() != 0 {
		t.Error("an answer carrying no live figure renewed the live-interest lease")
	}
}
