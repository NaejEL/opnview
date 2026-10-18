package collect_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"testing"
)

// D5's local-delivery rule (the live validation of 17 October 2026, specs/SPEC-backend-completion-plugins.md):
// a destination that is the IPv4 limited broadcast (255.255.255.255), an IPv4 multicast address (224.0.0.0/4) or
// an IPv6 multicast address (ff00::/8) is delivered to the firewall itself. Its end is This Firewall, and the
// connection is never north_south. The recording predates the rule: it holds such a connection as one that
// leaves by an interface no leg names. This file reads, from the world as stored and never from the plugin's
// code, what the rule changes:
//   - the connection's per-direction figures, by NJ5's table with the firewall itself as the forward direction's
//     exit and the reverse direction's entry;
//   - the aggregate rows that hold the connection, which it moves between keys.

// localDeliveryDecision names the decision, as the spec numbers it.
const localDeliveryDecision = "D5"

// isLocalDeliveryDestination reports whether an address is one D5 names, read here from the decision's own three
// ranges.
func isLocalDeliveryDestination(text string) bool {
	address, err := netip.ParseAddr(text)
	if err != nil {
		return false
	}
	address = address.WithZone("").Unmap()
	if address.Is4() {
		return address == netip.AddrFrom4([4]byte{255, 255, 255, 255}) ||
			netip.MustParsePrefix("224.0.0.0/4").Contains(address)
	}
	return netip.MustParsePrefix("ff00::/8").Contains(address)
}

// localFlow is one logged leg whose destination D5 names, with what an aggregate keys it by.
type localFlow struct {
	digest         string
	observedAt     int64
	action         string
	bytes          int64
	srcInterface   sql.NullInt64
	srcClient      sql.NullInt64
	owner          sql.NullInt64
	rule           sql.NullInt64
	srcAddress     string
	dstAddress     string
	srcIsFirewall  bool
	direction      string
	device         string
	currentPeer    sql.NullString
	currentHeading string
}

// capturedSide is one interface of a capture configuration, as the netflow plugin stored it.
type capturedSide struct {
	ingress, egress bool
	exported        string
	parentOf        []string
}

// localDelivery is D5's class in one world: its legs, and what the per-direction figures of its connections are
// read from.
type localDelivery struct {
	flows []localFlow
	// connections are the legs that are a connection of their own entering by an interface: logged `in`, with
	// no second leg. A leg logged `out` is the firewall's own and already entered from the firewall.
	connections map[string]localFlow
	identifiers map[string]string
	// configurations are the capture configurations by the instant they were first read, in that order.
	configurations []int64
	captured       map[int64]map[string]capturedSide
	// joined are the records joined to a leg, by "<digest>|<orientation>".
	joined    map[string][]joinedRecord
	ambiguous map[string]bool
}

// joinedRecord is a record's figures, as the join stored them.
type joinedRecord struct {
	octets, packets int64
}

