package sqldb_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestARegisteredStatementIsNeverTranslatedWhenItRuns is PA-AC23's translation clause (PA15 of
// specs/SPEC-backend-completion-plugins.md): a statement file is translated when it is registered,
// running its statements translates nothing, and a text first seen at run time is translated once.
// Not parallel, so no other test of the package translates a text while it counts.
func TestARegisteredStatementIsNeverTranslatedWhenItRuns(t *testing.T) {
	database := storetest.Open(t)
	ctx := context.Background()
	text, err := sqldb.Statement("dirty_hours")
	if err != nil {
		t.Fatal(err)
	}
	before := sqldb.TranslationCount()
	for range 5 {
		rows, err := sqldb.Query(ctx, database.SQL, text, map[string]any{"since": int64(0)})
		if err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		if _, err := sqldb.QueryInts(ctx, database.SQL, "dirty_hours", map[string]any{"since": int64(0)}); err != nil {
			t.Fatal(err)
		}
	}
	if after := sqldb.TranslationCount(); after != before {
		t.Errorf("running a registered statement ten times translated %d texts, want none", after-before)
	}
	adhoc := "SELECT count(*) FROM flow WHERE observed_at >= :since AND 'example_translation' <> ''"
	for range 3 {
		if err := sqldb.QueryRow(ctx, database.SQL, adhoc, map[string]any{"since": int64(0)}).Scan(new(int64)); err != nil {
			t.Fatal(err)
		}
	}
	if after := sqldb.TranslationCount(); after != before+1 {
		t.Errorf("a new text run three times was translated %d times, want once", after-before)
	}
}
