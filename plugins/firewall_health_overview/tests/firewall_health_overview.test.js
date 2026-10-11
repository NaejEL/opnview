// The firewall_health_overview widget's own tests, run by
// `opnview plugin test plugins/firewall_health_overview` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges two samples of the firewall's processor use, 0.25 then 0.5 five minutes later, with
// the sampler's provider reachable.
function seed(t) {
  t.clock(AT);
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'measurement_sample')");
  var sample = function (id, at, value) {
    t.sql("INSERT INTO measurement_sample (id, subject_kind, subject_key, measure, unit, value, sampled_at) " +
      "VALUES (:id, 'firewall', '', 'cpu_use_ratio', 'ratio', :value, :at)", { id: id, at: at, value: value });
  };
  sample(1, AT - 600, 0.25);
  sample(2, AT - 300, 0.5);
}

test("a sampled metric is a measured tile; one no subject reported is absent, never a 0", function (t) {
  seed(t);
  var answer = t.widget("firewall_health_overview", { period: "1h", metrics: ["cpu", "swap"] });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var tiles = answer.body.data.tiles;
  t.equal(tiles.length, 2, "one tile per metric");
  var cpu = tiles[0];
  t.equal([cpu.metric, cpu.subject_kind, cpu.measure, cpu.state, cpu.value, cpu.last_sampled_at],
    ["cpu", "firewall", "cpu_use_ratio", "measured", 0.5, AT - 300], "the latest sample");
  t.equal(cpu.series, [{ at: AT - 600, value: 0.25 }, { at: AT - 300, value: 0.5 }], "its history");
  t.assert(!("threshold_state" in cpu), "no threshold set, no threshold state");
  t.equal([tiles[1].metric, tiles[1].state, tiles[1].value, tiles[1].series], ["swap", "absent", null, []],
    "swap was never sampled");
  t.golden("processor_sampled.json", answer.raw);
});

test("thresholds the reader set colour the tile, and show_last_sampled = false leaves the stamp out", function (t) {
  seed(t);
  var warning = t.widget("firewall_health_overview",
    { period: "1h", metrics: ["cpu"], thresholds: { cpu: { warning: 0.4, critical: 0.9 } } }).body.data.tiles[0];
  t.equal(warning.threshold_state, "warning", "0.5 is past the warning threshold");
  var critical = t.widget("firewall_health_overview",
    { period: "1h", metrics: ["cpu"], thresholds: { cpu: { critical: 0.5 } } }).body.data.tiles[0];
  t.equal(critical.threshold_state, "critical", "0.5 reaches the critical threshold");
  var unstamped = t.widget("firewall_health_overview",
    { period: "1h", metrics: ["cpu"], show_last_sampled: false }).body.data.tiles[0];
  t.assert(!("last_sampled_at" in unstamped), "no stamp");
});
