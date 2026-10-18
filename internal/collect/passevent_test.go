package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// What a reader of the event stream (internal/web/surfaces/events) is told after a filter-log pass:
// the pass's own commit announces the aggregate refresh that rewrote the current hour, and that
// hour's slot is computed by that very pass -- a pass that read records, and one that read none.

// heardFrom listens on every channel the core publishes on and returns what it hears, once it is
// known to be listening.
func heardFrom(t *testing.T, database *store.Store) <-chan sqldb.Notification {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	heard := make(chan sqldb.Notification, 256)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = database.Listen(ctx, sqldb.Channels(), func(notification sqldb.Notification) { heard <- notification })
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := database.NotifyCollectionPass(context.Background(), "probe", "probe"); err != nil {
			t.Fatal(err)
		}
		select {
		case notification := <-heard:
			if notification.Payload["kind"] == "probe" {
				for {
					select {
					case <-heard:
						continue
					case <-time.After(200 * time.Millisecond):
					}
					return heard
				}
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("the listener heard nothing in ten seconds")
	return nil
}

// refreshOfHour waits for an aggregate refresh whose 1 h range holds the hour starting at hour.
func refreshOfHour(t *testing.T, heard <-chan sqldb.Notification, hour int64) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case notification := <-heard:
			if notification.Channel != sqldb.ChannelAggregateRefresh {
				continue
			}
			ranges, _ := notification.Payload["ranges"].([]any)
			for _, entry := range ranges {
				span, _ := entry.(map[string]any)
				from, _ := span["from"].(float64)
				to, _ := span["to"].(float64)
				if span["period"] == vocab.PeriodHour.Name && int64(from) <= hour && int64(to) >= hour+3600 {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no aggregate refresh announced the hour starting at %d", hour)
		}
	}
}

// TestEachFilterLogPassAnnouncesTheCurrentHourItRefreshed: every pass -- one storing a record, one
// storing another, one storing none -- rewrites the current hour's slot at its own instant, holding
// every flow of the hour, and announces that hour on commit.
func TestEachFilterLogPassAnnouncesTheCurrentHourItRefreshed(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewNetflowHarness(t)
	heard := heardFrom(t, harness.Store)
	hour := vocab.PeriodHour.SlotStart(collecttest.NetflowReadSecond)
	passes := []struct {
		at   int64
		legs []collecttest.LegRow
	}{
		{hour + 600, []collecttest.LegRow{singleLeg("fresh-1", hour+595, "tcp", 6, 50001, 443, "pass")}},
		{hour + 620, []collecttest.LegRow{singleLeg("fresh-2", hour+615, "tcp", 6, 50002, 443, "pass")}},
		{hour + 640, nil},
	}
	for index, pass := range passes {
		harness.LoggedAt(t, pass.at, pass.legs...)
		if rows := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM volume_aggregate_1h
			WHERE period_start_at = $1 AND computed_at = $2`, hour, pass.at); rows == 0 {
			t.Errorf("pass %d left the current hour's slot computed before it", index)
		}
		flowBytes := collecttest.ScalarCount(t, harness.Store, `SELECT coalesce(sum(packet_bytes), 0) FROM flow
			WHERE observed_at >= $1 AND observed_at < $2`, hour, hour+3600)
		if bytes := collecttest.ScalarCount(t, harness.Store, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
			WHERE period_start_at = $1`, hour); flowBytes == 0 || bytes != flowBytes {
			t.Errorf("after pass %d the current hour holds %d bytes while its flows hold %d", index, bytes, flowBytes)
		}
		refreshOfHour(t, heard, hour)
	}
}

// TestALivePassAnnouncesItsFineSamples: a live pass that stored fine samples announces them, with the
// instant of the newest, so a reader showing a live figure asks again -- which renews the lease.
func TestALivePassAnnouncesItsFineSamples(t *testing.T) {
	t.Parallel()
	harness, interest := arrangeLive(t)
	heard := heardFrom(t, harness.Store)
	ctx := context.Background()
	interest.Renew(harness.Clock.Now())
	if err := harness.Collector.CollectLive(ctx); err != nil {
		t.Fatalf("a live pass: %v", err)
	}
	if fineRows(t, harness.Store) == 0 {
		t.Fatal("the live pass stored no fine sample, so it has nothing to announce")
	}
	var newest int64
	if err := harness.Store.DB().QueryRowContext(ctx,
		"SELECT max(sampled_at) FROM measurement_sample WHERE live_cadence_seconds IS NOT NULL").Scan(&newest); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case notification := <-heard:
			if notification.Channel != sqldb.ChannelLiveSample {
				continue
			}
			sampledAt, _ := notification.Payload["sampled_at"].(float64)
			if notification.Payload["kind"] != "measurement_sample" || notification.Payload["provider_key"] == "" ||
				int64(sampledAt) != newest {
				t.Errorf("the live pass announced %+v, its newest sample being at %d", notification.Payload, newest)
			}
			return
		case <-deadline:
			t.Fatal("the live pass announced nothing")
		}
	}
}
