package store

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestOnlyTheTemplatesOfAnotherBaseAreRemoved is decision 6 of the performance cycle (18 October 2026):
// a template whose comment names another base template than this build's is dropped by its exact name;
// one naming this build's base, and a database carrying no such comment, are left where they are.
func TestOnlyTheTemplatesOfAnotherBaseAreRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := os.Getenv(TestDatabaseVariable)
	pool, err := sqldb.OpenPool(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	current, err := baseTemplateName()
	if err != nil {
		t.Fatal(err)
	}
	create := func(comment string) string {
		name, err := uniqueName("opnview_test_")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize())
		})
		if comment != "" {
			if _, err := pool.ExecContext(ctx, "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS "+
				quoteLiteral(comment)); err != nil {
				t.Fatal(err)
			}
		}
		return name
	}
	stale := create(templateMarker + "opnview_template_0000000000000000")
	kept := create(templateMarker + current)
	unmarked := create("")
	dropped, err := RemoveStaleTestTemplates(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dropped, stale) || slices.Contains(dropped, kept) || slices.Contains(dropped, unmarked) {
		t.Errorf("dropped %v: want %s among them, and neither %s nor %s", dropped, stale, kept, unmarked)
	}
	for name, want := range map[string]bool{stale: false, kept: true, unmarked: true} {
		var exists bool
		if err := pool.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).
			Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Errorf("%s exists %v after the removal, want %v", name, exists, want)
		}
	}
}
