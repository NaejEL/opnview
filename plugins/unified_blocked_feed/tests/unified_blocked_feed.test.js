// The unified_blocked_feed widget's own tests, run by
// `opnview plugin test plugins/unified_blocked_feed` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface, one client on it, one rule, a list somebody assigned to
// advertising, and four refusals of the last hour: a record the rule blocked, a record blocked by
// a rid no rule carries, a lookup the advertising list refused, and a lookup no list is recorded
// for.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES (10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', " +
    "'192.0.2.10', :at, :at)", { at: at });
  t.sql("INSERT INTO rule (id, pf_label, description, action, discovered_at) " +
    "VALUES (5, 'label-five', 'Block the guests', 'block', :at)", { at: at });
  t.sql("INSERT INTO blocklist (id, name, purpose, purpose_assigned_at, first_seen_at, last_seen_at) " +
    "VALUES (1, 'list-one', 'advertising', :at, :at, :at)", { at: at });
  var flow = function (id, rid, rule) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, log_reason, packet_bytes, rid, rule_id, rule_lookup_state, " +
      "traffic_scope) VALUES (:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, 10, '192.0.2.10', " +
      "'203.0.113.7', 'tcp', 4, 'block', 'in', 'match', 100, :rid, :rule, :state, 'north_south')",
      { id: id, digest: "d" + id, at: at + id, rid: rid, rule: rule, state: rule === null ? "not_found" : "resolved" });
  };
  flow(1, "label-five", 5);
  flow(2, "label-gone", null);
  var lookup = function (id, domain, list) {
    t.sql("INSERT INTO dns_resolution (id, lookup_key, client_address, client_id, domain, resolver, " +
      "action, blocklist_id, looked_up_at, ingested_at, interface_id, interface_lookup_state) VALUES " +
      "(:id, :key, '192.0.2.10', 10, :domain, 'unbound', 'block', :list, :at, :at, 1, 'resolved')",
      { id: id, key: "k" + id, domain: domain, list: list, at: at + 10 + id });
  };
  lookup(1, "ads.example", 1);
  lookup(2, "nolist.example", null);
}

test("every refusal is listed newest first, each naming what decided it", function (t) {
  seed(t);
  var answer = t.widget("unified_blocked_feed", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.entries.map(function (entry) { return [entry.engine_kind, entry.decided_by.kind]; }),
    [["dns_list_not_recorded", "list_not_recorded"], ["dns_advertising_list", "list"],
      ["firewall_rule", "rule_not_found"], ["firewall_rule", "rule"]], "the newest first");
  t.equal(data.entries[3].decided_by.rule.pf_label, "label-five", "the rule a pf label names");
  t.equal([data.entries[1].target, data.entries[1].target_kind], ["ads.example", "domain"], "the domain refused");
  t.equal([data.entries[3].target, data.entries[3].port, data.entries[3].protocol], ["203.0.113.7", null, "tcp"],
    "an address target carries its protocol, and its port is null where none was logged");
  t.equal([data.entries[1].port, data.entries[1].protocol], [null, null], "a refused lookup has neither");
  t.equal(data.entries[3]["interface"].identifier, "opt1", "the interface it was refused on");
  t.equal(data.sources.map(function (source) { return [source.provider_kind, source.count.value]; }),
    [["firewall_log", 2], ["dns_lookup", 2], ["security_event", 0]], "each contributing source");
  t.equal(data.total.value, 4, "the total");
  t.golden("four_refusals.json", answer.raw);
});

test("a narrowed feed says what it narrowed, and limit keeps the newest", function (t) {
  seed(t);
  var narrowed = t.widget("unified_blocked_feed", { period: "1h", engines: ["dns_advertising_list"] }).body.data;
  t.equal(narrowed.entries.map(function (entry) { return entry.engine_kind; }), ["dns_advertising_list"],
    "one engine");
  t.equal(narrowed.excluded_engine_kinds, { dns_list_not_recorded: 1, firewall_rule: 2 }, "what was left out");
  var limited = t.widget("unified_blocked_feed", { period: "1h", limit: 1 }).body.data;
  t.equal([limited.entries.length, limited.remainder.members], [1, 3], "one shown, three in the remainder");
});

test("the groups count the whole window", function (t) {
  seed(t);
  var byEngine = t.widget("unified_blocked_feed", { period: "1h", group_by: "engine" }).body.data;
  t.equal(byEngine.groups.map(function (group) { return [group.key, group.count.value]; }),
    [["firewall_rule", 2], ["dns_advertising_list", 1], ["dns_list_not_recorded", 1]], "by engine");
  var byTarget = t.widget("unified_blocked_feed", { period: "1h", group_by: "target" }).body.data;
  t.equal(byTarget.groups[0], { key: "address:203.0.113.7", count: { measure: "refusals", lower_bound: true, value: 2 } },
    "by target");
  var ungrouped = t.widget("unified_blocked_feed", { period: "1h" }).body.data;
  t.assert(!("groups" in ungrouped), "no group without group_by");
});

test("with no source probed and nothing stored, the answer is no data, never an empty feed", function (t) {
  t.clock(AT);
  var answer = t.widget("unified_blocked_feed");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
