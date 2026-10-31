package purge_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The retention purge runs against the schema as it is: every table it names exists. It is
// what the live validation of 31 October 2026 found broken -- the purge still named
// state_snapshot, deleted with wave 4c of ADR 0020, and one failing statement rolls the whole
// purge back, so nothing was purged at all -- and nothing in the suite had run the purge.
func TestThePurgeRunsAgainstTheCurrentSchema(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	if err := database.Purge(context.Background(), time.Now().Unix()); err != nil {
		t.Fatalf("the purge: %v", err)
	}
}
