package strip

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The rolling strip (C3 of the backend-completion cycle, C-AC12 and C-AC13): every live figure
// names its measure, span, cadence and bound; each decision series carries complete_until and the
// buckets after it are gaps, never zeros; the buckets sum to the fixture's decisions; logged and
// sampled figures are never summed.

// at is the instant the strip is drawn at: a multiple of every cadence the tests use.
const at int64 = 1790000400

type world struct {
	database *store.Store
	ctx      context.Context
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	w := &world{database: database, ctx: ctx}
	w.exec(t, `INSERT INTO interface (id, identifier, device, description, link_type, link_kind, first_seen_at,
		last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', ?, ?)`, at-3600, at-3600)
	w.exec(t, `INSERT INTO interface_network (interface_id, network_address, prefix_length, address_family, origin,
		first_detected_at, last_detected_at, updated_at) VALUES (1, '192.0.2.0', 24, 4, 'detected', ?, ?, ?)`,
		at-3600, at-3600, at-3600)
	return w
}

func (w *world) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := w.database.DB().ExecContext(w.ctx, statement, arguments...); err != nil {
		t.Fatalf("%v\n%s", err, statement)
	}
}

// healthy makes a kind's provider active, its last pass healthy at checkedAt.
func (w *world) healthy(t *testing.T, kind, key string, checkedAt int64) {
	t.Helper()
	w.exec(t, "UPDATE provider SET is_active = 1 WHERE kind = ? AND provider_key = ?", kind, key)
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
func (w *world) flow(t *testing.T, id, observedAt int64, action string, reason any) {
	t.Helper()
	w.exec(t, `INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state,
		src_interface_id, src_address, dst_address, protocol, ip_version, action, direction, packet_bytes,
		rule_lookup_state, traffic_scope, log_reason) VALUES (?, ?, ?, ?, 'vlan0.10', 'resolved', 1, '192.0.2.10',
		'203.0.113.7', 'tcp', 4, ?, 'in', 100, 'pending', 'north_south', ?)`,
		id, "d"+time.Unix(id, 0).Format("150405"), observedAt, observedAt, action, reason)
}

// refusal stores one lookup a resolver refused.
func (w *world) refusal(t *testing.T, key string, lookedUpAt int64) {
	t.Helper()
	w.exec(t, `INSERT INTO dns_resolution (lookup_key, client_address, domain, resolver, action, looked_up_at,
		ingested_at) VALUES (?, '192.0.2.10', 'example.org', 'unbound', 'block', ?, ?)`, key, lookedUpAt, lookedUpAt)
}

// sample stores one rate reading of one inside address with one outside peer.
func (w *world) sample(t *testing.T, sampledAt int64, cadence *int64, in float64) {
	t.Helper()
	providerID, err := w.database.ProviderID(w.ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	for _, reading := range []vocab.MeasurementSample{
		{SubjectKind: vocab.SubjectInterfaceEndpoint, SubjectKey: vocab.InterfaceEndpointKey("vlan0.10", "192.0.2.10"),
			Measure: vocab.MeasureRateBitsIn, Value: in},
		{SubjectKind: vocab.SubjectInterfaceEndpoint, SubjectKey: vocab.InterfaceEndpointKey("vlan0.10", "192.0.2.10"),
			Measure: vocab.MeasureRateBitsOut, Value: 2 * in},
		{SubjectKind: vocab.SubjectInterfaceEndpointPair,
			SubjectKey: vocab.InterfaceEndpointPairKey("vlan0.10", "192.0.2.10", "203.0.113.7"),
			Measure:    vocab.MeasureRateBitsIn, Value: in},
	} {
		reading.ProviderID, reading.Unit, reading.SampledAt, reading.LiveCadenceSeconds = &providerID,
			"bit_per_second", sampledAt, cadence
		if err := w.database.InsertMeasurementSample(w.ctx, reading); err != nil {
			t.Fatal(err)
		}
	}
}

func (w *world) strip(t *testing.T, cadence, window int64, interest *live.Interest) *Strip {
	t.Helper()
	answer, err := Read(w.ctx, w.database, Request{Now: time.Unix(at, 0), CadenceSeconds: cadence,
		WindowSeconds: window, Interest: interest})
	if err != nil {
		t.Fatalf("drawing the strip: %v", err)
	}
	return answer
}

func TestLiveFieldsAndStripDecisionsSumToTheRecordsAndBucketsPastCompleteUntilAreGaps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.flow(t, 1, at-100, "pass", nil)
	w.flow(t, 2, at-95, "pass", nil)
	w.flow(t, 3, at-81, "pass", nil)
	w.flow(t, 4, at-50, "block", "match")
	w.flow(t, 5, at-49, "block", "match")
	w.flow(t, 6, at-45, "block", "state-mismatch")
	w.flow(t, 7, at-44, "reject", nil)
	w.refusal(t, "k1", at-30)
	w.refusal(t, "k2", at-28)
	w.healthy(t, "firewall_log", "pf", at-20)
	w.healthy(t, "dns_lookup", "unbound", at-20)

	strip := w.strip(t, 10, 120, nil)
	if strip.CadenceSeconds != 10 || strip.Window.From != at-120 || strip.Window.To != at+10 {
		t.Fatalf("the strip is %+v over %+v", strip.CadenceSeconds, strip.Window)
	}
	want := map[string]int64{SeriesAllowed: 3, SeriesFirewallRule: 2, SeriesFirewallNoRule: 1,
		SeriesFirewallReasonNotRecorded: 1, SeriesDNSRefusal: 2}
	total := int64(0)
	for _, series := range strip.Decisions {
		if series.CompleteUntil == nil || *series.CompleteUntil != at-20 {
			t.Errorf("%s: complete_until is %v, want the last healthy pass", series.Key, series.CompleteUntil)
		}
		if !series.LowerBound || series.Measure == "" {
			t.Errorf("%s: names no measure or no bound", series.Key)
		}
		sum := int64(0)
		for _, bucket := range series.Buckets {
			if bucket.To > at-20 && !bucket.Gap {
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
		if series.Key != SeriesAllowed {
			continue
		}
		for _, bucket := range series.Buckets {
			if bucket.From == at-70 && (bucket.Gap || bucket.Value == nil || *bucket.Value != 0) {
				t.Errorf("the covered bucket [%d, %d) with no allowed record reads %+v", bucket.From, bucket.To, bucket)
			}
		}
	}
}

func TestLiveFieldsAndStripWithNoHealthyPassEveryDecisionBucketIsAGap(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.flow(t, 1, at-100, "pass", nil)
	strip := w.strip(t, 10, 120, nil)
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
	w := newWorld(t)
	cadence := int64(10)
	w.sample(t, at-40, &cadence, 1000)
	w.sample(t, at-30, &cadence, 3000)
	interest := live.New(nil)
	strip := w.strip(t, 10, 60, interest)
	if !interest.Held(time.Unix(at, 0)) || strip.LiveInterestUntil == nil {
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
			sampled := bucket.From == at-40 || bucket.From == at-30
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
			if bucket.From != at-30 || bucket.Value == nil {
				continue
			}
			want := map[string]float64{SeriesInbound: 3000, SeriesOutbound: 6000, SeriesInterInterface: 0,
				SeriesOutboundUnsplit: 0}[series.Key]
			if *bucket.Value != want {
				t.Errorf("%s at %d reads %v, want %v", series.Key, bucket.From, *bucket.Value, want)
			}
		}
	}
}

func TestLivePersistenceAWindowAcrossALiveAndAnIdlePeriodHasASampleInEveryBaselineBucket(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	cadence := int64(10)
	// Baseline samples every 300 s; a live period of fine samples in the middle.
	for instant := at - 3000; instant < at; instant += 300 {
		w.sample(t, instant, nil, 100)
	}
	for instant := at - 1500; instant < at-1200; instant += 10 {
		w.sample(t, instant, &cadence, 500)
	}
	strip := w.strip(t, 300, 3000, nil)
	for _, series := range strip.Throughput {
		for _, bucket := range series.Buckets {
			if bucket.To <= at && bucket.From >= at-3000 && bucket.Gap {
				t.Errorf("%s: the baseline bucket [%d, %d) holds no sample", series.Key, bucket.From, bucket.To)
			}
		}
	}
}

func TestLiveFieldsAndStripAWindowOutOfRangeIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, window := range []int64{19, 3601} {
		_, err := Read(w.ctx, w.database, Request{Now: time.Unix(at, 0), CadenceSeconds: 10, WindowSeconds: window})
		if _, refused := err.(*ErrWindowOutOfRange); !refused {
			t.Errorf("a window of %d s at a 10 s cadence answered %v", window, err)
		}
	}
}
