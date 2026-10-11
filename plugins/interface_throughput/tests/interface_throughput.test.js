// The interface_throughput widget's own tests, run by
// `opnview plugin test plugins/interface_throughput` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the sampler's provider reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'measurement_sample')");
}

// seed arranges one interface and three samples of its inbound byte counter: 1000 bytes, then 4000
// five minutes later (10 bytes a second), then 500, a reset.
function seed(t) {
  t.clock(AT);
  reachable(t);
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: AT - 3000 });
  var sample = function (id, at, value) {
    t.sql("INSERT INTO measurement_sample (id, subject_kind, subject_key, measure, unit, value, sampled_at) " +
      "VALUES (:id, 'interface', 'vlan0.10', 'bytes_in', 'bytes', :value, :at)", { id: id, at: at, value: value });
  };
  sample(1, AT - 600, 1000);
  sample(2, AT - 300, 4000);
  sample(3, AT - 100, 500);
}

test("each direction of each interface is a series of rates from its own counters", function (t) {
  seed(t);
  var answer = t.widget("interface_throughput", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var series = answer.body.data.series;
  t.equal(series.map(function (line) { return line.key; }), ["interface:1:in", "interface:1:out"], "the series");
  var inbound = series[0];
  t.equal([inbound.measure, inbound.unit, inbound.lower_bound], ["interface_counter", "bits_per_second", true],
    "a counter measure, never logged");
  t.equal(inbound.points, [{ at: AT - 300, value: 80 }], "10 bytes a second is 80 bits a second");
  t.equal(inbound.resets, 1, "the counter that went down is a reset, not a point");
  t.equal(inbound.current, { at: AT - 300, value: 80 }, "the latest point brought forward");
  t.equal([series[1].points, series[1].current], [[], null], "no outbound sample, no point");
  t.golden("one_interface.json", answer.raw);
});

test("measure = bytes answers the increase, and direction selects the series", function (t) {
  seed(t);
  var series = t.widget("interface_throughput", { period: "1h", measure: "bytes", direction: "in" }).body.data.series;
  t.equal(series.length, 1, "the inbound series alone");
  t.equal(series[0].points, [{ at: AT - 300, value: 3000 }], "3000 bytes between the two samples");
});

test("an installation with no sample answers no point", function (t) {
  t.clock(AT);
  reachable(t);
  var answer = t.widget("interface_throughput", { period: "1h" });
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  t.equal(answer.body.data.series, [], "no interface, no series");
});
