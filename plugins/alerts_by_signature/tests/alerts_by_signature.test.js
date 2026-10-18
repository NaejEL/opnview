// The alerts_by_signature widget's own tests, run by `opnview plugin test plugins/alerts_by_signature`
// against a database of their own. The rows are the table shape's: each a list of cells in the order
// of data.columns, read here by column name.

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

// byRule indexes the rows by "<provider key> <rule identity>".
function byRule(data) {
  var rows = {};
  data.rows.forEach(function (row) {
    rows[cell(data, row, "provider_key") + " " + cell(data, row, "rule_identity")] = row;
  });
  return rows;
}

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
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'example_one', 'exdev1', 'Inside one', 'ethernet', 'vlan', :at, :at), " +
    "(2, 'example_two', 'exdev2', 'Inside two', 'ethernet', 'vlan', :at, :at)", { at: at });
  t.sql("INSERT INTO plugin_installation.interface_map (device, description, interface_id, discovered_at) VALUES " +
    "('exdev1', 'Inside one', 1, :at), ('exdev2', 'Inside two', 2, :at)", { at: at });
  t.sql("INSERT INTO plugin_installation.client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
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

test("one row per provider and rule identity, every field served, rows plus remainder the events", function (t) {
  seed(t);
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.columns, ["provider_kind", "provider_key", "provider_display_name", "rule_identity", "signature",
    "events", "last_seen_at", "normalised_severity", "provider_severity", "severity_state", "severity_reason",
    "category", "rule_source", "clients", "interfaces", "this_firewall", "observed_on"], "the columns");
  t.equal(data.rows.length, 6, "six (provider, rule identity) pairs");
  t.equal(data.total, { measure: "security_events", lower_bound: true, value: 9 }, "the total");
  var shown = data.rows.reduce(function (sum, row) { return sum + cell(data, row, "events").value; }, 0);
  t.equal(shown + data.remainder.events.value, 9, "rows plus remainder are the events");
  var rows = byRule(data);
  var first = rows["ids_a 9000001"];
  t.equal([cell(data, first, "provider_kind"), cell(data, first, "provider_display_name"),
    cell(data, first, "signature"), cell(data, first, "events").value, cell(data, first, "last_seen_at")],
    ["security_event", "Example engine A", "example signature 9000001", 3, AT - 700], "the row names its provider");
  t.equal([cell(data, first, "category"), cell(data, first, "rule_source")], ["example-category", "example.rules"],
    "the category and the ruleset");
  t.golden("nine_alerts.json", answer.raw);
});

test("the severity is the event's own, else the cache's, else unknown with its reason", function (t) {
  seed(t);
  var data = t.widget("alerts_by_signature", { period: "1h" }).body.data;
  var rows = byRule(data);
  var severity = function (key) {
    var row = rows[key];
    return [cell(data, row, "normalised_severity"), cell(data, row, "provider_severity"),
      cell(data, row, "severity_state"), cell(data, row, "severity_reason")];
  };
  t.equal(severity("ids_a 9000001"), ["high", "2", "resolved", null], "the cache's, with the raw value beside it");
  t.equal(severity("ids_a 9000002"), [null, null, "unknown", "not_reported_by_provider"],
    "found with no severity: unknown, and the category still served");
  t.equal(cell(data, rows["ids_a 9000002"], "category"), "example-category-two", "the category of a rule with no severity");
  t.equal(severity("ids_a 9000003"), [null, null, "unknown", "rule_not_found"], "a rule the provider does not know");
  t.equal(severity("ids_a 9000004"), [null, null, "unknown", "not_fetched"], "a rule never fetched");
  t.equal(severity("ids_b 9000005"), ["critical", null, "resolved", null], "the event's own severity");
  t.equal(severity("ids_b 9000001"), [null, null, "unknown", "not_fetched"],
    "the cache is per provider: the other provider's answer is not this one's");
});

test("the clients and interfaces at either end, this firewall, and the device observed on", function (t) {
  seed(t);
  var data = t.widget("alerts_by_signature", { period: "1h" }).body.data;
  var rows = byRule(data);
  var ends = function (key) {
    var row = rows[key];
    return [cell(data, row, "clients"), cell(data, row, "interfaces"), cell(data, row, "this_firewall")];
  };
  t.equal(ends("ids_a 9000003"), [[10, 20], [1, 2], false], "an east-west event names both ends");
  t.equal(ends("ids_a 9000002"), [[20], [2], false], "an inbound event names its destination client");
  t.equal(ends("ids_a 9000004"), [[], [2], true], "an event to this firewall");
  t.equal(cell(data, rows["ids_a 9000001"], "observed_on"),
    [{ device: "exdev1", interface_id: 1, state: "resolved" }], "a discovered device names its interface");
  t.equal(cell(data, rows["ids_a 9000002"], "observed_on"),
    [{ device: "exdev2^", interface_id: null, state: "not_found" }], "a device no interface carries is served raw");
  t.equal(cell(data, rows["ids_a 9000004"], "observed_on"),
    [{ device: null, interface_id: null, state: "not_reported" }], "no device reported");
  t.equal(data.clients.map(function (client) { return client.id; }), [10, 20], "the clients named");
  t.equal(data.interfaces.map(function (iface) { return iface.id; }), [1, 2], "the interfaces named");
});

