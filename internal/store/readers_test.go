package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// B8d and decision 10 of specs/SPEC-step-5b-widget-api.md: the read functions run on
// read-only connections, from one statement cache.

// TestTheReadFunctionsRunPreparedStatementsFromOneCache is AC31: a first call of each read
// function prepares its statements once, a second call prepares nothing more, and the
// statements came from the one cache the read connections hold.
func TestTheReadFunctionsRunPreparedStatementsFromOneCache(t *testing.T) {
	t.Parallel()
	network := storetest.Populated(t, 2, 6, 300, 2*86400)
	ctx := context.Background()
	readAll := func() {
		t.Helper()
		from, to := network.Now-86400-1234, network.Now
		if _, err := network.DB.ReadVolumeWindow(ctx, from, to, network.Now); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadConnectionTree(ctx, from, to, reads.TreeRootOwner); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadAttributionRate(ctx, from, to, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadInterfaceClients(ctx, from, to); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadCurrentRates(ctx, network.Now); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadPublicAddresses(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := network.DB.ReadSiteTotals(ctx, vocab.PeriodDay, from, to); err != nil {
			t.Fatal(err)
		}
	}
	before := network.DB.ReadStatementCount()
	readAll()
	first := network.DB.ReadStatementCount()
	if first <= before {
		t.Fatal("the read functions prepared nothing, so they did not run through the cache")
	}
	readAll()
	if second := network.DB.ReadStatementCount(); second != first {
		t.Errorf("a second round of reads prepared %d statements again", second-first)
	}
}

// TestTheReadConnectionsCannotWrite is decision 10's guarantee, restated by P7: the read
// connections log in as the read role, in read-only transactions, so a write that reached them is
// refused by PostgreSQL itself -- by the transaction's mode, and by the role's privileges.
func TestTheReadConnectionsCannotWrite(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := database.Reader.DB.ExecContext(ctx, "DELETE FROM setting"); err == nil ||
		!strings.Contains(err.Error(), "25006") {
		t.Errorf("a write on the read connections was not refused as a read-only transaction: %v", err)
	}
	connection, err := database.Reader.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN READ WRITE"); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "DELETE FROM setting"); err == nil || !strings.Contains(err.Error(), "42501") {
		t.Errorf("a write in a read-write transaction on the read connections was not refused by privilege: %v", err)
	}
	_, _ = connection.ExecContext(ctx, "ROLLBACK")
	_ = connection.Close()
	if _, err := database.Reader.ExecContext(ctx, "DELETE FROM setting"); err == nil {
		t.Error("the read pool's querier accepted a write")
	}
	if _, err := database.WidgetRow(ctx, "read_volume_slots", nil); err == nil {
		t.Error("a statement that is not a widget statement ran through WidgetRows")
	}
}
