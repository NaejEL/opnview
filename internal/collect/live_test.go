package collect

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The live pass (C3 of the backend-completion cycle, C-AC10 to C-AC12), with a fake clock: it runs
// only while the live-interest lease holds, at the built-in insight plugin's live cadence (10 s by
// default), through the broker; the baseline pass runs throughout; one round of calls serves both.

// arrangeLive is a sampling harness whose collector holds a lease of 30 s, its baseline interval
// the measurement default, so the broker's mirror serves the baseline what a live pass just read.
func arrangeLive(t *testing.T) (*probeHarness, *live.Interest) {
	t.Helper()
	harness := arrangeMeasurementCollection(t)
	settings := config.Defaults()
	settings.DiscoveryInterval = 0
	harness.collector.Configure(settings)
	interest := live.New(func() time.Duration { return 30 * time.Second })
	harness.collector.SetLiveInterest(interest)
	return harness, interest
}

// fineRows counts the fine samples stored.
func fineRows(t *testing.T, database *store.Store) int {
	t.Helper()
	var count int
	if err := database.DB().QueryRowContext(context.Background(),
		"SELECT count(*) FROM measurement_sample WHERE live_cadence_seconds IS NOT NULL").Scan(&count); err != nil {
		t.Fatalf("counting the fine samples: %v", err)
	}
	return count
}

func TestLiveCadenceAndInterestThePassRunsOnlyWhileTheLeaseHolds(t *testing.T) {
	t.Parallel()
	harness, interest := arrangeLive(t)
	ctx := context.Background()
	calls := func() int { return len(harness.fake.RequestsTo(opnsense.TrafficTop)) }
	before := calls()

	// Nobody has asked for a live figure: no live pass, no call.
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatalf("a live tick with no lease: %v", err)
	}
	if harness.collector.LivePasses() != 0 || calls() != before {
		t.Fatalf("a tick with no lease ran %d live passes and made %d calls", harness.collector.LivePasses(),
			calls()-before)
	}

	// A request for a live figure renews the lease: the next tick samples, as a fine sample.
	interest.Renew(harness.clock.Now())
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatalf("a live pass: %v", err)
	}
	if harness.collector.LivePasses() != 1 || calls() != before+1 {
		t.Fatalf("a tick under the lease ran %d live passes and %d calls, want 1 and 1",
			harness.collector.LivePasses(), calls()-before)
	}
	if fineRows(t, harness.store) == 0 {
		t.Fatal("the live pass stored no fine sample")
	}

	// Before the cadence has elapsed, a tick runs nothing.
	harness.clock.advance(5 * time.Second)
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatal(err)
	}
	if harness.collector.LivePasses() != 1 {
		t.Fatalf("a tick 5 s after a live pass at a 10 s cadence ran one more")
	}

	// Past the cadence, the next one runs and asks the firewall again.
	harness.clock.advance(6 * time.Second)
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatal(err)
	}
	if harness.collector.LivePasses() != 2 || calls() != before+2 {
		t.Fatalf("a tick 11 s after the last ran %d live passes and %d calls, want 2 and 2",
			harness.collector.LivePasses(), calls()-before)
	}

	// The lease lapses live_interest_seconds after its last renewal: the live pass stops.
	harness.clock.advance(20 * time.Second)
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatal(err)
	}
	if harness.collector.LivePasses() != 2 || calls() != before+2 {
		t.Fatalf("a tick 31 s after the last renewal ran a live pass")
	}

	// The baseline pass runs whether or not the lease holds.
	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("the baseline pass with the lease lapsed: %v", err)
	}
	var baseline int
	if err := harness.store.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM measurement_sample WHERE live_cadence_seconds IS NULL").Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	if baseline == 0 {
		t.Fatal("the baseline pass stored nothing")
	}
}

func TestLiveCadenceAndInterestTheLoopTicksAtTheLiveCadence(t *testing.T) {
	t.Parallel()
	harness, _ := arrangeLive(t)
	var task *Task
	for _, candidate := range harness.collector.Tasks(config.NewLive(config.Defaults())) {
		if candidate.Name == "live sampling" {
			candidate := candidate
			task = &candidate
		}
	}
	if task == nil {
		t.Fatal("no live sampling loop is scheduled")
	}
	if got := task.Interval(); got != 10*time.Second {
		t.Errorf("the live loop ticks every %s, want the insight plugin's default live cadence of 10s", got)
	}
	// A saved cadence reaches the loop at its next wait.
	if err := harness.store.SetSetting(context.Background(), "insight_live_cadence_seconds", "4",
		referenceEpoch()); err != nil {
		t.Fatal(err)
	}
	if got := task.Interval(); got != 4*time.Second {
		t.Errorf("after saving 4 s the live loop ticks every %s", got)
	}
	// Never below the lower bound of the setting, nor below traffic/top's 2-second capture.
	if err := harness.store.SetSetting(context.Background(), "insight_live_cadence_seconds", "1",
		referenceEpoch()); err != nil {
		t.Fatal(err)
	}
	if got := task.Interval(); got != 2*time.Second {
		t.Errorf("a cadence of 1 s ticks every %s, want the 2 s floor", got)
	}
}

