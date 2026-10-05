package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The collection side of step 5: AC4, AC8, AC10, AC13, AC14, AC17 and the last part of AC20.

// interfacesBody returns the interfaces_info fixture's rows, for a test to rewrite.
func interfacesBody(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(fixtureBody(t, "interfaces_info.json"), &body); err != nil {
		t.Fatalf("decoding the interfaces fixture: %v", err)
	}
	return body
}

// upstreamByIdentifier reads which stored interfaces are upstream.
func upstreamByIdentifier(t *testing.T, database *store.Store) map[string]bool {
	t.Helper()
	rows, err := database.DB().Query("SELECT identifier, is_upstream FROM interface")
	if err != nil {
		t.Fatalf("reading the interfaces: %v", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]bool{}
	for rows.Next() {
		var (
			identifier string
			upstream   bool
		)
		if err := rows.Scan(&identifier, &upstream); err != nil {
			t.Fatalf("reading the interfaces: %v", err)
		}
		result[identifier] = upstream
	}
	return result
}

// TestAnInterfaceIsUpstreamExactlyWhenItReportsAGateway is AC8.
func TestAnInterfaceIsUpstreamExactlyWhenItReportsAGateway(t *testing.T) {
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	expected := func(body map[string]any) map[string]bool {
		result := map[string]bool{}
		for _, row := range body["rows"].([]any) {
			object := row.(map[string]any)
			gateways, _ := object["gateways"].([]any)
			result[object["identifier"].(string)] = len(gateways) > 0
		}
		return result
	}
	check := func(body map[string]any, what string) {
		t.Helper()
		got := upstreamByIdentifier(t, harness.store)
		for identifier, want := range expected(body) {
			if got[identifier] != want {
				t.Errorf("%s: %s is upstream %t, the response says %t", what, identifier, got[identifier], want)
			}
		}
	}
	original := interfacesBody(t)
	check(original, "the fixture")
	if count := scalarCount(t, harness.store, "SELECT count(*) FROM interface WHERE is_upstream = 1"); count != 1 {
		t.Fatalf("%d interfaces are upstream from a fixture with one gateway", count)
	}

	// Moving gateways[] to another interface moves the property.
	moved := interfacesBody(t)
	rows := moved["rows"].([]any)
	for _, row := range rows {
		object := row.(map[string]any)
		object["gateways"] = []any{}
		if object["identifier"] == "example_if_b" {
			object["gateways"] = []any{"192.0.2.254"}
		}
	}
	harness.fake.answerJSON(opnsense.InterfacesInfo, moved)
	if err := harness.collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("discovering: %v", err)
	}
	check(moved, "after moving the gateway")

	// Rewriting every description, every user label and every identifier changes nothing:
	// the property follows gateways[] wherever the names go.
	if _, err := harness.store.DB().Exec("UPDATE interface SET user_label = 'example label ' || id"); err != nil {
		t.Fatalf("labelling: %v", err)
	}
	renamed := interfacesBody(t)
	for index, row := range renamed["rows"].([]any) {
		object := row.(map[string]any)
		object["description"] = fmt.Sprintf("example rewritten description %d", index)
		object["identifier"] = fmt.Sprintf("example_renamed_%d", index)
		object["device"] = fmt.Sprintf("exrenamed%d", index)
	}
	harness.fake.answerJSON(opnsense.InterfacesInfo, renamed)
	if err := harness.collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("discovering: %v", err)
	}
	check(renamed, "after renaming everything")
}

// flowClassification is what AC10 compares per record.
type flowClassification struct {
	srcInterface, dstInterface, srcClient, dstClient *int64
	scope                                            string
}

