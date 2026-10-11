//go:build reclassifycost

// This file is compiled only under the `reclassifycost` build tag, which
// ci/pre-deployment.sh passes in a step of its own (B8h of
// specs/SPEC-step-5b-widget-api.md). It is not under `predeployment`: that tag puts a
// test into the -race run, and a race-instrumented reclassification of a million rows
// would measure the race detector rather than the reclassification. It is not in the
// everyday suite either, which has a 60-second budget and builds no million-row
// database. The maintainer's decision 17: the cost is measured and recorded, with no
// budget, so the test fails only when the reclassification itself fails.

package collect

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// The seed's parameters at the scale size, the ones sql/schema-checks.sh passes for
// its million-row database (NOW, INTERFACES, CLIENTS, RULES, ALERTS, PAIR_ROWS, OWNERS,
// IPV6_EVERY and SCALE_FLOW_ROWS there), so the figure is measured on the same data
// the scale plans are.
var scaleSeed = map[string]any{
	"now": int64(1750000000), "interfaces": 6, "clients": 40, "rules": 12,
	"flow_rows": 1000000, "alerts": 500, "pair_rows": 2000, "owners": 3, "ipv6_every": 11,
}

// TestReclassifyEverythingWallTimeAtTheMillionRowSeed is AC35: it builds the
// million-row seed of sql/seed.sql, runs ReclassifyEverything on it twice, and prints
// both wall times. The second run is the idempotence the live validation checks (L11):
// it must change nothing, which the aggregate row counts before and after it assert.
func TestReclassifyEverythingWallTimeAtTheMillionRowSeed(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	seeded := time.Now()
	applySeed(t, database)
	var flows int64
	if err := database.DB().QueryRowContext(ctx, "SELECT count(*) FROM flow").Scan(&flows); err != nil {
		t.Fatal(err)
	}
	if flows < 1000000 {
		t.Fatalf("the seed holds %d flows, short of the million-row size", flows)
	}
	fmt.Printf("reclassify-cost: seeded %d flows in %s\n", flows, time.Since(seeded).Round(time.Millisecond))

	fake := newFakeFirewall(t)
	clock := newFixedClock(time.Unix(scaleSeed["now"].(int64), 0).UTC())
	collector := New(newFakeClient(t, fake), database, clock)

	started := time.Now()
	if err := collector.ReclassifyEverything(ctx); err != nil {
		t.Fatalf("reclassifying everything: %v", err)
	}
	first := time.Since(started)
	before := pairingAndSlots(t, database)

	started = time.Now()
	if err := collector.ReclassifyEverything(ctx); err != nil {
		t.Fatalf("reclassifying everything again: %v", err)
	}
	second := time.Since(started)
	if after := pairingAndSlots(t, database); after != before {
		t.Errorf("a second ReclassifyEverything changed the pairing or the slots: %s, then %s", before, after)
	}
	fmt.Printf("reclassify-cost: ReclassifyEverything over %d flows took %s; again, with nothing to move, %s\n",
		flows, first.Round(time.Millisecond), second.Round(time.Millisecond))
	if fake.requestCount() != 0 {
		t.Errorf("reclassifying made %d requests to the firewall", fake.requestCount())
	}
}

// applySeed runs sql/seed.sql with the scale parameters, as sqlite3's .param does for
// sql/schema-checks.sh: each statement gets the parameters it names.
func applySeed(t *testing.T, database *store.Store) {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "sql", "seed.sql"))
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}
	for _, statement := range store.SplitStatements(string(text)) {
		var arguments []any
		for name, value := range scaleSeed {
			if mentions(statement, name) {
				arguments = append(arguments, sql.Named(name, value))
			}
		}
		if _, err := database.DB().Exec(statement, arguments...); err != nil {
			t.Fatalf("seeding: %v\n%s", err, firstSQLLine(statement))
		}
	}
}

// mentions reports whether a statement names :name as a whole parameter.
func mentions(statement, name string) bool {
	token := ":" + name
	for offset := 0; ; {
		index := strings.Index(statement[offset:], token)
		if index < 0 {
			return false
		}
		after := offset + index + len(token)
		if after >= len(statement) || !strings.ContainsRune(
			"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_", rune(statement[after])) {
			return true
		}
		offset = after
	}
}

func firstSQLLine(statement string) string {
	for _, line := range strings.Split(statement, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return trimmed
		}
	}
	return ""
}

// pairingAndSlots summarises what a reclassification can change: the pairing and the
// hourly volume slots.
func pairingAndSlots(t *testing.T, database *store.Store) string {
	t.Helper()
	var pairs, slots, bytes int64
	if err := database.DB().QueryRow(`SELECT
		(SELECT count(*) FROM flow WHERE pair_outcome IS NOT NULL),
		(SELECT count(*) FROM volume_aggregate_1h),
		(SELECT ifnull(sum(bytes), 0) FROM volume_aggregate_1h)`).Scan(&pairs, &slots, &bytes); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d paired records, %d hourly rows, %d bytes", pairs, slots, bytes)
}
