package widgets

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/widget"
)

// C2 and C4 of the backend-completion cycle: destinations no name was inferred for are shown by
// their address, operator and country, never folded into one unnamed bucket; and a service is a
// port and a protocol together, served wherever the catalogue binds them.

// eligibleFlows counts the eligible flows of a window -- read_attribution_rate's rule -- with an
// extra condition on the flow (f).
func (f *fixture) eligibleFlows(t *testing.T, window widget.Interval, extra string, arguments ...any) int64 {
	t.Helper()
	return f.scalar(t, `SELECT count(*) FROM flow AS f WHERE f.observed_at >= ? AND f.observed_at < ?
		AND f.dst_interface_id IS NULL AND f.src_client_id IS NOT NULL AND f.src_is_this_firewall = 0
		AND f.dst_is_this_firewall = 0 AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg') `+extra,
		append([]any{window.From, window.To}, arguments...)...)
}

// TestUnnamedDestinationsAreAddressRowsThatMakeTheEligibleFlows is the first part of C-AC7: for
// top_sites at every scope and truncation, the rows, the remainder and the names below min_flows
// make the eligible flows, and each address row carries its address and its operator and country,
// or the state of its lookup.
func TestUnnamedDestinationsAreAddressRowsThatMakeTheEligibleFlows(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	// The fixture holds named, unnamed, partly named and unplaced destinations.
	if f.scalar(t, `SELECT count(DISTINCT f.dst_address) FROM flow AS f JOIN domain_attribution AS a ON a.flow_id = f.id
		WHERE f.dst_address IN (SELECT f2.dst_address FROM flow AS f2 LEFT JOIN domain_attribution AS a2 ON a2.flow_id = f2.id
		WHERE a2.flow_id IS NULL AND f2.dst_interface_id IS NULL AND f2.src_client_id IS NOT NULL)`) == 0 {
		t.Fatal("the fixture holds no partly named address, so this test is vacuous")
	}
	// A named flow the attribution rate does not count -- here a record of no known client -- names
	// no row either, so the equality holds whatever the attribution named.
	f.exec(t, "UPDATE flow SET src_client_id = NULL WHERE id = (SELECT min(flow_id) FROM domain_attribution)")
	scopes := []struct {
		name       string
		parameters map[string]any
		extra      string
		arguments  []any
	}{
		{name: "whole", parameters: map[string]any{}},
		{name: "one interface", parameters: map[string]any{"interfaces": []any{map[string]any{"kind": "interface",
			"by": "device", "value": f.interfaces[0].device}}}, extra: "AND f.src_interface_id = ?",
			arguments: []any{f.interfaces[0].id}},
		{name: "one client", parameters: map[string]any{"clients": []any{map[string]any{"kind": "client",
			"by": "mac", "value": "00:00:5e:00:53:00"}}},
			extra: "AND f.src_client_id = (SELECT id FROM client WHERE mac = ?)", arguments: []any{"00:00:5e:00:53:00"}},
	}
	addressRows, unplaced := 0, 0
	for _, period := range []string{"1h", "24h", "7d", "30d"} {
		for _, scope := range scopes {
			for _, truncation := range []map[string]any{{}, {"limit": 2}, {"min_flows": 3}} {
				parameters := map[string]any{"period": period}
				for key, value := range scope.parameters {
					parameters[key] = value
				}
				for key, value := range truncation {
					parameters[key] = value
				}
				response := f.respond(t, "top_sites", parameters)
				sites := decodeData[topSitesData](t, response)
				var shown int64
				for _, row := range sites.Rows {
					shown += row.Flows.Value
					switch row.Kind {
					case widget.DestinationKindSite:
						if row.Name == "" {
							t.Errorf("%s %s: a site row with no name", period, scope.name)
						}
					case widget.DestinationKindAddress:
						addressRows++
						if row.Address == "" || row.LookupState == "" {
							t.Errorf("%s %s: an address row without its address or its lookup state", period, scope.name)
						}
						if row.LookupState == "resolved" && (row.CountryCode == nil || row.ASN == nil || row.Operator == nil) {
							t.Errorf("%s %s: the placed address %s lacks its country or operator", period, scope.name, row.Address)
						}
						if row.LookupState != "resolved" {
							unplaced++
						}
					default:
						t.Errorf("%s %s: a row of kind %q", period, scope.name, row.Kind)
					}
				}
				total := shown + sites.Remainder.Count.Value + sites.BelowMinFlows.Count.Value
				eligible := f.eligibleFlows(t, response.Window.Requested, scope.extra, scope.arguments...)
				if total != eligible || sites.Total.Count.Value != eligible {
					t.Errorf("%s %s %v: rows %d + remainder %d + below %d = %d, total %d, eligible flows %d",
						period, scope.name, truncation, shown, sites.Remainder.Count.Value,
						sites.BelowMinFlows.Count.Value, total, sites.Total.Count.Value, eligible)
				}
				if scope.name == "whole" && sites.Attribution.EligibleFlows.Value != eligible {
					t.Errorf("%s: attribution.eligible_flows reads %d, the flows %d", period,
						sites.Attribution.EligibleFlows.Value, eligible)
				}
			}
		}
	}
	if addressRows == 0 || unplaced == 0 {
		t.Errorf("address rows %d, unplaced %d: the fixture exercises neither", addressRows, unplaced)
	}
}

