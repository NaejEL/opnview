// The alerts_by_client_and_interface widget's own tests, run by
// `opnview plugin test plugins/alerts_by_client_and_interface` against a database of their own. The
// rows are the table shape's: each a list of cells in the order of data.columns, read here by name.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// cell reads one cell of a row by its column's name.
function cell(data, row, name) {
  var index = data.columns.indexOf(name);
  if (index < 0) {
    throw new Error("no column " + name);
  }
  return row[index];
}

// subjectKey names a row: its client, this firewall, or the address no client was matched to.
function subjectKey(data, row) {
  var kind = cell(data, row, "subject_kind");
  if (kind === "client") {
    return "client " + cell(data, row, "client_id");
  }
  return kind === "unmatched" ? "unmatched " + cell(data, row, "address") : kind;
}

// bySubject indexes the rows by subjectKey.
function bySubject(data) {
  var rows = {};
  data.rows.forEach(function (row) { rows[subjectKey(data, row)] = row; });
  return rows;
}

// seed arranges two inside interfaces with one client each, two providers of the security_event
// kind, the rule-info answers of the first, and eleven events of the last hour:
//   ids_a 9000001  client 10 to an outside address, three events, observed on a discovered device;
//                  found, severity high, raw value "2"
//   ids_a 9000002  an outside address to client 20, two events, observed on a device no interface
//                  carries; found with no severity
//   ids_a 9000003  client 10 to client 20, one event; the provider does not know the rule
//   ids_a 9000004  an outside address to this firewall, one event, no device reported; never fetched
//   ids_b 9000005  client 10 to an outside address, one event, with its own severity, critical
//   ids_b 9000001  the same identity on the other provider, one event: never fetched there
//   ids_a 9000001  once more, an outside address to this firewall
//   ids_a 9000006  an outside address to another: no client and no end at this firewall; never fetched
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
  event({ provider: "ids_a", rule: "9000001", ago: 300, src: "203.0.113.9", dst: "198.51.100.1",
    dstInterface: 2, dstFirewall: true });
  event({ provider: "ids_a", rule: "9000006", ago: 200, src: "203.0.113.50", dst: "203.0.113.51" });
}

test("an event between two clients counts under both, and the distinct total is the events", function (t) {
  seed(t);
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.columns, ["subject_kind", "client_id", "address", "events", "as_source", "as_destination",
    "worst_severity", "interfaces", "top_signatures", "last_seen_at"], "the columns");
  t.equal(data.distinct_events, { measure: "security_events", lower_bound: true, value: 11 }, "eleven events");
  var rows = bySubject(data);
  var split = function (key) {
    var row = rows[key];
    return [cell(data, row, "events").value, cell(data, row, "as_source").value, cell(data, row, "as_destination").value];
  };
  t.equal(split("client 10"), [6, 6, 0], "client 10: the source of six events, the east-west one among them");
  t.equal(split("client 20"), [3, 0, 3], "client 20: the destination of three, the east-west one among them");
  var rowsSum = data.rows.reduce(function (sum, row) { return sum + cell(data, row, "events").value; }, 0);
  t.equal(rowsSum, 12, "the rows count the east-west event twice, so they need not sum to the total");
  t.golden("eleven_alerts.json", answer.raw);
});

test("an outside source attributes the event to its destination client", function (t) {
  seed(t);
  var data = t.widget("alerts_by_client_and_interface", { period: "1h" }).body.data;
  var row = bySubject(data)["client 20"];
  t.equal(cell(data, row, "top_signatures").map(function (signature) {
    return [signature.provider_key, signature.rule_identity, signature.events.value];
  }), [["ids_a", "9000002", 2], ["ids_a", "9000003", 1]], "the signatures it was the destination of");
  t.equal(cell(data, row, "interfaces"), [2], "its interface");
});

