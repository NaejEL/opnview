package plugin

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// receivedKinds is the core's side of the test: what reached a kind.
type receivedKinds struct {
	mutex   sync.Mutex
	records map[string][]provider.Answer
}

func (r *receivedKinds) ReceivePushed(_ context.Context, kind, key string, records []provider.Answer) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.records[kind+"/"+key] = append(r.records[kind+"/"+key], records...)
	return nil
}

func (r *receivedKinds) ObservePushCondition(context.Context, string, string, provider.PushCondition) error {
	return nil
}

// recordedGaps keeps the gaps the hub recorded.
type recordedGaps struct {
	mutex   sync.Mutex
	reasons map[string][]string
}

func (g *recordedGaps) RecordPushGap(_ context.Context, subscription, reason string, _, _ time.Time, _ int64,
	_ string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.reasons[subscription] = append(g.reasons[subscription], reason)
	return nil
}

// copyTestPlugin copies a directory of testdata/ into a data directory's plugins/.
func copyTestPlugin(t *testing.T, name string) string {
	t.Helper()
	dataDir := t.TempDir()
	directory := filepath.Join(dataDir, PluginsDirectory, name)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join("testdata", name, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, entry.Name()), content, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

// TestScriptDecodedEntriesDeliverToAKindAndATableWithLivenessAndGaps is PA-AC11: a datagram entry and a line
// entry, each decoded by its script one message per call, deliver rows to the plugin's own table, and a line
// entry delivers records to a push-deliverable kind; each entry is alive once a message reached it, a message
// its decode function throws on is recorded as a gap as a core protocol's lost message is, and a message no
// entry's filter admits reaches none.
func TestScriptDecodedEntriesDeliverToAKindAndATableWithLivenessAndGaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	loaded, err := Load(ctx, Options{DataDir: copyTestPlugin(t, "example_decoded"), Store: database,
		Builtins: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("refused: %+v", refusals)
	}
	kinds := &receivedKinds{records: map[string][]provider.Answer{}}
	gaps := &recordedGaps{reasons: map[string][]string{}}
	at := time.Unix(1_790_000_000, 0).UTC()
	hub := push.NewHub(push.Options{Now: func() time.Time { return at }, Gaps: gaps})
	subscriptions, err := loaded.PushSubscriptions(PushEnvironment{Store: database, Kinds: kinds})
	if err != nil {
		t.Fatal(err)
	}
	for _, subscription := range subscriptions {
		if err := hub.Subscribe(subscription); err != nil {
			t.Fatal(err)
		}
	}
	for _, listener := range [][2]string{{"datagram", push.TransportUDP}, {"line", push.TransportTCP}} {
		if err := hub.AddLoopbackListener(listener[0], listener[1]); err != nil {
			t.Fatal(err)
		}
	}
	sender := netip.MustParseAddr("192.0.2.1")
	hub.Senders().Set([]netip.Addr{sender})
	deliver := func(protocol, transport, payload string) {
		if err := hub.Deliver(ctx, protocol, transport, []byte(payload), sender); err != nil {
			t.Fatal(err)
		}
	}
	deliver("datagram", push.TransportUDP, "\x07alpha")
	deliver("line", push.TransportTCP, "READING beta 9")
	deliver("line", push.TransportTCP, "READING boom")
	deliver("line", push.TransportTCP, "LOG 192.0.2.10 198.51.100.7 1200")
	deliver("line", push.TransportTCP, "UNKNOWN nobody reads this")
	hub.Flush(ctx)
	hub.Tick(ctx, true)

	var readings []string
	rows, err := database.SQL.QueryContext(ctx,
		"SELECT sender || '=' || value FROM plugin_example_decoded.readings ORDER BY sender")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var reading string
		if err := rows.Scan(&reading); err != nil {
			t.Fatal(err)
		}
		readings = append(readings, reading)
	}
	_ = rows.Close()
	if len(readings) != 2 || readings[0] != "alpha=7" || readings[1] != "beta=9" {
		t.Errorf("the readings stored are %v, want alpha=7 from the datagram and beta=9 from the line", readings)
	}
	logged := kinds.records["firewall_log/example_decoded"]
	if len(logged) != 1 || logged[0]["src_address"] != "192.0.2.10" || logged[0]["packet_bytes"] != int64(1200) {
		t.Errorf("the firewall_log records delivered are %v", logged)
	}
	for _, entry := range []string{"datagrams", "lines", "log_lines"} {
		state, found := hub.SubscriptionState("example_decoded." + entry)
		if !found || !state.Liveness.Alive {
			t.Errorf("the entry %s is not alive after its message: %+v", entry, state)
		}
	}
	if reasons := gaps.reasons["example_decoded.lines"]; len(reasons) != 1 || reasons[0] != push.GapScriptFailed {
		t.Errorf("the message the decode function threw on was recorded as %v, not one %s gap", reasons,
			push.GapScriptFailed)
	}
	if len(gaps.reasons["example_decoded.datagrams"]) != 0 || len(gaps.reasons["example_decoded.log_lines"]) != 0 {
		t.Errorf("gaps were recorded for entries that lost nothing: %v", gaps.reasons)
	}
}
