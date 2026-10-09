package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// This firewall as an end: AC1 to AC6 of specs/SPEC-step-5a-live-corrections.md.
//
// Every fixture takes its interface and client counts as parameters and carries both
// address families; the addresses are example values the code under test reads back out
// of the rows and never names.

// upstreamV6 gives the test network's upstream interface an IPv6 address of its own, as
// discovery records one, and returns it.
func upstreamV6(t *testing.T, network *testNetwork) string {
	t.Helper()
	address := "2001:db8:ff00::2"
	bits := int64(64)
	if err := network.db.UpsertInterfaceAddress(context.Background(), InterfaceAddress{
		InterfaceID: network.upstream.id, SourceField: SourceFieldAddr6, Address: address,
		PrefixLength: &bits, AddressFamily: 6,
	}, nil, network.now-86400); err != nil {
		t.Fatalf("writing the upstream IPv6 address: %v", err)
	}
	return address
}

// firstClient returns the first client of one interface and family.
func firstClient(t *testing.T, network *testNetwork, interfaceID int64, v6 bool) (testClient, bool) {
	t.Helper()
	for _, client := range network.clients {
		if client.interfaceID == interfaceID && client.v6 == v6 {
			return client, true
		}
	}
	return testClient{}, false
}

// endState is how one end of a stored flow was placed.
type endState struct {
	firewall  bool
	iface     *int64
	client    *int64
	direction string
}

func flowEnd(t *testing.T, database *Store, address string, source bool) endState {
	t.Helper()
	column := "dst"
	if source {
		column = "src"
	}
	var (
		state             endState
		firewall          int64
		iface, client     *int64
		srcAddr, dstAddr  string
		trafficDirection  string
		selectedAddress   = column + "_address"
		selectedFirewall  = column + "_is_this_firewall"
		selectedInterface = column + "_interface_id"
		selectedClient    = column + "_client_id"
	)
	query := fmt.Sprintf(`SELECT %s, %s, %s, src_address, dst_address, traffic_direction
		FROM classified_flow WHERE %s = ? ORDER BY id LIMIT 1`, selectedFirewall, selectedInterface,
		selectedClient, selectedAddress)
	if err := database.DB().QueryRow(query, address).Scan(&firewall, &iface, &client, &srcAddr,
		&dstAddr, &trafficDirection); err != nil {
		t.Fatalf("reading the flow at %s: %v", address, err)
	}
	state.firewall, state.iface, state.client, state.direction = firewall == 1, iface, client, trafficDirection
	return state
}