// TestNoNodeStandsForEveryUnnamedDestinationTogether is the second and third parts of C-AC7: no
// response of top_sites, sites_by_client, connection_tree (every root) or owner_activity holds a
// node for every unnamed destination together, and each traffic_sankey unnamed bin lists its
// addresses, summing to the bin.
func TestNoNodeStandsForEveryUnnamedDestinationTogether(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	forbidden := regexp.MustCompile(`"(site:none|site:|address:|unnamed|unnamed_destinations)"`)
	inspect := func(label string, response widget.Response) {
		encoded, err := json.Marshal(response.Data)
		if err != nil {
			t.Fatal(err)
		}
		if match := forbidden.Find(encoded); match != nil {
			t.Errorf("%s holds the node %s", label, match)
		}
	}
	for _, period := range []string{"24h", "30d"} {
		inspect("top_sites "+period, f.respond(t, "top_sites", map[string]any{"period": period}))
		inspect("sites_by_client "+period, f.respond(t, "sites_by_client", map[string]any{"period": period}))
		inspect("owner_activity "+period, f.respond(t, "owner_activity", map[string]any{"period": period}))
		for _, root := range []string{"interface", "client", "owner"} {
			for _, depth := range []int{2, 3} {
				response := f.respond(t, "connection_tree", map[string]any{"period": period, "root": root, "depth": depth})
				inspect(fmt.Sprintf("connection_tree %s %s %d", period, root, depth), response)
				tree := decodeData[treeData](t, response)
				addresses := 0
				for _, node := range tree.Outside {
					for _, child := range node.Children {
						if strings.HasPrefix(child.Key, "address:") {
							addresses++
						}
					}
				}
				if addresses == 0 {
					t.Errorf("connection_tree %s %s: no address node, so the rule is unasserted", period, root)
				}
			}
		}
		for _, right := range []string{"operator", "country"} {
			response := f.respond(t, "traffic_sankey", map[string]any{"period": period, "right": right})
			var sankey struct {
				Other struct {
					Unnamed []widget.UnplacedBin `json:"unnamed"`
				} `json:"other"`
			}
			encoded, err := json.Marshal(response.Data)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &sankey); err != nil {
				t.Fatal(err)
			}
			listed := 0
			for _, bin := range sankey.Other.Unnamed {
				if bin.Members == nil {
					t.Fatalf("traffic_sankey %s: the bin %s lists no address", right, bin.Reason)
				}
				var bytes, connections int64
				for _, member := range *bin.Members {
					bytes += member.Volume.Bytes
					connections += member.Volume.Connections
				}
				listed += len(*bin.Members)
				if bytes != bin.Volume.Bytes || connections != bin.Volume.Connections ||
					int64(len(*bin.Members)) != bin.Addresses {
					t.Errorf("traffic_sankey %s %s: the bin's addresses sum to %d bytes over %d, not its %d over %d",
						period, bin.Reason, bytes, len(*bin.Members), bin.Volume.Bytes, bin.Addresses)
				}
			}
			if listed == 0 {
				t.Errorf("traffic_sankey %s %s: no unnamed address, so the rule is unasserted", period, right)
			}
		}
	}
}

