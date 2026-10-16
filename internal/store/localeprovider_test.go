package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestADatabaseThatDoesNotOrderByBytesIsRefused: a database created with another locale provider
// -- ICU here, which still reports a C collation name -- is refused at start, naming the reason.
func TestADatabaseThatDoesNotOrderByBytesIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := os.Getenv(TestDatabaseVariable)
	name, err := uniqueName("opnview_test_")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := sqldb.OpenPool(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	if _, err := pool.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+
		" TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'en-US' LOCALE 'C'"); err != nil {
		t.Fatalf("creating a database with the ICU provider: %v", err)
	}
	database := &TestDatabase{name: name, admin: admin}
	t.Cleanup(func() { _ = database.Drop(context.Background()) })
	dsn, err := withDatabase(admin, name)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, dsn)
	if err == nil {
		_ = opened.Close()
	}
	if !errors.Is(err, ErrCollation) {
		t.Errorf("a database ordering by ICU gave %v, not ErrCollation", err)
	}
}
