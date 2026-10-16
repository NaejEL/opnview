package reads_test

import (
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestTheRecentGapsAreReadThroughTheirIndex is AC20's plan half: the read is a search
// of idx_collection_gap_detected_at, and never a scan of collection_gap.
func TestTheRecentGapsAreReadThroughTheirIndex(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	plan := storetest.PlanOf(t, database, reads.RecentGapsStatement, int64(0))
	if !storetest.UsesIndex(plan, "idx_collection_gap_detected_at", "") {
		t.Errorf("the read does not search the detected_at index: %+v", plan)
	}
	if storetest.Scans(plan, "collection_gap") {
		t.Errorf("the read scans collection_gap: %+v", plan)
	}
}
