package store_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// testDatabase creates a database of the test's own, cloned from the schema's template, and drops
// it when the test ends. It returns the connection parameter.
func testDatabase(t testing.TB) string {
	t.Helper()
	database, err := store.NewTestDatabase(context.Background(), store.TestDatabaseOptions{})
	if err != nil {
		t.Fatalf("creating the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping the test database: %v", err)
		}
	})
	return database.DSN
}

// TestTheCloneStrategyIsTheInstancesSetting is E.1.2's FILE_COPY against WAL_LOG: the strategy a test
// database is cloned with is the one its instance's setting names, FILE_COPY when it names none, and a
// value that is neither is refused rather than read as one of them.
func TestTheCloneStrategyIsTheInstancesSetting(t *testing.T) {
	for value, want := range map[string]string{"": "FILE_COPY", "FILE_COPY": "FILE_COPY", "WAL_LOG": "WAL_LOG",
		"wal_log": ""} {
		t.Setenv(store.CloneStrategyVariable, value)
		strategy, err := store.CloneStrategy()
		if want == "" {
			if err == nil {
				t.Errorf("%q was read as %s", value, strategy)
			}
			continue
		}
		if err != nil || strategy != want {
			t.Errorf("%q gave %q (%v), not %s", value, strategy, err, want)
		}
	}
}
