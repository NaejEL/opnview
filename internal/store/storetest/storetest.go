// Package storetest gives a test a database of its own: a clone of the schema's template on the
// test instance (store.NewTestDatabase), opened, and dropped when the test ends.
package storetest

import (
	"context"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// Database creates a database for the test and returns its connection parameter. It is dropped,
// with its read role, when the test and its subtests end.
func Database(t testing.TB) string {
	t.Helper()
	database, err := store.NewTestDatabase(context.Background())
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

// EmptyDatabase creates a database with nothing applied to it, for a test of what Open does to an
// empty or an outdated database.
func EmptyDatabase(t testing.TB) string {
	t.Helper()
	database, err := store.NewEmptyTestDatabase(context.Background())
	if err != nil {
		t.Fatalf("creating the empty test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping the test database: %v", err)
		}
	})
	return database.DSN
}

// Open creates a database for the test and opens it. The store is closed, and the database
// dropped, when the test ends.
func Open(t testing.TB) *store.Store {
	t.Helper()
	dsn := Database(t)
	database, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database: %v", err)
		}
	})
	return database
}

// A fixture built once per process: its database is filled by the first test that asks for it,
// becomes a template, and every test -- the first included -- gets a clone of its own, so a fixture
// that takes seconds to derive costs a clone per test. What the build returns besides the database
// is handed to every caller.

type builtFixture struct {
	once     sync.Once
	value    any
	template *store.TestDatabase
	failed   bool
}

var (
	fixturesMutex sync.Mutex
	fixtures      = map[string]*builtFixture{}
)

// Fixture returns the value build returned for the key, and a database cloned from the one build
// filled. build runs once per process and key, on the first caller's test, against a database of
// its own, which it must leave with no connection open. The clone is dropped when the test ends;
// the template when DropFixtures runs, at the end of the package's tests.
func Fixture[T any](t testing.TB, key string, build func(t testing.TB, dsn string) T) (T, string) {
	t.Helper()
	fixturesMutex.Lock()
	entry, found := fixtures[key]
	if !found {
		entry = &builtFixture{}
		fixtures[key] = entry
	}
	fixturesMutex.Unlock()
	entry.once.Do(func() {
		entry.failed = true
		template, err := store.NewTestDatabase(context.Background())
		if err != nil {
			t.Fatalf("creating the fixture database: %v", err)
		}
		entry.template = template
		entry.value = build(t, template.DSN)
		entry.failed = false
	})
	var zero T
	if entry.failed {
		t.Fatalf("the fixture %s failed to build in another test", key)
		return zero, ""
	}
	clone, err := store.NewTestDatabaseFrom(context.Background(), entry.template)
	if err != nil {
		t.Fatalf("cloning the fixture %s: %v", key, err)
	}
	t.Cleanup(func() {
		if err := clone.Drop(context.Background()); err != nil {
			t.Errorf("dropping the fixture clone: %v", err)
		}
	})
	value, _ := entry.value.(T)
	return value, clone.DSN
}

// DropFixtures drops every fixture template the process built. A package whose tests use Fixture
// calls it from its TestMain, after the tests ran.
func DropFixtures() {
	fixturesMutex.Lock()
	defer fixturesMutex.Unlock()
	for key, entry := range fixtures {
		if entry.template != nil {
			_ = entry.template.Drop(context.Background())
		}
		delete(fixtures, key)
	}
}