// TestUnderNoDomainsEveryDestinationIsInAddressForm is the fourth part of C-AC7: under
// aggregate_mode = no_domains, connection_tree (every root, every depth) and client_traffic_detail
// answer byte for byte as the same database with every domain_attribution row removed, and their
// data equal the full mode's over that database; top_sites and sites_by_client are withheld whole.
func TestUnderNoDomainsEveryDestinationIsInAddressForm(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	type request struct {
		widgetType string
		parameters map[string]any
	}
	var requests []request
	for _, period := range []string{"1h", "24h", "30d"} {
		for _, root := range []string{"interface", "client", "owner"} {
			for _, depth := range []int{1, 2, 3} {
				requests = append(requests, request{"connection_tree",
					map[string]any{"period": period, "root": root, "depth": depth}})
			}
		}
		for _, mac := range []string{"00:00:5e:00:53:00", "00:00:5e:00:53:04"} {
			requests = append(requests, request{"client_traffic_detail", map[string]any{"period": period,
				"client": map[string]any{"kind": "client", "by": "mac", "value": mac}}})
		}
	}
	answer := func(item request) []byte {
		encoded, err := json.Marshal(f.respond(t, item.widgetType, item.parameters))
		check(t, err)
		return encoded
	}
	dataOf := func(item request) []byte {
		encoded, err := json.Marshal(f.respond(t, item.widgetType, item.parameters).Data)
		check(t, err)
		return encoded
	}
	if f.scalar(t, "SELECT count(*) FROM domain_attribution") == 0 {
		t.Fatal("the fixture names nothing, so this test is vacuous")
	}
	check(t, f.db.SetSetting(ctx, config.KeyAggregateMode, widget.AggregateModeNoDomains, f.now))
	withNames := map[int][]byte{}
	for index, item := range requests {
		withNames[index] = answer(item)
	}
	for _, withheld := range []string{"top_sites", "sites_by_client"} {
		response := f.respond(t, withheld, map[string]any{"period": "24h"})
		if response.Condition != widget.ConditionWithheldByAggregateMode || response.Data != nil {
			t.Errorf("%s is not withheld whole under no_domains", withheld)
		}
	}
	f.exec(t, "DELETE FROM domain_attribution")
	for index, item := range requests {
		if without := answer(item); string(without) != string(withNames[index]) {
			t.Errorf("%s %v: the answer under no_domains depends on whether a name was inferred\nwith:    %s\nwithout: %s",
				item.widgetType, item.parameters, withNames[index], without)
		}
	}
	noDomains := map[int][]byte{}
	for index, item := range requests {
		noDomains[index] = dataOf(item)
	}
	check(t, f.db.SetSetting(ctx, config.KeyAggregateMode, "full", f.now))
	for index, item := range requests {
		if full := dataOf(item); string(full) != string(noDomains[index]) {
			t.Errorf("%s %v: the data under no_domains is not the data of a database naming nothing",
				item.widgetType, item.parameters)
		}
	}
}

