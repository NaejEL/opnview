package store

import (
	"context"
	"strings"
	"testing"
)

// B8d and decision 10 of specs/SPEC-step-5b-widget-api.md: the read functions run on
// read-only connections, from one statement cache.

// TestTheReadFunctionsRunPreparedStatementsFromOneCache is AC31: a first call of each read
// function prepares its statements once, a second call prepares nothing more, and the
// statements came from the one cache the read connections hold.
func TestTheReadFunctionsRunPreparedStatementsFromOneCache(t *testing.T) {
	t.Parallel()
	network := populated(t, 2, 6, 300, 2*86400)
	ctx := context.Background()
	readAll := func() {
		t.Helper()
		from, to := network.now-86400-1234, network.now
		if _, err := network.db.ReadVolumeWindow(ctx, from, to, network.now); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadConnectionTree(ctx, from, to, TreeRootOwner); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadAttributionRate(ctx, from, to, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadInterfaceClients(ctx, from, to); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadCurrentRates(ctx, network.now); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadPublicAddresses(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := network.db.ReadSiteTotals(ctx, PeriodDay, from, to); err != nil {
			t.Fatal(err)
		}
	}
	before := network.db.ReadPreparedStatementCount()
	readAll()
	first := network.db.ReadPreparedStatementCount()
	if first <= before {
		t.Fatal("the read functions prepared nothing, so they did not run through the cache")
	}
	readAll()
	if second := network.db.ReadPreparedStatementCount(); second != first {
		t.Errorf("a second round of reads prepared %d statements again", second-first)
	}
}

// TestTheReadConnectionsCannotWrite is decision 10's guarantee: the read connections are
// query_only, so a write that reached them is refused by SQLite itself.
func TestTheReadConnectionsCannotWrite(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := database.reader.db.ExecContext(ctx, "DELETE FROM setting"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "readonly") && !strings.Contains(err.Error(), "query_only") {
		t.Errorf("a write on the read connections was not refused: %v", err)
	}
	if _, err := database.reader.ExecContext(ctx, "DELETE FROM setting"); err == nil {
		t.Error("the read pool's querier accepted a write")
	}
	if _, err := database.WidgetRow(ctx, "read_volume_slots", nil); err == nil {
		t.Error("a statement that is not a widget statement ran through WidgetRows")
	}
}
