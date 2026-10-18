// The blocked_dns_lookups widget's own tests, run by `opnview plugin test plugins/blocked_dns_lookups`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface, one client on it, a list somebody assigned to advertising and
// one nobody assigned, and the lookups of the last hour: two refusals of one name by the
// advertising list, one by the unassigned list from an address no client carries, one attributed
// to no list, and one answered, which no row counts.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO plugin_installation.client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES (10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', " +
    "'192.0.2.10', :at, :at)", { at: at });
  t.sql("INSERT INTO blocklist (id, name, purpose, purpose_assigned_at, first_seen_at, last_seen_at) VALUES " +
    "(1, 'list-one', 'advertising', :at, :at, :at), (2, 'list-two', NULL, NULL, :at, :at)", { at: at });
  var lookup = function (id, domain, action, list, client, address) {
    t.sql("INSERT INTO dns_resolution (id, lookup_key, client_address, client_id, domain, resolver, " +
      "action, blocklist_id, looked_up_at, ingested_at, interface_id, interface_lookup_state) VALUES " +
      "(:id, :key, :address, :client, :domain, 'unbound', :action, :list, :at, :at, :interface, :state)",
      { id: id, key: "k" + id, address: address, client: client, domain: domain, action: action, list: list,
        at: at + id, interface: client === null ? null : 1, state: client === null ? "pending" : "resolved" });
  };
  lookup(1, "Ads.Example.", "block", 1, 10, "192.0.2.10");
  lookup(2, "ads.example", "block", 1, 10, "192.0.2.10");
  lookup(3, "tracker.example", "drop", 2, null, "192.0.2.99");
  lookup(4, "nolist.example", "block", null, 10, "192.0.2.10");
  lookup(5, "allowed.example", "pass", null, 10, "192.0.2.10");
}

test("refused names are ranked by count, each with its list, its purpose and who asked", function (t) {
  seed(t);
  var answer = t.widget("blocked_dns_lookups", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.rows.map(function (row) { return [row.domain, row.purpose, row.lookups.value]; }),
    [["ads.example", "advertising", 2], ["nolist.example", "not_recorded", 1],
      ["tracker.example", "unassigned", 1]], "the most refused first, ties by name");
  t.equal([data.rows[0].list, data.rows[0].clients, data.rows[0].interfaces], ["list-one", [10], [1]],
    "the list that refused it and who asked");
  t.equal([data.rows[2].clients, data.rows[2].unmatched_client_addresses], [[], ["192.0.2.99"]],
    "an address no client carries");
  t.equal(data.total, { measure: "dns_lookups", lower_bound: true, value: 4 }, "the answered lookup is no refusal");
  t.golden("four_refusals.json", answer.raw);
});

test("min_count, limit and purposes narrow the table and say what they left out", function (t) {
  seed(t);
  var frequent = t.widget("blocked_dns_lookups", { period: "1h", min_count: 2 }).body.data;
  t.equal([frequent.rows.length, frequent.below_min_count.members, frequent.below_min_count.count.value],
    [1, 2, 2], "the names refused once are below the minimum");
  var limited = t.widget("blocked_dns_lookups", { period: "1h", limit: 1 }).body.data;
  t.equal([limited.remainder.members, limited.remainder.count.value], [2, 2], "the rest");
  var advertising = t.widget("blocked_dns_lookups", { period: "1h", purposes: ["advertising"] }).body.data;
  t.equal(advertising.rows.map(function (row) { return row.domain; }), ["ads.example"], "one purpose");
});

test("with no source probed and nothing stored, the answer is no data, never a zero", function (t) {
  t.clock(AT);
  var answer = t.widget("blocked_dns_lookups");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