// TestBeyondTheFlowHorizonUnnamedDestinationsAreANamedState is the last part of C-AC7: once the
// retention purge has removed part of the window, the address rows hold only the flows still kept
// and the answer names the state, never an estimate of what was purged.
func TestBeyondTheFlowHorizonUnnamedDestinationsAreANamedState(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	horizon := f.now - 86400
	f.exec(t, "INSERT INTO retention_purge (id, purged_before, purged_at) VALUES (1, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET purged_before = EXCLUDED.purged_before", horizon, f.now)
	f.exec(t, "DELETE FROM flow WHERE observed_at < ?", horizon)
	for _, widgetType := range []string{"top_sites", "sites_by_client"} {
		response := f.respond(t, widgetType, map[string]any{"period": "7d"})
		if !widget.Contains(response.States, widget.StateUnnamedBeyondHorizon) {
			t.Errorf("%s 7d: the window reaches past the horizon and names no state (%v)", widgetType, response.States)
		}
		inside := f.respond(t, widgetType, map[string]any{"period": "1h"})
		if widget.Contains(inside.States, widget.StateUnnamedBeyondHorizon) {
			t.Errorf("%s 1h: a window inside the horizon names the state", widgetType)
		}
	}
	sites := decodeData[topSitesData](t, f.respond(t, "top_sites", map[string]any{"period": "7d"}))
	var addressFlows int64
	for _, row := range sites.Rows {
		if row.Kind == widget.DestinationKindAddress {
			addressFlows += row.Flows.Value
		}
	}
	addressFlows += sites.Remainder.Count.Value + sites.BelowMinFlows.Count.Value
	kept := f.scalar(t, `SELECT count(*) FROM flow AS f LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
		WHERE a.flow_id IS NULL AND f.observed_at >= ? AND f.dst_interface_id IS NULL AND f.src_client_id IS NOT NULL
		AND f.src_is_this_firewall = 0 AND f.dst_is_this_firewall = 0
		AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')`, f.now-7*86400)
	if sites.Remainder.Count.Value+sites.BelowMinFlows.Count.Value == 0 && addressFlows != kept {
		t.Errorf("the address rows hold %d flows, the kept unnamed flows %d: an estimate, or a loss", addressFlows, kept)
	}
}

// presetData reads the backticked tokens of each preset's Data paragraph.
func presetData(t *testing.T) map[string]map[string]bool {
	t.Helper()
	file, err := os.Open("../../../docs/widget-catalogue.md")
	check(t, err)
	defer func() { _ = file.Close() }()
	result := map[string]map[string]bool{}
	var (
		current string
		inData  bool
		ticks   = regexp.MustCompile("`([^`]+)`")
		typed   = regexp.MustCompile("^\\*\\*Type\\*\\* — `([a-z0-9_]+)`")
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "### ") {
			current, inData = "", false
		}
		if match := typed.FindStringSubmatch(line); match != nil {
			current = match[1]
			result[current] = map[string]bool{}
			continue
		}
		if strings.HasPrefix(line, "**Data**") {
			inData = true
		} else if strings.TrimSpace(line) == "" {
			inData = false
		}
		if inData && current != "" {
			for _, match := range ticks.FindAllStringSubmatch(line, -1) {
				result[current][match[1]] = true
			}
		}
	}
	check(t, scanner.Err())
	return result
}

