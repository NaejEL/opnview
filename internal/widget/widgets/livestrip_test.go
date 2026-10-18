package widgets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget"
)

// The rolling strip, served by the live_strip widget plugin (PA13 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md, PA-AC21; C3 of the backend-completion cycle, C-AC12 and
// C-AC13). Its answer's data equals, byte for byte, what the Go strip it replaced answered over the same
// worlds (plugins/live_strip/testdata/go-strip, recorded from internal/live/strip before it was removed);
// and every assertion of that package's tests holds through the widget: every live figure names its
// measure, span, cadence and bound; each decision series carries complete_until and the buckets after it
// are gaps, never zeros; the buckets sum to the fixture's decisions; logged and sampled figures are never
// summed.

// stripAt is the instant the strip is drawn at: a multiple of every cadence the tests use.
const stripAt int64 = 1790000400

type stripWorld struct {
	database *store.Store
	ctx      context.Context
}

func newStripWorld(t *testing.T) *stripWorld {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	w := &stripWorld{database: database, ctx: ctx}
	w.exec(t, `INSERT INTO interface (id, identifier, device, description, link_type, link_kind, first_seen_at,
		last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', $1, $2)`, stripAt-3600, stripAt-3600)
	w.exec(t, `INSERT INTO interface_network (interface_id, network_address, prefix_length, address_family, origin,
		first_detected_at, last_detected_at, updated_at) VALUES (1, '192.0.2.0', 24, 4, 'detected', $1, $2, $3)`,
		stripAt-3600, stripAt-3600, stripAt-3600)
	return w
}

func (w *stripWorld) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := w.database.DB().ExecContext(w.ctx, statement, arguments...); err != nil {
		t.Fatalf("%v\n%s", err, statement)
	}
}

