package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The firewall's offset from UTC, survey gap 7, measured from its own clock.

// systemTimeAt is a systemTime answer whose wall clock is the reference instant as a
// firewall at offset seconds from UTC writes it, in the shape the survey records.
func systemTimeAt(offset time.Duration, zone string) map[string]any {
	wall := collecttest.ReferenceInstant().Add(offset)
	return map[string]any{
		"uptime":   "11 days, 10:17:36",
		"datetime": wall.Format("Mon Jan _2 15:04:05 ") + zone + wall.Format(" 2006"),
		"loadavg":  "0.42, 0.31, 0.25",
	}
}

// oldestObserved is the earliest observed_at stored.
func oldestObserved(t *testing.T, harness *collecttest.ProbeHarness) int64 {
	t.Helper()
	return int64(collecttest.ScalarCount(t, harness.Store, "SELECT min(observed_at) FROM flow"))
}

// TestTheFilterLogIsStoredInUTCWhateverTheFirewallsZone: the same page read from a
// firewall on UTC and from one on CEST is stored two hours apart, because the CEST
// firewall wrote its local time and discovery measured how far that is from UTC.
func TestTheFilterLogIsStoredInUTCWhateverTheFirewallsZone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	onUTC := collecttest.ArrangeFilterLogCollection(t)
	if err := onUTC.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting from the firewall on UTC: %v", err)
	}

	onCEST := collecttest.ArrangeFilterLogCollection(t)
	onCEST.Fake.AnswerJSON(opnsense.SystemTime, systemTimeAt(2*time.Hour, "CEST"))
	if err := onCEST.Collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("rediscovering: %v", err)
	}
	if offset := onCEST.Collector.FirewallOffsetSeconds(); offset != 7200 {
		t.Fatalf("a CEST wall clock measured %ds from UTC, want 7200", offset)
	}
	if err := onCEST.Collector.CollectFirewallLog(ctx); err != nil {
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
	t.Parallel()
	ctx := context.Background()
	harness := collecttest.ArrangeFilterLogCollection(t)

	fresh := collecttest.NewTestCollector(t, providertest.NewClient(t, harness.Fake), harness.Store, harness.Clock)
	harness.Fake.AnswerJSON(opnsense.SystemTime, systemTimeAt(2*time.Hour, "CEST"))
	if err := fresh.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting before discovery: %v", err)
	}
	if offset := fresh.FirewallOffsetSeconds(); offset != 7200 {
		t.Errorf("the collector measured %ds before its first read, want 7200", offset)
	}

	silent := collecttest.ArrangeFilterLogCollection(t)
	blind := collecttest.NewTestCollector(t, providertest.NewClient(t, silent.Fake), silent.Store, silent.Clock)
	silent.Fake.Answer(opnsense.SystemTime, 404, nil)
	if err := blind.CollectFirewallLog(ctx); err == nil {
		t.Error("a pass with no way to know the firewall's zone succeeded")
	}
	if stored := collecttest.CountRows(t, silent.Store, "flow"); stored != 0 {
		t.Errorf("%d records were stored at a guessed time", stored)
	}
}

// TestTheBrokersNormaliserSaysTheOffsetIsUnknownUntilItIsRead is N4 of the live validation of
// 17 October 2026: the normaliser the broker's mirror reads does not pass a zero offset off as a
// measurement before discovery has read the firewall's clock.
func TestTheBrokersNormaliserSaysTheOffsetIsUnknownUntilItIsRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := collecttest.ArrangeFilterLogCollection(t)
	fresh := collecttest.NewTestCollector(t, providertest.NewClient(t, harness.Fake), harness.Store, harness.Clock)
	before, err := fresh.KnownNormaliser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !before.OffsetUnknown {
		t.Errorf("before discovery the broker's normaliser claimed a known offset of %ds", before.OffsetSeconds)
	}
	harness.Fake.AnswerJSON(opnsense.SystemTime, systemTimeAt(2*time.Hour, "CEST"))
	if err := fresh.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("discovering: %v", err)
	}
	after, err := fresh.KnownNormaliser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.OffsetUnknown || after.OffsetSeconds != 7200 {
		t.Errorf("after discovery the broker's normaliser is %+v, want a known 7200 s", after)
	}
}
