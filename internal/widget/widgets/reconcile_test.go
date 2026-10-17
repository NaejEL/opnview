package widgets

import (
	"testing"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/shapes"
)

// AC11 and AC12: reconciliation, and remainders that make every total whole.

// windowTotal is the logged bytes of the fixture's window over classified_flow, each
// connection once.
func (f *fixture) windowTotal(t *testing.T, window envelope.Interval, extra string) (int64, int64) {
	t.Helper()
	var bytes, connections int64
	row := f.db.DB().QueryRow(`SELECT coalesce(sum(packet_bytes), 0), count(*) FROM classified_flow
		WHERE observed_at >= $1 AND observed_at < $2 AND exit_leg_device IS NULL `+extra, window.From, window.To)
	check(t, row.Scan(&bytes, &connections))
	return bytes, connections
}

// TestEveryReconciliationCountsAPairedConnectionOnce is AC11 on a fixture with paired
// connections: the matrix total equals sum(packet_bytes) over classified_flow with
// exit_leg_device IS NULL; the map's placed plus unplaced equals the allowed total, with
// every unplaced bin present; each tree level sums to its parent on both sides; the Sankey's
// ribbons plus Other equal the total; the person cards plus Unassigned equal the owner
// family's total.
func TestEveryReconciliationCountsAPairedConnectionOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	if f.scalar(t, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'") == 0 {
		t.Fatal("the fixture holds no paired connection, so this test is vacuous")
	}
	matrix := f.respond(t, "interface_traffic_matrix", map[string]any{"period": "24h"})
	window := matrix.Window.Requested
	bytes, connections := f.windowTotal(t, window, "")
	cells := decodeData[matrixData](t, matrix)
	var cellBytes int64
	for _, cell := range cells.Cells {
		cellBytes += cell.Volume.Bytes
	}
	if cells.Total.Bytes != bytes || cellBytes != bytes || cells.Total.Connections != connections {
		t.Errorf("the matrix holds %d bytes in its cells and %d in its total, the flows %d", cellBytes,
			cells.Total.Bytes, bytes)
	}

	mapped := decodeData[shapes.MapData](t, f.respond(t, "passed_traffic_world_map", map[string]any{"period": "24h", "scope": "all"}))
	allowed, _ := f.windowTotal(t, window, "AND action = 'pass'")
	var placed int64
	for _, point := range mapped.Points {
		placed += point.Volume.Bytes
	}
	var unplaced int64
	reasons := map[string]bool{}
	for _, bin := range mapped.Unplaced {
		unplaced += bin.Volume.Bytes
		reasons[bin.Reason] = true
	}
	if placed+mapped.BelowMinimum.Volume.Bytes+unplaced != allowed || mapped.Total.Bytes != allowed {
		t.Errorf("the map places %d and leaves %d unplaced, the allowed flows hold %d", placed, unplaced, allowed)
	}
	for _, reason := range []string{figures.UnplacedMiss, figures.UnplacedPending, figures.UnplacedNoRow, figures.UnplacedNoCoordinates} {
		if !reasons[reason] {
			t.Errorf("the unplaced bin %s is absent", reason)
		}
	}

	for _, root := range []string{"interface", "client", "owner"} {
		tree := decodeData[treeData](t, f.respond(t, "connection_tree", map[string]any{"period": "24h", "root": root}))
		for side, nodes := range map[string][]*treeNode{"inside": tree.Inside, "outside": tree.Outside} {
			var sum int64
			for _, node := range nodes {
				sum += node.Connections.Value
				var children int64
				for _, child := range node.Children {
					children += child.Connections.Value
				}
				if len(node.Children) > 0 && children != node.Connections.Value {
					t.Errorf("%s root, %s: %s's children sum to %d, not %d", root, side, node.Key, children,
						node.Connections.Value)
				}
			}
			if sum != connections || tree.Total.Connections.Value != connections {
				t.Errorf("%s root, %s: the level sums to %d, the flows %d", root, side, sum, connections)
			}
		}
	}

	for _, right := range []string{"interface", "operator", "country"} {
		sankey := decodeData[sankeyData](t, f.respond(t, "traffic_sankey", map[string]any{"period": "24h", "right": right}))
		var ribbons int64
		for _, ribbon := range sankey.Ribbons {
			ribbons += ribbon.Bytes.Value
		}
		if ribbons+sankey.Other.Bytes.Value != bytes || sankey.Total.Bytes != bytes {
			t.Errorf("right %s: ribbons %d plus Other %d, total %d", right, ribbons, sankey.Other.Bytes.Value, bytes)
		}
	}

	cards := decodeData[ownerActivityData](t, f.respond(t, "owner_activity", map[string]any{"period": "24h"}))
	owned, _ := f.windowTotal(t, window, "AND local_client_id IS NOT NULL AND traffic_scope <> 'this_firewall'")
	var cardBytes int64
	for _, card := range cards.Cards {
		cardBytes += card.Volume.Bytes
	}
	if cards.Unassigned == nil || cardBytes+cards.Unassigned.Volume.Bytes != owned || cards.Total.Bytes != owned {
		t.Errorf("the cards hold %d and the Unassigned card the rest of %d", cardBytes, owned)
	}
	hidden := decodeData[ownerActivityData](t, f.respond(t, "owner_activity", map[string]any{"period": "24h", "include_unassigned": false}))
	if hidden.Unassigned != nil || !hidden.TotalsExcludeUnassigned {
		t.Error("with include_unassigned false the response does not say its totals exclude the Unassigned card")
	}
}

