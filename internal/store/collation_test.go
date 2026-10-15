package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestTextOrdersAsBytesAndFoldsAsciiAlone is P-AC7's first half: on the database opnview runs on,
// mixed-case and non-ASCII names order exactly as their bytes do, as SQLite ordered them, and
// lower() folds ASCII letters and leaves every other letter as it is, as SQLite's lower() did.
func TestTextOrdersAsBytesAndFoldsAsciiAlone(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	names := []string{"beta", "Beta", "BETA", "école", "École", "zeta", "Zeta", "_under", "alpha-2",
		"alpha10", "alpha2", "über", "Uber", "日本"}
	rows, err := database.DB().Query("SELECT name FROM (SELECT unnest(CAST(? AS text[])) AS name) AS n ORDER BY name",
		"{"+joinQuoted(names)+"}")
	if err != nil {
		t.Fatal(err)
	}
	var ordered []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		ordered = append(ordered, name)
	}
	_ = rows.Close()
	expected := append([]string(nil), names...)
	sort.Strings(expected)
	if !slices.Equal(ordered, expected) {
		t.Errorf("the database orders %q, byte order is %q", ordered, expected)
	}
	var folded string
	if err := database.DB().QueryRow("SELECT lower(?)", "ÉCOLE.Example.ÜBER").Scan(&folded); err != nil {
		t.Fatal(err)
	}
	if folded != "École.example.Über" {
		t.Errorf("lower() folds to %q, not ASCII alone", folded)
	}
}

// joinQuoted renders names as the elements of a PostgreSQL array literal.
func joinQuoted(names []string) string {
	result := ""
	for index, name := range names {
		if index > 0 {
			result += ","
		}
		result += `"` + name + `"`
	}
	return result
}

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
