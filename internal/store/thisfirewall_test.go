package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// This firewall as an end: AC1 to AC6 of specs/SPEC-step-5a-live-corrections.md.
//
// Every fixture takes its interface and client counts as parameters and carries both
// address families; the addresses are example values the code under test reads back out
// of the rows and never names.

// upstreamV6 gives the test network's upstream interface an IPv6 address of its own, as
// discovery records one, and returns it.
func upstreamV6(t *testing.T, network *storetest.Network) string {
	t.Helper()
	address := "2001:db8:ff00::2"
	bits := int64(64)
	if err := network.DB.UpsertInterfaceAddress(context.Background(), vocab.InterfaceAddress{
		InterfaceID: network.Upstream.ID, SourceField: vocab.SourceFieldAddr6, Address: address,
		PrefixLength: &bits, AddressFamily: 6,
	}, nil, network.Now-86400); err != nil {
		t.Fatalf("writing the upstream IPv6 address: %v", err)
	}
	return address
}

// firstClient returns the first client of one interface and family.
func firstClient(t *testing.T, network *storetest.Network, interfaceID int64, v6 bool) (storetest.Client, bool) {
	t.Helper()
	for _, client := range network.Clients {
		if client.InterfaceID == interfaceID && client.V6 == v6 {
			return client, true
		}
	}
	return storetest.Client{}, false
}

// endState is how one end of a stored flow was placed.
type endState struct {
	firewall  bool
	iface     *int64
	client    *int64
	direction string
}

func flowEnd(t *testing.T, database *store.Store, address string, source bool) endState {
	t.Helper()
	column := "dst"
	if source {
		column = "src"
	}
	var (
		state             endState
		firewall          bool
		iface, client     *int64
		srcAddr, dstAddr  string
		trafficDirection  string
		selectedAddress   = column + "_address"
		selectedFirewall  = column + "_is_this_firewall"
		selectedInterface = column + "_interface_id"
		selectedClient    = column + "_client_id"
	)
	query := fmt.Sprintf(`SELECT %s, %s, %s, src_address, dst_address, traffic_direction
		FROM classified_flow WHERE %s = $1 ORDER BY id LIMIT 1`, selectedFirewall, selectedInterface,
		selectedClient, selectedAddress)
	if err := database.DB().QueryRow(query, address).Scan(&firewall, &iface, &client, &srcAddr,
		&dstAddr, &trafficDirection); err != nil {
		t.Fatalf("reading the flow at %s: %v", address, err)
	}
	state.firewall, state.iface, state.client, state.direction = firewall, iface, client, trafficDirection
	return state
}

