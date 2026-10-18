//go:build reclassifycost

// This file is compiled only under the `reclassifycost` build tag, which
// sql/schema-checks.sh passes in a section of its own (B8h of
// specs/SPEC-step-5b-widget-api.md). It is not under `predeployment`: that tag puts a
// test into the -race run, and a race-instrumented reclassification of a million rows
// would measure the race detector rather than the reclassification. It is not in the
// everyday suite either, which has a 60-second budget and builds no million-row
// database. The maintainer's decision 17: the cost is measured and recorded, with no
// budget, so the test fails only when the reclassification itself fails.

package collect_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/store"
)

// The two variables sql/schema-checks.sh sets when it runs this test.
const (
	// reclassifySeedDatabaseVariable is the PostgreSQL connection string of a copy of the
	// million-row seed schema-checks has already built, planned and timed the widget
	// budgets on. Reusing it rather than seeding a second million rows here saves the
	// minutes the seed takes.
	reclassifySeedDatabaseVariable = "OPNVIEW_RECLASSIFY_SEED_DATABASE"
	// reclassifySeedNowVariable is the seed's own instant, NOW there.
	reclassifySeedNowVariable = "OPNVIEW_RECLASSIFY_SEED_NOW"
)

// TestReclassifyEverythingWallTimeAtTheMillionRowSeed is AC35: it opens the million-row
// seed sql/schema-checks.sh built, runs ReclassifyEverything on it twice, and prints both
// wall times. The second run is the idempotence the live validation checks (L11): it must
// change nothing, which the pairing and slot figures before and after it assert.
//
// It is not run on its own: without the seed schema-checks hands it, there is nothing at
// the size the figure is defined for, and it says so and fails rather than measure a
// smaller database under the same name.
func TestReclassifyEverythingWallTimeAtTheMillionRowSeed(t *testing.T) {
	dsn, rawNow := os.Getenv(reclassifySeedDatabaseVariable), os.Getenv(reclassifySeedNowVariable)
	if dsn == "" || rawNow == "" {
		t.Fatalf("%s and %s are not set: this measurement runs inside sql/schema-checks.sh, on the "+
			"million-row seed it built", reclassifySeedDatabaseVariable, reclassifySeedNowVariable)
	}
	now, err := strconv.ParseInt(rawNow, 10, 64)
	if err != nil {
		t.Fatalf("%s is %q, not an instant: %v", reclassifySeedNowVariable, rawNow, err)
	}
	ctx := context.Background()
	database, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("opening the seed: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var flows int64
	if err := database.DB().QueryRowContext(ctx, "SELECT count(*) FROM flow").Scan(&flows); err != nil {
		t.Fatal(err)
	}
	if flows < 1000000 {
		t.Fatalf("the seed holds %d flows, short of the million-row size", flows)
	}

	fake := providertest.NewFirewall(t)
	clock := collecttest.NewFixedClock(time.Unix(now, 0).UTC())
	collector := collecttest.NewTestCollector(t, providertest.NewClient(t, fake), database, clock)

	started := time.Now()
	if err := collector.Derive(ctx, derive.Derivation{Everything: true}); err != nil {
		t.Fatalf("reclassifying everything: %v", err)
	}
	first := time.Since(started)
	before := pairingAndSlots(t, database)

	started = time.Now()
	if err := collector.Derive(ctx, derive.Derivation{Everything: true}); err != nil {
		t.Fatalf("reclassifying everything again: %v", err)
	}
	second := time.Since(started)
	if after := pairingAndSlots(t, database); after != before {
		t.Errorf("a second ReclassifyEverything changed the pairing or the slots: %s, then %s", before, after)
	}
	fmt.Printf("reclassify-cost: ReclassifyEverything over %d flows took %s; again, with nothing to move, %s\n",
		flows, first.Round(time.Millisecond), second.Round(time.Millisecond))
	if fake.RequestCount() != 0 {
		t.Errorf("reclassifying made %d requests to the firewall", fake.RequestCount())
	}
}

// pairingAndSlots summarises what a reclassification can change: the pairing and the
// hourly volume slots.
func pairingAndSlots(t *testing.T, database *store.Store) string {
	t.Helper()
	var pairs, slots, bytes int64
	if err := database.DB().QueryRow(`SELECT
		(SELECT count(*) FROM flow WHERE pair_outcome IS NOT NULL),
		(SELECT count(*) FROM volume_aggregate_1h),
		(SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h)`).Scan(&pairs, &slots, &bytes); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d paired records, %d hourly rows, %d bytes", pairs, slots, bytes)
}
