// The public_address widget's own tests, run by `opnview plugin test plugins/public_address` against
// a database of their own. The byte identity of every recorded answer of the widget is the Go
// suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one upstream interface -- one carrying a gateway -- which held a private IPv4
// address and then a shared one (RFC 6598), an IPv6 address in no special-purpose block, and a
// gateway.
function seed(t) {
  t.clock(AT);
  var day = 86400;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, is_upstream, " +
    "first_seen_at, last_seen_at) VALUES (1, 'wan', 'igc0', 'Uplink', 'ethernet', 'physical', true, :at, :at)",
    { at: AT - 10 * day });
  var held = function (id, field, address, prefix, family, first, last) {
    t.sql("INSERT INTO interface_address (id, interface_id, source_field, address, prefix_length, " +
      "address_family, first_seen_at, last_seen_at) VALUES (:id, 1, :field, :address, :prefix, :family, " +
      ":first, :last)", { id: id, field: field, address: address, prefix: prefix, family: family,
      first: first, last: last });
  };
  held(1, "addr4", "192.168.1.2", 24, 4, AT - 10 * day, AT - 5 * day);
  held(2, "addr4", "100.64.0.5", 10, 4, AT - 5 * day, AT);
  held(3, "addr6", "2a00:1450::5", 64, 6, AT - 10 * day, AT);
  held(4, "gateways", "100.64.0.1", null, 4, AT - 10 * day, AT);
}

test("an upstream interface behind another NAT says so, and no special-purpose address is public",
  function (t) {
    seed(t);
    var answer = t.widget("public_address");
    t.equal(answer.status, 200, "the status");
    var upstream = answer.body.data.upstreams[0];
    t.equal(upstream["interface"].identifier, "wan", "the upstream interface");
    t.equal(upstream.current.map(function (address) { return [address.address, address.public, address.change_state]; }),
      [["100.64.0.5", false, "changed"], ["2a00:1450::5", true, "not_recorded_only_one_address_seen"]],
      "the addresses it holds");
    t.equal(upstream.current[0].special_purpose.reference, "RFC 6598", "the registry block, cited");
    t.equal(upstream.behind_upstream_nat, true, "behind another NAT");
    t.equal(upstream.history.map(function (address) { return address.address; }), ["192.168.1.2"], "what it held");
    t.equal(upstream.gateways.map(function (address) { return address.address; }), ["100.64.0.1"], "its gateway");
    t.equal(answer.body.data.registries, ["iana-ipv4-special-registry", "iana-ipv6-special-registry"],
      "the registries cited");
    t.golden("behind_upstream_nat.json", answer.raw);
  });

test("families, gateways and show_history narrow what is listed", function (t) {
  seed(t);
  var ipv6 = t.widget("public_address", { families: ["ipv6"] }).body.data.upstreams[0];
  t.equal([ipv6.current.length, ipv6.behind_upstream_nat], [1, null], "no IPv4 address, nothing said about NAT");
  var other = t.widget("public_address", { gateways: ["203.0.113.1"] }).body.data;
  t.equal(other.upstreams, [], "no upstream behind another gateway");
  var current = t.widget("public_address", { show_history: false }).body.data.upstreams[0];
  t.assert(!("history" in current) && !("history_remainder" in current), "no history asked, none given");
});

test("an installation with no upstream interface lists none", function (t) {
  t.clock(AT);
  var answer = t.widget("public_address");
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  t.equal(answer.body.data.upstreams, [], "no upstream");
});
