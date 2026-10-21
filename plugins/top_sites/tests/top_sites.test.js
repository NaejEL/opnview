// The top_sites widget's own tests, run by `opnview plugin test plugins/top_sites` against a
// database of their own. The byte identity of every recorded answer of the widget is the Go suite's
// (internal/widget/widgets, golden_test.go); these say what the widget does, in the widget's own
// words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface and two clients on it. Client 10 opens three outbound flows in
// the last hour: two named example.org by lookup timing, delays 2 s and 4 s, and one no lookup
// named. Client 11 opens one outbound flow, named example.net. A lookup stored inside the window is
// the evidence a resolver source answered for it.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO plugin_installation.client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', :at, :at), " +
    "(11, 'mac', '00:11:22:33:44:66', 1, '00:11:22:33:44:66', '192.0.2.11', :at, :at)", { at: at });
  t.sql("INSERT INTO dns_resolution (id, lookup_key, client_address, domain, resolver, action, " +
    "looked_up_at, ingested_at) VALUES " +
    "(1, 'k1', '192.0.2.10', 'example.org', 'unbound', 'pass', :at, :at), " +
    "(2, 'k2', '192.0.2.11', 'example.net', 'unbound', 'pass', :at, :at)", { at: at });
  var flow = function (id, client, address, bytes) {
    t.sql("INSERT INTO plugin_pf.flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, :client, :src, '203.0.113.7', 'tcp', 4, " +
      "'pass', 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, client: client, src: address, bytes: bytes });
  };
  flow(1, 10, "192.0.2.10", 1000);
  flow(2, 10, "192.0.2.10", 3000);
  flow(3, 10, "192.0.2.10", 500);
  flow(4, 11, "192.0.2.11", 700);
  var attribution = function (flowId, resolution, site, delay) {
    t.sql("INSERT INTO domain_attribution (flow_id, dns_resolution_id, site_name, " +
      "correlation_delay_seconds, attributed_at, method) VALUES " +
      "(:flow, :resolution, :site, :delay, :at, 'lookup_timing')",
      { flow: flowId, resolution: resolution, site: site, delay: delay, at: at + 60 });
  };
  attribution(1, 1, "example.org", 2);
  attribution(2, 1, "example.org", 4);
  attribution(4, 2, "example.net", 1);
}

test("site names are ranked by attributed flows, with the window's attribution beside them", function (t) {
  seed(t);
  var answer = t.widget("top_sites");
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.grouping, "site_name", "not grouped");
  t.equal(data.rows.map(function (row) { return [row.kind, row.name || row.address, row.flows.value, row.clients]; }),
    [["site", "example.org", 2, 1], ["site", "example.net", 1, 1], ["address", "203.0.113.7", 1, 1]],
    "the ranking, the flow no name was inferred for shown by its address");
  t.equal([data.rows[2].lookup_state, data.rows[2].country_code, data.rows[2].asn, data.rows[2].operator],
    ["no_row", null, null, null], "the address never looked up says so");
  t.equal(data.rows[0].client_ids, [10], "who reached example.org");
  t.equal(data.rows[0].methods, { resolver_cache_answer: 0, lookup_timing: 2, not_recorded_beyond_flow_horizon: 0 },
    "its flows per method");
  t.equal([data.total.members, data.total.count.value], [3, 4], "the total: every eligible flow");
  t.equal([data.attribution.eligible_flows.value, data.attribution.named_flows.value, data.attribution.rate],
    [4, 3, 0.75], "the window's attribution");
  t.golden("two_sites.json", answer.raw);
});

test("limit and min_flows set names aside, and the parts make the total", function (t) {
  seed(t);
  var data = t.widget("top_sites", { limit: 1 }).body.data;
  t.equal(data.rows.map(function (row) { return row.name; }), ["example.org"], "the first name alone");
  t.equal([data.remainder.members, data.remainder.count.value], [2, 2], "example.net and the address past the limit");
  var minimum = t.widget("top_sites", { min_flows: 2 }).body.data;
  t.equal([minimum.below_min_flows.members, minimum.below_min_flows.count.value], [2, 2],
    "example.net and the address below it");
});

test("a client reference narrows the names to that client's", function (t) {
  seed(t);
  var data = t.widget("top_sites", { clients: [{ kind: "client", by: "mac", value: "00:11:22:33:44:66" }] }).body.data;
  t.equal(data.rows.map(function (row) { return row.name; }), ["example.net"], "client 11's name");
});

test("a partly named address gives its named flows to the site and the rest to its address row",
  function (t) {
    seed(t);
    t.sql("INSERT INTO plugin_maxmind.geo_asn (address, provider_id, lookup_state, country_code, asn, operator, " +
      "dataset_build_at, looked_up_at) VALUES ('203.0.113.7', (SELECT id FROM provider WHERE kind = " +
      "'ip_geolocation' ORDER BY id LIMIT 1), 'resolved', 'NL', 64500, 'Example Operator', :at, :at)",
      { at: AT - 600 });
    var data = t.widget("top_sites").body.data;
    var address = data.rows.filter(function (row) { return row.kind === "address"; });
    t.equal(address.map(function (row) {
      return [row.address, row.flows.value, row.bytes.value, row.country_code, row.asn, row.operator, row.lookup_state];
    }), [["203.0.113.7", 1, 500, "NL", 64500, "Example Operator", "resolved"]],
    "the address's unnamed flow, with its operator and its country");
    var sum = data.rows.reduce(function (total, row) { return total + row.flows.value; }, 0);
    t.equal(sum + data.remainder.count.value + data.below_min_flows.count.value,
      data.attribution.eligible_flows.value, "the rows make the eligible flows");
});