// TestThisFirewallIsRecognisedAtEveryEndInBothFamilies is AC2.
func TestThisFirewallIsRecognisedAtEveryEndInBothFamilies(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {4, 13}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			upstream6 := upstreamV6(t, network)
			dns := int64(53)
			var specs []networkFlow
			expected := map[string]*int64{}
			// A client of each family on each inside interface, reaching the firewall's
			// own address on that interface.
			for _, iface := range network.inside {
				id := iface.id
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.id, v6)
					if !found {
						continue
					}
					target := iface.address
					if v6 {
						target = iface.v6
					}
					expected[target] = &id
					specs = append(specs, networkFlow{observedAt: network.now - 40, device: iface.device,
						direction: "in", src: client.address, dst: target, dstPort: &dns,
						protocol: "udp", action: "pass", bytes: 70})
				}
			}
			upstreamID := network.upstream.id
			expected[network.upstream.address] = &upstreamID
			expected[upstream6] = &upstreamID
			for index, address := range []string{network.upstream.address, upstream6} {
				v6 := index == 1
				// The firewall's own traffic, and an outside packet to the firewall.
				specs = append(specs,
					networkFlow{observedAt: network.now - 30, device: network.upstream.device,
						direction: "out", src: address, dst: outside(11, v6), dstPort: &dns,
						protocol: "udp", action: "pass", bytes: 80},
					networkFlow{observedAt: network.now - 20, device: network.upstream.device,
						direction: "in", src: outside(12, v6), dst: address, dstPort: &dns,
						protocol: "udp", action: "block", bytes: 60})
			}
			// The loopback, in both families.
			for _, loopback := range []string{"127.0.0.1", "::1"} {
				expected[loopback] = nil
				specs = append(specs, networkFlow{observedAt: network.now - 10,
					device: network.upstream.device, direction: "out", src: loopback,
					dst: outside(13, loopback == "::1"), dstPort: &dns, protocol: "udp",
					action: "pass", bytes: 50})
			}
			addresses := network.insert(t, specs)

			// A security event and a lookup from each firewall address, and a lookup
			// logged under the name `localhost`.
			suricata, _ := network.db.ProviderID(ctx, "security_event", "suricata")
			index := 0
			for address := range expected {
				index++
				if err := network.db.InsertSecurityEvent(ctx, suricata, SecurityEvent{
					ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: network.now - 5,
					IngestedAt: network.now - 1, RuleIdentity: "1", Signature: "example signature",
					EventAction: "allowed", SrcAddress: address, DstAddress: outside(14, false),
				}); err != nil {
					t.Fatalf("writing an event: %v", err)
				}
				if err := network.db.InsertDNSResolution(ctx, DNSResolution{
					LookupKey: fmt.Sprintf("example-firewall-lookup-%d", index), ClientAddress: address,
					Domain: "example.invalid", Resolver: "unbound", Action: "pass",
					LookedUpAt: network.now - 5, IngestedAt: network.now - 1,
				}); err != nil {
					t.Fatalf("writing a lookup: %v", err)
				}
			}
			localhost := "localhost"
			address, resolution, err := network.db.ResolveLeaseHostname(ctx, localhost, network.now-5)
			if err != nil || resolution != ClientResolutionThisFirewall {
				t.Fatalf("`localhost` resolved to %q, %q, %v; it names this firewall", address, resolution, err)
			}
			if err := network.db.InsertDNSResolution(ctx, DNSResolution{
				LookupKey: "example-localhost-lookup", ClientAddress: address, ClientHostname: &localhost,
				ClientResolution: resolution, Domain: "example.invalid", Resolver: "unbound",
				Action: "pass", LookedUpAt: network.now - 5, IngestedAt: network.now - 1,
			}); err != nil {
				t.Fatalf("writing the localhost lookup: %v", err)
			}
			for address := range expected {
				addresses = append(addresses, address)
			}
			addresses = append(addresses, address)
			network.classifyAndRefresh(t, addresses)
			if _, err := network.db.ReclassifyAll(ctx, network.now); err != nil {
				t.Fatalf("reclassifying everything: %v", err)
			}

			for address, iface := range expected {
				for _, source := range []bool{true, false} {
					if queryInt(t, network.db, fmt.Sprintf("SELECT count(*) FROM flow WHERE %s_address = ?",
						map[bool]string{true: "src", false: "dst"}[source]), address) == 0 {
						continue
					}
					end := flowEnd(t, network.db, address, source)
					if !end.firewall {
						t.Errorf("the end at %s (source %t) is not recorded as this firewall", address, source)
					}
					if render(end.iface) != render(iface) {
						t.Errorf("the end at %s records the interface %s, not %s", address, render(end.iface),
							render(iface))
					}
					if end.client != nil {
						t.Errorf("the end at %s carries the client %d", address, *end.client)
					}
				}
				if got := queryInt(t, network.db, `SELECT count(*) FROM security_event
					WHERE src_address = ? AND src_is_this_firewall = 1 AND src_client_id IS NULL
					  AND src_interface_id IS ?`, address, optional(iface)); got != 1 {
					t.Errorf("the event from %s is not recorded as this firewall's", address)
				}
				if got := queryInt(t, network.db, `SELECT count(*) FROM dns_resolution
					WHERE client_address = ? AND client_is_this_firewall = 1 AND client_id IS NULL
					  AND interface_id IS ?`, address, optional(iface)); got != 1 {
					t.Errorf("the lookup from %s is not recorded as this firewall's", address)
				}
				if clients := queryInt(t, network.db, "SELECT count(*) FROM client WHERE last_address = ?",
					address); clients != 0 {
					t.Errorf("%d client rows sit at the firewall address %s", clients, address)
				}
			}
			if got := queryInt(t, network.db, `SELECT count(*) FROM dns_resolution
				WHERE lookup_key = 'example-localhost-lookup' AND client_is_this_firewall = 1
				  AND client_id IS NULL`); got != 1 {
				t.Error("the lookup logged under `localhost` is not recorded as this firewall's")
			}
			if carried := queryInt(t, network.db, `SELECT count(*) FROM flow
				WHERE (src_is_this_firewall = 1 AND src_client_id IS NOT NULL)
				   OR (dst_is_this_firewall = 1 AND dst_client_id IS NOT NULL)`); carried != 0 {
				t.Errorf("%d flows carry a client on an end that is this firewall", carried)
			}
			if outsideEnds := queryInt(t, network.db, `SELECT count(*) FROM classified_flow
				WHERE traffic_direction IN ('inbound', 'outbound')
				  AND (src_is_this_firewall = 1 OR dst_is_this_firewall = 1)`); outsideEnds != 0 {
				t.Errorf("%d flows with an end that is this firewall count as crossing the edge", outsideEnds)
			}
		})
	}
}

