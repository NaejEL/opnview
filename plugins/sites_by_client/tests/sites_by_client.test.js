// The sites_by_client widget's own tests, run by `opnview plugin test plugins/sites_by_client`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

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
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
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

test("each client's row carries its top site names and its attribution rate", function (t) {
  seed(t);
  var answer = t.widget("sites_by_client");
  t.equal(answer.status, 200, "the status");
  var rows = answer.body.data.rows;
  t.equal(rows.map(function (row) { return row.client.id; }), [10, 11], "the most named flows first");
  var first = rows[0];
  t.equal(first.state, "named", "client 10 is named");
  t.equal(first.sites.map(function (site) { return [site.kind, site.name || site.address, site.flows.value, site.bytes.value]; }),
    [["site", "example.org", 2, 4000], ["address", "203.0.113.7", 1, 500]],
    "client 10's site, then its unnamed flow by its address");
  t.equal(first.sites[1].lookup_state, "no_row", "an address never looked up says so");
  t.equal([first.sites[0].mean_delay_seconds, first.sites[0].max_delay_seconds], [3, 4], "its delays");
  t.equal([first.attribution.rate, first.attribution.rate_state], [2 / 3, "defined"], "client 10's rate");
  t.golden("two_clients.json", answer.raw);
});

test("a client with eligible flows and none named keeps its row, and the rate can be left out", function (t) {
  seed(t);
  t.sql("DELETE FROM domain_attribution WHERE flow_id = 4", {});
  var rows = t.widget("sites_by_client", { show_attribution_rate: false }).body.data.rows;
  var second = rows[1];
  t.equal([second.client.id, second.state], [11, "no_site_name_inferred"], "client 11");
  t.equal(second.sites.map(function (site) { return [site.kind, site.address, site.flows.value]; }),
    [["address", "203.0.113.7", 1]], "its flow, shown by its address");
  t.assert(rows.every(function (row) { return !("attribution" in row); }), "no row carries a rate");
});

test("min_flows sets the rarely reached names aside", function (t) {
  seed(t);
  var rows = t.widget("sites_by_client", { min_flows: 2 }).body.data.rows;
  t.equal([rows[1].sites, rows[1].below_min_flows.members, rows[1].below_min_flows.count.value], [[], 1, 1],
    "example.net below the minimum");
  t.equal([rows[0].below_min_flows.members, rows[0].below_min_flows.count.value], [1, 1],
    "client 10's address row below it as well");
});
