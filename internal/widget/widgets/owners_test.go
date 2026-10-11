package widgets

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/widget"
)

// C1 of the backend-completion cycle seen through the widget API: a change of ownership, a label
// and a purpose written through the store's own write paths, and the next responses.

// ownerTotals sums an owner_activity response's cards per owner id, the unassigned card under 0.
func ownerTotals(t *testing.T, response widget.Response) (map[int64]int64, int64) {
	t.Helper()
	decoded := decodeData[ownerActivityData](t, response)
	totals := map[int64]int64{}
	var sum int64
	for _, card := range decoded.Cards {
		if card.Owner != nil {
			totals[card.Owner.ID] = card.Volume.Bytes
			sum += card.Volume.Bytes
		}
	}
	if decoded.Unassigned != nil {
		totals[0] = decoded.Unassigned.Volume.Bytes
		sum += decoded.Unassigned.Volume.Bytes
	}
	return totals, sum
}

// staleSlots is the stale hour slots a response's freshness reports.
func staleSlots(response widget.Response) int64 {
	var stale int64
	for _, entry := range response.Freshness {
		stale += entry.StaleSlots
	}
	return stale
}

// TestAReassignmentMovesTheClientsVolumeToItsNewOwnerAfterTheNextDerivation is C-AC5: after a
// reassignment and the next derivation, owner_activity at 1h, 24h, 7d and 30d attributes the
// client's volume to the new owner, and the owners plus the unassigned card sum to the value before;
// before the derivation, the slots holding the client's traffic are reported stale.
func TestAReassignmentMovesTheClientsVolumeToItsNewOwnerAfterTheNextDerivation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	periods := []string{"1h", "24h", "7d", "30d"}
	from, to := f.owners[0], f.owners[1]
	// The client assigned to the first person, which the fixture gave traffic in every period.
	client := f.scalar(t, "SELECT id FROM client WHERE owner_id = ? ORDER BY id LIMIT 1", from)

	before := map[string]map[int64]int64{}
	sums := map[string]int64{}
	stale := map[string]int64{}
	for _, period := range periods {
		response := f.respond(t, "owner_activity", map[string]any{"period": period})
		before[period], sums[period] = ownerTotals(t, response)
		stale[period] = staleSlots(response)
	}
	if before["30d"][from] == 0 {
		t.Fatal("the fixture gave the first person no traffic, so the test proves nothing")
	}
	changes, err := f.db.AssignClients(ctx, to, []int64{client}, f.now)
	if err != nil || !changes[0].Changed {
		t.Fatalf("reassigning: %+v (%v)", changes, err)
	}
	for _, period := range []string{"24h", "7d", "30d"} {
		response := f.respond(t, "owner_activity", map[string]any{"period": period})
		if got := staleSlots(response); got <= stale[period] {
			t.Errorf("%s: before the derivation the response reports %d stale slots, as before the reassignment",
				period, got)
		}
	}
	if _, err := f.db.RefreshAggregates(ctx, f.now, f.now, nil); err != nil {
		t.Fatalf("deriving: %v", err)
	}
	for _, period := range periods {
		response := f.respond(t, "owner_activity", map[string]any{"period": period})
		totals, sum := ownerTotals(t, response)
		if sum != sums[period] {
			t.Errorf("%s: the owners and the unassigned card sum to %d, %d before", period, sum, sums[period])
		}
		moved := before[period][from] - totals[from]
		if moved < 0 || totals[to] != before[period][to]+moved || totals[0] != before[period][0] {
			t.Errorf("%s: the volume moved from %v to %v; the client's share must move from the first person to the second",
				period, before[period], totals)
		}
		if period != "1h" && moved == 0 {
			t.Errorf("%s: nothing moved to the new owner", period)
		}
		if got := staleSlots(response); got != stale[period] {
			t.Errorf("%s: after the derivation %d slots are stale, %d before the reassignment", period, got, stale[period])
		}
	}
}

// TestALabelAndAPurposeAreVisibleInTheNextResponse is C-AC6: a label written through the store's
// write path is in the next interface_volume_ranking, and a purpose in the next
// blocked_dns_lookups.
func TestALabelAndAPurposeAreVisibleInTheNextResponse(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	label := "Example new label"
	target := f.interfaces[1].id
	if _, err := f.db.SetInterfaceUserLabel(ctx, target, &label); err != nil {
		t.Fatal(err)
	}
	ranking := decodeData[interfaceRankingData](t, f.respond(t, "interface_volume_ranking", nil))
	found := false
	for _, bar := range ranking.Bars {
		if bar.Interface.ID == target {
			found = true
			if bar.Interface.UserLabel == nil || *bar.Interface.UserLabel != label {
				t.Errorf("the ranking carries the label %v", bar.Interface.UserLabel)
			}
		}
	}
	if !found {
		t.Fatal("the labelled interface is not in the ranking")
	}

	list := f.scalar(t, "SELECT id FROM blocklist WHERE name = 'example-list-b'")
	purpose := "parental"
	if _, err := f.db.SetBlocklistPurpose(ctx, list, &purpose, f.now); err != nil {
		t.Fatal(err)
	}
	lookups := decodeData[blockedLookupsData](t, f.respond(t, "blocked_dns_lookups", nil))
	found = false
	for _, row := range lookups.Rows {
		if row.List != nil && *row.List == "example-list-b" {
			found = true
			if row.Purpose != purpose {
				t.Errorf("the refused lookups carry the purpose %q", row.Purpose)
			}
		}
	}
	if !found {
		t.Fatal("the list is not in the refused lookups")
	}
}
