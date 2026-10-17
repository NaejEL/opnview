package store

import (
	"context"
	"fmt"
	"os"
	"time"
)

// SQLFiles is the store's embedded SQL, for this package's external tests.
var SQLFiles = sqlFiles

// CloneStrategy is cloneStrategy.
func CloneStrategy() (string, error) { return cloneStrategy() }

// NewEmptyTestDatabase creates a database with nothing in it, not even the schema, for a test of
// what Open does to an empty or an outdated database.
func NewEmptyTestDatabase(ctx context.Context) (*TestDatabase, error) {
	admin := os.Getenv(TestDatabaseVariable)
	if admin == "" {
		return nil, fmt.Errorf("store: %s is not set", TestDatabaseVariable)
	}
	name, err := uniqueName("opnview_test_")
	if err != nil {
		return nil, err
	}
	started := time.Now()
	if err := createDatabase(ctx, admin, name, ""); err != nil {
		return nil, err
	}
	dsn, err := withDatabase(admin, name)
	if err != nil {
		return nil, err
	}
	return newTestDatabase(dsn, name, admin, started), nil
}
