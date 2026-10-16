package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
	network := storetest.NewNetwork(t, 3, 9)
	ctx := context.Background()
	// Unlimited retention: every slot of the span is computed and kept.
	if err := network.DB.SetSetting(ctx, "retention_seconds", "0", network.Now); err != nil {
		t.Fatal(err)
	}
	const span = 960 * 86400
	addresses := network.Insert(t, network.FlowsFor(400, span))
	network.ClassifyAndRefresh(t, addresses)
	if _, err := network.DB.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
		VALUES (1, 'example owner', 0, 0), (2, 'second example owner', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := network.DB.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0 WHERE id % 2 = 0`); err != nil {
		t.Fatal(err)
	}
	attributeNetwork(t, network)
	recomputeEverything(t, network)

	// The calendar the fixture spans.
	from := network.Now - span
	months := map[int]bool{}
	for start := vocab.PeriodMonth.SlotStart(from); start < network.Now; start = vocab.PeriodMonth.SlotEnd(start) {
		months[int((vocab.PeriodMonth.SlotEnd(start)-start)/86400)] = true
	}
	for _, days := range []int{28, 29, 30, 31} {
		if !months[days] {
			t.Fatalf("the fixture spans no %d-day month", days)
		}
	}
	crossing := int64(0)
	for start := vocab.PeriodWeek.SlotStart(from); start < network.Now; start = vocab.PeriodWeek.SlotEnd(start) {
		if time.Unix(start, 0).UTC().Year() != time.Unix(vocab.PeriodWeek.SlotEnd(start)-1, 0).UTC().Year() {
			crossing = start
		}
	}
	if crossing == 0 {
		t.Fatal("the fixture spans no ISO week crossing a year boundary")
	}

	compare := func(stage string) {
		t.Helper()
		flows := readFlows(t, network.DB)
		for _, period := range vocab.Periods() {
			for _, family := range allFamilies {
				expected := expectedFamily(flows, period, family)
				stored := storedFamily(t, network.DB, period, family)
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
		if bytes := storetest.QueryInt(t, network.DB, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_7d "+
			"WHERE period_start_at = ?", crossing); bytes == 0 {
			t.Errorf("%s: the week crossing the year boundary holds nothing", stage)
		}
	}
	compare("after the first refresh")

	// A late ingestion into a closed slot of the oldest month, an owner reassigned, and a client
	// re-pointed by a lease naming its address.
	oldest := network.Clients[0]
	late := network.Insert(t, []storetest.Flow{{ObservedAt: from + 3600, Device: oldest.Device, Direction: "in",
		Src: oldest.Address, Dst: storetest.Outside(5, oldest.V6), Protocol: "tcp", Action: "pass", Bytes: 999}})
	if _, err := network.DB.DB().Exec(`UPDATE client SET owner_id = 2, owner_assigned_at = ? WHERE owner_id = 1`,
		network.Now); err != nil {
		t.Fatal(err)
	}
	var unleased storetest.Client
	for _, candidate := range network.Clients {
		if !candidate.Leased {
			unleased = candidate
			break
		}
	}
	mac := "00:00:00:00:fe:02"
	if _, err := network.DB.UpsertClient(ctx, vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac},
		InterfaceID: &unleased.InterfaceID, MAC: &mac, LastAddress: &unleased.Address}, network.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := network.DB.Reclassify(ctx, append(late, unleased.Address), network.Now); err != nil {
		t.Fatal(err)
	}
	recomputeEverything(t, network)
	compare(fmt.Sprintf("after the corrections at %d", network.Now))
}
