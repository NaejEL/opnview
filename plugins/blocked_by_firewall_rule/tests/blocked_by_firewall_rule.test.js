// The blocked_by_firewall_rule widget's own tests, run by
// `opnview plugin test plugins/blocked_by_firewall_rule` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface, one client on it, a rule the user wrote (5), an automatic
// rule (6), and five records of the last hour: two blocked by rule 5, one by rule 6, one by a rid
// no rule carries any more, and one passed, which no row counts.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO interface_map (device, description, interface_id, discovered_at) " +
    "VALUES ('vlan0.10', 'Inside', 1, :at)", { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES (10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', " +
    "'192.0.2.10', :at, :at)", { at: at });
  t.sql("INSERT INTO rule (id, pf_label, description, action, is_automatic, discovered_at) VALUES " +
    "(5, 'label-five', 'Block the guests', 'block', false, :at), " +
    "(6, 'label-six', 'Default deny', 'block', true, :at)", { at: at });
  var flow = function (id, action, rid, rule) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, log_reason, packet_bytes, rid, rule_id, rule_lookup_state, " +
      "traffic_scope) VALUES (:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, 10, '192.0.2.10', " +
      "'203.0.113.7', 'tcp', 4, :action, 'in', 'match', 100, :rid, :rule, :state, 'north_south')",
      { id: id, digest: "d" + id, at: at + id, action: action, rid: rid, rule: rule,
        state: rule === null ? "not_found" : "resolved" });
  };
  flow(1, "block", "label-five", 5);
  flow(2, "reject", "label-five", 5);
  flow(3, "block", "label-six", 6);
  flow(4, "block", "label-gone", null);
  flow(5, "pass", "label-five", 5);
}

test("rules are ranked by blocked records, and a rid no rule carries is its own row", function (t) {
  seed(t);
  var answer = t.widget("blocked_by_firewall_rule", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.rows.map(function (row) { return [row.state, row.blocked_connections.value]; }),
    [["resolved", 2], ["rule_not_found", 1], ["resolved", 1]], "the most blocked first, ties in key order");
  t.equal(data.rows[0].rule, { id: 5, pf_label: "label-five", description: "Block the guests", action: "",
    automatic: false }, "the rule as the firewall described it");
  t.equal([data.rows[1].rule, data.rows[1].rid], [null, "label-gone"], "the rid of a rule removed since");
  t.equal(data.rows[0].fired_on, [{ device: "vlan0.10", description: "Inside", interface_id: 1 }],
    "the device it fired on");
  t.equal([data.rows[0].interfaces, data.rows[0].clients], [[1], [10]], "what it blocked");
  t.equal(data.total.value, 4, "the passed record is no refusal");
  t.golden("four_refusals.json", answer.raw);
});

test("each shown rule carries its timeline, which sums to its count", function (t) {
  seed(t);
  var rows = t.widget("blocked_by_firewall_rule", { period: "1h" }).body.data.rows;
  rows.forEach(function (row) {
    var sum = row.timeline.reduce(function (total, bucket) { return total + (bucket.value || 0); }, 0);
    t.equal(sum, row.blocked_connections.value, "the timeline of " + (row.rid || "a rule"));
    t.equal(row.timeline.length, 60, "one bucket per minute of the hour");
  });
});

test("automatic rules can be left out, and limit drops the rest into the remainder", function (t) {
  seed(t);
  var manual = t.widget("blocked_by_firewall_rule", { period: "1h", include_automatic: false }).body.data;
  t.equal(manual.rows.length, 2, "rule 6 left out");
  t.equal([manual.excluded_automatic.members, manual.excluded_automatic.count.value], [1, 1], "and counted");
  var limited = t.widget("blocked_by_firewall_rule", { period: "1h", limit: 1 }).body.data;
  t.equal([limited.rows.length, limited.remainder.members, limited.remainder.count.value], [1, 2, 2],
    "one row shown, two dropped");
});

test("with no source probed and nothing stored, the answer is no data, never a zero", function (t) {
  t.clock(AT);
  var answer = t.widget("blocked_by_firewall_rule");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