// TestThisFirewallIsRecognisedAtEveryEndInBothFamilies is AC2.
func TestThisFirewallIsRecognisedAtEveryEndInBothFamilies(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {4, 13}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			upstream6 := upstreamV6(t, network)
			dns := int64(53)
			var specs []storetest.Flow
			expected := map[string]*int64{}
			// A client of each family on each inside interface, reaching the firewall's
			// own address on that interface.
			for _, iface := range network.Inside {
				id := iface.ID
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.ID, v6)
					if !found {
						continue
					}
					target := iface.Address
					if v6 {
						target = iface.V6
					}
					expected[target] = &id
					specs = append(specs, storetest.Flow{ObservedAt: network.Now - 40, Device: iface.Device,
						Direction: "in", Src: client.Address, Dst: target, DstPort: &dns,
						Protocol: "udp", Action: "pass", Bytes: 70})
				}
			}
			upstreamID := network.Upstream.ID
			expected[network.Upstream.Address] = &upstreamID
			expected[upstream6] = &upstreamID
			for index, address := range []string{network.Upstream.Address, upstream6} {
				v6 := index == 1
				// The firewall's own traffic, and an outside packet to the firewall.
				specs = append(specs,
					storetest.Flow{ObservedAt: network.Now - 30, Device: network.Upstream.Device,
						Direction: "out", Src: address, Dst: storetest.Outside(11, v6), DstPort: &dns,
						Protocol: "udp", Action: "pass", Bytes: 80},
					storetest.Flow{ObservedAt: network.Now - 20, Device: network.Upstream.Device,
						Direction: "in", Src: storetest.Outside(12, v6), Dst: address, DstPort: &dns,
						Protocol: "udp", Action: "block", Bytes: 60})
			}
			// The loopback, in both families.
			for _, loopback := range []string{"127.0.0.1", "::1"} {
				expected[loopback] = nil
				specs = append(specs, storetest.Flow{ObservedAt: network.Now - 10,
					Device: network.Upstream.Device, Direction: "out", Src: loopback,
					Dst: storetest.Outside(13, loopback == "::1"), DstPort: &dns, Protocol: "udp",
					Action: "pass", Bytes: 50})
			}
			addresses := network.Insert(t, specs)

			// A security event and a lookup from each firewall address, and a lookup
			// logged under the name `localhost`.
			suricata, _ := network.DB.ProviderID(ctx, "security_event", "suricata")
			index := 0
			for address := range expected {
				index++
				if err := network.DB.InsertSecurityEvent(ctx, suricata, vocab.SecurityEvent{
					ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: network.Now - 5,
					IngestedAt: network.Now - 1, RuleIdentity: "1", Signature: "example signature",
					EventAction: "allowed", SrcAddress: address, DstAddress: storetest.Outside(14, false),
				}); err != nil {
					t.Fatalf("writing an event: %v", err)
				}
				if err := storetest.InsertDNSResolution(ctx, network.DB, vocab.DNSResolution{
					LookupKey: fmt.Sprintf("example-firewall-lookup-%d", index), ClientAddress: address,
					Domain: "example.invalid", Resolver: "unbound", Action: "pass",
					LookedUpAt: network.Now - 5, IngestedAt: network.Now - 1,
				}); err != nil {
					t.Fatalf("writing a lookup: %v", err)
				}
			}
			localhost := "localhost"
			address, resolution, err := network.DB.ResolveLeaseHostname(ctx, localhost, network.Now-5)
			if err != nil || resolution != vocab.ClientResolutionThisFirewall {
				t.Fatalf("`localhost` resolved to %q, %q, %v; it names this firewall", address, resolution, err)
			}
			if err := storetest.InsertDNSResolution(ctx, network.DB, vocab.DNSResolution{
				LookupKey: "example-localhost-lookup", ClientAddress: address, ClientHostname: &localhost,
				ClientResolution: resolution, Domain: "example.invalid", Resolver: "unbound",
				Action: "pass", LookedUpAt: network.Now - 5, IngestedAt: network.Now - 1,
			}); err != nil {
				t.Fatalf("writing the localhost lookup: %v", err)
			}
			for address := range expected {
				addresses = append(addresses, address)
			}
			addresses = append(addresses, address)
			network.ClassifyAndRefresh(t, addresses)
			if _, err := network.DB.ReclassifyAll(ctx, network.Now); err != nil {
				t.Fatalf("reclassifying everything: %v", err)
			}

			for address, iface := range expected {
				for _, source := range []bool{true, false} {
					if storetest.QueryInt(t, network.DB, fmt.Sprintf("SELECT count(*) FROM flow WHERE %s_address = $1",
						map[bool]string{true: "src", false: "dst"}[source]), address) == 0 {
						continue
					}
					end := flowEnd(t, network.DB, address, source)
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
				if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM security_event
					WHERE src_address = $1 AND src_is_this_firewall AND src_client_id IS NULL
					  AND src_interface_id IS NOT DISTINCT FROM $2`, address, sqldb.Optional(iface)); got != 1 {
					t.Errorf("the event from %s is not recorded as this firewall's", address)
				}
				if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM dns_resolution
					WHERE client_address = $1 AND client_is_this_firewall AND client_id IS NULL
					  AND interface_id IS NOT DISTINCT FROM $2`, address, sqldb.Optional(iface)); got != 1 {
					t.Errorf("the lookup from %s is not recorded as this firewall's", address)
				}
				if clients := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE last_address = $1",
					address); clients != 0 {
					t.Errorf("%d client rows sit at the firewall address %s", clients, address)
				}
			}
			if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM dns_resolution
				WHERE lookup_key = 'example-localhost-lookup' AND client_is_this_firewall
				  AND client_id IS NULL`); got != 1 {
				t.Error("the lookup logged under `localhost` is not recorded as this firewall's")
			}
			if carried := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
				WHERE (src_is_this_firewall AND src_client_id IS NOT NULL)
				   OR (dst_is_this_firewall AND dst_client_id IS NOT NULL)`); carried != 0 {
				t.Errorf("%d flows carry a client on an end that is this firewall", carried)
			}
			if outsideEnds := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM classified_flow
				WHERE traffic_direction IN ('inbound', 'outbound')
				  AND (src_is_this_firewall OR dst_is_this_firewall)`); outsideEnds != 0 {
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			margin := int64(flows.DefaultDiscoveryIntervalSeconds)
			for _, v6 := range []bool{false, true} {
				// An address the upstream interface held from first to last, and holds no
				// longer: the latest discovery of the interface is later.
				held := storetest.Outside(40, v6)
				first, last := network.Now-20000, network.Now-10000
				bits := int64(24)
				field := vocab.SourceFieldAddr4
				if v6 {
					bits, field = 64, vocab.SourceFieldAddr6
				}
				if _, err := network.DB.DB().ExecContext(ctx, `INSERT INTO interface_address
					(interface_id, source_field, address, prefix_length, address_family, first_seen_at,
					 last_seen_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`, network.Upstream.ID, field, held, bits,
					map[bool]int64{false: 4, true: 6}[v6], first, last); err != nil {
					t.Fatalf("writing the held address: %v", err)
				}
				if _, err := network.DB.DB().ExecContext(ctx, "UPDATE interface SET last_seen_at = $1 WHERE id = $2",
					network.Now, network.Upstream.ID); err != nil {
					t.Fatalf("moving the discovery: %v", err)
				}
				peer := storetest.Outside(41, v6)
				port := int64(443)
				cases := []struct {
					at   int64
					held bool
				}{
					{first - margin - 1, false}, {first - margin, true}, {first + 10, true},
					{last + margin, true}, {last + margin + 1, false}, {network.Now - 10, false},
				}
				var specs []storetest.Flow
				for _, one := range cases {
					specs = append(specs, storetest.Flow{ObservedAt: one.at, Device: network.Upstream.Device,
						Direction: "out", Src: held, Dst: peer, DstPort: &port, Protocol: "tcp",
						Action: "pass", Bytes: 100})
				}
				network.ClassifyAndRefresh(t, network.Insert(t, specs))
				for _, one := range cases {
					got := storetest.QueryInt(t, network.DB, `SELECT CAST(src_is_this_firewall AS INTEGER) FROM flow
						WHERE src_address = $1 AND observed_at = $2`, held, one.at)
					if (got == 1) != one.held {
						t.Errorf("a record at %+d s from the holding is this firewall: %t, want %t",
							one.at-first, got == 1, one.held)
					}
				}
			}
		})
	}
}

// TestThisFirewallIsItsOwnKeyInTheVolumeFamilyAndTheWindow is AC4 for the volume family and the
// window. The connection tree AC4 also names is now the connection_tree widget plugin's, whose level
// sums TestEveryReconciliationCountsAPairedConnectionOnce (internal/widget/widgets) checks.
func TestThisFirewallIsItsOwnKeyInTheVolumeFamilyAndTheWindow(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {4, 11}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			upstream6 := upstreamV6(t, network)
			specs := network.FlowsFor(200, 4*3600)
			dns := int64(53)
			for _, iface := range network.Inside {
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.ID, v6)
					if !found {
						continue
					}
					target := iface.Address
					if v6 {
						target = iface.V6
					}
					specs = append(specs, storetest.Flow{ObservedAt: network.Now - 77, Device: iface.Device,
						Direction: "in", Src: client.Address, Dst: target, DstPort: &dns, Protocol: "udp",
						Action: "pass", Bytes: 90})
				}
			}
			for index, address := range []string{network.Upstream.Address, upstream6} {
				specs = append(specs, storetest.Flow{ObservedAt: network.Now - 66, Device: network.Upstream.Device,
					Direction: "out", Src: address, Dst: storetest.Outside(30+index, index == 1), DstPort: &dns,
					Protocol: "udp", Action: "pass", Bytes: 120})
			}
			network.ClassifyAndRefresh(t, network.Insert(t, specs))
			recomputeEverything(t, network)

			// The volume key equals a direct sum over flow.
			direct := storetest.QueryInt(t, network.DB, `SELECT coalesce(sum(packet_bytes), 0) FROM flow
				WHERE src_is_this_firewall OR dst_is_this_firewall`)
			if direct == 0 {
				t.Fatal("no flow has an end that is this firewall, so the key is not exercised")
			}
			for _, period := range vocab.Periods() {
				got := storetest.QueryInt(t, network.DB, fmt.Sprintf(`SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_%s
					WHERE traffic_direction IN ('to_this_firewall', 'from_this_firewall')
					  AND traffic_scope = 'this_firewall'`, period.Name))
				if got != direct {
					t.Errorf("this firewall's %s key holds %d bytes, a direct sum %d", period.Name, got, direct)
				}
			}
			// The outside column holds none of them.
			if leaked := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM volume_aggregate_1h
				WHERE traffic_direction IN ('inbound', 'outbound')
				  AND peer_address IN (SELECT address FROM this_firewall_address)`); leaked != 0 {
				t.Errorf("%d outside-column rows name an address this firewall holds", leaked)
			}
			window, err := network.DB.ReadVolumeWindow(ctx, network.Now-86400, network.Now+1, network.Now)
			if err != nil {
				t.Fatalf("reading the window: %v", err)
			}
			if got := window.ByDirection()[vocab.DirectionToThisFirewall].Bytes +
				window.ByDirection()[vocab.DirectionFromThisFirewall].Bytes; got != direct {
				t.Errorf("the window's this-firewall directions hold %d bytes, a direct sum %d", got, direct)
			}
		})
	}
}

