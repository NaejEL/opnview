// The security_alerts_over_time widget's own tests, run by
// `opnview plugin test plugins/security_alerts_over_time` against a database of their own.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges two inside interfaces with one client each, two providers of the security_event
// kind, the rule-info answers of the first, and nine events of the last hour:
//   ids_a 9000001  client 10 to an outside address, three events, observed on a discovered device;
//                  found, severity high, raw value "2"
//   ids_a 9000002  an outside address to client 20, two events, observed on a device no interface
//                  carries; found with no severity
//   ids_a 9000003  client 10 to client 20, one event; the provider does not know the rule
//   ids_a 9000004  an outside address to this firewall, one event, no device reported; never fetched
//   ids_b 9000005  client 10 to an outside address, one event, with its own severity, critical
//   ids_b 9000001  the same identity on the other provider, one event: never fetched there
function seed(t) {
  seedWith(t, "reachable", "healthy");
}

// providers makes the two example engines the only providers of the security_event kind, active,
// with the availability given; none, when state is null.
function providers(t, state, outcome) {
  t.sql("DELETE FROM source_availability WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'security_event')");
  t.sql("DELETE FROM provider WHERE kind = 'security_event'");
  t.sql("INSERT INTO provider (kind, provider_key, display_name, concurrent, is_active, registered_at) VALUES " +
    "('security_event', 'ids_a', 'Example engine A', true, true, :at), " +
    "('security_event', 'ids_b', 'Example engine B', true, true, :at)", { at: AT - 3000 });
  if (state !== null) {
    t.sql("INSERT INTO source_availability (provider_id, state, outcome, probe, checked_at) " +
      "SELECT id, :state, :outcome, 'alerts', :at FROM provider WHERE kind = 'security_event'",
      { state: state, outcome: outcome, at: AT - 60 });
  }
}

