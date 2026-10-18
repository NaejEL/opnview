package collect_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// derivedWorld is the example_derived test plugin loaded on a store, with its collector and the sink of its
// push entry.
type derivedWorld struct {
	database    *store.Store
	collector   *collect.Collector
	clock       *collecttest.FixedClock
	derivations map[string]provider.Derivation
	sink        push.Sink
	// tasks are the derivations' loops, built once: building them routes the pass notifications to them.
	tasks []core.Task
}

// newDerivedWorld loads testdata/example_derived on a database of its own.
func newDerivedWorld(t *testing.T) *derivedWorld {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	copyDirectory(t, filepath.Join("testdata", "example_derived"),
		filepath.Join(dataDir, plugin.PluginsDirectory, "example_derived"))
	database := collecttest.NewPluginStore(t)
	loaded, err := plugin.Load(ctx, plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		if refusal.Plugin == "example_derived" {
			t.Fatalf("refused: %+v", refusal)
		}
	}
	clock := collecttest.NewFixedClock(collecttest.ReferenceInstant())
	collector := collecttest.NewTestCollectorWithSources(t, providertest.NewClient(t, providertest.NewFirewall(t)),
		database, clock, loaded.Set)
	world := &derivedWorld{database: database, collector: collector, clock: clock,
		derivations: map[string]provider.Derivation{}}
	world.tasks = collector.DerivationTasks()
	for _, derivation := range loaded.Set.Derivations() {
		world.derivations[derivation.Name()] = derivation
	}
	subscriptions, err := loaded.PushSubscriptions(plugin.PushEnvironment{Store: database, Session: collector,
		Kinds: collector})
	if err != nil {
		t.Fatal(err)
	}
	for _, subscription := range subscriptions {
		if subscription.ID == "example_derived.pings" {
			world.sink = subscription.Sink
		}
	}
	if world.sink == nil || world.derivations["example_derived.summarise"] == nil ||
		world.derivations["example_derived.tally"] == nil {
		t.Fatalf("the plugin's push entry or its two derivations are missing: %v", world.derivations)
	}
	return world
}

// deliver hands datagrams to the push entry, decoded by the real datagram protocol, as one batch.
func (w *derivedWorld) deliver(t *testing.T, payloads ...string) {
	t.Helper()
	protocol, known := push.Lookup("datagram")
	if !known {
		t.Fatal("the datagram protocol is not registered")
	}
	decoder := protocol.NewDecoder(push.Limits{})
	var records []push.Record
	for _, payload := range payloads {
		decoded := decoder.Decode([]byte(payload), push.Meta{Sender: netip.MustParseAddr("192.0.2.1"),
			Transport: push.TransportUDP, ReceivedAt: w.clock.Now()})
		records = append(records, decoded.Records...)
	}
	ctx := context.Background()
	prepared := w.sink.Prepare(ctx, records)
	if prepared.Failed != 0 {
		t.Fatalf("the decode function lost %d messages", prepared.Failed)
	}
	if err := w.sink.Store(ctx, prepared); err != nil {
		t.Fatal(err)
	}
}

// run runs one derivation as the collector does, returning what it did.
func (w *derivedWorld) run(t *testing.T, name string) (provider.DerivationOutcome, error) {
	t.Helper()
	return w.derivations["example_derived."+name].Run(context.Background(), w.collector.Collector, w.collector.Now())
}

// table reads one of the plugin's tables as sorted lines.
func (w *derivedWorld) table(t *testing.T, query string) string {
	t.Helper()
	rows, err := w.database.SQL.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var key string
		var value int64
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("%s=%d", key, value))
	}
	return strings.Join(lines, " ")
}

// woken reports whether a derivation's loop was woken, consuming the wake.
func woken(derivation provider.Derivation) bool {
	select {
	case <-derivation.Wake():
		return true
	default:
		return false
	}
}

