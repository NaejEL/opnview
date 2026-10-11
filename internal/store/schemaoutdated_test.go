package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestADatabaseFromBeforeStep5BIsRefusedNamingTheRebuild is the code side of the rebuild
// ROADMAP.md, step 5, documents: a database written by a build before step 5B holds
// source_availability without its outcome column, the in-place schema cannot absorb that,
// and Open refuses with ErrSchemaOutdated -- naming the remedy -- rather than with SQLite's
// "no column named outcome" alone. The table is created exactly as a1e2707's schema.sql
// declared it.
func TestADatabaseFromBeforeStep5BIsRefusedNamingTheRebuild(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, DatabaseFilename)
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE source_availability (
		provider_id INTEGER PRIMARY KEY,
		state       TEXT NOT NULL
		            CHECK (state IN ('reachable', 'present_but_disabled', 'unavailable')),
		probe       TEXT NOT NULL,
		detail      TEXT,
		checked_at  INTEGER NOT NULL CHECK (checked_at >= 0 AND checked_at < 4102444800))`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(context.Background(), directory)
	if err == nil {
		_ = database.Close()
		t.Fatal("a database from before step 5B opened, so its missing column went unnoticed")
	}
	if !errors.Is(err, ErrSchemaOutdated) {
		t.Fatalf("the refusal is not ErrSchemaOutdated: %v", err)
	}
	if !strings.Contains(err.Error(), "outcome") || !strings.Contains(err.Error(), "aside") {
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
