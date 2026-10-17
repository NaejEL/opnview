package store_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Classification: AC9, AC11, AC26, AC27 and AC31.

// aggregateTables lists every aggregate table and the pair volume, the derived tables a
// recomputation must reproduce.
func aggregateTables() []string {
	var tables []string
	for _, family := range []string{"volume_aggregate_", "owner_volume_aggregate_",
		"client_volume_aggregate_", "domain_volume_aggregate_", "rule_volume_aggregate_",
		"peer_volume_aggregate_"} {
		for _, period := range vocab.Periods() {
			tables = append(tables, family+period.Name)
		}
	}
	return append(tables, "pair_volume_observation")
}

// derivedSnapshot renders every derived row, without its id and its freshness instants,
// so two states can be compared.
func derivedSnapshot(t *testing.T, database *store.Store) string {
	t.Helper()
	ctx := context.Background()
	var builder strings.Builder
	for _, table := range aggregateTables() {
		rows, err := database.DB().QueryContext(ctx, "SELECT * FROM "+table)
		if err != nil {
			t.Fatalf("reading %s: %v", table, err)
		}
		columns, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatalf("reading %s: %v", table, err)
			}
			var parts []string
			for index, column := range columns {
				if column == "id" || column == "computed_at" || column == "ingested_at" {
					continue
				}
				parts = append(parts, fmt.Sprintf("%s=%v", column, values[index]))
			}
			lines = append(lines, strings.Join(parts, ","))
		}
		_ = rows.Close()
		sort.Strings(lines)
		builder.WriteString(table + "\n" + strings.Join(lines, "\n") + "\n")
	}
	return builder.String()
}

// recomputeEverything rewrites every slot holding a flow, whatever its freshness.
func recomputeEverything(t *testing.T, network *storetest.Network) {
	t.Helper()
	instants := allFlowInstants(t, network.DB)
	if _, err := network.DB.RefreshAggregates(context.Background(), 0, network.Now, instants); err != nil {
		t.Fatalf("recomputing: %v", err)
	}
}

