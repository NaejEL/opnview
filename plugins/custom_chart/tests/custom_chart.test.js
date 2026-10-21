// The custom_chart widget's own tests, run by `opnview plugin test plugins/custom_chart` against a
// database of their own. The byte identity of every recorded answer of the widget is the Go suite's
// (internal/widget/widgets, golden_test.go); these say what the widget does, in the widget's own
// words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges two samples of the firewall's processor use in one minute, 0.25 and 0.75, with the
// sampler's provider reachable.
function seed(t) {
  t.clock(AT);
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'measurement_sample')");
  var sample = function (id, at, value) {
    t.sql("INSERT INTO plugin_insight.measurement_sample (subject_kind, subject_key, measure, unit, value, sampled_at) " +
      "VALUES ('firewall', '', 'cpu_use_ratio', 'ratio', :value, :at)", { at: at, value: value });
  };
  sample(1, AT - 590, 0.25);
  sample(2, AT - 580, 0.75);
}

test("a chart with no series draws nothing, and says so", function (t) {
  t.clock(AT);
  var answer = t.widget("custom_chart", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  t.equal([answer.body.data.series, answer.body.data.annotations], [[], []], "no series, no annotation");
  t.equal(answer.body.availability, [], "no source is bound");
});

test("a firewall metric series averages each bucket's samples and binds its own provider", function (t) {
  seed(t);
  var answer = t.widget("custom_chart", { period: "1h", series: [{ source: "firewall_metric", metric: "cpu" }] });
  t.equal(answer.condition, "reachable", "the condition");
  var line = answer.body.data.series[0];
  t.equal([line.key, line.condition, line.measure, line.unit, line.aggregation],
    ["series:0", "reachable", "sampled_gauge", "ratio", "mean"], "the series");
  var measured = line.buckets.filter(function (bucket) { return !bucket.gap; });
  t.equal(measured.length, 1, "one bucket holds the two samples");
  t.equal(measured[0].value, 0.5, "their mean");
  t.equal(line.total, null, "a gauge has no total");
  t.equal(answer.body.availability.map(function (provider) { return provider.kind; }), ["measurement_sample"],
    "the response binds the series' provider");
  t.golden("processor_series.json", answer.raw);
});

test("a series whose reference resolves to nothing fails alone, with no bucket", function (t) {
  seed(t);
  var answer = t.widget("custom_chart", { period: "1h", series: [
    { source: "traffic", client: { kind: "client", by: "mac", value: "00:00:5e:00:53:99" } },
    { source: "firewall_metric", metric: "cpu" }
  ] });
  var series = answer.body.data.series;
  t.equal([series[0].condition, series[0].buckets], ["unresolved_reference", null], "the failed series");
  t.equal(series[0].unresolved_references.length, 1, "it carries its own unresolved reference");
  t.equal(series[1].condition, "reachable", "the other series is drawn");
});