// TestAFlowToThisFirewallIsNeverAttributedAndAFlowFromItIsOutsideTheRate is AC5.
func TestAFlowToThisFirewallIsNeverAttributedAndAFlowFromItIsOutsideTheRate(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 8}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			dns := int64(53)
			var specs []storetest.Flow
			for _, v6 := range []bool{false, true} {
				client, found := firstClient(t, network, network.Inside[0].ID, v6)
				if !found {
					continue
				}
				target := network.Inside[0].Address
				if v6 {
					target = network.Inside[0].V6
				}
				specs = append(specs, storetest.Flow{ObservedAt: network.Now - 50, Device: network.Inside[0].Device,
					Direction: "in", Src: client.Address, Dst: target, DstPort: &dns, Protocol: "udp",
					Action: "pass", Bytes: 70})
				// The client looked one name up just before: the only lookup in the window.
				if err := storetest.InsertDNSResolution(ctx, network.DB, vocab.DNSResolution{
					LookupKey: fmt.Sprintf("example-before-firewall-%t", v6), ClientAddress: client.Address,
					Domain: "example-one.example.invalid", Resolver: "unbound", Action: "pass",
					AnswerSource: storetest.Ptr("Recursion"), LookedUpAt: network.Now - 52, IngestedAt: network.Now - 1,
				}); err != nil {
					t.Fatalf("writing a lookup: %v", err)
				}
			}
			specs = append(specs, storetest.Flow{ObservedAt: network.Now - 40, Device: network.Upstream.Device,
				Direction: "out", Src: network.Upstream.Address, Dst: storetest.Outside(50, false), DstPort: &dns,
				Protocol: "udp", Action: "pass", Bytes: 90})
			addresses := network.Insert(t, specs)
			for _, client := range network.Clients {
				addresses = append(addresses, client.Address)
			}
			network.ClassifyAndRefresh(t, addresses)
			candidates, err := sqldb.Statement("attribution_candidates")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := sqldb.Query(ctx, network.DB.DB(), candidates, map[string]any{"from": network.Now - 100, "to": network.Now})
			if err != nil {
				t.Fatalf("reading the candidates: %v", err)
			}
			var candidateIDs []int64
			for rows.Next() {
				var id, at int64
				var client any
				// The destination is the fifth column since the resolver-cache cycle: the
				// exact evidence is searched from it.
				var address, destination string
				if err := rows.Scan(&id, &at, &client, &address, &destination); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				candidateIDs = append(candidateIDs, id)
			}
			_ = rows.Close()
			for _, id := range candidateIDs {
				if storetest.QueryInt(t, network.DB, "SELECT CAST(dst_is_this_firewall AS INTEGER) FROM flow WHERE id = $1", id) == 1 {
					t.Errorf("the flow %d to this firewall is an attribution candidate", id)
				}
			}
			if _, err := network.DB.Attribute(ctx, network.Now-100, network.Now, 5, network.Now); err != nil {
				t.Fatalf("attributing: %v", err)
			}
			if named := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM domain_attribution AS a
				JOIN flow AS f ON f.id = a.flow_id WHERE f.dst_is_this_firewall`); named != 0 {
				t.Errorf("%d flows to this firewall carry a site name", named)
			}
			rate, err := network.DB.ReadAttributionRate(ctx, network.Now-100, network.Now, nil)
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	client, _ := firstClient(t, network, network.Inside[0].ID, false)
	dns := int64(53)
	network.Insert(t, []storetest.Flow{{ObservedAt: network.Now - 50, Device: network.Inside[0].Device,
		Direction: "in", Src: client.Address, Dst: network.Inside[0].Address, DstPort: &dns,
		Protocol: "udp", Action: "pass", Bytes: 70}})
	if err := storetest.InsertDNSResolution(ctx, network.DB, vocab.DNSResolution{
		LookupKey: "example-stale", ClientAddress: client.Address, Domain: "example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: storetest.Ptr("Recursion"),
		LookedUpAt: network.Now - 52, IngestedAt: network.Now - 1,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	// The attribution 5A wrote, when the destination was an unplaced end. 5A had one
	// method, which the method column now names.
	if _, err := network.DB.DB().ExecContext(ctx, `INSERT INTO domain_attribution (flow_id, dns_resolution_id,
		site_name, correlation_delay_seconds, attributed_at, method)
		SELECT f.id, r.id, 'example.invalid', 2, $1, 'lookup_timing' FROM flow AS f, dns_resolution AS r
		WHERE r.lookup_key = 'example-stale'`, network.Now); err != nil {
		t.Fatalf("writing the stale attribution: %v", err)
	}
	network.ClassifyAndRefresh(t, []string{client.Address, network.Inside[0].Address})
	result, err := network.DB.Attribute(ctx, network.Now-100, network.Now, 5, network.Now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if left := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); left != 0 {
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			specs := network.FlowsFor(150, 2*3600)
			dns := int64(53)
			var phantoms []string
			for _, iface := range network.Inside {
				for _, v6 := range []bool{false, true} {
					client, found := firstClient(t, network, iface.ID, v6)
					if !found {
						continue
					}
					target := iface.Address
					if v6 {
						target = iface.V6
					}
					phantoms = append(phantoms, target)
					specs = append(specs, storetest.Flow{ObservedAt: network.Now - 33, Device: iface.Device,
						Direction: "in", Src: client.Address, Dst: target, DstPort: &dns, Protocol: "udp",
						Action: "pass", Bytes: 64})
				}
			}
			addresses := network.Insert(t, specs)
			network.ClassifyAndRefresh(t, addresses)

			// The 5A state: a phantom address-level client at each firewall address, named
			// by its flows as their destination, an east-west scope, and the 5A fingerprint.
			for _, address := range phantoms {
				var interfaceID int64
				if err := network.DB.DB().QueryRow(`SELECT interface_id FROM this_firewall_address
					WHERE address = $1 LIMIT 1`, address).Scan(&interfaceID); err != nil {
					t.Fatalf("reading the interface of %s: %v", address, err)
				}
				id, err := network.DB.UpsertClient(ctx, vocab.Client{
					Identity:    vocab.ClientIdentity{Kind: vocab.IdentityAddressInInterface, Key: flows.AddressIdentityKey(&interfaceID, address, network.Now)},
					InterfaceID: &interfaceID, LastAddress: &address,
				}, network.Now)
				if err != nil {
					t.Fatalf("writing a phantom: %v", err)
				}
				if _, err := network.DB.DB().ExecContext(ctx, `UPDATE flow SET dst_is_this_firewall = false,
					dst_interface_id = $1, dst_client_id = $2,
					traffic_scope = CASE WHEN src_interface_id IS NOT NULL THEN 'east_west' ELSE 'north_south' END
					WHERE dst_address = $3`, interfaceID, id, address); err != nil {
					t.Fatalf("writing the 5A state: %v", err)
				}
				if _, err := network.DB.DB().ExecContext(ctx, `UPDATE address_classification
					SET evidence = $1 WHERE address = $2`, fmt.Sprintf("network|%d|-", interfaceID), address); err != nil {
					t.Fatalf("writing the 5A fingerprint: %v", err)
				}
			}
			recomputeEverything(t, network)
			if before := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM client
				WHERE last_address IN (SELECT address FROM this_firewall_address)`); before == 0 {
				t.Fatal("the 5A state holds no phantom, so the purge is not exercised")
			}

			result, err := network.DB.ReclassifyAll(ctx, network.Now)
			if err != nil {
				t.Fatalf("reclassifying everything: %v", err)
			}
			if _, err := storetest.RefreshAggregates(ctx, network.DB, network.Now, network.Now, result.FlowInstants); err != nil {
				t.Fatalf("refreshing: %v", err)
			}
			if left := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM client
				WHERE last_address IN (SELECT address FROM this_firewall_address)`); left != 0 {
				t.Errorf("%d phantom clients at the firewall's addresses survived", left)
			}
			if result.ClientsPurged == 0 {
				t.Error("the reclassification reports no client purged")
			}
			slots := derivedSnapshot(t, network.DB)
			recomputeEverything(t, network)
			if recomputed := derivedSnapshot(t, network.DB); recomputed != slots {
				t.Error("the slots after the reclassification differ from a recomputation")
			}

			second, err := network.DB.ReclassifyAll(ctx, network.Now)
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
	if vocab.ScopeThisFirewall != "this_firewall" || vocab.DirectionToThisFirewall != "to_this_firewall" ||
		vocab.DirectionFromThisFirewall != "from_this_firewall" || vocab.ClientResolutionThisFirewall != "this_firewall_hostname" {
		t.Error("an identifier does not use the term this firewall")
	}
	for _, loopback := range []string{"127.0.0.1", "127.8.9.10", "::1", "localhost", "LocalHost.", "::ffff:127.0.0.1"} {
		if !flows.IsLoopback(loopback) {
			t.Errorf("%s is not recognised as the loopback", loopback)
		}
	}
	for _, other := range []string{"198.51.100.1", "2001:db8::1", "localhost.example.invalid", "example-host"} {
		if flows.IsLoopback(other) {
			t.Errorf("%s is recognised as the loopback", other)
		}
	}
}

// discoveryPass writes one discovery as internal/collect does: the previous discovery's
// instant read before anything is written, then every interface and the addresses it
// reported, in the order given. upstream lists the upstream addresses this discovery read;
// every inside interface reports its own two addresses.
func discoveryPass(t *testing.T, network *storetest.Network, at int64, upstreamFirst bool, upstream []vocab.InterfaceAddress) {
	t.Helper()
	ctx := context.Background()
	var previous *int64
	if latest, found, err := network.DB.LatestInterfaceDiscoveryAt(ctx); err != nil {
		t.Fatalf("reading the previous discovery: %v", err)
	} else if found {
		previous = &latest
	}
	writeInterface := func(id int64, addresses []vocab.InterfaceAddress) {
		if _, err := network.DB.DB().ExecContext(ctx, "UPDATE interface SET last_seen_at = $1 WHERE id = $2",
			at, id); err != nil {
			t.Fatalf("writing an interface: %v", err)
		}
		for _, address := range addresses {
			if err := network.DB.UpsertInterfaceAddress(ctx, address, previous, at); err != nil {
				t.Fatalf("writing an address: %v", err)
			}
		}
	}
	bits4, bits6 := int64(24), int64(64)
	inside := func() {
		for _, iface := range network.Inside {
			writeInterface(iface.ID, []vocab.InterfaceAddress{
				{InterfaceID: iface.ID, SourceField: vocab.SourceFieldAddr4, Address: iface.Address, PrefixLength: &bits4, AddressFamily: 4},
				{InterfaceID: iface.ID, SourceField: vocab.SourceFieldAddr6, Address: iface.V6, PrefixLength: &bits6, AddressFamily: 6},
			})
		}
	}
	if upstreamFirst {
		writeInterface(network.Upstream.ID, upstream)
		inside()
	} else {
		inside()
		writeInterface(network.Upstream.ID, upstream)
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
					t.Parallel()
					network := storetest.NewNetwork(t, counts[0], counts[1])
					held4, held6 := storetest.Outside(120, false), storetest.Outside(120, true)
					bits4, bits6 := int64(24), int64(64)
					read := []vocab.InterfaceAddress{
						{InterfaceID: network.Upstream.ID, SourceField: vocab.SourceFieldAddr4, Address: held4,
							PrefixLength: &bits4, AddressFamily: 4},
						{InterfaceID: network.Upstream.ID, SourceField: vocab.SourceFieldAddr6, Address: held6,
							PrefixLength: &bits6, AddressFamily: 6},
					}
					for _, discovery := range []struct {
						at   int64
						read bool
					}{
						{network.Now - 9000, true}, {network.Now - 8700, true}, {network.Now - 8400, false},
						{network.Now - 8100, false}, {network.Now - 7800, true},
					} {
						var upstream []vocab.InterfaceAddress
						if discovery.read {
							upstream = read
						}
						discoveryPass(t, network, discovery.at, upstreamFirst, upstream)
					}
					margin := int64(flows.DefaultDiscoveryIntervalSeconds)
					port := int64(443)
					for _, held := range []string{held4, held6} {
						if holdings := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM interface_address
							WHERE address = $1`, held); holdings != 2 {
							t.Errorf("the address %s released and re-acquired is %d rows, not two holdings", held, holdings)
						}
						v6 := held == held6
						cases := []struct {
							at   int64
							held bool
						}{
							{network.Now - 8850, true},          // inside the first holding
							{network.Now - 8700 + margin, true}, // its end, margin included
							{network.Now - 8250, false},         // between the holdings
							{network.Now - 7600, true},          // inside the second
						}
						var specs []storetest.Flow
						for _, one := range cases {
							specs = append(specs, storetest.Flow{ObservedAt: one.at, Device: network.Upstream.Device,
								Direction: "out", Src: held, Dst: storetest.Outside(122, v6), DstPort: &port, Protocol: "tcp",
								Action: "pass", Bytes: 100})
						}
						network.ClassifyAndRefresh(t, network.Insert(t, specs))
						for _, one := range cases {
							got := storetest.QueryInt(t, network.DB, `SELECT CAST(src_is_this_firewall AS INTEGER) FROM flow
								WHERE src_address = $1 AND observed_at = $2`, held, one.at)
							if (got == 1) != one.held {
								t.Errorf("a record from %s at %d is this firewall: %t, want %t", held,
									one.at-network.Now, got == 1, one.held)
							}
						}
					}
				})
		}
	}
}

