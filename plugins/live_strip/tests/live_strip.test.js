// The live_strip widget's own tests, run by `opnview plugin test plugins/live_strip` against a database
// of their own. The byte identity of its answers with the Go strip it replaced is the Go suite's
// (internal/widget/widgets, livestrip_test.go); these say what the strip does, in the widget's own words.

// AT is the instant the tests answer at: a multiple of every cadence they use.
var AT = 1790000400;

function seed(t) {
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, first_seen_at, " +
    "last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)", { at: AT - 3600 });
  t.sql("INSERT INTO interface_network (id, interface_id, network_address, prefix_length, address_family, " +
    "origin, updated_at) VALUES (1, 1, '192.0.2.0', 24, 4, 'operator', :at)", { at: AT - 3600 });
}

function flow(t, id, at, action) {
  t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_interface_id, src_address, dst_address, protocol, ip_version, action, direction, packet_bytes, " +
    "rule_lookup_state, traffic_scope) VALUES (:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, '192.0.2.10', " +
    "'203.0.113.7', 'tcp', 4, :action, 'in', 100, 'pending', 'north_south')",
    { id: id, digest: "d" + id, at: at, action: action });
}

test("an installation with nothing stored draws every bucket, each a gap, and holds no row", function (t) {
  t.clock(AT);
  seed(t);
  var answer = t.widget("live_strip");
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  if (data === null) {
    // A degraded source with nothing stored answers no data at all, never a strip of zeros.
    t.assert(answer.condition !== "reachable" && answer.condition !== "reachable_no_rows", "no data only when degraded");
    return;
  }
  t.equal(data.throughput.map(function (series) { return series.key; }),
    ["outbound", "inbound", "inter_interface", "outbound_unsplit"], "the throughput series");
  t.assert(data.throughput.every(function (series) {
    return series.measure === "sampled" && series.buckets.every(function (bucket) { return bucket.gap && bucket.value === null; });
  }), "every throughput bucket is a gap with no value");
});

test("the allowed decisions are counted per bucket, from the records' own timestamps", function (t) {
  t.clock(AT);
  seed(t);
  flow(t, 1, AT - 100, "pass");
  flow(t, 2, AT - 95, "pass");
  flow(t, 3, AT - 81, "pass");
  var data = t.widget("live_strip", { window_seconds: 120 }).body.data;
  t.equal([data.window.to - data.window.from, data.cadence_seconds], [130, 10], "two minutes at the live cadence");
  var allowed = data.decisions.filter(function (series) { return series.key === "allowed"; })[0];
  t.equal(allowed.measure, "logged", "the allowed series is logged");
  var counted = allowed.buckets.reduce(function (sum, bucket) { return sum + (bucket.value || 0); }, 0);
  t.equal(counted, 3, "the buckets hold the three decisions");
  t.assert(data.throughput.every(function (series) { return series.measure === "sampled"; }) &&
    data.decisions.every(function (series) { return series.measure !== "sampled"; }), "the two measures are never mixed");
});
