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
	before := network.db.ReadStatementCount()
	readAll()
	first := network.db.ReadStatementCount()
	if first <= before {
		t.Fatal("the read functions prepared nothing, so they did not run through the cache")
	}
	readAll()
	if second := network.db.ReadStatementCount(); second != first {
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
	if _, err := database.reader.db.ExecContext(ctx, "DELETE FROM setting"); err == nil ||
		!strings.Contains(err.Error(), "25006") {
		t.Errorf("a write on the read connections was not refused as a read-only transaction: %v", err)
	}
	connection, err := database.reader.db.Conn(ctx)
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
	if _, err := database.reader.ExecContext(ctx, "DELETE FROM setting"); err == nil {
		t.Error("the read pool's querier accepted a write")
	}
	if _, err := database.WidgetRow(ctx, "read_volume_slots", nil); err == nil {
		t.Error("a statement that is not a widget statement ran through WidgetRows")
	}
}

// TestTheReadRoleCannotReadASecretNorRaiseItself is P-AC12's second and third halves: each object
// the read role holds nothing on fails with a permission error when read directly on the read
// connections, and no function called inside a SELECT gives the role what it does not hold.
func TestTheReadRoleCannotReadASecretNorRaiseItself(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	for _, object := range unreadableObjects {
		t.Run(object, func(t *testing.T) {
			if _, err := database.reader.db.QueryContext(ctx, "SELECT * FROM "+object); err == nil ||
				!strings.Contains(err.Error(), "42501") {
				t.Errorf("reading %s on the read connections gave %v, not a permission error", object, err)
			}
		})
	}
	for name, statement := range map[string]string{
		"taking another role":    "SELECT set_config('role', 'postgres', false)",
		"reading a server file":  "SELECT pg_read_file('postgresql.conf')",
		"listing a directory":    "SELECT pg_ls_dir('.')",
		"reading through a view": "SELECT count(*) FROM pg_catalog.pg_authid",
	} {
		rows, err := database.reader.db.QueryContext(ctx, statement)
		if err == nil {
			err = rows.Err()
			for rows.Next() {
			}
			_ = rows.Close()
		}
		if err == nil {
			t.Errorf("%s succeeded on the read connections", name)
		}
	}
	var role string
	if err := database.reader.db.QueryRowContext(ctx, "SELECT current_user").Scan(&role); err != nil ||
		role != database.reader.role {
		t.Errorf("the read connections run as %q (%v)", role, err)
	}
}