func classifications(t *testing.T, database *store.Store) map[string]flowClassification {
	t.Helper()
	rows, err := database.DB().Query(`SELECT log_digest, src_interface_id, dst_interface_id,
		src_client_id, dst_client_id, traffic_scope FROM flow`)
	if err != nil {
		t.Fatalf("reading the flows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]flowClassification{}
	for rows.Next() {
		var (
			digest string
			c      flowClassification
		)
		if err := rows.Scan(&digest, &c.srcInterface, &c.dstInterface, &c.srcClient, &c.dstClient, &c.scope); err != nil {
			t.Fatalf("reading the flows: %v", err)
		}
		result[digest] = c
	}
	return result
}

func render(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(*value)
}

// TestTheClassificationDoesNotDependOnTheOrderRecordsArrive is AC10.
func TestTheClassificationDoesNotDependOnTheOrderRecordsArrive(t *testing.T) {
	var records []any
	if err := json.Unmarshal(fixtureBody(t, "firewall_log.json"), &records); err != nil {
		t.Fatalf("decoding the filter-log fixture: %v", err)
	}
	// Two more records that make the order matter: a remote address named first on an
	// upstream interface, and an inside address whose only evidence is a later sighting.
	records = append(records,
		map[string]any{"__digest__": "exampledigest0000000000000000101", "__timestamp__": "2026-09-26T23:50:01",
			"interface": "exdev3", "action": "pass", "dir": "out", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "198.51.100.40", "dst": "203.0.113.20", "srcport": "40000", "dstport": "443",
			"reason": "match"},
		map[string]any{"__digest__": "exampledigest0000000000000000102", "__timestamp__": "2026-09-26T23:50:00",
			"interface": "exdev1", "action": "pass", "dir": "in", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "192.0.2.77", "dst": "198.51.100.40", "srcport": "40001", "dstport": "22",
			"reason": "match"})
	reversed := make([]any, len(records))
	for index, record := range records {
		reversed[len(records)-1-index] = record
	}

	results := []map[string]flowClassification{}
	for _, page := range [][]any{records, reversed} {
		harness := newProbeHarness(t)
		arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
		harness.fake.answerJSON(opnsense.FirewallLog, page)
		if err := harness.collector.probeFirewallLog(context.Background()); err != nil {
			t.Fatalf("probing: %v", err)
		}
		if err := harness.collector.CollectFirewallLog(context.Background()); err != nil {
			t.Fatalf("collecting: %v", err)
		}
		results = append(results, classifications(t, harness.store))
	}
	if len(results[0]) != len(records) || len(results[1]) != len(records) {
		t.Fatalf("the two databases hold %d and %d flows of %d records", len(results[0]), len(results[1]), len(records))
	}
	placed := 0
	for digest, first := range results[0] {
		second := results[1][digest]
		if render(first.srcInterface) != render(second.srcInterface) ||
			render(first.dstInterface) != render(second.dstInterface) ||
			render(first.srcClient) != render(second.srcClient) ||
			render(first.dstClient) != render(second.dstClient) || first.scope != second.scope {
			t.Errorf("the record %s is classified %+v in one order and %+v in the other", digest, first, second)
		}
		if first.srcInterface != nil {
			placed++
		}
	}
	if placed == 0 {
		t.Error("no record's source was placed, so the comparison is vacuous")
	}
}

// TestNonLoggingRulesPerInterfaceEqualADirectCountOverTheFixture is AC13.
func TestNonLoggingRulesPerInterfaceEqualADirectCountOverTheFixture(t *testing.T) {
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	if stored := scalarCount(t, harness.store, "SELECT count(*) FROM rule WHERE interface IS NOT NULL"); stored == 0 {
		t.Fatal("no rule's interface was stored")
	}
	if stored := scalarCount(t, harness.store,
		"SELECT count(*) FROM rule WHERE pf_label = 'example-rule-label-2' AND interface = 'example_if_a,example_if_b'"); stored != 1 {
		t.Error("a rule's interface field was not stored verbatim")
	}

	direct := map[string]int{}
	for _, row := range fixtureRecords(t, "search_rule.json") {
		if legacy := decode.Flag(row, "legacy"); legacy != nil && *legacy {
			continue
		}
		if logs := decode.Flag(row, "log"); logs == nil || *logs {
			continue
		}
		list, _ := decode.String(row, "interface")
		for _, key := range strings.Split(list, ",") {
			if key != "" {
				direct[key]++
			}
		}
	}
	logging, err := harness.store.ReadRuleLogging(context.Background())
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	ids := map[string]int64{}
	for identifier := range upstreamByIdentifier(t, harness.store) {
		ids[identifier] = int64(scalarCount(t, harness.store, "SELECT id FROM interface WHERE identifier = ?", identifier))
	}
	if len(direct) == 0 {
		t.Fatal("the fixture holds no model rule that does not log")
	}
	for identifier, id := range ids {
		if got := logging.NotLoggingByInterface[id]; got != int64(direct[identifier]) {
			t.Errorf("%s: %d rules do not log, a direct count over the fixture says %d", identifier, got, direct[identifier])
		}
	}
	if logging.UnresolvedNotLogging != 1 || logging.FloatingNotLogging != 1 {
		t.Errorf("the legacy rule and the floating rule are counted %d and %d, not 1 and 1",
			logging.UnresolvedNotLogging, logging.FloatingNotLogging)
	}
}

// TestOnePairReadOnTwoInterfacesAtOneInstantIsTwoReadings is AC14.
func TestOnePairReadOnTwoInterfacesAtOneInstantIsTwoReadings(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	pair := func() map[string]any {
		return map[string]any{"status": "ok", "records": []any{map[string]any{
			"address": "198.51.100.10", "rate_bits_in": 100, "rate_bits_out": 50,
			"cumulative_bytes_in": 25, "cumulative_bytes_out": 12,
			"details": []any{map[string]any{"address": "203.0.113.9", "rate_bits": 100, "cumulative_bytes": 25}},
		}}}
	}
	harness.fake.answerJSON(opnsense.TrafficTop, map[string]any{"example_if_a": pair(), "example_if_b": pair()})
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	keys := stringColumn(t, harness.store, `SELECT DISTINCT subject_key FROM measurement_sample
		WHERE subject_kind = 'interface_endpoint_pair' AND measure = 'rate_bits_in' ORDER BY subject_key`)
	want := []string{"exdev0 198.51.100.10 203.0.113.9", "exdev1 198.51.100.10 203.0.113.9"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Errorf("one pair on two interfaces stored the subjects %v, not %v", keys, want)
	}
	if rows := scalarCount(t, harness.store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'interface_endpoint_pair' AND measure = 'rate_bits_in'`); rows != 2 {
		t.Errorf("%d inbound rate readings were stored, not 2", rows)
	}
	// With one peer, the record's own outbound figure is the pair's.
	if rows := scalarCount(t, harness.store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'interface_endpoint_pair' AND measure = 'rate_bits_out' AND value = 50`); rows != 2 {
		t.Errorf("%d outbound readings were stored for a record with one peer, not 2", rows)
	}
}

// TestTelemetryIsSampledFromVerifiedFieldsAndAMissingFieldIsNoSample is AC17.
func TestTelemetryIsSampledFromVerifiedFieldsAndAMissingFieldIsNoSample(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	reading := func(kind, key, measure string) []float64 {
		var values []float64
		rows, err := harness.store.DB().Query(`SELECT value FROM measurement_sample
			WHERE subject_kind = ? AND subject_key = ? AND measure = ?`, kind, key, measure)
		if err != nil {
			t.Fatalf("reading %s: %v", measure, err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var value float64
			if err := rows.Scan(&value); err != nil {
				t.Fatalf("reading %s: %v", measure, err)
			}
			values = append(values, value)
		}
		return values
	}
	for _, expected := range []struct {
		kind, key, measure string
		value              float64
	}{
		{"interface", "exdev0", "errors_in", 3},
		{"interface", "exdev0", "errors_out", 1},
		{"gateway", "EXAMPLE_GW_A", "delay_milliseconds", 12.3},
		{"gateway", "EXAMPLE_GW_A", "delay_stddev_milliseconds", 1.4},
		{"gateway", "EXAMPLE_GW_A", "loss_ratio", 0.005},
		// swapinfo -k writes KiB; the reading is in bytes.
		{"firewall", "/dev/example-swap-a", "swap_total_bytes", 2097152 * 1024},
		{"firewall", "/dev/example-swap-a", "swap_used_bytes", 1024 * 1024},
		{"firewall", "/dev/example-swap-b", "swap_total_bytes", 1048576 * 1024},
	} {
		values := reading(expected.kind, expected.key, expected.measure)
		if len(values) != 1 || values[0] < expected.value-1e-9 || values[0] > expected.value+1e-9 {
			t.Errorf("%s %s %s is %v, not %v", expected.kind, expected.key, expected.measure, values, expected.value)
		}
	}
	// A field the response does not carry, or carries as "~", is no reading at all.
	for _, absent := range []struct{ kind, key, measure string }{
		{"interface", "exdev1", "errors_in"},
		{"gateway", "EXAMPLE_GW_B", "delay_milliseconds"},
		{"gateway", "EXAMPLE_GW_B", "loss_ratio"},
		{"firewall", "/dev/example-swap-b", "swap_used_bytes"},
	} {
		if values := reading(absent.kind, absent.key, absent.measure); len(values) != 0 {
			t.Errorf("an absent %s %s %s was stored as %v", absent.kind, absent.key, absent.measure, values)
		}
	}
	// Swap comes from systemSwap and from nothing else: systemResources carries no swap figure.
	if registered := opnsense.SystemSwap.Path; registered != "/api/diagnostics/system/systemSwap" {
		t.Errorf("the swap endpoint is registered at %s", registered)
	}
	for _, text := range []string{"1.2 ms", "0.0 ms"} {
		if _, ok := parseMilliseconds(text); !ok {
			t.Errorf("%q was not read", text)
		}
	}
	for _, text := range []string{"~", "", "1.2", "-3 ms"} {
		if _, ok := parseMilliseconds(text); ok {
			t.Errorf("%q was read as a round-trip time", text)
		}
	}
	if value, ok := parseLossRatio("12.5 %"); !ok || value != 0.125 {
		t.Errorf("12.5 %% was read as %v", value)
	}
	if _, ok := parseLossRatio("~"); ok {
		t.Error("~ was read as a loss")
	}
}

// TestAnUnavailableGatewayEndpointIsRecordedInTheAvailability is AC4's degraded state.
func TestAnUnavailableGatewayEndpointIsRecordedInTheAvailability(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	harness.fake.answer(opnsense.GatewayStatus, http.StatusNotFound, []byte(`{"errorMessage":"Endpoint not found"}`))
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
	if !strings.Contains(detail, "gateway latency and loss (the endpoint did not answer)") {
		t.Errorf("the availability detail is %q, which does not record the gateway endpoint", detail)
	}
	if rows := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'gateway'"); rows != 0 {
		t.Errorf("%d gateway readings were stored from an endpoint that did not answer", rows)
	}

	harness.fake.answerJSON(opnsense.GatewayStatus, map[string]any{"items": []any{}, "status": "failed"})
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight); !strings.Contains(detail,
		"gateway latency and loss (the endpoint reported no gateway)") {
		t.Errorf("an answer with no gateway left the detail %q", detail)
	}
	if registered := opnsense.GatewayStatus.Path; registered != "/api/routes/gateway/status" {
		t.Errorf("the gateway endpoint is registered at %s", registered)
	}
}

// TestAnUnavailableSwapEndpointIsRecordedInTheAvailability is AC4's degraded state for the
// second endpoint the amendment of 4 October 2026 admits.
func TestAnUnavailableSwapEndpointIsRecordedInTheAvailability(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	harness.fake.answer(opnsense.SystemSwap, http.StatusNotFound, []byte(`{"errorMessage":"Endpoint not found"}`))
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
	if !strings.Contains(detail, "swap (the endpoint did not answer)") {
		t.Errorf("the availability detail is %q, which does not record the swap endpoint", detail)
	}
	if rows := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE measure IN ('swap_total_bytes', 'swap_used_bytes')"); rows != 0 {
		t.Errorf("%d swap readings were stored from an endpoint that did not answer", rows)
	}

	// A firewall with no swap device is a fact about the machine, recorded, never a zero.
	harness.clock.advance(time.Minute)
	harness.fake.answerJSON(opnsense.SystemSwap, map[string]any{"swap": []any{}})
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight); !strings.Contains(detail,
		"swap (this firewall reports no swap device)") {
		t.Errorf("an answer with no swap device left the detail %q", detail)
	}
	if rows := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE measure IN ('swap_total_bytes', 'swap_used_bytes')"); rows != 0 {
		t.Errorf("%d swap readings were stored for a firewall with no swap device", rows)
	}
}

