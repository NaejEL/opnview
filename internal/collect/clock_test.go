package collect

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// The firewall's offset from UTC, survey gap 7, measured from its own clock.

// systemTimeAt is a systemTime answer whose wall clock is the reference instant as a
// firewall at offset seconds from UTC writes it, in the shape the survey records.
func systemTimeAt(offset time.Duration, zone string) map[string]any {
	wall := referenceInstant().Add(offset)
	return map[string]any{
		"uptime":   "11 days, 10:17:36",
		"datetime": wall.Format("Mon Jan _2 15:04:05 ") + zone + wall.Format(" 2006"),
		"loadavg":  "0.42, 0.31, 0.25",
	}
}

// oldestObserved is the earliest observed_at stored.
func oldestObserved(t *testing.T, harness *probeHarness) int64 {
	t.Helper()
	return int64(scalarCount(t, harness.store, "SELECT min(observed_at) FROM flow"))
}

// TestTheFilterLogIsStoredInUTCWhateverTheFirewallsZone: the same page read from a
// firewall on UTC and from one on CEST is stored two hours apart, because the CEST
// firewall wrote its local time and discovery measured how far that is from UTC.
func TestTheFilterLogIsStoredInUTCWhateverTheFirewallsZone(t *testing.T) {
	ctx := context.Background()

	onUTC := arrangeFilterLogCollection(t)
	if err := onUTC.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting from the firewall on UTC: %v", err)
	}

	onCEST := arrangeFilterLogCollection(t)
	onCEST.fake.answerJSON(opnsense.SystemTime, systemTimeAt(2*time.Hour, "CEST"))
	if err := onCEST.collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("rediscovering: %v", err)
	}
	if offset := onCEST.collector.FirewallOffsetSeconds(); offset != 7200 {
		t.Fatalf("a CEST wall clock measured %ds from UTC, want 7200", offset)
	}
	if err := onCEST.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting from the firewall on CEST: %v", err)
	}

	if difference := oldestObserved(t, onUTC) - oldestObserved(t, onCEST); difference != 7200 {
		t.Errorf("the two firewalls' records are %ds apart, want the 7200 s between the zones",
			difference)
	}
}

// TestNoFilterLogLineIsStoredBeforeTheOffsetIsKnown: a collector that runs before
// discovery measures the offset itself, and with no clock to measure it from it
// stores nothing rather than a guessed time.
func TestNoFilterLogLineIsStoredBeforeTheOffsetIsKnown(t *testing.T) {
	ctx := context.Background()
	harness := arrangeFilterLogCollection(t)

	fresh := New(newFakeClient(t, harness.fake), harness.store, harness.clock)
	harness.fake.answerJSON(opnsense.SystemTime, systemTimeAt(2*time.Hour, "CEST"))
	if err := fresh.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting before discovery: %v", err)
	}
	if offset := fresh.FirewallOffsetSeconds(); offset != 7200 {
		t.Errorf("the collector measured %ds before its first read, want 7200", offset)
	}

	silent := arrangeFilterLogCollection(t)
	blind := New(newFakeClient(t, silent.fake), silent.store, silent.clock)
	silent.fake.answer(opnsense.SystemTime, 404, nil)
	if err := blind.CollectFirewallLog(ctx); err == nil {
		t.Error("a pass with no way to know the firewall's zone succeeded")
	}
	if stored := countRows(t, silent.store, "flow"); stored != 0 {
		t.Errorf("%d records were stored at a guessed time", stored)
	}
}
