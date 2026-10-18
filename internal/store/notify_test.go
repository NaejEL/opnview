package store_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// listening starts a listener on every channel and returns what it hears, once it is known to be
// listening: it publishes a probe until the probe comes back, and drops it.
func listening(t *testing.T, database *store.Store) <-chan sqldb.Notification {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	heard := make(chan sqldb.Notification, 64)
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
	for {
		if err := database.NotifyCollectionPass(context.Background(), "probe", "probe"); err != nil {
			t.Fatalf("publishing the probe: %v", err)
		}
		select {
		case notification := <-heard:
			if notification.Payload["kind"] == "probe" {
				for {
					select {
					case <-heard:
						continue
					case <-time.After(100 * time.Millisecond):
					}
					return heard
				}
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the listener heard nothing in ten seconds")
		}
	}
}

// next returns the next notification heard, or fails the test.
func next(t *testing.T, heard <-chan sqldb.Notification) sqldb.Notification {
	t.Helper()
	select {
	case notification := <-heard:
		return notification
	case <-time.After(10 * time.Second):
		t.Fatal("no notification was heard in ten seconds")
		return sqldb.Notification{}
	}
}

// TestACommittedPassEmitsItsNotificationAndARolledBackOneNone is P-AC14's first half: a committed
// collection pass, plugin pass and aggregate refresh each publish the documented notification, and
// a transaction that rolls back publishes nothing -- the sentinel published after it is the next
// thing heard.
func TestACommittedPassEmitsItsNotificationAndARolledBackOneNone(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	heard := listening(t, database)

	if err := database.NotifyCollectionPass(ctx, "dhcp_lease", "kea"); err != nil {
		t.Fatal(err)
	}
	if got := next(t, heard); got.Channel != sqldb.ChannelCollectionPass || got.Payload["kind"] != "dhcp_lease" ||
		got.Payload["provider_key"] != "kea" {
		t.Errorf("the collection pass published %+v", got)
	}

	if _, err := database.EnsurePluginTable(ctx, exampleReadings("example_one")); err != nil {
		t.Fatal(err)
	}
	if err := database.WritePluginPass(ctx, "example_one", "example_reader", plugintables.PluginRows{"readings": {
		{"sampled_at": int64(1790000000), "subject": "a", "value": 1.0}}}, nil, 1790000000); err != nil {
		t.Fatal(err)
	}
	if got := next(t, heard); got.Channel != sqldb.ChannelPluginPass || got.Payload["plugin"] != "example_one" ||
		got.Payload["provider"] != "example_reader" {
		t.Errorf("the plugin pass published %+v", got)
	}

	// A pass that fails stores nothing and publishes nothing; nor does a transaction that publishes
	// and then rolls back.
	if err := database.WritePluginPass(ctx, "example_one", "example_reader", plugintables.PluginRows{"readings": {
		{"sampled_at": int64(1790000000), "subject": nil, "value": 1.0}}}, nil, 1790000000); err == nil {
		t.Fatal("a row with a NULL key was stored")
	}
	transaction, err := database.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqldb.Notify(ctx, transaction, sqldb.ChannelPluginPass, map[string]string{"plugin": "rolled_back"}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := database.NotifyCollectionPass(ctx, "sentinel", "sentinel"); err != nil {
		t.Fatal(err)
	}
	if got := next(t, heard); got.Payload["kind"] != "sentinel" {
		t.Errorf("a pass that did not commit published %+v", got)
	}

	now := int64(1790000000)
	refresh, err := storetest.RefreshAggregates(ctx, database, now, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(refresh.Rewritten) == 0 {
		t.Fatal("the refresh rewrote no slot, so it has nothing to announce")
	}
	got := next(t, heard)
	periods, _ := got.Payload["periods"].([]any)
	if got.Channel != sqldb.ChannelAggregateRefresh || len(periods) == 0 {
		t.Errorf("the aggregate refresh published %+v", got)
	}
}

// TestTheDocumentedChannelsAreTheChannels: the notification table of docs/plugins.md lists exactly
// the channels the core publishes on (F-AC5).
func TestTheDocumentedChannelsAreTheChannels(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "plugins.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	table := text[strings.Index(text, "| Channel | Published when | Payload |"):]
	var documented []string
	for _, line := range strings.Split(table, "\n")[2:] {
		if !strings.HasPrefix(line, "| `") {
			break
		}
		documented = append(documented, strings.Trim(strings.Split(line, "|")[1], " `"))
	}
	sort.Strings(documented)
	channels := sqldb.Channels()
	sort.Strings(channels)
	if strings.Join(documented, " ") != strings.Join(channels, " ") {
		t.Errorf("the document lists %v, the core publishes on %v", documented, channels)
	}
}