test("each sort orders differently", function (t) {
  seed(t);
  var order = function (sort) {
    var data = t.widget("alerts_by_signature", { period: "1h", sort: sort }).body.data;
    return data.rows.map(function (row) { return cell(data, row, "provider_key") + " " + cell(data, row, "rule_identity"); });
  };
  t.equal(order("count"), ["ids_a 9000001", "ids_a 9000002", "ids_a 9000003", "ids_a 9000004", "ids_b 9000001",
    "ids_b 9000005"], "the most frequent first, ties in key order");
  t.equal(order("severity"), ["ids_b 9000005", "ids_a 9000001", "ids_a 9000002", "ids_a 9000003", "ids_a 9000004",
    "ids_b 9000001"], "the most severe first, unknown last, ties by count");
  t.equal(order("recency"), ["ids_a 9000003", "ids_a 9000002", "ids_a 9000001", "ids_b 9000001", "ids_b 9000005",
    "ids_a 9000004"], "the most recent first");
});

test("limit drops the rest into a counted remainder", function (t) {
  seed(t);
  var data = t.widget("alerts_by_signature", { period: "1h", limit: 2 }).body.data;
  t.equal([data.rows.length, data.remainder.members, data.remainder.events.value], [2, 4, 4], "two shown, four dropped");
});

test("each severity and each provider changes the answer", function (t) {
  seed(t);
  var keys = function (parameters) {
    parameters.period = "1h";
    var data = t.widget("alerts_by_signature", parameters).body.data;
    if (data === null) {
      // Nothing in the window passes the filter.
      return [];
    }
    return data.rows.map(function (row) { return cell(data, row, "provider_key") + " " + cell(data, row, "rule_identity"); });
  };
  t.equal(keys({ severities: ["unknown"] }), ["ids_a 9000002", "ids_a 9000003", "ids_a 9000004", "ids_b 9000001"],
    "unknown alone");
  t.equal(keys({ severities: ["critical"] }), ["ids_b 9000005"], "critical alone");
  t.equal(keys({ severities: ["high"] }), ["ids_a 9000001"], "high alone");
  t.equal(keys({ severities: ["medium", "low", "informational"] }), [], "none of these");
  t.equal(keys({ providers: [{ kind: "provider", by: "kind_and_key", value: "security_event/ids_b" }] }),
    ["ids_b 9000001", "ids_b 9000005"], "one provider");
  t.equal(keys({ clients: [{ kind: "client", by: "mac", value: "00:00:5e:00:53:14" }] }),
    ["ids_a 9000002", "ids_a 9000003"], "a client at either end");
  t.equal(keys({ interfaces: [{ kind: "interface", by: "identifier", value: "example_two" }] }),
    ["ids_a 9000002", "ids_a 9000003", "ids_a 9000004"], "an interface at either end");
});

// The conditions of S6-11 (specs/SPEC-step-6-alerts.md): the envelope's rules, with no IDS at all.

test("an unreachable engine with nothing stored answers unavailable and no data", function (t) {
  t.clock(AT);
  providers(t, "unavailable", "not_found");
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "unavailable", null], "unavailable, no data");
});

test("a stopped engine answers present but disabled", function (t) {
  t.clock(AT);
  providers(t, "present_but_disabled", "service_not_running");
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal([answer.status, answer.condition, answer.body.data], [200, "present_but_disabled", null],
    "present but disabled, no data");
});

test("a running engine with no alert in the window answers reachable with no rows", function (t) {
  t.clock(AT);
  providers(t, "reachable", "healthy_empty");
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "reachable_no_rows"], "reachable, no rows");
});

test("stored events, then an engine gone, answer the history under unavailable", function (t) {
  seedWith(t, "unavailable", "not_found");
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal([answer.status, answer.condition], [200, "unavailable"], "unavailable");
  t.equal(answer.body.data.total.value, 9, "the history");
});

test("with no provider of the kind registered, the answer is no data, never a failure", function (t) {
  t.clock(AT);
  t.sql("DELETE FROM source_availability WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'security_event')");
  t.sql("DELETE FROM provider WHERE kind = 'security_event'");
  var answer = t.widget("alerts_by_signature", { period: "1h" });
  t.equal([answer.status, answer.body.data], [200, null], "no data");
  t.assert(answer.condition !== "reachable" && answer.condition !== "reachable_no_rows",
    "the condition is not a reachable one: " + answer.condition);
});