// render prints a nullable id.
func render(value *int64) string {
	if value == nil {
		return "none"
	}
	return fmt.Sprint(*value)
}

// TestThisFirewallIsDecidedAsOfTheRowsInstant is AC3: an address the firewall held over
// an interval is this firewall for a record inside it, margin included, and not for one
// outside it.
func TestThisFirewallIsDecidedAsOfTheRowsInstant(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 7}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			margin := int64(DefaultDiscoveryIntervalSeconds)
			for _, v6 := range []bool{false, true} {
				// An address the upstream interface held from first to last, and holds no
				// longer: the latest discovery of the interface is later.
				held := outside(40, v6)
				first, last := network.now-20000, network.now-10000
				bits := int64(24)
				field := SourceFieldAddr4
				if v6 {
					bits, field = 64, SourceFieldAddr6
				}
				if _, err := network.db.DB().ExecContext(ctx, `INSERT INTO interface_address
					(interface_id, source_field, address, prefix_length, address_family, first_seen_at,
					 last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, network.upstream.id, field, held, bits,
					map[bool]int64{false: 4, true: 6}[v6], first, last); err != nil {
					t.Fatalf("writing the held address: %v", err)
				}
				if _, err := network.db.DB().ExecContext(ctx, "UPDATE interface SET last_seen_at = ? WHERE id = ?",
					network.now, network.upstream.id); err != nil {
					t.Fatalf("moving the discovery: %v", err)
				}
				peer := outside(41, v6)
				port := int64(443)
				cases := []struct {
					at   int64
					held bool
				}{
					{first - margin - 1, false}, {first - margin, true}, {first + 10, true},
					{last + margin, true}, {last + margin + 1, false}, {network.now - 10, false},
				}
				var specs []networkFlow
				for _, one := range cases {
					specs = append(specs, networkFlow{observedAt: one.at, device: network.upstream.device,
						direction: "out", src: held, dst: peer, dstPort: &port, protocol: "tcp",
						action: "pass", bytes: 100})
				}
				network.classifyAndRefresh(t, network.insert(t, specs))
				for _, one := range cases {
					got := queryInt(t, network.db, `SELECT src_is_this_firewall FROM flow
						WHERE src_address = ? AND observed_at = ?`, held, one.at)
					if (got == 1) != one.held {
						t.Errorf("a record at %+d s from the holding is this firewall: %t, want %t",
							one.at-first, got == 1, one.held)
					}
				}
			}
		})
	}
}

// TestThisFirewallIsItsOwnKeyAndHasATreeNodeOnEachSide is AC4.
func TestThisFirewallIsItsOwnKeyAndHasATreeNodeOnEachSide(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {4, 11}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			upstream6 := upstreamV6(t, network)
			specs := network.flowsFor(200, 4*3600)
			dns := int64(53)
			for _, iface := range network.inside {
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.id, v6)
					if !found {
						continue
					}
					target := iface.address
					if v6 {
						target = iface.v6
					}
					specs = append(specs, networkFlow{observedAt: network.now - 77, device: iface.device,
						direction: "in", src: client.address, dst: target, dstPort: &dns, protocol: "udp",
						action: "pass", bytes: 90})
				}
			}
			for index, address := range []string{network.upstream.address, upstream6} {
				specs = append(specs, networkFlow{observedAt: network.now - 66, device: network.upstream.device,
					direction: "out", src: address, dst: outside(30+index, index == 1), dstPort: &dns,
					protocol: "udp", action: "pass", bytes: 120})
			}
			network.classifyAndRefresh(t, network.insert(t, specs))
			recomputeEverything(t, network)

			// The volume key equals a direct sum over flow.
			direct := queryInt(t, network.db, `SELECT coalesce(sum(packet_bytes), 0) FROM flow
				WHERE src_is_this_firewall = 1 OR dst_is_this_firewall = 1`)
			if direct == 0 {
				t.Fatal("no flow has an end that is this firewall, so the key is not exercised")
			}
			for _, period := range Periods() {
				got := queryInt(t, network.db, fmt.Sprintf(`SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_%s
					WHERE traffic_direction IN ('to_this_firewall', 'from_this_firewall')
					  AND traffic_scope = 'this_firewall'`, period.Name))
				if got != direct {
					t.Errorf("this firewall's %s key holds %d bytes, a direct sum %d", period.Name, got, direct)
				}
			}
			// The outside column holds none of them.
			if leaked := queryInt(t, network.db, `SELECT count(*) FROM volume_aggregate_1h
				WHERE traffic_direction IN ('inbound', 'outbound')
				  AND peer_address IN (SELECT address FROM this_firewall_address)`); leaked != 0 {
				t.Errorf("%d outside-column rows name an address this firewall holds", leaked)
			}
			window, err := network.db.ReadVolumeWindow(ctx, network.now-86400, network.now+1, network.now)
			if err != nil {
				t.Fatalf("reading the window: %v", err)
			}
			if got := window.ByDirection()[DirectionToThisFirewall].Bytes +
				window.ByDirection()[DirectionFromThisFirewall].Bytes; got != direct {
				t.Errorf("the window's this-firewall directions hold %d bytes, a direct sum %d", got, direct)
			}

			for _, root := range []TreeRoot{TreeRootInterface, TreeRootClient, TreeRootOwner} {
				tree, err := network.db.ReadConnectionTree(ctx, network.now-86400, network.now+1, root)
				if err != nil {
					t.Fatalf("reading the tree: %v", err)
				}
				var insideFirewall, outsideFirewall int64
				for _, node := range tree.Inside {
					if node.Key == TreeNodeThisFirewall {
						insideFirewall = node.Bytes
					}
					assertLevelSums(t, root, node)
				}
				for _, node := range tree.Outside {
					if node.Key == TreeNodeThisFirewall {
						outsideFirewall = node.Bytes
					}
					assertLevelSums(t, root, node)
				}
				fromFirewall := queryInt(t, network.db, `SELECT coalesce(sum(packet_bytes), 0) FROM classified_flow
					WHERE traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND peer_address IS NOT NULL`)
				toFirewall := queryInt(t, network.db, `SELECT coalesce(sum(packet_bytes), 0) FROM classified_flow
					WHERE traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND peer_address IS NULL`)
				if insideFirewall != fromFirewall || fromFirewall == 0 {
					t.Errorf("%s root: the inside this-firewall node holds %d bytes, its flows %d",
						root, insideFirewall, fromFirewall)
				}
				if outsideFirewall != toFirewall || toFirewall == 0 {
					t.Errorf("%s root: the outside this-firewall node holds %d bytes, its flows %d",
						root, outsideFirewall, toFirewall)
				}
				// The firewall's ends are under no operator, no unplaced condition, no
				// "interface:none" and no "client:none": every flow of this firewall is
				// accounted for by the two nodes above, so the other nodes hold exactly the
				// rest.
				var rest int64
				for _, node := range tree.Inside {
					if node.Key != TreeNodeThisFirewall {
						rest += node.Bytes
					}
				}
				others := queryInt(t, network.db, `SELECT coalesce(sum(packet_bytes), 0) FROM classified_flow
					WHERE observed_at >= ? AND observed_at < ? AND exit_leg_device IS NULL
					  AND NOT (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
					           AND (peer_address IS NOT NULL OR local_interface_id IS NULL))`,
					network.now-86400, network.now+1)
				if rest != others {
					t.Errorf("%s root: the other inside nodes hold %d bytes, the other flows %d", root, rest, others)
				}
			}
		})
	}
}

// assertLevelSums checks that a node's children sum to it.
func assertLevelSums(t *testing.T, root TreeRoot, node *TreeNode) {
	t.Helper()
	if len(node.Children) == 0 {
		return
	}
	var bytes, connections int64
	for _, child := range node.Children {
		bytes += child.Bytes
		connections += child.Connections
	}
	if bytes != node.Bytes || connections != node.Connections {
		t.Errorf("%s root: the children of %s sum to %d bytes and %d connections, the node holds %d and %d",
			root, node.Key, bytes, connections, node.Bytes, node.Connections)
	}
}

// TestAFlowToThisFirewallIsNeverAttributedAndAFlowFromItIsOutsideTheRate is AC5.
func TestAFlowToThisFirewallIsNeverAttributedAndAFlowFromItIsOutsideTheRate(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 8}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			dns := int64(53)
			var specs []networkFlow
			for _, v6 := range []bool{false, true} {
				client, found := firstClient(t, network, network.inside[0].id, v6)
				if !found {
					continue
				}
				target := network.inside[0].address
				if v6 {
					target = network.inside[0].v6
				}
				specs = append(specs, networkFlow{observedAt: network.now - 50, device: network.inside[0].device,
					direction: "in", src: client.address, dst: target, dstPort: &dns, protocol: "udp",
					action: "pass", bytes: 70})
				// The client looked one name up just before: the only lookup in the window.
				if err := network.db.InsertDNSResolution(ctx, DNSResolution{
					LookupKey: fmt.Sprintf("example-before-firewall-%t", v6), ClientAddress: client.address,
					Domain: "example-one.example.invalid", Resolver: "unbound", Action: "pass",
					AnswerSource: ptr("Recursion"), LookedUpAt: network.now - 52, IngestedAt: network.now - 1,
				}); err != nil {
					t.Fatalf("writing a lookup: %v", err)
				}
			}
			specs = append(specs, networkFlow{observedAt: network.now - 40, device: network.upstream.device,
				direction: "out", src: network.upstream.address, dst: outside(50, false), dstPort: &dns,
				protocol: "udp", action: "pass", bytes: 90})
			addresses := network.insert(t, specs)
			for _, client := range network.clients {
				addresses = append(addresses, client.address)
			}
			network.classifyAndRefresh(t, addresses)
			candidates, err := Statement("attribution_candidates")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := network.db.DB().QueryContext(ctx, candidates, sql.Named("from", network.now-100),
				sql.Named("to", network.now))
			if err != nil {
				t.Fatalf("reading the candidates: %v", err)
			}
			var candidateIDs []int64
			for rows.Next() {
				var id, at int64
				var client any
				var address string
				if err := rows.Scan(&id, &at, &client, &address); err != nil {
					t.Fatal(err)
				}
				candidateIDs = append(candidateIDs, id)
			}
			_ = rows.Close()
			for _, id := range candidateIDs {
				if queryInt(t, network.db, "SELECT dst_is_this_firewall FROM flow WHERE id = ?", id) == 1 {
					t.Errorf("the flow %d to this firewall is an attribution candidate", id)
				}
			}
			if _, err := network.db.Attribute(ctx, network.now-100, network.now, 5, network.now); err != nil {
				t.Fatalf("attributing: %v", err)
			}
			if named := queryInt(t, network.db, `SELECT count(*) FROM domain_attribution AS a
				JOIN flow AS f ON f.id = a.flow_id WHERE f.dst_is_this_firewall = 1`); named != 0 {
				t.Errorf("%d flows to this firewall carry a site name", named)
			}
			rate, err := network.db.ReadAttributionRate(ctx, network.now-100, network.now, nil)
			if err != nil {
				t.Fatalf("reading the rate: %v", err)
			}
			if rate.EligibleFlows != 0 {
				t.Errorf("the rate counts %d eligible flows; the window holds only flows to and from this firewall",
					rate.EligibleFlows)
			}
		})
	}
}

// TestAnAttributionMadeBeforeTheFirewallWasRecognisedIsRemoved: a 5A-era attribution of a
// flow whose destination is this firewall goes at the next attribution of its window.
func TestAnAttributionMadeBeforeTheFirewallWasRecognisedIsRemoved(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	client, _ := firstClient(t, network, network.inside[0].id, false)
	dns := int64(53)
	network.insert(t, []networkFlow{{observedAt: network.now - 50, device: network.inside[0].device,
		direction: "in", src: client.address, dst: network.inside[0].address, dstPort: &dns,
		protocol: "udp", action: "pass", bytes: 70}})
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-stale", ClientAddress: client.address, Domain: "example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: ptr("Recursion"),
		LookedUpAt: network.now - 52, IngestedAt: network.now - 1,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	// The attribution 5A wrote, when the destination was an unplaced end.
	if _, err := network.db.DB().ExecContext(ctx, `INSERT INTO domain_attribution (flow_id, dns_resolution_id,
		site_name, correlation_delay_seconds, attributed_at)
		SELECT f.id, r.id, 'example.invalid', 2, ? FROM flow AS f, dns_resolution AS r
		WHERE r.lookup_key = 'example-stale'`, network.now); err != nil {
		t.Fatalf("writing the stale attribution: %v", err)
	}
	network.classifyAndRefresh(t, []string{client.address, network.inside[0].address})
	result, err := network.db.Attribute(ctx, network.now-100, network.now, 5, network.now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if left := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); left != 0 {
		t.Errorf("%d attributions of a flow to this firewall survived", left)
	}
	if len(result.FlowInstants) == 0 {
		t.Error("removing the attribution did not report its slot for the refresh")
	}
}

// TestReclassifyEverythingPurgesThePhantomClientsAtFirewallAddresses is AC6: a database
// holding the clients 5A minted at the firewall's own addresses is corrected, its slots
// equal a recomputation, and a second run writes nothing.
func TestReclassifyEverythingPurgesThePhantomClientsAtFirewallAddresses(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {4, 12}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			specs := network.flowsFor(150, 2*3600)
			dns := int64(53)
			var phantoms []string
			for _, iface := range network.inside {
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.id, v6)
					if !found {
						continue
					}
					target := iface.address
					if v6 {
						target = iface.v6
					}
					phantoms = append(phantoms, target)
					specs = append(specs, networkFlow{observedAt: network.now - 33, device: iface.device,
						direction: "in", src: client.address, dst: target, dstPort: &dns, protocol: "udp",
						action: "pass", bytes: 64})
				}
			}
			addresses := network.insert(t, specs)
			network.classifyAndRefresh(t, addresses)

			// The 5A state: a phantom address-level client at each firewall address, named
			// by its flows as their destination, an east-west scope, and the 5A fingerprint.
			for _, address := range phantoms {
				var interfaceID int64
				if err := network.db.DB().QueryRow(`SELECT interface_id FROM this_firewall_address
					WHERE address = ? LIMIT 1`, address).Scan(&interfaceID); err != nil {
					t.Fatalf("reading the interface of %s: %v", address, err)
				}
				id, err := network.db.UpsertClient(ctx, Client{
					Identity:    ClientIdentity{Kind: IdentityAddressInInterface, Key: AddressIdentityKey(&interfaceID, address, network.now)},
					InterfaceID: &interfaceID, LastAddress: &address,
				}, network.now)
				if err != nil {
					t.Fatalf("writing a phantom: %v", err)
				}
				if _, err := network.db.DB().ExecContext(ctx, `UPDATE flow SET dst_is_this_firewall = 0,
					dst_interface_id = ?, dst_client_id = ?,
					traffic_scope = CASE WHEN src_interface_id IS NOT NULL THEN 'east_west' ELSE 'north_south' END
					WHERE dst_address = ?`, interfaceID, id, address); err != nil {
					t.Fatalf("writing the 5A state: %v", err)
				}
				if _, err := network.db.DB().ExecContext(ctx, `UPDATE address_classification
					SET evidence = ? WHERE address = ?`, fmt.Sprintf("network|%d|-", interfaceID), address); err != nil {
					t.Fatalf("writing the 5A fingerprint: %v", err)
				}
			}
			recomputeEverything(t, network)
			if before := queryInt(t, network.db, `SELECT count(*) FROM client
				WHERE last_address IN (SELECT address FROM this_firewall_address)`); before == 0 {
				t.Fatal("the 5A state holds no phantom, so the purge is not exercised")
			}

			result, err := network.db.ReclassifyAll(ctx, network.now)
			if err != nil {
				t.Fatalf("reclassifying everything: %v", err)
			}
			if _, err := network.db.RefreshAggregates(ctx, network.now, network.now, result.FlowInstants); err != nil {
				t.Fatalf("refreshing: %v", err)
			}
			if left := queryInt(t, network.db, `SELECT count(*) FROM client
				WHERE last_address IN (SELECT address FROM this_firewall_address)`); left != 0 {
				t.Errorf("%d phantom clients at the firewall's addresses survived", left)
			}
			if result.ClientsPurged == 0 {
				t.Error("the reclassification reports no client purged")
			}
			slots := derivedSnapshot(t, network.db)
			recomputeEverything(t, network)
			if recomputed := derivedSnapshot(t, network.db); recomputed != slots {
				t.Error("the slots after the reclassification differ from a recomputation")
			}

			second, err := network.db.ReclassifyAll(ctx, network.now)
			if err != nil {
				t.Fatalf("reclassifying again: %v", err)
			}
			if len(second.FlowInstants) != 0 || len(second.LookupInstants) != 0 || second.ClientsPurged != 0 ||
				second.Placed != 0 {
				t.Errorf("a second run wrote: %d flows, %d lookups, %d clients purged, %d placed",
					len(second.FlowInstants), len(second.LookupInstants), second.ClientsPurged, second.Placed)
			}
		})
	}
}

// TestTheTermIsOpnsensesThisFirewall is AC1 in code: the identifiers use the term, and the
// loopback is recognised by its form, address or name, in both families.
func TestTheTermIsOpnsensesThisFirewall(t *testing.T) {
	t.Parallel()
	if ScopeThisFirewall != "this_firewall" || DirectionToThisFirewall != "to_this_firewall" ||
		DirectionFromThisFirewall != "from_this_firewall" || ClientResolutionThisFirewall != "this_firewall_hostname" ||
		TreeNodeThisFirewall != "this_firewall" {
		t.Error("an identifier does not use the term this firewall")
	}
	for _, loopback := range []string{"127.0.0.1", "127.8.9.10", "::1", "localhost", "LocalHost.", "::ffff:127.0.0.1"} {
		if !IsLoopback(loopback) {
			t.Errorf("%s is not recognised as the loopback", loopback)
		}
	}
	for _, other := range []string{"198.51.100.1", "2001:db8::1", "localhost.example.invalid", "example-host"} {
		if IsLoopback(other) {
			t.Errorf("%s is recognised as the loopback", other)
		}
	}
}

// discoveryPass writes one discovery as internal/collect does: the previous discovery's
// instant read before anything is written, then every interface and the addresses it
// reported, in the order given. upstream lists the upstream addresses this discovery read;
// every inside interface reports its own two addresses.
func discoveryPass(t *testing.T, network *testNetwork, at int64, upstreamFirst bool, upstream []InterfaceAddress) {
	t.Helper()
	ctx := context.Background()
	var previous *int64
	if latest, found, err := network.db.LatestInterfaceDiscoveryAt(ctx); err != nil {
		t.Fatalf("reading the previous discovery: %v", err)
	} else if found {
		previous = &latest
	}
	writeInterface := func(id int64, addresses []InterfaceAddress) {
		if _, err := network.db.DB().ExecContext(ctx, "UPDATE interface SET last_seen_at = ? WHERE id = ?",
			at, id); err != nil {
			t.Fatalf("writing an interface: %v", err)
		}
		for _, address := range addresses {
			if err := network.db.UpsertInterfaceAddress(ctx, address, previous, at); err != nil {
				t.Fatalf("writing an address: %v", err)
			}
		}
	}
	bits4, bits6 := int64(24), int64(64)
	inside := func() {
		for _, iface := range network.inside {
			writeInterface(iface.id, []InterfaceAddress{
				{InterfaceID: iface.id, SourceField: SourceFieldAddr4, Address: iface.address, PrefixLength: &bits4, AddressFamily: 4},
				{InterfaceID: iface.id, SourceField: SourceFieldAddr6, Address: iface.v6, PrefixLength: &bits6, AddressFamily: 6},
			})
		}
	}
	if upstreamFirst {
		writeInterface(network.upstream.id, upstream)
		inside()
	} else {
		inside()
		writeInterface(network.upstream.id, upstream)
	}
}

// TestAnAddressReleasedAndReacquiredIsNotHeldInBetween is AC3 across two holdings of one
// address: discovery read it, then twice did not read it at all, then read it again. The
// holdings are two rows whatever order the interfaces are written in, and a record made
// between them -- further than the margin from both -- is not this firewall, while
// records inside either holding are.
func TestAnAddressReleasedAndReacquiredIsNotHeldInBetween(t *testing.T) {
	t.Parallel()
	for _, upstreamFirst := range []bool{false, true} {
		for _, counts := range [][2]int{{2, 4}, {3, 7}} {
			t.Run(fmt.Sprintf("upstream first %t, %d interfaces, %d clients", upstreamFirst, counts[0], counts[1]),
				func(t *testing.T) {
					network := newTestNetwork(t, counts[0], counts[1])
					held4, held6 := outside(120, false), outside(120, true)
					bits4, bits6 := int64(24), int64(64)
					read := []InterfaceAddress{
						{InterfaceID: network.upstream.id, SourceField: SourceFieldAddr4, Address: held4,
							PrefixLength: &bits4, AddressFamily: 4},
						{InterfaceID: network.upstream.id, SourceField: SourceFieldAddr6, Address: held6,
							PrefixLength: &bits6, AddressFamily: 6},
					}
					for _, discovery := range []struct {
						at   int64
						read bool
					}{
						{network.now - 9000, true}, {network.now - 8700, true}, {network.now - 8400, false},
						{network.now - 8100, false}, {network.now - 7800, true},
					} {
						var upstream []InterfaceAddress
						if discovery.read {
							upstream = read
						}
						discoveryPass(t, network, discovery.at, upstreamFirst, upstream)
					}
					margin := int64(DefaultDiscoveryIntervalSeconds)
					port := int64(443)
					for _, held := range []string{held4, held6} {
						if holdings := queryInt(t, network.db, `SELECT count(*) FROM interface_address
							WHERE address = ?`, held); holdings != 2 {
							t.Errorf("the address %s released and re-acquired is %d rows, not two holdings", held, holdings)
						}
						v6 := held == held6
						cases := []struct {
							at   int64
							held bool
						}{
							{network.now - 8850, true},          // inside the first holding
							{network.now - 8700 + margin, true}, // its end, margin included
							{network.now - 8250, false},         // between the holdings
							{network.now - 7600, true},          // inside the second
						}
						var specs []networkFlow
						for _, one := range cases {
							specs = append(specs, networkFlow{observedAt: one.at, device: network.upstream.device,
								direction: "out", src: held, dst: outside(122, v6), dstPort: &port, protocol: "tcp",
								action: "pass", bytes: 100})
						}
						network.classifyAndRefresh(t, network.insert(t, specs))
						for _, one := range cases {
							got := queryInt(t, network.db, `SELECT src_is_this_firewall FROM flow
								WHERE src_address = ? AND observed_at = ?`, held, one.at)
							if (got == 1) != one.held {
								t.Errorf("a record from %s at %d is this firewall: %t, want %t", held,
									one.at-network.now, got == 1, one.held)
							}
						}
					}
				})
		}
	}
}