// TestADerivationRunsOnItsTriggersSeriallyAndWritesNothingTwice is PA-AC6: a derivation runs after a pass of
// the kind it names (and, scoped to a provider, only that provider's), after a batch its plugin's push entry
// stored, and on its cadence; two triggers arriving together run it once, serially; a second run over the
// same inputs writes nothing; counters add in one batched write; and a run that throws writes nothing and
// records its closed outcome in its plugin's availability.
func TestADerivationRunsOnItsTriggersSeriallyAndWritesNothingTwice(t *testing.T) {
	t.Parallel()
	w := newDerivedWorld(t)
	summarise, tally := w.derivations["example_derived.summarise"], w.derivations["example_derived.tally"]
	const summary = "SELECT sender, total FROM plugin_example_derived.summary ORDER BY sender"
	const tallies = "SELECT trigger, runs FROM plugin_example_derived.tallies ORDER BY trigger"

	// After a batch of its push entry: both derivations are woken and handed the batch.
	w.deliver(t, "PING alpha 3", "PING beta 4", "NOT a ping", "PING alpha 5")
	if !woken(summarise) || !woken(tally) {
		t.Fatal("a stored push batch woke no derivation")
	}
	outcome, err := w.run(t, "summarise")
	if err != nil || !outcome.Ran || outcome.Written != 2 {
		t.Fatalf("the summary after the batch: %+v, %v", outcome, err)
	}
	if got := w.table(t, summary); got != "alpha=8 beta=4" {
		t.Fatalf("the summary is %q", got)
	}
	if _, err := w.run(t, "tally"); err != nil {
		t.Fatal(err)
	}

	// After a pass of the kind it names; the tally names the pf provider alone.
	w.collector.Triggers.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "firewall_log", "provider_key": "pf"}})
	w.collector.Triggers.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "firewall_log", "provider_key": "other"}})
	w.collector.Triggers.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "dhcp_lease", "provider_key": "pf"}})
	if !woken(summarise) || !woken(tally) {
		t.Fatal("a pass of the named kind woke no derivation")
	}
	// Two triggers together, two loops' worth of calls at once: one run takes both, the other nothing.
	var group sync.WaitGroup
	outcomes := make([]provider.DerivationOutcome, 2)
	for index := range outcomes {
		group.Add(1)
		go func() {
			defer group.Done()
			outcomes[index], _ = w.run(t, "tally")
		}()
	}
	group.Wait()
	if outcomes[0].Ran == outcomes[1].Ran {
		t.Errorf("two simultaneous runs of one derivation both ran, or neither: %+v", outcomes)
	}
	if got := w.table(t, tallies); got != "pass=1 push=1" {
		t.Errorf("the tally is %q: counters add, once per change, and the pass of another provider is not the "+
			"tally's", got)
	}

	// A second run over the same inputs writes nothing: the summary's pass changes change no row.
	outcome, err = w.run(t, "summarise")
	if err != nil || !outcome.Ran || outcome.Written != 0 {
		t.Errorf("the summary's run over unchanged pings wrote %+v, %v", outcome, err)
	}

	// On its cadence: with nothing noted, the summary still runs -- and changes nothing -- and the tally,
	// which has no cadence, runs no script.
	w.clock.Advance(10 * time.Minute)
	if outcome, err := w.run(t, "summarise"); err != nil || !outcome.Ran || outcome.Written != 0 {
		t.Errorf("the cadence run: %+v, %v", outcome, err)
	}
	if outcome, err := w.run(t, "tally"); err != nil || outcome.Ran {
		t.Errorf("a derivation with nothing noted and no cadence ran: %+v, %v", outcome, err)
	}
	tasks := map[string]bool{}
	for _, task := range w.tasks {
		tasks[task.Name] = task.Interval() == 10*time.Minute
	}
	if !tasks["derivation example_derived.summarise"] {
		t.Errorf("the summary's loop does not run at its cadence: %v", tasks)
	}

	// A run that throws writes nothing, keeps what it was handed, and records script_failed.
	w.deliver(t, "PING gamma 1")
	w.collector.Triggers.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "firewall_log", "provider_key": "broken"}})
	err = w.collector.RunDerivation(context.Background(), summarise)
	var failure *provider.DerivationFailure
	if !errors.As(err, &failure) || failure.Outcome != "script_failed" {
		t.Fatalf("the throwing run returned %v", err)
	}
	if got := w.table(t, summary); got != "alpha=8 beta=4" {
		t.Errorf("a throwing run wrote: the summary is %q", got)
	}
	var outcomeCode string
	if err := w.database.SQL.QueryRowContext(context.Background(), `SELECT a.outcome FROM source_availability AS a
		JOIN provider AS p ON p.id = a.provider_id WHERE p.kind = 'example_ping' AND p.provider_key = 'example_derived'`).
		Scan(&outcomeCode); err != nil {
		t.Fatal(err)
	}
	if outcomeCode != "script_failed" {
		t.Errorf("the plugin's availability reads %q, not script_failed", outcomeCode)
	}
	if _, err := w.run(t, "summarise"); err == nil {
		t.Error("the failed run's changes were dropped: the next run did not meet them again")
	}
}

// TestADerivationsOutputsDoNotDependOnArrivalOrder is PA-AC6's last bullet, by PS-AC34's method: the same
// batches stored forward, reversed and interleaved with runs give the same outputs, and a further run
// writes nothing.
func TestADerivationsOutputsDoNotDependOnArrivalOrder(t *testing.T) {
	t.Parallel()
	batches := [][]string{{"PING alpha 1", "PING beta 2"}, {"PING gamma 3"}, {"PING alpha 4", "PING gamma 5"}}
	orders := map[string]func(t *testing.T, w *derivedWorld){
		"forward": func(t *testing.T, w *derivedWorld) {
			for _, batch := range batches {
				w.deliver(t, batch...)
			}
		},
		"reverse": func(t *testing.T, w *derivedWorld) {
			for index := len(batches) - 1; index >= 0; index-- {
				w.deliver(t, batches[index]...)
			}
		},
		"interleaved": func(t *testing.T, w *derivedWorld) {
			w.deliver(t, batches[1]...)
			if _, err := w.run(t, "summarise"); err != nil {
				t.Fatal(err)
			}
			w.deliver(t, batches[2]...)
			w.deliver(t, batches[0]...)
		},
	}
	outputs := map[string]string{}
	for name, arrive := range orders {
		w := newDerivedWorld(t)
		arrive(t, w)
		if _, err := w.run(t, "summarise"); err != nil {
			t.Fatal(err)
		}
		outputs[name] = w.table(t, "SELECT sender || ':' || pings, total FROM plugin_example_derived.summary ORDER BY sender")
		w.deliver(t)
		if outcome, err := w.run(t, "summarise"); err != nil || outcome.Written != 0 {
			t.Errorf("%s: a further run wrote %+v, %v", name, outcome, err)
		}
	}
	if outputs["forward"] != "alpha:2=5 beta:1=2 gamma:2=8" || outputs["reverse"] != outputs["forward"] ||
		outputs["interleaved"] != outputs["forward"] {
		t.Errorf("the outputs depend on the arrival order: %v", outputs)
	}
}
