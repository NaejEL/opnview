// The owner_activity widget's own tests, run by `opnview plugin test plugins/owner_activity` against
// a database of their own. The byte identity of every recorded answer of the widget is the Go
// suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// seed arranges one inside interface, one person, and three clients on the interface: client 10 is
// the person's and opens two outbound flows (3000 bytes passed, 200 blocked) in the last hour;
// client 13 is the person's too and sends nothing; client 11 is nobody's and opens one flow of 700
// bytes.
function seed(t) {
  t.clock(AT);
  reachable(t);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO owner (id, display_name, created_at, updated_at) VALUES (1, 'Example Person', :at, :at)",
    { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, owner_id, " +
    "owner_assigned_at, first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', 1, :at, :at, :at), " +
    "(11, 'mac', '00:11:22:33:44:66', 1, '00:11:22:33:44:66', '192.0.2.11', NULL, NULL, :at, :at), " +
    "(13, 'mac', '00:11:22:33:44:88', 1, '00:11:22:33:44:88', '192.0.2.13', 1, :at, :at, :at)", { at: at });
  var flow = function (id, client, address, action, bytes) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, :client, :src, '203.0.113.7', 'tcp', 4, " +
      ":action, 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, client: client, src: address, action: action, bytes: bytes });
  };
  flow(1, 10, "192.0.2.10", "pass", 3000);
  flow(2, 10, "192.0.2.10", "block", 200);
  flow(3, 11, "192.0.2.11", "pass", 700);
}

test("a person's card sums their machines, and the Unassigned card holds the rest", function (t) {
  seed(t);
  var answer = t.widget("owner_activity", { show_timeline: false });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.cards.length, 1, "one person");
  var card = data.cards[0];
  t.equal([card.key, card.owner.display_name, card.value, card.machines], ["owner:1", "Example Person", 3200, 1],
    "the person's card");
  t.equal(card.chips.map(function (chip) { return [chip.client.id, chip.state]; }),
    [[10, "measured"], [13, "no_data_in_period"]], "the measured machine first, the silent one named");
  t.equal(card.chips[1].volume, null, "a machine with no data has no volume, not a zero");
  t.equal(card.chips[0].current_rate.state, "no_current_sample", "no sample is not a rate of 0");
  t.equal(data.unassigned.chips.map(function (chip) { return chip.client.id; }), [11], "the unassigned machine");
  t.equal(data.total.bytes, 3900, "the cards and the Unassigned card make the total");
  t.assert(!("timeline" in card), "no timeline asked for");
  t.golden("one_person.json", answer.raw);
});

test("the timeline holds the window's buckets, and clients_per_card drops the rest", function (t) {
  seed(t);
  var data = t.widget("owner_activity", { period: "1h", clients_per_card: 1 }).body.data;
  var card = data.cards[0];
  t.equal(card.timeline.length, 60, "one bucket a minute");
  var bytes = 0;
  card.timeline.forEach(function (bucket) {
    if (bucket.volume !== undefined) {
      bytes += bucket.volume.bytes;
    }
  });
  t.equal(bytes, 3200, "the person's bytes, in their buckets");
  t.equal([card.chips.length, card.chip_remainder.members], [1, 1], "the silent machine dropped");
});

test("include_unassigned = false hides the Unassigned card and says so", function (t) {
  seed(t);
  var data = t.widget("owner_activity", { include_unassigned: false, show_timeline: false }).body.data;
  t.equal([data.unassigned, data.totals_exclude_unassigned, data.total.bytes], [null, true, 3200],
    "the totals exclude it");
});

test("an installation where nobody created a person answers no_owner_exists", function (t) {
  t.clock(AT);
  var answer = t.widget("owner_activity");
  t.equal(answer.condition, "no_owner_exists", "the condition");
});