// TestTheRefreshRunsAfterEachFilterLogPass is the last part of AC20.
func TestTheRefreshRunsAfterEachFilterLogPass(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	if err := harness.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	now := harness.clock.Now().UTC().Unix()
	for _, period := range store.Periods() {
		table := "volume_aggregate_" + period.Name
		if rows := scalarCount(t, harness.store, "SELECT count(*) FROM "+table+" WHERE computed_at = ?", now); rows == 0 {
			t.Errorf("%s holds no slot computed by the pass", table)
		}
	}
	if derived := scalarCount(t, harness.store, "SELECT count(*) FROM pair_volume_observation"); derived == 0 {
		t.Error("the pass derived no per-pair volume")
	}
	// A pass that reads nothing still refreshes the current slots.
	harness.fake.answerJSON(opnsense.FirewallLog, []any{})
	harness.clock.advance(time.Minute)
	if err := harness.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("collecting an empty page: %v", err)
	}
	later := harness.clock.Now().UTC().Unix()
	current := store.PeriodMonth.SlotStart(later)
	if rows := scalarCount(t, harness.store, `SELECT count(*) FROM volume_aggregate_30d
		WHERE period_start_at = ? AND computed_at = ?`, current, later); rows == 0 {
		t.Error("an empty pass did not rewrite the current month")
	}
}