func TestLiveCadenceAndInterestNParallelReadersMakeOneRoundOfCallsPerCadence(t *testing.T) {
	t.Parallel()
	harness, interest := arrangeLive(t)
	ctx := context.Background()
	before := len(harness.fake.RequestsTo(opnsense.TrafficTop))
	// Eight readers renew the lease at the same instant, and as many ticks run at once.
	var group sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		group.Add(1)
		go func() {
			defer group.Done()
			interest.Renew(harness.clock.Now())
			if err := harness.collector.CollectLive(ctx); err != nil {
				t.Errorf("a live tick: %v", err)
			}
		}()
	}
	group.Wait()
	if got := len(harness.fake.RequestsTo(opnsense.TrafficTop)) - before; got != 1 {
		t.Errorf("eight readers in one cadence made %d calls to traffic/top, want 1", got)
	}
}

func TestLivePersistenceOneRoundOfCallsServesTheLiveAndTheBaselinePass(t *testing.T) {
	t.Parallel()
	harness, interest := arrangeLive(t)
	ctx := context.Background()
	interest.Renew(harness.clock.Now())
	if err := harness.collector.CollectLive(ctx); err != nil {
		t.Fatal(err)
	}
	fine := fineRows(t, harness.store)
	calls := len(harness.fake.RequestsTo(opnsense.TrafficTop))
	// The baseline pass falls due during live mode: the broker serves it the live pass's answer.
	harness.clock.advance(3 * time.Second)
	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(harness.fake.RequestsTo(opnsense.TrafficTop)); got != calls {
		t.Errorf("the baseline pass made %d more calls to traffic/top; the live pass's answer serves it", got-calls)
	}
	if got := fineRows(t, harness.store); got != 0 {
		t.Errorf("%d of the %d fine samples were not promoted by the baseline pass reading their capture", got, fine)
	}
	var duplicates int
	if err := harness.store.DB().QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT subject_kind, subject_key, measure, value FROM measurement_sample
		WHERE subject_kind IN ('interface_endpoint', 'interface_endpoint_pair', 'interface')
		GROUP BY 1, 2, 3, 4 HAVING count(*) > 1) AS d`).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 {
		t.Errorf("%d readings were stored twice, at two instants, from one capture", duplicates)
	}
}

func TestLiveEndpointsVerifiedAFailedLivePassYieldsAClosedOutcome(t *testing.T) {
	t.Parallel()
	harness, interest := arrangeLive(t)
	ctx := context.Background()
	harness.fake.Answer(opnsense.TrafficTop, http.StatusInternalServerError, []byte(`{}`))
	interest.Renew(harness.clock.Now())
	_ = harness.collector.CollectLive(ctx)
	state, _, _ := harness.availabilityOf(t, KindMeasurementSample, ProviderInsight)
	if state != store.StateUnavailable {
		t.Errorf("a live pass the firewall did not answer left the provider %q", state)
	}
	var outcome string
	if err := harness.store.DB().QueryRowContext(ctx, `SELECT a.outcome FROM source_availability AS a
		JOIN provider AS p ON p.id = a.provider_id WHERE p.kind = 'measurement_sample' AND p.provider_key = ?`,
		ProviderInsight).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if !store.AvailabilityOutcome(outcome).Valid() {
		t.Errorf("the outcome %q is not one of the closed availability outcomes", outcome)
	}
	var pairs int
	if err := harness.store.DB().QueryRowContext(ctx, `SELECT count(*) FROM measurement_sample
		WHERE live_cadence_seconds IS NOT NULL AND subject_kind IN ('interface_endpoint', 'interface_endpoint_pair')`).
		Scan(&pairs); err != nil {
		t.Fatal(err)
	}
	if pairs != 0 {
		t.Error("a per-pair snapshot the firewall did not answer stored a fine sample")
	}
}
