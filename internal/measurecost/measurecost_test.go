package measurecost

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The LV-C measuring tool (C3.0 and LV-C of the backend-completion cycle), against a fake firewall
// and a fake clock: it polls the endpoint at each candidate cadence through the broker, samples the
// processor stream, and reports per cadence the added points and the floor.

// fakeClock advances by each sleep, and yields to the stream sampler for a moment of real time.
type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mutex.Lock()
	if d > 0 {
		c.now = c.now.Add(d)
	}
	c.mutex.Unlock()
	time.Sleep(time.Millisecond)
	return ctx.Err()
}

func percent(value float64) *float64 { return &value }

func TestLiveEndpointsVerifiedTheMeasurementPollsEachCadenceThroughTheBroker(t *testing.T) {
	fake := providertest.NewFirewall(t)
	fake.Answer(collecttest.TrafficInterface, 200, []byte(`{"interfaces": {}}`))
	fake.AnswerStream(opnsense.CPUUsageStream, [][]byte{[]byte(`{"total": 10, "idle": 90}`),
		[]byte(`{"total": 20, "idle": 80}`)}, true)
	gate := broker.New(providertest.NewClient(t, fake), nil, broker.Options{})
	gate.Scheduler().SetSettings(schedule.Settings{MaxInFlight: 8, MaxPerSecond: 1e6})
	defer gate.Close()
	clock := &fakeClock{now: time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)}
	report, err := Measure(context.Background(), Options{Broker: gate, Endpoint: collecttest.TrafficInterface,
		Idle: time.Minute, Duration: time.Minute, Cadences: []time.Duration{30 * time.Second, 10 * time.Second},
		Clock: clock, StreamPoll: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("measuring: %v", err)
	}
	if report.Idle.Polls != 0 {
		t.Errorf("the idle baseline polled %d times", report.Idle.Polls)
	}
	if len(report.Cadences) != 2 || report.Cadences[0].CadenceSeconds != 10 || report.Cadences[1].CadenceSeconds != 30 {
		t.Fatalf("the cadences measured are %+v, want 10 s then 30 s", report.Cadences)
	}
	if report.Cadences[0].Polls != 6 || report.Cadences[1].Polls != 2 {
		t.Errorf("a minute at 10 s polled %d times and at 30 s %d times, want 6 and 2", report.Cadences[0].Polls,
			report.Cadences[1].Polls)
	}
	if got := len(fake.RequestsTo(collecttest.TrafficInterface)); got != 8 {
		t.Errorf("the firewall saw %d polls, want every poll to reach it: 8", got)
	}
	for _, phase := range report.Cadences {
		if phase.Failures != 0 || phase.LatencyMedianMS == nil || phase.LatencyP95MS == nil ||
			phase.ResponseBytesMedian == nil || *phase.ResponseBytesMedian != float64(len(`{"interfaces": {}}`)) {
			t.Errorf("the phase at %v s reads %+v", phase.CadenceSeconds, phase)
		}
	}
	fake.AssertEveryPathIsRegistered()
}

func TestLiveEndpointsVerifiedTheFloorIsTheFinestCadenceWithinTwoPoints(t *testing.T) {
	report := Report{Threshold: DefaultThreshold, Idle: Phase{ProcessorPercent: percent(10)},
		Cadences: []Phase{
			{CadenceSeconds: 2, ProcessorPercent: percent(15)},
			{CadenceSeconds: 5, ProcessorPercent: percent(12)},
			{CadenceSeconds: 10, ProcessorPercent: percent(11)},
			{CadenceSeconds: 30},
		}}
	Summarise(&report)
	if report.FloorSeconds == nil || *report.FloorSeconds != 5 {
		t.Fatalf("the floor is %v, want 5 s: the finest cadence adding at most 2 points", report.FloorSeconds)
	}
	if report.Cadences[0].WithinThreshold || *report.Cadences[0].AddedPoints != 5 {
		t.Errorf("2 s adding 5 points reads %+v", report.Cadences[0])
	}
	if report.Cadences[3].AddedPoints != nil || report.Cadences[3].WithinThreshold {
		t.Errorf("a cadence with no processor reading is within the threshold: %+v", report.Cadences[3])
	}
	// A cadence whose polls failed is no floor, however little it cost.
	report.Cadences[1].Failures = 1
	Summarise(&report)
	if report.FloorSeconds == nil || *report.FloorSeconds != 10 {
		t.Errorf("with 5 s failing the floor is %v, want 10 s", report.FloorSeconds)
	}
	// No processor reading at all: no floor, never a guess.
	report.Idle.ProcessorPercent = nil
	Summarise(&report)
	if report.FloorSeconds != nil {
		t.Errorf("with no idle baseline the floor is %v", *report.FloorSeconds)
	}
}

func TestLiveEndpointsVerifiedProcessorEventsAreReadAsTheInsightPluginReadsThem(t *testing.T) {
	if value, ok := processorPercent([]byte(`{"total": 25, "idle": 75}`)); !ok || value != 25 {
		t.Errorf("an event of 25 total and 75 idle read %v %v", value, ok)
	}
	for _, unreadable := range []string{`{"idle": 75}`, `not json`, `{"total": 0, "idle": 0}`} {
		if _, ok := processorPercent([]byte(unreadable)); ok {
			t.Errorf("%s was read as a processor use", unreadable)
		}
	}
	if _, err := Measure(context.Background(), Options{}); err != ErrNoCadence {
		t.Errorf("a measurement of no cadence answered %v", err)
	}
}
