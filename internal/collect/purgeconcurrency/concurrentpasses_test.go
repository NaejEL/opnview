package purgeconcurrency_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestThePassesAndThePurgeRunConcurrentlyAndEverySlotHoldsEveryFlowOnce is AC3: a filter-log
// loop, a lease loop, a resolver loop and a purge loop run concurrently, each for 36
// iterations, under a retention of 600 s while the clock crosses an hour, a day and an ISO
// week.
//
// Each filter-log pass offers a fresh record and a late one, observed just past the
// horizon -- a record the firewall's page delivered late, older than the horizon a purge
// at this instant applies but newer than the last purge's watermark, so it is stored. Every
// purge is requested inside the window this cycle closes: after the pass has stored its
// rows, before it derives them. Without the ordering it would move the late record into the
// purged part unplaced, and where no other row of its hour was ingested since -- across the
// hour boundary -- before any refresh read it. The filter log also re-offers its last 25
// records every pass, so records the purge has removed come back and must be refused; the
// resolver logs a lookup just before every outbound record, so the domain family fills;
// the lease pass re-points the address-level clients. The lease and resolver loops run
// freely beside the other two.
//
// Afterwards every slot of every family holds the flows ever ingested, each once; no purged
// row is unplaced, every record having an end the interface networks place; and the run
// ended within its bound.
func TestThePassesAndThePurgeRunConcurrentlyAndEverySlotHoldsEveryFlowOnce(t *testing.T) {
	t.Parallel()
	harness := arrangeEveryStoringSource(t)
	ctx := context.Background()
	// Nine minutes before a Monday's midnight, so the run crosses an hour, a day and a week.
	start := collecttest.ReferenceInstant().Add(23*time.Hour + 51*time.Minute)
	collecttest.SetClock(harness, start)
	if vocab.PeriodWeek.SlotStart(start.Unix()) == vocab.PeriodWeek.SlotStart(start.Add(18*time.Minute).Unix()) {
		t.Fatal("the run does not cross an ISO week, so the composed week is not exercised")
	}
	if err := harness.Store.SetSetting(ctx, "retention_seconds", "600", start.Unix()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	// The resolver's page starts empty; each resolver pass replaces it.
	emptyLookups := map[string]any{"total": 0, "rowCount": 0, "current": 1, "rows": []any{}}
	harness.Fake.AnswerJSON(collecttest.Declared("unbound", "search_queries"), emptyLookups)

	const iterations = 36
	var (
		offeredMutex sync.Mutex
		offered      []pageRecord
	)
	ticks := []chan struct{}{make(chan struct{}, iterations), make(chan struct{}, iterations)}
	failures := make(chan error, 4*iterations)

	// The purge is requested once a pass has stored its rows, and the pass goes on to its
	// derivation once the purge is either waiting for it or -- with no ordering -- done.
	// The next pass begins only once that purge has finished, so a late record is never
	// older than the last purge's watermark when it is offered.
	var (
		stored     = make(chan struct{})
		settled    = make(chan struct{})
		purgeDone  = make(chan struct{}, iterations)
		purgeWaits sync.Mutex
		waited     bool
	)
	harness.Collector.Hooks.PurgeWaits = func() {
		purgeWaits.Lock()
		waited = true
		purgeWaits.Unlock()
		settled <- struct{}{}
	}
	harness.Collector.Hooks.AfterStore = func() {
		stored <- struct{}{}
		<-settled
	}
	var loops sync.WaitGroup
	loop := func(name string, tick chan struct{}, pass func(int) error) {
		loops.Add(1)
		go func() {
			defer loops.Done()
			for i := 0; i < iterations; i++ {
				if tick != nil {
					<-tick
				}
				if err := pass(i); err != nil {
					failures <- fmt.Errorf("%s, iteration %d: %w", name, i, err)
				}
			}
		}()
	}

	loop("filter log", nil, func(i int) error {
		if i > 0 {
			<-purgeDone
		}
		harness.Clock.Advance(30 * time.Second)
		now := harness.Clock.Now().Unix()
		offeredMutex.Lock()
		offered = append(offered, generatedRecord(i, 5000, now-615), generatedRecord(i, 4000, now-5))
		var page []any
		for back := len(offered) - 1; back >= 0 && back >= len(offered)-25; back-- {
			page = append(page, offered[back].body())
		}
		offeredMutex.Unlock()
		// The page is generated, newest first, as the endpoint returns it.
		harness.Fake.AnswerJSON(opnsense.FirewallLog, page)
		err := harness.Collector.CollectFirewallLog(ctx)
		for _, tick := range ticks {
			tick <- struct{}{}
		}
		return err
	})
	loop("DHCP leases", ticks[0], func(int) error { return harness.Collector.CollectDHCPLease(ctx) })
	loop("resolver lookups", ticks[1], func(int) error {
		offeredMutex.Lock()
		var rows []any
		for back := len(offered) - 1; back >= 0 && back >= len(offered)-25; back-- {
			record := offered[back]
			if record.device != "exdev0" {
				continue
			}
			rows = append(rows, map[string]any{
				"client": record.src, "domain": fmt.Sprintf("example-site-%d.invalid", back%3),
				"time": record.observedAt - 2, "action": "Pass", "source": "Recursion", "rcode": "NOERROR",
				"dnssec_status": nil, "blocklist": "", "uuid": nil, "status": "0",
			})
		}
		offeredMutex.Unlock()
		// The lookups are generated, one just before every outbound record, newest first.
		harness.Fake.AnswerJSON(collecttest.Declared("unbound", "search_queries"), map[string]any{
			"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows})
		return harness.Collector.CollectDNSLookup(ctx)
	})
	loop("retention purge", nil, func(int) error {
		<-stored
		purgeWaits.Lock()
		waited = false
		purgeWaits.Unlock()
		err := harness.Collector.Purge(ctx)
		purgeWaits.Lock()
		ranInsideTheWindow := !waited
		purgeWaits.Unlock()
		if ranInsideTheWindow {
			settled <- struct{}{}
		}
		purgeDone <- struct{}{}
		if ranInsideTheWindow {
			return errors.New("the purge ran between a pass storing its rows and the pass's derivation")
		}
		return err
	})

	finished := make(chan struct{})
	go func() {
		loops.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Minute):
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		t.Fatalf("the loops did not finish within ten minutes, which is a deadlock:\n%s", stacks)
	}
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	// Every record was offered first when it was newer than the last purge's watermark, so
	// every one was ingested, and stored again never.
	var truth, connections int64
	truthBySlot := map[vocab.Period]map[int64]slotFigures{}
	for _, period := range vocab.Periods() {
		truthBySlot[period] = map[int64]slotFigures{}
	}
	for _, record := range offered {
		truth += record.bytes
		connections++
		for _, period := range vocab.Periods() {
			slot := truthBySlot[period][period.SlotStart(record.observedAt)]
			slot.add(record.action, record.bytes, 1)
			truthBySlot[period][period.SlotStart(record.observedAt)] = slot
		}
	}
	purgedBytes := int64(collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"))
	presentBytes := int64(collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow"))
	if purgedBytes == 0 || presentBytes == 0 {
		t.Errorf("the purged part holds %d bytes and flow %d: the run did not exercise both", purgedBytes,
			presentBytes)
	}
	if purgedBytes+presentBytes != truth {
		t.Errorf("the purged part holds %d bytes and flow %d; the records offered hold %d", purgedBytes,
			presentBytes, truth)
	}
	purgedRecords := int64(collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(connections), 0) FROM purged_flow_hour"))
	if stored := purgedRecords + int64(collecttest.CountRows(t, harness.Store, "flow")); stored != connections {
		t.Errorf("%d records are stored or purged, not the %d offered once each", stored, connections)
	}

	// Every slot of the families every flow reaches holds what the records sum to...
	for _, period := range vocab.Periods() {
		for _, family := range []string{"volume", "rule"} {
			bySlot := map[int64]slotFigures{}
			for key, figures := range storedSlots(t, harness.Store, period, family) {
				var start int64
				if _, err := fmt.Sscanf(key, "%d|", &start); err != nil {
					t.Fatalf("reading the slot %s: %v", key, err)
				}
				sum := bySlot[start]
				sum.bytes += figures.bytes
				sum.allowedBytes += figures.allowedBytes
				sum.blockedBytes += figures.blockedBytes
				sum.unknownBytes += figures.unknownBytes
				sum.allowed += figures.allowed
				sum.blocked += figures.blocked
				sum.unknown += figures.unknown
				bySlot[start] = sum
			}
			starts := make([]int64, 0, len(truthBySlot[period]))
			for start := range truthBySlot[period] {
				starts = append(starts, start)
			}
			sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
			for _, start := range starts {
				if got, want := bySlot[start], truthBySlot[period][start]; got != want {
					t.Errorf("the %s slot at %d of the %s family holds %+v; the records ingested into it "+
						"sum to %+v", period.Name, start, family, got, want)
				}
			}
			for start, got := range bySlot {
				if _, present := truthBySlot[period][start]; !present && got != (slotFigures{}) {
					t.Errorf("the %s slot at %d of the %s family holds %+v, and no record was ingested "+
						"into it", period.Name, start, family, got)
				}
			}
		}
	}
	// ...and every slot of every family is its purged part plus the flows present.
	assertSlotsArePurgedPartPlusFlows(t, harness.Store)
	if unplaced := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM purged_flow_hour
		WHERE src_interface_id IS NULL AND dst_interface_id IS NULL`); unplaced != 0 {
		t.Errorf("%d purged rows are frozen unplaced, while every record has an end the interface "+
			"networks place", unplaced)
	}
	if named := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM domain_volume_aggregate_1h"); named == 0 {
		t.Error("no flow was attributed a site name, so the domain family is not exercised")
	}
	if repointed := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM purged_flow_hour p
		JOIN client c ON c.id = p.local_client_id WHERE c.identity_kind <> 'address_in_interface'`); repointed == 0 {
		t.Error("no purged row names a leased client, so the lease pass's identities are not exercised")
	}
}