// TestEveryCatalogueBindingOfPortAndProtocolIsServed is the second part of C-AC8: every preset
// whose catalogue Data paragraph names flow.dst_port or flow.protocol carries both values in an
// answer where its binding draws them, the connection_tree level included; and the fixture's
// port 53 over udp and over tcp are two service slices, a record with no port being
// port:not_recorded.
func TestEveryCatalogueBindingOfPortAndProtocolIsServed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	// Where each preset binds a port and a protocol in its answer: a request, and a path whose
	// objects must carry both "port" and "protocol".
	bound := map[string]struct {
		parameters map[string]any
		path       []string
	}{
		"client_traffic_detail": {map[string]any{"period": "24h", "client": map[string]any{"kind": "client",
			"by": "mac", "value": "00:00:5e:00:53:00"}}, []string{"records"}},
		"traffic_composition": {map[string]any{"period": "24h", "group_by": "service_port", "slices": 50},
			[]string{"slices"}},
		"connection_tree":      {map[string]any{"period": "24h", "depth": 3}, []string{"outside", "children", "children"}},
		"unified_blocked_feed": {map[string]any{"period": "24h", "limit": 500}, []string{"entries"}},
	}
	// The presets whose Data paragraph reads the port without their binding drawing one: each
	// answers per peer or per rule, a figure no port divides, and says so here rather than silently.
	notDrawn := map[string]string{
		"blocked_traffic_world_map": "its points are outside peers, read beyond the flow horizon from the volume aggregates, which keep no port",
		"blocked_by_firewall_rule":  "its rows are rules; the port is read to qualify a record, and its binding draws none",
	}
	data := presetData(t)
	named := 0
	for widgetType, tokens := range data {
		if !tokens["flow.dst_port"] && !tokens["flow.protocol"] {
			continue
		}
		named++
		if reason, aside := notDrawn[widgetType]; aside {
			if tokens["flow.protocol"] {
				t.Errorf("%s names flow.protocol, and is listed as drawing no port (%s)", widgetType, reason)
			}
			continue
		}
		binding, found := bound[widgetType]
		if !found {
			t.Errorf("%s names flow.dst_port or flow.protocol and no answer of it is checked to carry them", widgetType)
			continue
		}
		encoded, err := json.Marshal(f.respond(t, widgetType, binding.parameters).Data)
		check(t, err)
		var decoded any
		check(t, json.Unmarshal(encoded, &decoded))
		ports, protocols := 0, 0
		var walk func(node any, path []string)
		walk = func(node any, path []string) {
			switch typed := node.(type) {
			case []any:
				for _, item := range typed {
					walk(item, path)
				}
			case map[string]any:
				if len(path) == 0 {
					if port, ok := typed["port"].(float64); ok && port > 0 {
						ports++
					}
					if protocol, ok := typed["protocol"].(string); ok && protocol != "" {
						protocols++
					}
					return
				}
				walk(typed[path[0]], path[1:])
			}
		}
		walk(decoded, binding.path)
		if ports == 0 || protocols == 0 {
			t.Errorf("%s: no answer member under %v carries both a port (%d) and a protocol (%d)", widgetType,
				binding.path, ports, protocols)
		}
	}
	if named < 4 {
		t.Fatalf("only %d presets name the port or the protocol: the catalogue was not parsed", named)
	}

	// 53/udp and 53/tcp are two services; a record with no port is port:not_recorded.
	at := f.now - 120
	client := f.clients[0]
	for index, protocol := range []string{"udp", "tcp"} {
		f.exec(t, `INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device, interface_lookup_state,
			src_interface_id, src_client_id, src_address, dst_address, dst_port, protocol, ip_version, action, direction,
			packet_bytes, rule_lookup_state, traffic_scope) VALUES (?, ?, ?, ?, 'resolved', ?,
			(SELECT id FROM client WHERE mac = '00:00:5e:00:53:00'), ?, '198.51.100.250', 53, ?, 4, 'pass', 'in', 100,
			'pending', 'north_south')`, fmt.Sprintf("example-dns-%d", index), at, at, client.iface.device,
			client.iface.id, client.address, protocol)
	}
	composition := decodeData[compositionData](t, f.respond(t, "traffic_composition",
		map[string]any{"period": "1h", "group_by": "service_port", "slices": 100}))
	keys := map[string]bool{}
	for _, slice := range composition.Slices {
		keys[slice.Key] = true
		if slice.Key == widget.ServicePortNotRecorded && (slice.Port != nil || slice.Protocol != nil) {
			t.Error("port:not_recorded names a port or a protocol")
		}
	}
	for _, key := range []string{"port:53/udp", "port:53/tcp"} {
		if !keys[key] {
			t.Errorf("the service %s is not a slice of its own: %v", key, keys)
		}
	}
	if f.scalar(t, "SELECT count(*) FROM classified_flow WHERE dst_port IS NULL AND exit_leg_device IS NULL "+
		"AND observed_at >= ?", f.now-3600) > 0 && !keys[widget.ServicePortNotRecorded] {
		t.Error("the records with no port are not the slice port:not_recorded")
	}
}