// readLocalDelivery reads D5's class from a world. A local-delivery leg that is part of a pair is refused
// loudly: nothing in the fixtures or the capture is one, and the reading below would not describe it.
func readLocalDelivery(t *testing.T, h *netflowWorld, ambiguous map[string]bool) localDelivery {
	t.Helper()
	ctx := context.Background()
	class := localDelivery{connections: map[string]localFlow{}, identifiers: map[string]string{},
		captured: map[int64]map[string]capturedSide{}, joined: map[string][]joinedRecord{}, ambiguous: ambiguous}
	rows, err := h.Store.DB().QueryContext(ctx, `SELECT f.log_digest, f.observed_at, f.action, f.packet_bytes,
		    f.src_interface_id, f.src_client_id, c.owner_id, f.rule_id, canonical_address(f.src_address),
		    canonical_address(f.dst_address), f.src_is_this_firewall, f.direction, f.interface_device,
		    coalesce(f.pair_outcome, ''), v.peer_address, v.traffic_direction
		FROM flow AS f
		JOIN classified_flow AS v ON v.id = f.id
		LEFT JOIN client AS c ON c.id = f.src_client_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var flow localFlow
		var pairing string
		if err := rows.Scan(&flow.digest, &flow.observedAt, &flow.action, &flow.bytes, &flow.srcInterface,
			&flow.srcClient, &flow.owner, &flow.rule, &flow.srcAddress, &flow.dstAddress, &flow.srcIsFirewall,
			&flow.direction, &flow.device, &pairing, &flow.currentPeer, &flow.currentHeading); err != nil {
			t.Fatal(err)
		}
		if !isLocalDeliveryDestination(flow.dstAddress) {
			continue
		}
		if pairing == "first_leg" || pairing == "second_leg" {
			t.Fatalf("D5: the local-delivery leg %s is part of a pair, which this reading does not describe",
				flow.digest)
		}
		class.flows = append(class.flows, flow)
		if flow.direction == "in" {
			class.connections[flow.digest] = flow
		}
	}
	_ = rows.Close()
	if len(class.flows) == 0 {
		return class
	}
	// NJ5 settles a direction export_gap when a gap of the export overlaps it, which the reading below does not
	// describe: a world with both is refused rather than read wrong.
	if gaps := collecttestScalar(t, h, "SELECT count(*) FROM collection_gap"); gaps > 0 {
		t.Fatalf("D5: the world holds %d collection gaps beside local-delivery legs, which this reading does not "+
			"describe", gaps)
	}
	interfaces, err := h.Store.DB().QueryContext(ctx, `SELECT device, identifier FROM interface`)
	if err != nil {
		t.Fatal(err)
	}
	for interfaces.Next() {
		var device, identifier string
		if err := interfaces.Scan(&device, &identifier); err != nil {
			t.Fatal(err)
		}
		class.identifiers[device] = identifier
	}
	_ = interfaces.Close()
	configurations, err := h.Store.DB().QueryContext(ctx, `SELECT c.first_read_at, i.identifier, i.ingress, i.egress,
		    i.exported, i.parent_of
		FROM plugin_netflow.capture_configuration AS c
		JOIN plugin_netflow.capture_interface AS i ON i.first_read_at = c.first_read_at
		WHERE c.readable AND c.enabled
		ORDER BY c.first_read_at`)
	if err != nil {
		t.Fatal(err)
	}
	for configurations.Next() {
		var at int64
		var identifier, exported, parents string
		var side capturedSide
		if err := configurations.Scan(&at, &identifier, &side.ingress, &side.egress, &exported, &parents); err != nil {
			t.Fatal(err)
		}
		side.exported = exported
		if parents != "" {
			side.parentOf = strings.Split(parents, ",")
		}
		if class.captured[at] == nil {
			class.captured[at] = map[string]capturedSide{}
			class.configurations = append(class.configurations, at)
		}
		class.captured[at][identifier] = side
	}
	_ = configurations.Close()
	records, err := h.Store.DB().QueryContext(ctx, `SELECT f.log_digest, r.orientation, r.octets, r.packets
		FROM plugin_netflow.flow_record AS r
		JOIN flow AS f ON f.id = r.joined_flow_id
		WHERE r.join_state = 'joined'`)
	if err != nil {
		t.Fatal(err)
	}
	for records.Next() {
		var digest, orientation string
		var record joinedRecord
		if err := records.Scan(&digest, &orientation, &record.octets, &record.packets); err != nil {
			t.Fatal(err)
		}
		if _, member := class.connections[digest]; member {
			class.joined[digest+"|"+orientation] = append(class.joined[digest+"|"+orientation], record)
		}
	}
	_ = records.Close()
	return class
}

// collecttestScalar answers one count from a world's database.
func collecttestScalar(t *testing.T, h *netflowWorld, query string) int64 {
	t.Helper()
	var count int64
	if err := h.Store.DB().QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// configurationAt is the capture configuration in force at an instant: the last one read at or before it.
func (c localDelivery) configurationAt(at int64) map[string]capturedSide {
	var found map[string]capturedSide
	for _, first := range c.configurations {
		if first <= at {
			found = c.captured[first]
		}
	}
	return found
}

// directionAfter is NJ5's answer for one direction of a local-delivery connection. The forward direction enters
// by the leg's interface and leaves by the firewall itself, so the only exporter that can count it is that
// interface's ingress; the reverse direction enters from the firewall and leaves by the interface, so the only one
// is its egress. pf refusing the leg stops nothing on either side: the firewall is the exit. With one exporter the
// figure is the sum of the records joined to the leg in that orientation -- measured when there are any, else
// missing, or ambiguous_records when the join left the direction's records ambiguous (N3); with none, it is the
// cause the interface's side gives, as NJ5's table orders them. The world is read long after the connection, so
// every state is settled.
func (c localDelivery) directionAfter(flow localFlow, direction string) (state string, octets, packets,
	records int64) {
	configuration := c.configurationAt(flow.observedAt)
	identifier := c.identifiers[flow.device]
	ingress := direction == "forward"
	side, captured := configuration[identifier]
	captured = captured && identifier != "" && (side.ingress || side.egress)
	if captured && side.exported == "yes" && ((ingress && side.ingress) || (!ingress && side.egress)) {
		for _, record := range c.joined[flow.digest+"|"+direction] {
			octets += record.octets
			packets += record.packets
			records++
		}
		switch {
		case records > 0:
			return "measured", octets, packets, records
		case c.ambiguous[flow.digest+"|"+direction]:
			return "ambiguous_records", 0, 0, 0
		default:
			return "missing", 0, 0, 0
		}
	}
	switch {
	case !captured && ingress && c.parentCopies(configuration, identifier):
		return "parent_copies_only", 0, 0, 0
	case !captured:
		return "interface_not_captured", 0, 0, 0
	case ingress && !side.ingress:
		return "ingress_not_captured", 0, 0, 0
	case !ingress && !side.egress:
		return "interface_not_captured", 0, 0, 0
	case side.exported == "no":
		return "interface_not_exported", 0, 0, 0
	default:
		return "capture_configuration_unknown", 0, 0, 0
	}
}

// parentCopies reports whether an exported interface captured on ingress lists an interface in its parent_of.
func (c localDelivery) parentCopies(configuration map[string]capturedSide, identifier string) bool {
	for _, side := range configuration {
		if side.exported != "yes" || !side.ingress {
			continue
		}
		for _, child := range side.parentOf {
			if child == identifier && identifier != "" {
				return true
			}
		}
	}
	return false
}

// rowExpectation is the per-direction change: a recorded row of a local-delivery connection takes NJ5's answer.
func (c localDelivery) rowExpectation() recordingExpectation {
	return recordingExpectation{decision: localDeliveryDecision,
		class:    "a connection to a limited broadcast or multicast destination, once leaving by an unknown interface",
		relation: "connection_measured_volume", regroups: true,
		match: func(row map[string]any) bool {
			_, member := c.connections[fmt.Sprint(row["connection_leg"])]
			return member
		},
		becomes: func(row map[string]any) {
			flow := c.connections[fmt.Sprint(row["connection_leg"])]
			state, octets, packets, records := c.directionAfter(flow, fmt.Sprint(row["direction"]))
			row["state"], row["bytes"], row["packets"], row["records"] = state, float64(octets), float64(packets),
				float64(records)
			row["counted_copy"], row["due_at"] = float64(0), nil
		}}
}

// conservedColumns are the additive columns of every aggregate family, which the regrouping's shift is checked
// on; a distinct count (distinct_peers, client_count) is not additive and is not among them.
var conservedColumns = []string{"bytes", "allowed_bytes", "blocked_bytes", "unknown_bytes", "allowed_connections",
	"blocked_connections", "unknown_connections", "netflow_bytes", "netflow_allowed_bytes", "netflow_blocked_bytes",
	"netflow_unknown_bytes"}

// insideClientFamilies are the families keyed by a connection's inside client, which hold no connection of the
// this_firewall scope (internal/store/aggregates, the client, peer and owner refreshes): a local-delivery
// connection leaves them.
var insideClientFamilies = map[string]bool{"client_volume": true, "peer_volume": true, "owner_volume": true}

// regrouping is the aggregate change: the rows that hold a local-delivery leg, under its recorded key or its key
// as classified now, and the shift of each additive column over those rows.
func (c localDelivery) regrouping(recorded map[string][]map[string]any) aggregateRegrouping {
	// The netflow figure each connection carried in the recording, by action part: an aggregate row carries its
	// first leg's.
	recordedNetflow := map[string]float64{}
	for _, row := range recorded["connection_measured_volume"] {
		if _, member := c.connections[fmt.Sprint(row["connection_leg"])]; member {
			number, _ := row["bytes"].(float64)
			recordedNetflow[fmt.Sprint(row["connection_leg"])] += number
		}
	}
	part := func(action string) string {
		switch action {
		case "pass":
			return "allowed"
		case "block", "reject":
			return "blocked"
		}
		return "unknown"
	}
	// Into the families every leg is in (volume, rule), the logged figures move between keys and keep their sum;
	// the netflow figure changes by NJ5's new answer. Out of the inside-client families goes every leg that had an
	// inside client -- its source's, being neither the firewall's own nor outside -- with what it carried.
	every, inside := map[string]float64{}, map[string]float64{}
	for _, flow := range c.flows {
		if !flow.srcIsFirewall && flow.srcInterface.Valid && flow.srcClient.Valid {
			inside["bytes"] -= float64(flow.bytes)
			inside[part(flow.action)+"_bytes"] -= float64(flow.bytes)
			inside[part(flow.action)+"_connections"]--
			inside["netflow_bytes"] -= recordedNetflow[flow.digest]
			inside["netflow_"+part(flow.action)+"_bytes"] -= recordedNetflow[flow.digest]
		}
		if _, member := c.connections[flow.digest]; !member {
			continue
		}
		after := 0.0
		for _, direction := range []string{"forward", "reverse"} {
			if _, octets, _, _ := c.directionAfter(flow, direction); octets > 0 {
				after += float64(octets)
			}
		}
		if c.configurationAt(flow.observedAt) == nil {
			after = 0
		}
		every["netflow_bytes"] += after - recordedNetflow[flow.digest]
		every["netflow_"+part(flow.action)+"_bytes"] += after - recordedNetflow[flow.digest]
	}
	return aggregateRegrouping{decision: localDeliveryDecision,
		class: "the aggregate rows holding a connection to a limited broadcast or multicast destination",
		touches: func(family string, row map[string]any) bool {
			for _, flow := range c.flows {
				if c.holds(family, row, flow) {
					return true
				}
			}
			return false
		},
		shift: func(family, column string) float64 {
			switch {
			case insideClientFamilies[family]:
				return inside[column]
			case family == "volume" || family == "rule_volume":
				return every[column]
			}
			return 0
		}}
}

// holds reports whether an aggregate row may hold a leg: the row's period holds the leg's instant, and the row's
// key is the leg's under its recorded classification or its classification now. The test is generous -- a row
// that shares the key's other columns is taken too -- because a row it takes is still checked, by the shift.
func (c localDelivery) holds(family string, row map[string]any, flow localFlow) bool {
	start, _ := row["period_start_at"].(float64)
	end, _ := row["period_end_at"].(float64)
	if float64(flow.observedAt) < start || float64(flow.observedAt) >= end {
		return false
	}
	peer := fmt.Sprint(row["peer_address"])
	switch family {
	case "volume":
		if !sameID(row["src_interface_id"], flow.srcInterface) {
			return false
		}
		if peer == flow.dstAddress || peer == flow.srcAddress {
			return true
		}
		return row["traffic_direction"] == flow.currentHeading && sameText(row["peer_address"], flow.currentPeer)
	case "rule_volume":
		return sameID(row["rule_id"], flow.rule) && sameID(row["src_interface_id"], flow.srcInterface)
	case "client_volume":
		return flow.srcClient.Valid && sameID(row["client_id"], flow.srcClient)
	case "peer_volume":
		return flow.srcClient.Valid && sameID(row["client_id"], flow.srcClient) && peer == flow.dstAddress
	case "owner_volume":
		return flow.srcClient.Valid && sameID(row["owner_id"], flow.owner)
	}
	return false
}

// sameID compares a decoded JSON identifier with a nullable one.
func sameID(value any, id sql.NullInt64) bool {
	number, isNumber := value.(float64)
	if !id.Valid {
		return value == nil
	}
	return isNumber && int64(number) == id.Int64
}

// sameText compares a decoded JSON text with a nullable one.
func sameText(value any, text sql.NullString) bool {
	if !text.Valid {
		return value == nil
	}
	return value == text.String
}

// aggregateRegrouping is a decision that moves connections between aggregate rows: the rows it touches -- under
// the recorded keys or the reproduced ones -- are not compared one by one, and each additive column's sum over
// them must instead differ from the recording's by exactly the shift the class gives. Every other row is
// compared exactly.
type aggregateRegrouping struct {
	decision string
	class    string
	touches  func(family string, row map[string]any) bool
	shift    func(family, column string) float64
}

// regroup checks one aggregate relation under the regroupings: it answers the recorded and reproduced rows no
// regrouping touches, for the exact comparison, and reports every shift that does not hold.
func regroup(t *testing.T, label, relation string, recorded, got []map[string]any,
	regroupings []aggregateRegrouping) (keptRecorded, keptGot []map[string]any, touched int) {
	t.Helper()
	family := strings.SplitN(relation, "_aggregate_", 2)[0]
	keptRecorded, keptGot = recorded, got
	for _, regrouping := range regroupings {
		var inRecorded, inGot []map[string]any
		split := func(rows []map[string]any, into *[]map[string]any) []map[string]any {
			var kept []map[string]any
			for _, row := range rows {
				if regrouping.touches(family, row) {
					*into = append(*into, row)
				} else {
					kept = append(kept, row)
				}
			}
			return kept
		}
		keptRecorded = split(keptRecorded, &inRecorded)
		keptGot = split(keptGot, &inGot)
		touched += len(inRecorded) + len(inGot)
		var wrong []string
		for _, column := range conservedColumns {
			sum := func(rows []map[string]any) float64 {
				total := 0.0
				for _, row := range rows {
					number, _ := row[column].(float64)
					total += number
				}
				return total
			}
			want := sum(inRecorded) + regrouping.shift(family, column)
			if have := sum(inGot); have != want {
				wrong = append(wrong, fmt.Sprintf("%s: %v reproduced, %v expected (%v recorded, shift %v)", column,
					have, want, sum(inRecorded), regrouping.shift(family, column)))
			}
		}
		sort.Strings(wrong)
		if len(wrong) > 0 {
			t.Errorf("%s, %s: %s, the rows of %s do not shift as the class says:\n  %s", label, relation,
				regrouping.decision, regrouping.class, strings.Join(wrong, "\n  "))
		}
	}
	return keptRecorded, keptGot, touched
}