// TestEveryTruncationReturnsARemainderThatMakesTheTotalWhole is AC12.
func TestEveryTruncationReturnsARemainderThatMakesTheTotalWhole(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	period := map[string]any{"period": "24h"}
	with := func(key string, value any) map[string]any {
		copied := map[string]any{"period": "24h"}
		copied[key] = value
		return copied
	}

	ranking := decodeData[interfaceRankingData](t, f.respond(t, "interface_volume_ranking", with("limit", 1)))
	var sum int64
	for _, bar := range ranking.Bars {
		sum += bar.Volume.Bytes
	}
	if len(ranking.Bars) != 1 || ranking.Remainder.Members == 0 ||
		sum+ranking.Remainder.Volume.Bytes+ranking.Unattributed.Bytes != ranking.Total.Bytes {
		t.Errorf("interface ranking: %d bars and a remainder of %+v do not make %d", len(ranking.Bars),
			ranking.Remainder, ranking.Total.Bytes)
	}

	clients := decodeData[clientRankingData](t, f.respond(t, "client_volume_ranking", with("limit", 1)))
	if len(clients.Bars) != 1 || clients.Bars[0].Volume.Bytes+clients.Remainder.Volume.Bytes+
		clients.Excluded.Volume.Bytes != clients.Total.Bytes {
		t.Errorf("client ranking: the remainder does not make the total: %+v", clients.Remainder)
	}

	for _, groupBy := range []string{"client", "interface", "protocol", "service_port"} {
		parameters := with("slices", 1)
		parameters["group_by"] = groupBy
		donut := decodeData[compositionData](t, f.respond(t, "traffic_composition", parameters))
		if len(donut.Slices) != 1 || donut.Other.Members == 0 ||
			donut.Slices[0].Volume.Bytes+donut.Other.Volume.Bytes != donut.Total.Bytes {
			t.Errorf("composition by %s: one slice and Other %+v do not make %d", groupBy, donut.Other, donut.Total.Bytes)
		}
	}

	tree := decodeData[treeData](t, f.respond(t, "connection_tree", with("limit_per_level", 1)))
	var top int64
	for _, node := range tree.Inside {
		top += node.Connections.Value
	}
	if len(tree.Inside) != 2 || tree.Inside[1].Remainder == nil || top != tree.Total.Connections.Value {
		t.Errorf("tree: one node and a remainder do not make the total (%d nodes)", len(tree.Inside))
	}

	sites := decodeData[topSitesData](t, f.respond(t, "top_sites", with("limit", 1)))
	if len(sites.Rows) != 1 || sites.Rows[0].Flows.Value+sites.Remainder.Count.Value+sites.BelowMinFlows.Count.Value !=
		sites.Total.Count.Value {
		t.Errorf("top sites: the remainder does not make the total: %+v", sites.Remainder)
	}

	for _, widgetType := range []string{"destination_countries", "destination_operators"} {
		table := decodeData[destinationsData](t, f.respond(t, widgetType, with("limit", 1)))
		var shown int64
		for _, row := range table.Rows {
			shown += row.Bytes.Value
		}
		var unresolved int64
		for _, bin := range table.Unresolved {
			unresolved += bin.Volume.Bytes
		}
		if len(table.Rows) != 1 || shown+table.Remainder.Volume.Bytes+unresolved != table.Total.Bytes {
			t.Errorf("%s: one row, the remainder and the unresolved row do not make %d", widgetType, table.Total.Bytes)
		}
	}

	sankey := decodeData[sankeyData](t, f.respond(t, "traffic_sankey", with("max_nodes", 1)))
	if len(sankey.Ribbons) != 1 || sankey.Other.Ribbons == 0 ||
		sankey.Ribbons[0].Bytes.Value+sankey.Other.Bytes.Value != sankey.Total.Bytes {
		t.Errorf("Sankey: one ribbon and Other do not make the total")
	}

	feed := decodeData[feedData](t, f.respond(t, "unified_blocked_feed", with("limit", 1)))
	if len(feed.Entries) != 1 || int64(len(feed.Entries))+feed.Remainder.Members != feed.Total.Value {
		t.Errorf("feed: one entry and a remainder of %d do not make %d", feed.Remainder.Members, feed.Total.Value)
	}

	rules := decodeData[ruleTableData](t, f.respond(t, "blocked_by_firewall_rule", with("limit", 1)))
	if len(rules.Rows) != 1 || rules.Rows[0].Blocked.Value+rules.Remainder.Count.Value+
		rules.ExcludedAutomatic.Count.Value != rules.Total.Value {
		t.Errorf("rules: one row and the remainder do not make %d", rules.Total.Value)
	}

	lookups := decodeData[blockedLookupsData](t, f.respond(t, "blocked_dns_lookups", with("limit", 1)))
	if len(lookups.Rows) != 1 || lookups.Rows[0].Lookups.Value+lookups.Remainder.Count.Value+
		lookups.BelowMinCount.Count.Value != lookups.Total.Value {
		t.Errorf("blocked lookups: one row and the remainder do not make %d", lookups.Total.Value)
	}

	cards := decodeData[ownerActivityData](t, f.respond(t, "owner_activity", with("clients_per_card", 1)))
	if cards.Unassigned == nil || len(cards.Unassigned.Chips) != 1 || cards.Unassigned.ChipRemainder.Members == 0 {
		t.Errorf("cards: the Unassigned card's chips were not cut to one with a remainder chip")
	}

	attribution := decodeData[attributionData](t, f.respond(t, "attribution_rate_per_client", with("limit", 1)))
	if len(attribution.Rows) != 1 || attribution.Remainder.Members == 0 {
		t.Errorf("attribution: no remainder past one row")
	}

	byClient := decodeData[sitesByClientData](t, f.respond(t, "sites_by_client", with("sites_per_client", 1)))
	for _, row := range byClient.Rows {
		if len(row.Sites) > 1 {
			t.Errorf("sites by client: a row kept %d sites", len(row.Sites))
		}
	}
	_ = period
}