// TestTheDecisionsOfABlockingReasonCompositionAreTheBlockedDecisionViews holds the core's decision
// rule (internal/widget/flows.statements.sql) equal to the blocked_decision view's engine kinds:
// per engine kind, the refused records each counts are the same.
func TestTheDecisionsOfABlockingReasonCompositionAreTheBlockedDecisionViews(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	f.exec(t, "UPDATE flow SET log_reason = 'state-mismatch' WHERE id IN (SELECT id FROM flow WHERE action = 'block' ORDER BY id LIMIT 3)")
	f.exec(t, "UPDATE flow SET log_reason = NULL WHERE id IN (SELECT id FROM flow WHERE action = 'block' ORDER BY id DESC LIMIT 2)")
	response := f.respond(t, "traffic_composition", map[string]any{"period": "30d", "group_by": "blocking_reason",
		"measure": "connections", "slices": 10})
	composition := decodeData[compositionData](t, response)
	window := response.Window.Requested
	for _, kind := range []string{widget.DecisionFirewallRule, widget.DecisionFirewallNoRule,
		widget.DecisionFirewallReasonNotRecorded} {
		want := f.scalar(t, `SELECT count(*) FROM blocked_decision AS d JOIN flow AS f ON d.source_table = 'flow'
			AND f.id = d.source_id WHERE d.engine_kind = ? AND d.occurred_at >= ? AND d.occurred_at < ?
			AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')`, kind, window.From, window.To)
		var got float64
		for _, slice := range composition.Slices {
			if slice.Key == "decision:"+kind {
				got = slice.Value
			}
		}
		if int64(got) != want || want == 0 {
			t.Errorf("decision %s: the composition counts %v, the blocked_decision view %d", kind, got, want)
		}
	}
}

// TestTheAdoptedCompositionKeysAreDeclaredAndApplied is the third part of C-AC8: the four keys the
// mockup adopted are in the manifest and the catalogue, and each data key changes the answer.
func TestTheAdoptedCompositionKeysAreDeclaredAndApplied(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	entries, _ := parseCatalogue(t)
	declared := map[string]string{}
	preset, _ := f.service.Preset("traffic_composition")
	for _, parameter := range preset.Params {
		declared[parameter.Key] = "rendering"
		if parameter.Data {
			declared[parameter.Key] = "data"
		}
	}
	for key, class := range map[string]string{"split_by": "data", "window_seconds": "data",
		"line_interpolation": "rendering", "group_by": "data"} {
		if declared[key] != class {
			t.Errorf("traffic_composition declares %s as %q, not %q", key, declared[key], class)
		}
		if !entries["traffic_composition"].tokens[key] {
			t.Errorf("the catalogue's Traffic composition does not name %s", key)
		}
	}
	if !entries["traffic_composition"].tokens["blocking_reason"] {
		t.Error("the catalogue's Traffic composition does not name blocking_reason")
	}
	if entries["traffic_composition"].tokens["destinations_per_owner"] || declared["destinations_per_owner"] != "" {
		t.Error("destinations_per_owner is adopted")
	}
	base := map[string]any{"period": "24h"}
	baseline, err := json.Marshal(f.respond(t, "traffic_composition", base).Data)
	check(t, err)
	for _, changed := range []map[string]any{{"group_by": "blocking_reason"}, {"split_by": "direction"},
		{"window_seconds": 120}} {
		parameters := map[string]any{"period": "24h"}
		for key, value := range changed {
			parameters[key] = value
		}
		answer, err := json.Marshal(f.respond(t, "traffic_composition", parameters).Data)
		check(t, err)
		if string(answer) == string(baseline) {
			t.Errorf("%v does not change the answer", changed)
		}
	}
	rendered, err := json.Marshal(f.respond(t, "traffic_composition",
		map[string]any{"period": "24h", "line_interpolation": "smooth"}).Data)
	check(t, err)
	if string(rendered) != string(baseline) {
		t.Error("line_interpolation, a rendering key, changes the data")
	}
}
