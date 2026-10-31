package plugin_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// THE PARITY OF THE ROLLUPS PLUGIN'S REFRESH WITH THE GO IT REPLACES (ADR 0021, step 3). One network,
// one set of flows placed and paired by the clients plugin, with a purged part and a flow of the
// current hour; the Go of internal/store/aggregates refreshed them once and its result is recorded in
// rollupsGolden (the commit that recorded it ran both and compared them, before the Go was deleted). The
// derivation must keep giving it: every row of the six families in the four periods, and of the
// daily per-pair volume, read by what it is rather than by its id or its computed_at.

const rollupsGolden = "testdata/rollups_parity.golden"

// rollupsTables are the tables the refresh writes, in the order the snapshot lists them.
var rollupsTables = func() []string {
	var tables []string
	for _, family := range []string{"volume_aggregate", "rule_volume_aggregate", "domain_volume_aggregate",
		"peer_volume_aggregate", "client_volume_aggregate", "owner_volume_aggregate"} {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			tables = append(tables, family+"_"+period)
		}
	}
	return append(tables, "pair_volume_observation")
}()

// rollupsFixture stores the flows of the last two days, a purged part of an hour before them, and
// an attribution, so every family and every period is exercised, the purged part included.
func rollupsFixture(t *testing.T, network *storetest.Network) {
	t.Helper()
	ctx := context.Background()
	network.Insert(t, network.FlowsFor(80, 2*86400))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		if _, err := network.DB.DB().ExecContext(ctx, statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement[:40], err)
		}
	}
	hour := network.Now - network.Now%3600 - 3*86400
	exec(`INSERT INTO plugin_rollups.purged_flow_hour (hour_start_at, src_interface_id, dst_interface_id, src_client_id,
		local_client_id, traffic_direction, peer_address, exit_leg_device, rule_id, site_name, action, bytes, connections,
		purged_at) VALUES ($1, $2, NULL, NULL, NULL, 'outbound', '203.0.113.7', NULL, NULL, 'purged.invalid.', 'pass', 4096, 3, $3)`,
		hour, network.Inside[0].ID, network.Now-86400)
	exec(`INSERT INTO plugin_unbound.dns_resolution (lookup_key, client_address, domain, resolver, action, answer_source,
		looked_up_at, ingested_at) VALUES ('k1', $1, 'named.invalid', 'unbound', 'pass', 'Recursion', $2, $2)`,
		network.Clients[0].Address, network.Now-7000)
	exec(`INSERT INTO plugin_unbound.domain_attribution (flow_id, dns_resolution_id, site_name, correlation_delay_seconds,
		attributed_at, method)
		SELECT f.id, r.id, 'named.invalid.', 1, $1, 'lookup_timing'
		FROM plugin_pf.flow AS f, plugin_unbound.dns_resolution AS r
		WHERE r.lookup_key = 'k1' AND f.src_address = $2
		ORDER BY f.id LIMIT 3`, network.Now, network.Clients[0].Address)
}

// rollupsSnapshot renders every row the refresh writes, by what it is.
func rollupsSnapshot(t *testing.T, database *store.Store) string {
	t.Helper()
	var lines []string
	for _, table := range rollupsTables {
		rows, err := database.DB().QueryContext(context.Background(), fmt.Sprintf(`
			SELECT to_jsonb(r) - 'id' - 'computed_at' - 'ingested_at' FROM plugin_rollups.%s AS r`, table))
		if err != nil {
			t.Fatalf("reading %s: %v", table, err)
		}
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, table+" "+row)
		}
		_ = rows.Close()
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestTheRollupsRefreshWritesWhatTheGoWrote(t *testing.T) {
	ctx := context.Background()
	network := storetest.NewNetwork(t, 2, 4)
	loaded, err := plugin.Load(ctx, plugin.Options{Store: network.DB, DerivationParameters: defaultDerivationParameters})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Fatalf("a built-in was refused: %v", refusal)
	}
	rollupsFixture(t, network)
	classifyWith(loaded)(t)

	recorded, err := os.ReadFile(rollupsGolden)
	if err != nil {
		t.Fatalf("the recorded result of the Go: %v", err)
	}
	want := strings.TrimSuffix(string(recorded), "\n")

	rollups, found := loaded.Plugin("rollups")
	if !found {
		t.Fatal("the rollups plugin did not load")
	}
	outcome, err := rollups.RunDerivation(ctx, nil, "refresh", network.Now)
	if err != nil {
		t.Fatalf("the derivation: %v", err)
	}
	if !outcome.Ran {
		t.Fatal("the derivation did not run")
	}
	got := rollupsSnapshot(t, network.DB)
	if got != want {
		t.Errorf("the SQL refreshed differently from the Go:\n--- Go ---\n%s\n--- SQL ---\n%s", want, got)
	}
	if !strings.Contains(got, "owner_volume_aggregate_30d") || !strings.Contains(got, "domain_volume_aggregate_1h") ||
		!strings.Contains(got, "pair_volume_observation") {
		t.Error("the fixture left a family empty: it proves too little")
	}
	// A second run over the same flows rewrites the current slots alone, to the same rows.
	if _, err := rollups.RunDerivation(ctx, nil, "refresh", network.Now+1); err != nil {
		t.Fatalf("the second run: %v", err)
	}
	if again := rollupsSnapshot(t, network.DB); again != got {
		t.Errorf("the second run changed the rows:\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
	if slots := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM plugin_rollups.refresh_slot WHERE NOT forced"); slots != 0 {
		t.Errorf("the second run rewrote %d slots that were neither forced nor current", slots)
	}
}

// refreshRollups runs the rollups plugin's refresh of a load, as the collector runs it after a pass.
func refreshRollups(t *testing.T, loaded *plugin.Loaded, now int64) {
	t.Helper()
	rollups, found := loaded.Plugin("rollups")
	if !found {
		t.Fatal("the rollups plugin did not load")
	}
	if _, err := rollups.RunDerivation(context.Background(), nil, "refresh", now); err != nil {
		t.Fatalf("the rollups refresh: %v", err)
	}
}
