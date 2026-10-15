package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestADatabaseFromBeforeStep5BIsRefusedNamingTheRebuild is the code side of the rebuild
// ROADMAP.md, step 5, documents: a database written by an earlier build holds
// source_availability without its outcome column, the in-place schema cannot absorb that, and
// Open refuses with ErrSchemaOutdated -- naming the remedy -- recognised from PostgreSQL's error
// code for an undefined column (P-AC5). The table is created as the earlier schema declared it.
func TestADatabaseFromBeforeStep5BIsRefusedNamingTheRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	empty, err := NewEmptyTestDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.Drop(context.Background()) })
	old, err := sqldb.OpenPool(empty.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE SCHEMA opnview",
		`CREATE TABLE opnview.source_availability (
		provider_id BIGINT PRIMARY KEY,
		state       TEXT NOT NULL
		            CHECK (state IN ('reachable', 'present_but_disabled', 'unavailable')),
		probe       TEXT NOT NULL,
		detail      TEXT,
		checked_at  BIGINT NOT NULL CHECK (checked_at >= 0 AND checked_at < 4102444800))`,
	} {
		if _, err := old.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, empty.DSN)
	if err == nil {
		_ = database.Close()
		t.Fatal("a database from an earlier build opened, so its missing column went unnoticed")
	}
	if !errors.Is(err, ErrSchemaOutdated) {
		t.Fatalf("the refusal is not ErrSchemaOutdated: %v", err)
	}
	if !strings.Contains(err.Error(), "outcome") || !strings.Contains(err.Error(), "rebuild") {
		t.Errorf("the refusal names neither the column nor the remedy: %v", err)
	}
}

// TestAFreshDatabaseIsNotReportedOutdated: the outdated-schema wording is reserved for a
// database some build wrote; a fresh one that opens is the ordinary case.
func TestAFreshDatabaseIsNotReportedOutdated(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatalf("reopening a database this build wrote: %v", err)
	}
	_ = reopened.Close()
}

// TestAHeldCancelledOrUnreachableStartIsNotReportedOutdated: a database this build wrote,
// failing to open for a reason that is not its schema -- another opnview holding it, a start
// cancelled half-way, a connection refused -- is never told to be rebuilt. Only a missing table
// or column is an outdated schema.
func TestAHeldCancelledOrUnreachableStartIsNotReportedOutdated(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)

	t.Run("held", func(t *testing.T) {
		reopened, err := Open(context.Background(), directory)
		if err == nil {
			_ = reopened.Close()
			t.Fatal("the database opened while another store held it")
		}
		if errors.Is(err, ErrSchemaOutdated) || !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("a held database was reported as %v", err)
		}
	})
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reopened, err := Open(ctx, directory)
		if err == nil {
			_ = reopened.Close()
			return
		}
		if errors.Is(err, ErrSchemaOutdated) {
			t.Fatalf("a cancelled start was reported as an outdated schema: %v", err)
		}
	})

	t.Run("refused", func(t *testing.T) {
		reopened, err := OpenWith(context.Background(), Options{
			DSN:  "host=127.0.0.1 port=1 user=opnview dbname=opnview sslmode=disable connect_timeout=1",
			Wait: 500 * time.Millisecond})
		if err == nil {
			_ = reopened.Close()
			t.Fatal("a refused connection opened")
		}
		if errors.Is(err, ErrSchemaOutdated) || !errors.Is(err, ErrDatabaseUnreachable) {
			t.Fatalf("a refused connection was reported as %v", err)
		}
	})
}
