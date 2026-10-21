package plugin_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// THE PARITY OF THE CLIENTS PLUGIN WITH THE GO IT REPLACES (ADR 0019 §4, ADR 0020 wave 3). The same
// network and the same records are stored twice: once classified and paired by the Go of
// internal/store/flows, once by the clients plugin's SQL-only derivation. Every flow's ends, scope and
// pairing, and every client, read by what they are rather than by their ids, must be the same.

// parityFixture is the records both databases receive: flows of every direction, blocked ones among them,
// and the two legs of several connections, one of them left without its first leg.
func parityFixture(network *storetest.Network) []storetest.Flow {
	specs := network.FlowsFor(60, 7200)
	v4, v6 := network.Clients[0], network.Clients[1]
	for index, connection := range []storetest.Connection{
		{Client: v4, Peer: storetest.Outside(101, false), At: network.Now - 900, IPID: 4001, Seq: 11, Lag: 0},
		{Client: v4, Peer: storetest.Outside(102, false), At: network.Now - 800, IPID: 4002, Seq: 12, Lag: 1},
		{Client: v6, Peer: storetest.Outside(103, true), At: network.Now - 700, IPID: 4003, Seq: 13, Lag: 0},
		{Client: v4, Peer: storetest.Outside(104, false), At: network.Now - 600, IPID: 4004, Seq: 14, Lag: 0},
	} {
		first, second := network.Legs(connection, "")
		if index == 3 {
			// The second leg alone: a NATed connection whose first record the firewall never logged.
			specs = append(specs, second)
			continue
		}
		specs = append(specs, first, second)
	}
	return specs
}

// snapshot renders every flow and every client by what they are.
func snapshot(t *testing.T, database *store.Store) string {
	t.Helper()
	ctx := context.Background()
	var lines []string
	rows, err := database.DB().QueryContext(ctx, `
		SELECT f.interface_device || ' ' || f.src_address || ' ' || f.dst_address || ' ' || f.observed_at,
		       coalesce(si.identifier, '-'), coalesce(sc.identity_kind || '/' || sc.identity_key, '-'), f.src_is_this_firewall,
		       coalesce(di.identifier, '-'), coalesce(dc.identity_kind || '/' || dc.identity_key, '-'), f.dst_is_this_firewall,
		       f.traffic_scope, coalesce(f.pair_outcome, '-'),
		       coalesce(p.interface_device || ' ' || p.src_address || ' ' || p.dst_address || ' ' || p.observed_at, '-')
		FROM flow AS f
		LEFT JOIN interface AS si ON si.id = f.src_interface_id
		LEFT JOIN interface AS di ON di.id = f.dst_interface_id
		LEFT JOIN client AS sc ON sc.id = f.src_client_id
		LEFT JOIN client AS dc ON dc.id = f.dst_client_id
		LEFT JOIN flow AS p ON p.id = f.paired_flow_id`)
	if err != nil {
		t.Fatalf("reading the flows: %v", err)
	}
	for rows.Next() {
		var key, srcInterface, srcClient, dstInterface, dstClient, scope, outcome, partner string
		var srcFirewall, dstFirewall bool
		if err := rows.Scan(&key, &srcInterface, &srcClient, &srcFirewall, &dstInterface, &dstClient, &dstFirewall, &scope,
			&outcome, &partner); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Join([]string{"flow", key, srcInterface, srcClient, boolText(srcFirewall), dstInterface,
			dstClient, boolText(dstFirewall), scope, outcome, partner}, " | "))
	}
	_ = rows.Close()
	rows, err = database.DB().QueryContext(ctx, `
		SELECT c.identity_kind, c.identity_key, coalesce(i.identifier, '-'), coalesce(c.last_address, '-')
		FROM client AS c LEFT JOIN interface AS i ON i.id = c.interface_id`)
	if err != nil {
		t.Fatalf("reading the clients: %v", err)
	}
	for rows.Next() {
		var kind, key, iface, address string
		if err := rows.Scan(&kind, &key, &iface, &address); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Join([]string{"client", kind, key, iface, address}, " | "))
	}
	_ = rows.Close()
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func boolText(value bool) string {
	if value {
		return "firewall"
	}
	return "-"
}

// parityGolden is what the Go of internal/store/flows placed and paired on the fixture, recorded once
// before that Go was deleted (commit 5ad1a5a, where this test ran both and compared them): the result
// the SQL must keep giving.
const parityGolden = "testdata/clients_parity.golden"

// defaultDerivationParameters are the core's values at a fresh installation's defaults.
func defaultDerivationParameters(context.Context) (map[string]any, error) {
	return map[string]any{"held_margin": int64(flows.DefaultDiscoveryIntervalSeconds),
		"leg_pairing_window": int64(flows.DefaultPairingWindowSeconds)}, nil
}

// classifyWith runs the clients plugin's derivation of a load, as the collector runs it after a pass.
func classifyWith(loaded *plugin.Loaded) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()
		clients, found := loaded.Plugin("clients")
		if !found {
			t.Fatal("the clients plugin did not load")
		}
		if _, err := clients.RunDerivation(context.Background(), nil, "place", storetest.ReferenceNow); err != nil {
			t.Fatalf("the clients derivation: %v", err)
		}
	}
}

func TestTheClientsDerivationPlacesAndPairsAsTheGoDid(t *testing.T) {
	ctx := context.Background()

	recorded, err := os.ReadFile(parityGolden)
	if err != nil {
		t.Fatalf("the recorded result of the Go: %v", err)
	}
	want := strings.TrimSuffix(string(recorded), "\n")

	bySQL := storetest.NewNetwork(t, 2, 4)
	specs := parityFixture(bySQL)
	bySQL.Insert(t, specs)
	loaded, err := plugin.Load(ctx, plugin.Options{Store: bySQL.DB, DerivationParameters: defaultDerivationParameters})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Fatalf("a built-in was refused: %v", refusal)
	}
	clients, found := loaded.Plugin("clients")
	if !found {
		t.Fatal("the clients plugin did not load")
	}
	outcome, err := clients.RunDerivation(ctx, nil, "place", bySQL.Now)
	if err != nil {
		t.Fatalf("the derivation: %v", err)
	}
	if !outcome.Ran {
		t.Fatal("the derivation did not run")
	}
	got := snapshot(t, bySQL.DB)
	if got != want {
		t.Errorf("the SQL placed and paired differently from the Go:\n--- Go ---\n%s\n--- SQL ---\n%s", want, got)
	}
	// A second run over the same evidence changes nothing: no flow is stamped with its instant, and
	// what the database says is the same. The rows the run reports written are its own work table's.
	later := bySQL.Now + 1
	if _, err := clients.RunDerivation(ctx, nil, "place", later); err != nil {
		t.Fatalf("the second run: %v", err)
	}
	if changed := storetest.QueryInt(t, bySQL.DB, "SELECT count(*) FROM flow WHERE changed_at = $1", later); changed != 0 {
		t.Errorf("the second run over unchanged evidence changed %d flows", changed)
	}
	if after := snapshot(t, bySQL.DB); after != got {
		t.Errorf("the second run changed what the database says:\n--- first ---\n%s\n--- second ---\n%s", got, after)
	}
}