test("this firewall's end is its own row, and an unmatched address is a row carrying it whole", function (t) {
  seed(t);
  var data = t.widget("alerts_by_client_and_interface", { period: "1h" }).body.data;
  var rows = bySubject(data);
  var firewall = rows.this_firewall;
  t.equal([cell(data, firewall, "client_id"), cell(data, firewall, "events").value,
    cell(data, firewall, "as_destination").value, cell(data, firewall, "worst_severity"),
    cell(data, firewall, "interfaces")], [null, 2, 2, "high", [2]], "this firewall, the destination of two events");
  var unmatched = rows["unmatched 203.0.113.50"];
  t.equal([cell(data, unmatched, "client_id"), cell(data, unmatched, "address"), cell(data, unmatched, "events").value],
    [null, "203.0.113.50", 1], "the unmatched source address, whole");
  var without = t.widget("alerts_by_client_and_interface", { period: "1h", include_unmatched: false }).body.data;
  t.equal(bySubject(without)["unmatched 203.0.113.50"], undefined, "absent without include_unmatched");
  t.equal([without.excluded_unmatched.members, without.excluded_unmatched.events.value, without.distinct_events.value],
    [1, 1, 11], "and counted apart, the total unchanged");
});

test("each sort orders differently", function (t) {
  seed(t);
  var order = function (sort) {
    var data = t.widget("alerts_by_client_and_interface", { period: "1h", sort: sort }).body.data;
    return data.rows.map(function (row) { return subjectKey(data, row); });
  };
  t.equal(order("count"), ["client 10", "client 20", "this_firewall", "unmatched 203.0.113.50"], "the most events first");
  t.equal(order("worst_severity"), ["client 10", "this_firewall", "client 20", "unmatched 203.0.113.50"],
    "the worst severity first, unknown last, ties by count");
});

test("limit drops the rest into a counted remainder, and each severity changes the answer", function (t) {
  seed(t);
  var limited = t.widget("alerts_by_client_and_interface", { period: "1h", limit: 1 }).body.data;
  t.equal([limited.rows.length, limited.remainder.members, limited.remainder.events.value], [1, 3, 6],
    "one row shown, three dropped");
  var subjects = function (severities) {
    var data = t.widget("alerts_by_client_and_interface", { period: "1h", severities: severities }).body.data;
    return data === null ? [] : data.rows.map(function (row) { return subjectKey(data, row); });
  };
  t.equal(subjects(["critical"]), ["client 10"], "critical");
  t.equal(subjects(["high"]), ["client 10", "this_firewall"], "high");
  t.equal(subjects(["unknown"]), ["client 20", "client 10", "this_firewall", "unmatched 203.0.113.50"], "unknown");
  t.equal(subjects(["medium", "low", "informational"]), [], "none");
  var interfaces = t.widget("alerts_by_client_and_interface", { period: "1h",
    interfaces: [{ kind: "interface", by: "identifier", value: "example_one" }] }).body.data;
  t.equal(interfaces.rows.map(function (row) { return subjectKey(interfaces, row); }), ["client 10", "client 20"],
    "an interface at either end");
});

// The conditions of S6-11 (specs/SPEC-step-6-alerts.md): the envelope's rules, with no IDS at all.

test("an unreachable engine with nothing stored answers unavailable and no data", function (t) {
  t.clock(AT);
  providers(t, "unavailable", "not_found");
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "unavailable", null], "unavailable, no data");
});

test("a stopped engine answers present but disabled", function (t) {
  t.clock(AT);
  providers(t, "present_but_disabled", "service_not_running");
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "present_but_disabled", null],
    "present but disabled, no data");
});

test("a running engine with no alert in the window answers reachable with no rows", function (t) {
  t.clock(AT);
  providers(t, "reachable", "healthy_empty");
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "reachable_no_rows"], "reachable, no rows");
});

test("stored events, then an engine gone, answer the history under unavailable", function (t) {
  seedWith(t, "unavailable", "not_found");
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "unavailable"], "unavailable");
  t.equal(answer.body.data.distinct_events.value, 11, "the history");
});

test("with no provider of the kind registered, the answer is no data, never a failure", function (t) {
  t.clock(AT);
  t.sql("DELETE FROM source_availability WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'security_event')");
  t.sql("DELETE FROM provider WHERE kind = 'security_event'");
  var answer = t.widget("alerts_by_client_and_interface", { period: "1h" });
  t.equal([answer.status, answer.body.data], [200, null], "no data");
  t.assert(answer.condition !== "reachable" && answer.condition !== "reachable_no_rows",
    "the condition is not a reachable one: " + answer.condition);
});