// healthy makes a kind's provider active, its last pass healthy at checkedAt.
func (w *stripWorld) healthy(t *testing.T, kind, key string, checkedAt int64) {
	t.Helper()
	w.exec(t, "UPDATE provider SET is_active = true WHERE kind = $1 AND provider_key = $2", kind, key)
	id, err := w.database.ProviderID(w.ctx, kind, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.database.SetAvailability(w.ctx, id, vocab.StateReachable, vocab.OutcomeHealthy, "GET /", nil,
		checkedAt); err != nil {
		t.Fatal(err)
	}
}

// flow stores one logged connection.
func (w *stripWorld) flow(t *testing.T, id, observedAt int64, action string, reason any) {
	t.Helper()
	w.exec(t, `INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state,
		src_interface_id, src_address, dst_address, protocol, ip_version, action, direction, packet_bytes,
		rule_lookup_state, traffic_scope, log_reason) VALUES ($1, $2, $3, $4, 'vlan0.10', 'resolved', 1, '192.0.2.10',
		'203.0.113.7', 'tcp', 4, $5, 'in', 100, 'pending', 'north_south', $6)`,
		id, "d"+time.Unix(id, 0).Format("150405"), observedAt, observedAt, action, reason)
}

// refusal stores one lookup a resolver refused.
func (w *stripWorld) refusal(t *testing.T, key string, lookedUpAt int64) {
	t.Helper()
	w.exec(t, `INSERT INTO dns_resolution (lookup_key, client_address, domain, resolver, action, looked_up_at,
		ingested_at) VALUES ($1, '192.0.2.10', 'example.org', 'unbound', 'block', $2, $3)`, key, lookedUpAt,
		lookedUpAt)
}

// sample stores one rate reading of one inside address with one outside peer.
func (w *stripWorld) sample(t *testing.T, sampledAt int64, cadence *int64, in float64) {
	t.Helper()
	providerID, err := w.database.ProviderID(w.ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	device, local, peer := "vlan0.10", "192.0.2.10", "203.0.113.7"
	for _, reading := range []vocab.MeasurementSample{
		{SubjectKind: vocab.SubjectInterfaceEndpoint, SubjectKey: endpointKey(device, local), Measure: vocab.MeasureRateBitsIn,
			Value: in},
		{SubjectKind: vocab.SubjectInterfaceEndpoint, SubjectKey: endpointKey(device, local), Measure: vocab.MeasureRateBitsOut,
			Value: 2 * in},
		{SubjectKind: vocab.SubjectInterfaceEndpointPair, SubjectKey: pairKey(device, local, peer),
			Measure: vocab.MeasureRateBitsIn, Value: in},
	} {
		reading.ProviderID, reading.Unit, reading.SampledAt, reading.LiveCadenceSeconds = &providerID,
			"bit_per_second", sampledAt, cadence
		if err := w.database.InsertMeasurementSample(w.ctx, withParts(reading)); err != nil {
			t.Fatal(err)
		}
	}
}

// liveCadence sets the insight plugin's live cadence: the cadence the strip is bucketed at, the built-in
// plugins' finest live pass.
func (w *stripWorld) liveCadence(t *testing.T, seconds int64) {
	t.Helper()
	if err := w.database.SetSetting(w.ctx, "insight_live_cadence_seconds", strconv.FormatInt(seconds, 10),
		stripAt-3600); err != nil {
		t.Fatal(err)
	}
}

// strip answers the live_strip widget, and returns its answer and its data as served.
func (w *stripWorld) strip(t *testing.T, window int64, interest *live.Interest) (stripAnswer, []byte) {
	t.Helper()
	service, err := widget.New(w.database, widget.Options{Now: func() time.Time { return time.Unix(stripAt, 0) },
		LiveInterest: interest})
	if err != nil {
		t.Fatal(err)
	}
	parameters := ""
	if window != 0 {
		parameters = `{"window_seconds":` + strconv.FormatInt(window, 10) + `}`
	}
	response, err := service.Respond(w.ctx, "live_strip", parameters)
	if err != nil {
		t.Fatalf("drawing the strip: %v", err)
	}
	encoded, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	var answer stripAnswer
	if err := json.Unmarshal(encoded, &answer); err != nil {
		t.Fatalf("the strip's data is not the strip: %v: %s", err, encoded)
	}
	answer.states = response.States
	for _, invalid := range response.InvalidParameters {
		answer.invalid = append(answer.invalid, invalid.Path)
	}
	return answer, encoded
}

// The strip's data, as the Go strip's types named it.
type stripAnswer struct {
	CadenceSeconds    int64                    `json:"cadence_seconds"`
	Window            struct{ From, To int64 } `json:"window"`
	LiveInterestUntil *int64                   `json:"live_interest_until"`
	Throughput        []stripThroughput        `json:"throughput"`
	Decisions         []stripDecisions         `json:"decisions"`
	states            []string
	invalid           []string
}

type stripThroughput struct {
	Key            string `json:"key"`
	Measure        string `json:"measure"`
	SpanSeconds    int64  `json:"span_seconds"`
	CadenceSeconds int64  `json:"cadence_seconds"`
	LowerBound     bool   `json:"lower_bound"`
	Buckets        []struct {
		From, To int64
		Gap      bool     `json:"gap"`
		Value    *float64 `json:"value"`
		Samples  int      `json:"samples"`
	} `json:"buckets"`
}

type stripDecisions struct {
	Key           string `json:"key"`
	Measure       string `json:"measure"`
	CompleteUntil *int64 `json:"complete_until"`
	LowerBound    bool   `json:"lower_bound"`
	Buckets       []struct {
		From, To int64
		Gap      bool   `json:"gap"`
		Value    *int64 `json:"value"`
	} `json:"buckets"`
}

// decisionWorld is the world of the decisions: seven logged connections, two refused lookups, both
// kinds' last pass healthy 20 s ago.
func decisionWorld(t *testing.T) *stripWorld {
	w := newStripWorld(t)
	w.flow(t, 1, stripAt-100, "pass", nil)
	w.flow(t, 2, stripAt-95, "pass", nil)
	w.flow(t, 3, stripAt-81, "pass", nil)
	w.flow(t, 4, stripAt-50, "block", "match")
	w.flow(t, 5, stripAt-49, "block", "match")
	w.flow(t, 6, stripAt-45, "block", "state-mismatch")
	w.flow(t, 7, stripAt-44, "reject", nil)
	w.refusal(t, "k1", stripAt-30)
	w.refusal(t, "k2", stripAt-28)
	w.healthy(t, "firewall_log", "pf", stripAt-20)
	w.healthy(t, "dns_lookup", "unbound", stripAt-20)
	return w
}

// goldenStrip reads one recorded answer of the Go strip.
func goldenStrip(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", "live_strip", "testdata", "go-strip", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestTheLiveStripWidgetAnswersByteForByteAsTheGoStrip is PA-AC21: over each world the Go strip was
// recorded on, the widget's data is the recorded answer, byte for byte.
func TestTheLiveStripWidgetAnswersByteForByteAsTheGoStrip(t *testing.T) {
	t.Parallel()
	check := func(name string, encoded []byte) {
		t.Helper()
		if want := goldenStrip(t, name); string(encoded) != string(want) {
			t.Errorf("%s: the widget answers\n%s\nwant\n%s", name, encoded, want)
		}
	}
	{
		w := decisionWorld(t)
		_, encoded := w.strip(t, 120, nil)
		check("decisions", encoded)
		pf, err := w.database.ProviderID(w.ctx, "firewall_log", "pf")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.database.RecordCollectionGap(w.ctx, vocab.CollectionGap{ProviderID: pf, IntervalStartAt: stripAt - 75,
			IntervalEndAt: stripAt - 62, Reason: vocab.GapDigestOutsideWindow, DetectedAt: stripAt - 60}); err != nil {
			t.Fatal(err)
		}
		w.exec(t, `INSERT INTO opnview_downtime (interval_start_at, interval_end_at, clean_stop, detected_at)
			VALUES ($1, $2, false, $3)`, stripAt-115, stripAt-104, stripAt-104)
		_, encoded = w.strip(t, 120, nil)
		check("decisions_with_gaps", encoded)
	}
	{
		w := newStripWorld(t)
		w.flow(t, 1, stripAt-100, "pass", nil)
		_, encoded := w.strip(t, 120, nil)
		check("no_healthy_pass", encoded)
	}
	{
		w := newStripWorld(t)
		cadence := int64(10)
		w.sample(t, stripAt-40, &cadence, 1000)
		w.sample(t, stripAt-30, &cadence, 3000)
		w.sample(t, stripAt-29, &cadence, 1500)
		_, encoded := w.strip(t, 60, live.New(nil))
		check("throughput", encoded)
	}
	{
		w := newStripWorld(t)
		w.liveCadence(t, 300)
		cadence := int64(10)
		for instant := stripAt - 3000; instant < stripAt; instant += 300 {
			w.sample(t, instant, nil, 100)
		}
		for instant := stripAt - 1500; instant < stripAt-1200; instant += 10 {
			w.sample(t, instant, &cadence, 500)
		}
		_, encoded := w.strip(t, 3000, nil)
		check("live_and_idle", encoded)
	}
}

func TestLiveFieldsAndStripDecisionsSumToTheRecordsAndBucketsPastCompleteUntilAreGaps(t *testing.T) {
	t.Parallel()
	strip, _ := decisionWorld(t).strip(t, 120, nil)
	if strip.CadenceSeconds != 10 || strip.Window.From != stripAt-120 || strip.Window.To != stripAt+10 {
		t.Fatalf("the strip is %+v over %+v", strip.CadenceSeconds, strip.Window)
	}
	want := map[string]int64{"allowed": 3, "firewall_rule": 2, "firewall_no_rule": 1,
		"firewall_reason_not_recorded": 1, "dns_refusal": 2}
	total := int64(0)
	for _, series := range strip.Decisions {
		if series.CompleteUntil == nil || *series.CompleteUntil != stripAt-20 {
			t.Errorf("%s: complete_until is %v, want the last healthy pass", series.Key, series.CompleteUntil)
		}
		if !series.LowerBound || series.Measure == "" {
			t.Errorf("%s: names no measure or no bound", series.Key)
		}
		sum := int64(0)
		for _, bucket := range series.Buckets {
			if bucket.To > stripAt-20 && !bucket.Gap {
				t.Errorf("%s: the bucket [%d, %d) ends after complete_until and is not a gap", series.Key,
					bucket.From, bucket.To)
			}
			if bucket.Gap && bucket.Value != nil && *bucket.Value == 0 {
				t.Errorf("%s: a gap reads a zero", series.Key)
			}
			if bucket.Value != nil {
				sum += *bucket.Value
			}
		}
		if sum != want[series.Key] {
			t.Errorf("%s: the buckets sum to %d, want %d", series.Key, sum, want[series.Key])
		}
		total += sum
	}
	if total != 9 {
		t.Errorf("the decisions sum to %d, want the fixture's 9", total)
	}
	// A covered bucket with no record of a series is a measured zero.
	for _, series := range strip.Decisions {
		if series.Key != "allowed" {
			continue
		}
		for _, bucket := range series.Buckets {
			if bucket.From == stripAt-70 && (bucket.Gap || bucket.Value == nil || *bucket.Value != 0) {
				t.Errorf("the covered bucket [%d, %d) with no allowed record reads %+v", bucket.From, bucket.To, bucket)
			}
		}
	}
}

func TestLiveFieldsAndStripWithNoHealthyPassEveryDecisionBucketIsAGap(t *testing.T) {
	t.Parallel()
	w := newStripWorld(t)
	w.flow(t, 1, stripAt-100, "pass", nil)
	strip, _ := w.strip(t, 120, nil)
	for _, series := range strip.Decisions {
		if series.CompleteUntil != nil {
			t.Errorf("%s: complete_until is set with no healthy pass recorded", series.Key)
		}
		for _, bucket := range series.Buckets {
			if !bucket.Gap {
				t.Errorf("%s: the bucket [%d, %d) is not a gap", series.Key, bucket.From, bucket.To)
			}
			if bucket.Value != nil && *bucket.Value == 0 {
				t.Errorf("%s: a gap reads a zero", series.Key)
			}
		}
	}
}

func TestLiveFieldsAndStripThroughputIsSampledPerBucketAndAGapWithoutASample(t *testing.T) {
	t.Parallel()
	w := newStripWorld(t)
	cadence := int64(10)
	w.sample(t, stripAt-40, &cadence, 1000)
	w.sample(t, stripAt-30, &cadence, 3000)
	interest := live.New(nil)
	strip, _ := w.strip(t, 60, interest)
	if !interest.Held(time.Unix(stripAt, 0)) || strip.LiveInterestUntil == nil {
		t.Error("drawing the strip did not renew the live-interest lease")
	}
	if len(strip.Throughput) != 4 {
		t.Fatalf("%d throughput series", len(strip.Throughput))
	}
	for _, series := range strip.Throughput {
		if series.Measure != "sampled" || series.SpanSeconds != vocab.SampleSeconds || series.CadenceSeconds != 10 ||
			!series.LowerBound {
			t.Errorf("%s: carries measure %q, span %d, cadence %d, bound %v", series.Key, series.Measure,
				series.SpanSeconds, series.CadenceSeconds, series.LowerBound)
		}
		for _, bucket := range series.Buckets {
			sampled := bucket.From == stripAt-40 || bucket.From == stripAt-30
			if sampled == bucket.Gap {
				t.Errorf("%s: the bucket [%d, %d) is gap=%v with %d samples", series.Key, bucket.From, bucket.To,
					bucket.Gap, bucket.Samples)
			}
			if bucket.Gap && bucket.Value != nil {
				t.Errorf("%s: a bucket with no sample carries a value", series.Key)
			}
		}
	}
	// Inbound is the peer's figure; outbound the address's total to its one outside peer.
	for _, series := range strip.Throughput {
		for _, bucket := range series.Buckets {
			if bucket.From != stripAt-30 || bucket.Value == nil {
				continue
			}
			want := map[string]float64{"inbound": 3000, "outbound": 6000, "inter_interface": 0,
				"outbound_unsplit": 0}[series.Key]
			if *bucket.Value != want {
				t.Errorf("%s at %d reads %v, want %v", series.Key, bucket.From, *bucket.Value, want)
			}
		}
	}
}

func TestLivePersistenceAWindowAcrossALiveAndAnIdlePeriodHasASampleInEveryBaselineBucket(t *testing.T) {
	t.Parallel()
	w := newStripWorld(t)
	w.liveCadence(t, 300)
	cadence := int64(10)
	// Baseline samples every 300 s; a live period of fine samples in the middle.
	for instant := stripAt - 3000; instant < stripAt; instant += 300 {
		w.sample(t, instant, nil, 100)
	}
	for instant := stripAt - 1500; instant < stripAt-1200; instant += 10 {
		w.sample(t, instant, &cadence, 500)
	}
	strip, _ := w.strip(t, 3000, nil)
	if strip.CadenceSeconds != 300 {
		t.Fatalf("the strip is bucketed at %d s, not the live cadence of 300 s", strip.CadenceSeconds)
	}
	for _, series := range strip.Throughput {
		for _, bucket := range series.Buckets {
			if bucket.To <= stripAt && bucket.From >= stripAt-3000 && bucket.Gap {
				t.Errorf("%s: the baseline bucket [%d, %d) holds no sample", series.Key, bucket.From, bucket.To)
			}
		}
	}
}

// TestLiveFieldsAndStripAWindowOutOfRangeIsDrawnAtTheNearestBoundAndSaid: a window the cadence does not
// allow -- under two cadences, or more buckets than a strip holds -- is drawn at the nearest bound, and
// the answer names the state window_out_of_range; one over an hour is outside what the manifest
// declares, reported invalid and drawn at the default, as every widget parameter is; the default window
// is in range.
func TestLiveFieldsAndStripAWindowOutOfRangeIsDrawnAtTheNearestBoundAndSaid(t *testing.T) {
	t.Parallel()
	w := newStripWorld(t)
	// A reachable filter log with nothing stored: the strip is drawn, its buckets measured zeros or gaps.
	w.healthy(t, "firewall_log", "pf", stripAt-20)
	strip, _ := w.strip(t, 19, nil)
	if drawn := strip.Window.To - strip.Window.From; len(strip.states) != 1 || strip.states[0] != "window_out_of_range" ||
		drawn < 20 || drawn > 30 {
		t.Errorf("a window of 19 s at a 10 s cadence is drawn over %d s with the states %v", drawn, strip.states)
	}
	w.liveCadence(t, 2)
	strip, _ = w.strip(t, 3000, nil)
	if drawn := strip.Window.To - strip.Window.From; len(strip.states) != 1 || strip.states[0] != "window_out_of_range" ||
		drawn < 2000 || drawn > 2002 {
		t.Errorf("a window of 3000 s at a 2 s cadence is drawn over %d s with the states %v", drawn, strip.states)
	}
	w.liveCadence(t, 10)
	strip, _ = w.strip(t, 3601, nil)
	if len(strip.invalid) != 1 || strip.invalid[0] != "window_seconds" || len(strip.states) != 0 ||
		strip.Window.To-strip.Window.From != 130 {
		t.Errorf("a window of 3601 s is drawn over %d s, reported invalid as %v with the states %v",
			strip.Window.To-strip.Window.From, strip.invalid, strip.states)
	}
	if strip, _ := w.strip(t, 0, nil); len(strip.states) != 0 || strip.Window.To-strip.Window.From != 130 {
		t.Errorf("the default window is drawn over %d s with the states %v", strip.Window.To-strip.Window.From,
			strip.states)
	}
}
