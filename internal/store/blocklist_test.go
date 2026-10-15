package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestBlocklistLastSeenNeverMovesBackwardsOnReingestion is AC33 of
// specs/SPEC-step-5b-widget-api.md, under decision 14: first_seen_at and last_seen_at
// are the earliest and the latest looked_up_at of a lookup naming the list, whatever
// order the lookups are ingested in and however late, and re-ingesting an older
// lookup moves neither backwards. The lookups are synthesised; the list name is not
// any real list's.
func TestBlocklistLastSeenNeverMovesBackwardsOnReingestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestStore(t)
	const base int64 = 1750000000
	const list = "an observed list name"

	ingest := func(index int, lookedUpAt, ingestedAt int64) {
		t.Helper()
		if err := database.InsertDNSResolution(ctx, vocab.DNSResolution{
			LookupKey:     fmt.Sprintf("blocklist-test-%d", index),
			ClientAddress: "192.0.2.10",
			Domain:        fmt.Sprintf("blocked-%d.example", index),
			Resolver:      "unbound",
			Action:        "block",
			BlocklistName: list,
			LookedUpAt:    lookedUpAt,
			IngestedAt:    ingestedAt,
		}); err != nil {
			t.Fatalf("ingesting lookup %d: %v", index, err)
		}
	}
	seen := func() (int64, int64) {
		t.Helper()
		var first, last int64
		if err := database.DB().QueryRowContext(ctx,
			"SELECT first_seen_at, last_seen_at FROM blocklist WHERE name = ?", list).
			Scan(&first, &last); err != nil {
			t.Fatalf("reading the blocklist row: %v", err)
		}
		return first, last
	}

	// A lookup ingested long after it was made: the instants are the lookup's.
	ingest(1, base+100, base+900)
	if first, last := seen(); first != base+100 || last != base+100 {
		t.Errorf("after one lookup the list reads first %d, last %d, not both %d",
			first, last, base+100)
	}
	// A newer lookup moves last_seen_at forwards only.
	ingest(2, base+200, base+950)
	if first, last := seen(); first != base+100 || last != base+200 {
		t.Errorf("after a newer lookup the list reads first %d, last %d", first, last)
	}
	// An OLDER lookup ingested later -- the ring buffer offering it again, or a pass
	// meeting it after a newer one -- widens first_seen_at and leaves last_seen_at.
	ingest(3, base+50, base+1000)
	if first, last := seen(); first != base+50 || last != base+200 {
		t.Errorf("after an older lookup ingested later the list reads first %d, last %d, "+
			"not %d and %d", first, last, base+50, base+200)
	}

	// The live check L13's query, which must count 0.
	var behind int64
	if err := database.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM blocklist b
		 WHERE b.last_seen_at < (SELECT max(r.looked_up_at) FROM dns_resolution r
		                         WHERE r.blocklist_id = b.id)`).Scan(&behind); err != nil {
		t.Fatalf("running the L13 query: %v", err)
	}
	if behind != 0 {
		t.Errorf("%d blocklist rows have a last_seen_at behind their latest lookup", behind)
	}
}
