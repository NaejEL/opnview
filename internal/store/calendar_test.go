//go:build predeployment

// This test is moved out of the everyday suite (ROADMAP.md, "Moving a test out of the everyday
// suite"): it refreshes every slot of every period over 960 days, twice, which costs about nine
// seconds of the store package's budget on its own; the refresh-contract and corrections tests of
// the everyday suite cover each correction on a short span.

package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestEverySlotEqualsTheFlowsAcrossTheCalendarAndItsCorrections is P-AC9's equivalence test: every
// slot of every family, at 1h, 24h, 7d and 30d, equals the same sums computed independently from
// the raw flows, over a fixture spanning a 28-, a 29-, a 30- and a 31-day month and an ISO week
// that crosses a year boundary -- and again after a late ingestion, an owner reassignment and a
// reclassification that re-points an identity. The paired connection, the retention shorter than an
// hour and the gaps are proven by the tests named in docs/data-model.md, "Storage fit", which this
// one does not restate.
func TestEverySlotEqualsTheFlowsAcrossTheCalendarAndItsCorrections(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 3, 9)
	ctx := context.Background()
	// Unlimited retention: every slot of the span is computed and kept.
	if err := network.db.SetSetting(ctx, "retention_seconds", "0", network.now); err != nil {
		t.Fatal(err)
	}
	const span = 960 * 86400
	addresses := network.insert(t, network.flowsFor(400, span))
	network.classifyAndRefresh(t, addresses)
	if _, err := network.db.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
		VALUES (1, 'example owner', 0, 0), (2, 'second example owner', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0 WHERE id % 2 = 0`); err != nil {
		t.Fatal(err)
	}
	attributeNetwork(t, network)
	recomputeEverything(t, network)

	// The calendar the fixture spans.
	from := network.now - span
	months := map[int]bool{}
	for start := PeriodMonth.SlotStart(from); start < network.now; start = PeriodMonth.SlotEnd(start) {
		months[int((PeriodMonth.SlotEnd(start)-start)/86400)] = true
	}
	for _, days := range []int{28, 29, 30, 31} {
		if !months[days] {
			t.Fatalf("the fixture spans no %d-day month", days)
		}
	}
	crossing := int64(0)
	for start := PeriodWeek.SlotStart(from); start < network.now; start = PeriodWeek.SlotEnd(start) {
		if time.Unix(start, 0).UTC().Year() != time.Unix(PeriodWeek.SlotEnd(start)-1, 0).UTC().Year() {
			crossing = start
		}
	}
	if crossing == 0 {
		t.Fatal("the fixture spans no ISO week crossing a year boundary")
	}

	compare := func(stage string) {
		t.Helper()
		flows := readFlows(t, network.db)
		for _, period := range Periods() {
			for _, family := range allFamilies {
				expected := expectedFamily(flows, period, family)
				stored := storedFamily(t, network.db, period, family)
				for key, want := range expected {
					if got := stored[key]; got != want {
						t.Errorf("%s: %s %s slot %s holds %+v, the flows sum to %+v", stage, family, period.Name,
							key, got, want)
					}
				}
				for key := range stored {
					if _, present := expected[key]; !present {
						t.Errorf("%s: %s %s holds the slot %s that no flow produces", stage, family, period.Name, key)
					}
				}
			}
		}
		if bytes := queryInt(t, network.db, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_7d "+
			"WHERE period_start_at = ?", crossing); bytes == 0 {
			t.Errorf("%s: the week crossing the year boundary holds nothing", stage)
		}
	}
	compare("after the first refresh")

	// A late ingestion into a closed slot of the oldest month, an owner reassigned, and a client
	// re-pointed by a lease naming its address.
	oldest := network.clients[0]
	late := network.insert(t, []networkFlow{{observedAt: from + 3600, device: oldest.device, direction: "in",
		src: oldest.address, dst: outside(5, oldest.v6), protocol: "tcp", action: "pass", bytes: 999}})
	if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 2, owner_assigned_at = ? WHERE owner_id = 1`,
		network.now); err != nil {
		t.Fatal(err)
	}
	var unleased testClient
	for _, candidate := range network.clients {
		if !candidate.leased {
			unleased = candidate
			break
		}
	}
	mac := "00:00:00:00:fe:02"
	if _, err := network.db.UpsertClient(ctx, Client{Identity: ClientIdentity{Kind: IdentityMAC, Key: mac},
		InterfaceID: &unleased.interfaceID, MAC: &mac, LastAddress: &unleased.address}, network.now); err != nil {
		t.Fatal(err)
	}
	if _, err := network.db.Reclassify(ctx, append(late, unleased.address), network.now); err != nil {
		t.Fatal(err)
	}
	recomputeEverything(t, network)
	compare(fmt.Sprintf("after the corrections at %d", network.now))
}