func allFlowInstants(t *testing.T, database *store.Store) []int64 {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(), "SELECT observed_at FROM flow")
	if err != nil {
		t.Fatalf("reading the flow instants: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var instants []int64
	for rows.Next() {
		var instant int64
		if err := rows.Scan(&instant); err != nil {
			t.Fatalf("reading the flow instants: %v", err)
		}
		instants = append(instants, instant)
	}
	return instants
}

// scopeDisagreements counts the flows whose scope disagrees with their interfaces. The
// schema's CHECK already refuses such a row; this reads the stored state.
func scopeDisagreements(t *testing.T, database *store.Store) int64 {
	return storetest.QueryInt(t, database, `SELECT count(*) FROM flow WHERE traffic_scope <> CASE
		WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL THEN 'east_west'
		ELSE 'north_south' END`)
}

// TestAnOutsideAddressSeenOnBothKindsOfInterfaceGetsNoClientAndNoInterface is AC9.
func TestAnOutsideAddressSeenOnBothKindsOfInterfaceGetsNoClientAndNoInterface(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {5, 11}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			for _, v6 := range []bool{false, true} {
				remote := storetest.Outside(77, v6)
				var client storetest.Client
				for _, candidate := range network.Clients {
					if candidate.V6 == v6 {
						client = candidate
						break
					}
				}
				port := int64(443)
				addresses := network.Insert(t, []storetest.Flow{
					// Seen on a non-upstream interface: the client's own, logged inbound there.
					{ObservedAt: network.Now - 30, Device: client.Device, Direction: "in",
						Src: client.Address, Dst: remote, DstPort: &port, Protocol: "tcp",
						Action: "pass", Bytes: 100},
					// And on the upstream one, as the source of a packet arriving there.
					{ObservedAt: network.Now - 20, Device: network.Upstream.Device, Direction: "in",
						Src: remote, Dst: client.Address, DstPort: &port, Protocol: "tcp",
						Action: "block", Bytes: 60},
					// And named as the destination of a record logged outbound upstream.
					{ObservedAt: network.Now - 10, Device: network.Upstream.Device, Direction: "out",
						Src: client.Address, Dst: remote, DstPort: &port, Protocol: "tcp",
						Action: "pass", Bytes: 80},
				})
				network.ClassifyAndRefresh(t, addresses)

				if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE last_address = $1",
					remote); rows != 0 {
					t.Errorf("the outside address %s has %d client rows", remote, rows)
				}
				if eastWest := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
					WHERE (src_address = $1 OR dst_address = $2) AND traffic_scope <> 'north_south'`,
					remote, remote); eastWest != 0 {
					t.Errorf("%d flows touching %s are not north-south", eastWest, remote)
				}
				if placed := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
					WHERE (src_address = $1 AND (src_interface_id IS NOT NULL OR src_client_id IS NOT NULL))
					   OR (dst_address = $2 AND (dst_interface_id IS NOT NULL OR dst_client_id IS NOT NULL))`,
					remote, remote); placed != 0 {
					t.Errorf("%d flows carry an interface or a client on the end of %s", placed, remote)
				}
				if inside := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
					WHERE src_address = $1 AND src_interface_id = $2`, client.Address, client.InterfaceID); inside == 0 {
					t.Errorf("the inside end %s lost its interface", client.Address)
				}
			}
		})
	}
}

// TestDirectionComesFromMembershipAndPartitionsEveryFlow is AC26.
func TestDirectionComesFromMembershipAndPartitionsEveryFlow(t *testing.T) {
	t.Parallel()
	network := storetest.Populated(t, 3, 9, 400, 3*86400)

	var total, totalConnections int64
	if err := network.DB.DB().QueryRow(
		"SELECT sum(packet_bytes), count(*) FROM flow").Scan(&total, &totalConnections); err != nil {
		t.Fatalf("reading the totals: %v", err)
	}
	var summed, summedConnections int64
	directions := map[string]bool{}
	rows, err := network.DB.DB().Query(`SELECT traffic_direction, sum(packet_bytes), count(*)
		FROM classified_flow GROUP BY traffic_direction`)
	if err != nil {
		t.Fatalf("reading the directions: %v", err)
	}
	for rows.Next() {
		var (
			direction          string
			bytes, connections int64
		)
		if err := rows.Scan(&direction, &bytes, &connections); err != nil {
			t.Fatalf("reading the directions: %v", err)
		}
		directions[direction] = true
		summed += bytes
		summedConnections += connections
	}
	_ = rows.Close()
	for _, direction := range []string{"outbound", "inbound", "inter_interface"} {
		if !directions[direction] {
			t.Errorf("the network holds no %s flow, so the partition is not exercised", direction)
		}
	}
	if summed != total || summedConnections != totalConnections {
		t.Errorf("inbound + outbound + between interfaces is %d bytes in %d connections, the whole "+
			"is %d in %d", summed, summedConnections, total, totalConnections)
	}

	// The same partition in every aggregate period.
	for _, period := range vocab.Periods() {
		table := "volume_aggregate_" + period.Name
		if got := storetest.QueryInt(t, network.DB, "SELECT sum(bytes) FROM "+table); got != total {
			t.Errorf("%s sums to %d bytes over its directions, not %d", table, got, total)
		}
	}

	// A packet leaving a client for the Internet is `in` on the client's interface, and it is
	// OUTBOUND: pf's dir is per interface, not per network.
	client := network.Clients[0]
	port := int64(443)
	network.Insert(t, []storetest.Flow{{ObservedAt: network.Now - 5, Device: client.Device, Direction: "in",
		Src: client.Address, Dst: storetest.Outside(200, client.V6), DstPort: &port, Protocol: "tcp",
		Action: "pass", Bytes: 10}})
	if _, err := network.DB.Reclassify(context.Background(),
		[]string{client.Address, storetest.Outside(200, client.V6)}, network.Now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	var direction string
	if err := network.DB.DB().QueryRow(`SELECT traffic_direction FROM classified_flow
		WHERE dst_address = $1 AND direction = 'in'`, storetest.Outside(200, client.V6)).Scan(&direction); err != nil {
		t.Fatalf("reading the direction: %v", err)
	}
	if direction != "outbound" {
		t.Errorf("a client-to-Internet flow logged in on the client's interface is %q", direction)
	}
}

// TestALookupIsPlacedFromTheSameEvidenceAsAFlow is AC27.
func TestALookupIsPlacedFromTheSameEvidenceAsAFlow(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 3, 6)
	ctx := context.Background()
	client := network.Clients[1]
	stranger := storetest.Outside(5, true)
	for index, address := range []string{client.Address, stranger} {
		if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
			LookupKey: fmt.Sprintf("example-lookup-%d", index), ClientAddress: address,
			Domain: "example.invalid", Resolver: "unbound", Action: "pass",
			LookedUpAt: network.Now - 60, IngestedAt: network.Now - 60,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	if pending := storetest.QueryInt(t, network.DB,
		"SELECT count(*) FROM dns_resolution WHERE interface_lookup_state = 'pending'"); pending != 2 {
		t.Errorf("%d lookups are pending before classification, not 2", pending)
	}
	if _, err := network.DB.Reclassify(ctx, []string{client.Address, stranger}, network.Now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	var (
		state       string
		interfaceID *int64
	)
	if err := network.DB.DB().QueryRow(`SELECT interface_lookup_state, interface_id FROM dns_resolution
		WHERE client_address = $1`, client.Address).Scan(&state, &interfaceID); err != nil {
		t.Fatalf("reading the lookup: %v", err)
	}
	if state != "resolved" || interfaceID == nil || *interfaceID != client.InterfaceID {
		t.Errorf("the client's lookup is %s on %v, not resolved on %d", state, interfaceID, client.InterfaceID)
	}
	if err := network.DB.DB().QueryRow(`SELECT interface_lookup_state FROM dns_resolution
		WHERE client_address = $1`, stranger).Scan(&state); err != nil {
		t.Fatalf("reading the lookup: %v", err)
	}
	if state != "not_found" {
		t.Errorf("a lookup from an address with no evidence is %q, not not_found", state)
	}
}

// TestReclassifyingAStepFourDatabaseClearsTheRemoteClients is AC11.
//
// Step 4 gave every remote address a client row on whatever interface named it first. This
// writes that shape -- remote clients named as flow ends, as an event's source and as a
// lookup's querier -- refreshes the aggregates from it, then runs what a first discovery of a
// run runs.
func TestReclassifyingAStepFourDatabaseClearsTheRemoteClients(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 3, 7)
	ctx := context.Background()
	var remoteClients []int64
	var remoteAddresses []string
	for index, v6 := range []bool{false, true, false} {
		remote := storetest.Outside(30+index, v6)
		var interfaceID *int64
		if index != 2 {
			interfaceID = &network.Upstream.ID
		}
		id, err := network.DB.UpsertClient(ctx, vocab.Client{
			Identity:    vocab.ClientIdentity{Kind: vocab.IdentityAddressInInterface, Key: flows.AddressIdentityKey(interfaceID, remote, network.Now)},
			InterfaceID: interfaceID, LastAddress: &remote,
		}, network.Now-7200)
		if err != nil {
			t.Fatalf("writing a remote client: %v", err)
		}
		remoteClients = append(remoteClients, id)
		remoteAddresses = append(remoteAddresses, remote)
		var client storetest.Client
		for _, candidate := range network.Clients {
			if candidate.V6 == v6 {
				client = candidate
			}
		}
		clientID := remoteClients[len(remoteClients)-1]
		insideInterface := client.InterfaceID
		if err := network.DB.InsertFlow(ctx, vocab.Flow{
			LogDigest: fmt.Sprintf("example-step4-%d", index), ObservedAt: network.Now - int64(600*(index+1)),
			IngestedAt: network.Now - 600, InterfaceDevice: network.Upstream.Device,
			InterfaceLookupState: vocab.LookupResolved, SrcInterfaceID: &insideInterface,
			DstInterfaceID: interfaceID, DstClientID: &clientID,
			SrcAddress: client.Address, DstAddress: remote, Protocol: "tcp", IPVersion: map[bool]int64{false: 4, true: 6}[v6],
			Action: "pass", Direction: "out", PacketBytes: 500, RuleLookupState: vocab.LookupPending,
		}); err != nil {
			t.Fatalf("writing a step-4 flow: %v", err)
		}
		eventProvider, _ := network.DB.ProviderID(ctx, "security_event", "suricata")
		if err := network.DB.InsertSecurityEvent(ctx, eventProvider, vocab.SecurityEvent{
			ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: network.Now - 300,
			IngestedAt: network.Now - 300, RuleIdentity: "1", Signature: "example", EventAction: "blocked",
			SrcAddress: remote, DstAddress: client.Address, SrcClientID: &clientID, SrcInterfaceID: interfaceID,
		}); err != nil {
			t.Fatalf("writing an event: %v", err)
		}
		if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
			LookupKey: fmt.Sprintf("example-step4-lookup-%d", index), ClientAddress: remote,
			ClientID: &clientID, Domain: "example.invalid", Resolver: "unbound", Action: "pass",
			LookedUpAt: network.Now - 300, IngestedAt: network.Now - 300,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now, nil); err != nil {
		t.Fatalf("refreshing the step-4 state: %v", err)
	}

	run := func() flows.Reclassification {
		result, err := network.DB.ReclassifyAll(ctx, network.Now)
		if err != nil {
			t.Fatalf("reclassifying: %v", err)
		}
		if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now, result.FlowInstants); err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		if _, err := network.DB.PurgeUnreferencedClients(ctx); err != nil {
			t.Fatalf("purging: %v", err)
		}
		return result
	}
	first := run()
	if len(first.FlowInstants) == 0 {
		t.Fatal("reclassifying the step-4 state changed no flow, so the test proves nothing")
	}
	// Checked by address and not by id: a removed row's id can be taken again by a client the
	// classification mints for an inside address.
	for _, remote := range remoteAddresses {
		if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE last_address = $1", remote); rows != 0 {
			t.Errorf("the remote address %s still has %d client rows", remote, rows)
		}
	}
	if violations := storetest.ForeignKeyViolations(t, network.DB); violations != 0 {
		t.Errorf("%d rows name a row that does not exist", violations)
	}
	if disagreements := scopeDisagreements(t, network.DB); disagreements != 0 {
		t.Errorf("%d flows break the traffic_scope CHECK", disagreements)
	}
	settled := derivedSnapshot(t, network.DB)
	recomputeEverything(t, network)
	if recomputed := derivedSnapshot(t, network.DB); recomputed != settled {
		t.Error("the slots after reclassification differ from a recomputation")
	}

	before := derivedSnapshot(t, network.DB)
	clientsBefore := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client")
	second := run()
	if len(second.FlowInstants) != 0 || len(second.LookupInstants) != 0 || second.ClientsPurged != 0 {
		t.Errorf("a second run changed %d flows, %d lookups and purged %d clients",
			len(second.FlowInstants), len(second.LookupInstants), second.ClientsPurged)
	}
	if after := derivedSnapshot(t, network.DB); after != before {
		t.Error("a second run changed the derived tables")
	}
	if clientsAfter := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client"); clientsAfter != clientsBefore {
		t.Errorf("a second run changed the client count from %d to %d", clientsBefore, clientsAfter)
	}
}

// TestALeaseNamingAnAddressSeenOnlyInFlowsRepointsItsRows is AC31.
func TestALeaseNamingAnAddressSeenOnlyInFlowsRepointsItsRows(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	var client storetest.Client
	for _, candidate := range network.Clients {
		if !candidate.Leased {
			client = candidate
			break
		}
	}
	port := int64(443)
	addresses := network.Insert(t, []storetest.Flow{
		{ObservedAt: network.Now - 40, Device: client.Device, Direction: "in", Src: client.Address,
			Dst: storetest.Outside(1, client.V6), DstPort: &port, Protocol: "tcp", Action: "pass", Bytes: 300},
		{ObservedAt: network.Now - 4000, Device: client.Device, Direction: "in", Src: client.Address,
			Dst: storetest.Outside(2, client.V6), DstPort: &port, Protocol: "tcp", Action: "pass", Bytes: 200},
	})
	if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
		LookupKey: "example-lookup-before", ClientAddress: client.Address, Domain: "example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: storetest.Ptr("Recursion"),
		LookedUpAt: network.Now - 42, IngestedAt: network.Now - 42,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	addresses = append(addresses, client.Address)
	network.ClassifyAndRefresh(t, addresses)
	if _, err := network.DB.Attribute(ctx, network.Now-86400, network.Now, 5, network.Now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	var addressLevel int64
	if err := network.DB.DB().QueryRow(`SELECT src_client_id FROM flow WHERE observed_at = $1`,
		network.Now-40).Scan(&addressLevel); err != nil {
		t.Fatalf("reading the flow's client: %v", err)
	}
	if kind := queryString(t, network.DB, "SELECT identity_kind FROM client WHERE id = $1", addressLevel); kind != vocab.IdentityAddressInInterface {
		t.Fatalf("an address seen only in flows has a %s identity", kind)
	}

	// A lease now names the address, with a MAC.
	mac := "00:00:00:00:fe:01"
	leaseClient, err := network.DB.UpsertClient(ctx, vocab.Client{
		Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac}, InterfaceID: &client.InterfaceID,
		MAC: &mac, LastAddress: &client.Address,
	}, network.Now)
	if err != nil {
		t.Fatalf("writing the leased client: %v", err)
	}
	result, err := network.DB.Reclassify(ctx, []string{client.Address}, network.Now)
	if err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if len(result.FlowInstants) != 2 {
		t.Errorf("re-pointing touched %d flows, not the two of the address", len(result.FlowInstants))
	}
	low, high, _ := spanOfInstants(result.FlowInstants)
	attribution, err := network.DB.Attribute(ctx, low, high, 5, network.Now)
	if err != nil {
		t.Fatalf("attributing again: %v", err)
	}
	forced := append(result.FlowInstants, attribution.FlowInstants...)
	if _, err := network.DB.RefreshAggregates(ctx, network.Now, network.Now, forced); err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	if stale := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
		WHERE src_address = $1 AND src_client_id IS DISTINCT FROM $2`, client.Address, leaseClient); stale != 0 {
		t.Errorf("%d flows of the address still name the old identity", stale)
	}
	if stale := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM dns_resolution
		WHERE client_address = $1 AND client_id IS DISTINCT FROM $2`, client.Address, leaseClient); stale != 0 {
		t.Errorf("%d lookups of the address still name the old identity", stale)
	}
	if attributed := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM domain_attribution a
		JOIN flow f ON f.id = a.flow_id WHERE f.observed_at = $1`, network.Now-40); attributed != 1 {
		t.Errorf("the flow the lookup names carries %d attributions after re-pointing", attributed)
	}
	if gone := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE id = $1", addressLevel); gone != 0 {
		t.Error("the superseded address-level identity is still there with nothing naming it")
	}
	settled := derivedSnapshot(t, network.DB)
	recomputeEverything(t, network)
	if derivedSnapshot(t, network.DB) != settled {
		t.Error("the slots after re-pointing differ from a recomputation")
	}
	if rows := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM client_volume_aggregate_24h
		WHERE client_id = $1`, leaseClient); rows == 0 {
		t.Error("the client family carries no slot under the better identity")
	}
}

func spanOfInstants(instants []int64) (int64, int64, bool) {
	if len(instants) == 0 {
		return 0, 0, false
	}
	low, high := instants[0], instants[0]
	for _, instant := range instants {
		low, high = min(low, instant), max(high, instant)
	}
	return low, high, true
}

func queryString(t *testing.T, database *store.Store, query string, arguments ...any) string {
	t.Helper()
	var value string
	if err := database.DB().QueryRow(query, arguments...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}