// TestARefreshRunningDuringAnIngestionDoesNotLoseItsSlot is the race behind AC20.
//
// A pass stamps its rows with the instant it began and commits them afterwards, while
// another pass's derivation runs in its own goroutine. That derivation must not move the
// refresh watermark past the stamp, or the rows commit "before" the watermark, no later
// refresh selects their slot, and a flow whose classification changes nothing -- here both
// ends outside, the shape of a blocked scan of the firewall's own address -- is never
// forced either. Its closed slot would stay empty for ever.
func TestARefreshRunningDuringAnIngestionDoesNotLoseItsSlot(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the first refresh: %v", err)
	}

	stamped, token := harness.collector.beginIngest()
	harness.clock.advance(5 * time.Second)
	// Another pass finishes meanwhile, and its derivation refreshes.
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the concurrent refresh: %v", err)
	}

	// The stamped row commits only now, in an hour that closed long ago.
	closedHour := store.PeriodHour.SlotStart(stamped) - 3*3600
	if err := harness.store.InsertFlow(ctx, store.Flow{
		LogDigest: "refresh-race-probe", ObservedAt: closedHour + 60, IngestedAt: stamped,
		InterfaceDevice: "example-unmapped-device", InterfaceLookupState: store.LookupNotFound,
		SrcAddress: "203.0.113.7", DstAddress: "203.0.113.8", Protocol: "tcp", IPVersion: 4,
		Action: "block", Direction: "in", PacketBytes: 60, RuleLookupState: store.LookupNotFound,
	}); err != nil {
		t.Fatalf("inserting the stamped flow: %v", err)
	}
	harness.collector.endIngest(token)

	harness.clock.advance(5 * time.Second)
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the next refresh: %v", err)
	}
	if bytes := scalarCount(t, harness.store, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
		WHERE period_start_at = ?`, closedHour); bytes != 60 {
		t.Errorf("the closed hour holds %d bytes after the next refresh, not the 60 of the flow "+
			"committed during the concurrent refresh", bytes)
	}
	// And the watermark is free again once nothing is in flight.
	if _, watermark := harness.collector.refreshInstant(); watermark != harness.clock.Now().UTC().Unix() {
		t.Errorf("the watermark is held at %d with nothing in flight", watermark)
	}
}

// TestARefreshBetweenTwoRowsOfOnePassDoesNotFreezeTheSlot is the second half of the race
// behind AC20, and the probe the second verifier wrote.
//
// The slot ALREADY HOLDS ROWS when the concurrent refresh runs: one row of the pass is
// committed, the refresh rewrites the slot, and a second row of the same pass, stamped with
// the same instant, commits afterwards. A refresh that stamped the slot with its own instant
// would make the slot look fresher than the second row, and no later refresh would read it
// again; the slot would hold the first row's bytes for ever. The slot is stamped with the
// watermark instead -- the stamp of the pass still in flight -- and a stamp equal to a row's
// counts as stale.
func TestARefreshBetweenTwoRowsOfOnePassDoesNotFreezeTheSlot(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the first refresh: %v", err)
	}

	stamped, token := harness.collector.beginIngest()
	closedHour := store.PeriodHour.SlotStart(stamped) - 3*3600
	insert := func(digest string) {
		t.Helper()
		if err := harness.store.InsertFlow(ctx, store.Flow{
			LogDigest: digest, ObservedAt: closedHour + 60, IngestedAt: stamped,
			InterfaceDevice: "example-unmapped-device", InterfaceLookupState: store.LookupNotFound,
			SrcAddress: "203.0.113.7", DstAddress: "203.0.113.8", Protocol: "tcp", IPVersion: 4,
			Action: "block", Direction: "in", PacketBytes: 60, RuleLookupState: store.LookupNotFound,
		}); err != nil {
			t.Fatalf("inserting %s: %v", digest, err)
		}
	}
	insert("refresh-race-first")
	harness.clock.advance(5 * time.Second)
	// Another pass's derivation runs between the two rows, and finds the slot holding the first.
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the concurrent refresh: %v", err)
	}
	if bytes := scalarCount(t, harness.store, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
		WHERE period_start_at = ?`, closedHour); bytes != 60 {
		t.Fatalf("the concurrent refresh left %d bytes in the slot, not the first row's 60, so the "+
			"slot did not hold rows when it ran", bytes)
	}
	insert("refresh-race-second")
	harness.collector.endIngest(token)

	harness.clock.advance(5 * time.Second)
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the next refresh: %v", err)
	}
	flowBytes := scalarCount(t, harness.store, `SELECT sum(packet_bytes) FROM flow
		WHERE observed_at >= ? AND observed_at < ?`, closedHour, closedHour+3600)
	if flowBytes != 120 {
		t.Fatalf("the closed hour's flows hold %d bytes, not the two rows' 120", flowBytes)
	}
	if bytes := scalarCount(t, harness.store, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
		WHERE period_start_at = ?`, closedHour); bytes != flowBytes {
		t.Errorf("the closed hour holds %d bytes while its flows hold %d", bytes, flowBytes)
	}
	// And a refresh with nothing in flight stamps the slot later than its rows, so it rests.
	harness.clock.advance(5 * time.Second)
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("a quiet refresh: %v", err)
	}
	if computed := scalarCount(t, harness.store, `SELECT max(computed_at) FROM volume_aggregate_1h
		WHERE period_start_at = ?`, closedHour); int64(computed) <= stamped {
		t.Errorf("the slot is stamped %d, not later than its rows' %d, so it would never rest", computed, stamped)
	}
}