// TestBroadcastAndMulticastDeliveredToTheFirewallAreThisFirewall is D5's second clause (live validation of 17
// October 2026): a packet logged to the limited broadcast or a multicast address -- 255.255.255.255, 224.0.0.0/4,
// ff00::/8 -- is delivered to the firewall itself, so its destination end is This Firewall, the flow is classified
// this_firewall, never north_south, and no client is made of the address; a unicast outside destination stays
// north_south.
func TestBroadcastAndMulticastDeliveredToTheFirewallAreThisFirewall(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 4, 13)
	device := map[bool]string{}
	sources := map[bool]string{}
	for _, iface := range network.Inside {
		for _, v6 := range []bool{false, true} {
			if client, found := firstClient(t, network, iface.ID, v6); found && sources[v6] == "" {
				sources[v6], device[v6] = client.Address, iface.Device
			}
		}
	}
	if sources[false] == "" || sources[true] == "" {
		t.Fatal("the network has no inside client of each family")
	}
	mdns := int64(5353)
	var specs []storetest.Flow
	destinations := map[string]bool{"255.255.255.255": false, "224.0.0.251": false, "239.255.255.250": false,
		"ff02::fb": true}
	for destination, v6 := range destinations {
		specs = append(specs, storetest.Flow{ObservedAt: network.Now - 40, Device: device[v6], Direction: "in",
			Src: sources[v6], Dst: destination, DstPort: &mdns, Protocol: "udp", Action: "pass", Bytes: 90})
	}
	outside := storetest.Outside(21, false)
	specs = append(specs, storetest.Flow{ObservedAt: network.Now - 30, Device: device[false], Direction: "in",
		Src: sources[false], Dst: outside, DstPort: &mdns, Protocol: "udp", Action: "pass", Bytes: 90})
	addresses := network.Insert(t, specs)
	network.ClassifyAndRefresh(t, addresses)
	for destination := range destinations {
		if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
			WHERE dst_address = $1 AND dst_is_this_firewall AND dst_client_id IS NULL
			  AND traffic_scope = 'this_firewall'`, destination); got != 1 {
			t.Errorf("the flow to %s is not delivered to this firewall", destination)
		}
		if clients := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE last_address = $1",
			destination); clients != 0 {
			t.Errorf("%d clients were made of %s", clients, destination)
		}
	}
	if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM classified_flow
		WHERE dst_address = ANY ($1) AND traffic_direction <> 'to_this_firewall'`,
		[]string{"255.255.255.255", "224.0.0.251", "239.255.255.250", "ff02::fb"}); got != 0 {
		t.Errorf("%d flows to a broadcast or multicast address are not to_this_firewall", got)
	}
	if got := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
		WHERE dst_address = $1 AND traffic_scope = 'north_south' AND NOT dst_is_this_firewall`, outside); got != 1 {
		t.Errorf("the unicast outside destination is no longer north_south")
	}
}