// seedWith is seed, with the providers' availability given.
function seedWith(t, state, outcome) {
  t.clock(AT);
  var at = AT - 3000;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'example_one', 'exdev1', 'Inside one', 'ethernet', 'vlan', :at, :at), " +
    "(2, 'example_two', 'exdev2', 'Inside two', 'ethernet', 'vlan', :at, :at)", { at: at });
  t.sql("INSERT INTO interface_map (device, description, interface_id, discovered_at) VALUES " +
    "('exdev1', 'Inside one', 1, :at), ('exdev2', 'Inside two', 2, :at)", { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES (10, 'mac', '00:00:5e:00:53:0a', 1, '00:00:5e:00:53:0a', " +
    "'192.0.2.10', :at, :at), (20, 'mac', '00:00:5e:00:53:14', 2, '00:00:5e:00:53:14', '198.51.100.20', :at, :at)",
    { at: at });
  providers(t, state, outcome);
  var provider = function (key) {
    return "(SELECT id FROM provider WHERE kind = 'security_event' AND provider_key = '" + key + "')";
  };
  t.sql("INSERT INTO provider_rule_info (provider_id, rule_identity, lookup_state, normalised_severity, " +
    "provider_severity, category, rule_source, fetched_at) VALUES " +
    "(" + provider("ids_a") + ", '9000001', 'found', 'high', '2', 'example-category', 'example.rules', :at), " +
    "(" + provider("ids_a") + ", '9000002', 'found', NULL, NULL, 'example-category-two', 'example.rules', :at), " +
    "(" + provider("ids_a") + ", '9000003', 'not_found', NULL, NULL, NULL, NULL, :at)", { at: at });
  var key = 0;
  var event = function (spec) {
    key++;
    t.sql("INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at, rule_identity, " +
      "signature, event_action, normalised_severity, src_address, dst_address, protocol, in_interface_device, " +
      "src_client_id, src_interface_id, src_is_this_firewall, dst_client_id, dst_interface_id, dst_is_this_firewall) " +
      "VALUES (" + provider(spec.provider) + ", :key, :at, :at, :rule, :signature, 'allowed', :severity, :src, :dst, " +
      "'TCP', :device, :src_client, :src_interface, :src_firewall, :dst_client, :dst_interface, :dst_firewall)",
      { key: "k" + key, at: AT - spec.ago, rule: spec.rule, signature: "example signature " + spec.rule,
        severity: spec.severity || null, src: spec.src, dst: spec.dst, device: spec.device || null,
        src_client: spec.srcClient || null, src_interface: spec.srcInterface || null,
        src_firewall: spec.srcFirewall || false, dst_client: spec.dstClient || null,
        dst_interface: spec.dstInterface || null, dst_firewall: spec.dstFirewall || false });
  };
  [900, 800, 700].forEach(function (ago) {
    event({ provider: "ids_a", rule: "9000001", ago: ago, src: "192.0.2.10", dst: "203.0.113.7", device: "exdev1",
      srcClient: 10, srcInterface: 1 });
  });
  [600, 500].forEach(function (ago) {
    event({ provider: "ids_a", rule: "9000002", ago: ago, src: "203.0.113.8", dst: "198.51.100.20",
      device: "exdev2^", dstClient: 20, dstInterface: 2 });
  });
  event({ provider: "ids_a", rule: "9000003", ago: 100, src: "192.0.2.10", dst: "198.51.100.20", device: "exdev1",
    srcClient: 10, srcInterface: 1, dstClient: 20, dstInterface: 2 });
  event({ provider: "ids_a", rule: "9000004", ago: 1200, src: "203.0.113.9", dst: "198.51.100.1",
    dstInterface: 2, dstFirewall: true });
  event({ provider: "ids_b", rule: "9000005", ago: 1100, severity: "critical", src: "192.0.2.10",
    dst: "203.0.113.7", device: "exdev1", srcClient: 10, srcInterface: 1 });
  event({ provider: "ids_b", rule: "9000001", ago: 1000, src: "192.0.2.10", dst: "203.0.113.7", device: "exdev1",
    srcClient: 10, srcInterface: 1 });
}


// seriesBy indexes the series by severity.
function seriesBy(data) {
  var out = {};
  data.series.forEach(function (series) { out[series.key] = series; });
  return out;
}

// bucketSum is the sum of a series' buckets.
function bucketSum(series) {
  return series.buckets.reduce(function (sum, bucket) { return sum + (bucket.value === null ? 0 : bucket.value.value); }, 0);
}

test("one series per severity present, unknown included, buckets summing to the series and series to the events", function (t) {
  seed(t);
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.series.map(function (series) { return series.key; }), ["critical", "high", "unknown"],
    "the severities present, most severe first, unknown last");
  var sum = 0;
  data.series.forEach(function (series) {
    t.equal(bucketSum(series), series.total.value, series.key + ": the buckets sum to the series");
    sum += series.total.value;
  });
  t.equal([sum, data.total.value], [9, 9], "the series sum to the events");
  t.equal(seriesBy(data).unknown.reasons, {
    not_fetched: { measure: "security_events", lower_bound: true, value: 2 },
    not_reported_by_provider: { measure: "security_events", lower_bound: true, value: 2 },
    rule_not_found: { measure: "security_events", lower_bound: true, value: 1 }
  }, "the unknown series says why, per reason");
  t.equal([data.bucket_seconds, data.bucket_requested, data.bucket_coarsened], [60, "auto", false],
    "the width decision 8 gives an hour");
  t.equal(data.series[0].buckets.length, 60, "one bucket per minute of the hour");
  t.golden("nine_alerts.json", answer.raw);
});

test("every series names its providers", function (t) {
  seed(t);
  var series = seriesBy(t.widget("security_alerts_over_time", { period: "1h" }).body.data);
  var named = function (key) {
    return series[key].providers.map(function (provider) {
      return [provider.kind, provider.provider_key, provider.display_name, provider.events.value];
    });
  };
  t.equal(named("critical"), [["security_event", "ids_b", "Example engine B", 1]], "critical");
  t.equal(named("high"), [["security_event", "ids_a", "Example engine A", 3]], "high");
  t.equal(named("unknown"), [["security_event", "ids_a", "Example engine A", 4],
    ["security_event", "ids_b", "Example engine B", 1]], "unknown, from both");
});

