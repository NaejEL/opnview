// The attribution_rate_per_client widget's own tests, run by
// `opnview plugin test plugins/attribution_rate_per_client` against a database of their own. The
// byte identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

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
  t.sql("INSERT INTO plugin_unbound.dns_resolution (id, lookup_key, client_address, domain, resolver, action, " +
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
    t.sql("INSERT INTO plugin_unbound.domain_attribution (flow_id, dns_resolution_id, site_name, " +
      "correlation_delay_seconds, attributed_at, method) VALUES " +
      "(:flow, :resolution, :site, :delay, :at, 'lookup_timing')",
      { flow: flowId, resolution: resolution, site: site, delay: delay, at: at + 60 });
  };
  attribution(1, 1, "example.org", 2);
  attribution(2, 1, "example.org", 4);
  attribution(4, 2, "example.net", 1);
}

test("each client's rate is its named flows over its eligible ones, worst first", function (t) {
  seed(t);
  var answer = t.widget("attribution_rate_per_client");
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal([data.resolver_covered, data.cache_covered], [true, false], "a resolver answered, its cache was not read");
  t.equal(data.rows.map(function (row) { return row.client.id; }), [10, 11], "the worst rate first");
  var first = data.rows[0];
  t.equal([first.eligible_flows.value, first.named_flows.value, first.eligible_bytes.value], [3, 2, 4500],
    "client 10's counts");
  t.equal(first.methods, { resolver_cache_answer: 0, lookup_timing: 2, not_recorded_beyond_flow_horizon: 0 },
    "client 10's flows per method");
  t.equal([first.rate, first.rate_state], [2 / 3, "defined"], "client 10's rate");
  t.equal([first.mean_delay_seconds, first.max_delay_seconds], [3, 4], "client 10's delays");
  t.equal(data.remainder, { members: 0, count: { measure: "logged", lower_bound: true, value: 0 } }, "nothing dropped");
  t.golden("two_clients.json", answer.raw);
});

test("best_first reverses the order, and limit drops the rest into the remainder", function (t) {
  seed(t);
  var data = t.widget("attribution_rate_per_client", { sort: "best_first", limit: 1 }).body.data;
  t.equal(data.rows.map(function (row) { return row.client.id; }), [11], "the best rate alone");
  t.equal([data.remainder.members, data.remainder.count.value], [1, 3], "client 10 and its eligible flows dropped");
  var volume = t.widget("attribution_rate_per_client", { sort: "by_volume" }).body.data;
  t.equal(volume.rows.map(function (row) { return row.client.id; }), [10, 11], "the most eligible bytes first");
});

test("with no lookup in the window, a rate is undefined and never 0", function (t) {
  seed(t);
  t.sql("DELETE FROM domain_attribution", {});
  t.sql("DELETE FROM dns_resolution", {});
  var data = t.widget("attribution_rate_per_client").body.data;
  t.equal(data.resolver_covered, false, "no resolver evidence");
  data.rows.forEach(function (row) {
    t.equal([row.rate, row.rate_state], [null, "undefined_no_resolver_evidence"], "client " + row.client.id);
  });
});