test("each severity and each provider changes the answer", function (t) {
  seed(t);
  var keys = function (parameters) {
    parameters.period = "1h";
    var data = t.widget("security_alerts_over_time", parameters).body.data;
    if (data === null) {
      return [];
    }
    return data.series.map(function (series) { return series.key + ":" + series.total.value; });
  };
  t.equal(keys({ severities: ["critical"] }), ["critical:1"], "critical");
  t.equal(keys({ severities: ["high"] }), ["high:3"], "high");
  t.equal(keys({ severities: ["unknown"] }), ["unknown:5"], "unknown");
  t.equal(keys({ severities: ["medium"] }), [], "medium");
  t.equal(keys({ severities: ["low"] }), [], "low");
  t.equal(keys({ severities: ["informational"] }), [], "informational");
  t.equal(keys({ providers: [{ kind: "provider", by: "kind_and_key", value: "security_event/ids_a" }] }),
    ["high:3", "unknown:4"], "one provider");
  t.equal(keys({ providers: [{ kind: "provider", by: "kind_and_key", value: "security_event/ids_b" }] }),
    ["critical:1", "unknown:1"], "the other");
  t.equal(keys({ clients: [{ kind: "client", by: "mac", value: "00:00:5e:00:53:14" }] }),
    ["unknown:3"], "a client at either end");
  t.equal(keys({ interfaces: [{ kind: "interface", by: "identifier", value: "example_two" }] }),
    ["unknown:4"], "an interface at either end");
});

test("a bucket asked for is used, and one too fine for the window is coarsened and says so", function (t) {
  seed(t);
  var quarter = t.widget("security_alerts_over_time", { period: "1h", bucket: "15m" }).body.data;
  t.equal([quarter.bucket_seconds, quarter.bucket_coarsened], [900, false], "a quarter-hour");
  quarter.series.forEach(function (series) {
    t.equal(bucketSum(series), series.total.value, series.key + ": the quarter-hour buckets sum to the series");
  });
  var fine = t.widget("security_alerts_over_time", { period: "30d", bucket: "1m" }).body.data;
  t.equal([fine.bucket_seconds, fine.bucket_requested, fine.bucket_coarsened], [3600, "1m", true],
    "a minute over thirty days is served at the hour");
  t.assert(fine.series[0].buckets.length <= 1000, "no more than a thousand buckets");
});

// The conditions of S6-11 (specs/SPEC-step-6-alerts.md): the envelope's rules, with no IDS at all.

test("an unreachable engine with nothing stored answers unavailable and no data", function (t) {
  t.clock(AT);
  providers(t, "unavailable", "not_found");
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "unavailable", null], "unavailable, no data");
});

test("a stopped engine answers present but disabled", function (t) {
  t.clock(AT);
  providers(t, "present_but_disabled", "service_not_running");
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "present_but_disabled", null],
    "present but disabled, no data");
});

test("a running engine with no alert in the window answers reachable with no rows", function (t) {
  t.clock(AT);
  providers(t, "reachable", "healthy_empty");
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "reachable_no_rows"], "reachable, no rows");
});

test("stored events, then an engine gone, answer the history under unavailable", function (t) {
  seedWith(t, "unavailable", "not_found");
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "unavailable"], "unavailable");
  t.equal(answer.body.data.total.value, 9, "the history");
});

test("with no provider of the kind registered, the answer is no data, never a failure", function (t) {
  t.clock(AT);
  t.sql("DELETE FROM source_availability WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'security_event')");
  t.sql("DELETE FROM provider WHERE kind = 'security_event'");
  var answer = t.widget("security_alerts_over_time", { period: "1h" });
  t.equal([answer.status, answer.body.data], [200, null], "no data");
  t.assert(answer.condition !== "reachable" && answer.condition !== "reachable_no_rows",
    "the condition is not a reachable one: " + answer.condition);
});
