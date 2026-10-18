// The instant the tests answer at, so every window and instant in an answer is the same each run.
var AT = 1790000400;

function sample(t, secondsAgo, target, latency) {
  t.sql("INSERT INTO plugin_example_latency.probe (measured_at, target, latency_ms) " +
    "VALUES (:at, :target, :latency)", { at: AT - secondsAgo, target: target, latency: latency });
}

test("each target is ranked by its 75th percentile, slowest first", function (t) {
  t.clock(AT);
  [12, 30, 18, 50].forEach(function (latency, index) { sample(t, 600 + index, "alpha", latency); });
  sample(t, 700, "beta", 75);
  sample(t, 90000, "gamma", 999);
  var answer = t.widget("example_latency_percentile", { percentile: 75, limit: 1 });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.body.data.bars, [{ target: "beta", samples: 1, value: 75 }], "the slowest target");
  t.equal(answer.body.data.remainder, { members: 1, samples: 4 }, "what the limit dropped");
  t.golden("percentile_75.json", answer.raw);
});

test("a value out of range takes its default, and an unknown key is reported, never applied", function (t) {
  t.clock(AT);
  var answer = t.widget("example_latency_percentile", { percentile: 101, colour: "red" });
  t.equal(answer.body.parameters.percentile, 50, "the default");
  t.equal(answer.body.invalid_parameters[0].path, "percentile", "the invalid value");
  t.equal(answer.body.unknown_parameters, { colour: "red" }, "the unknown key");
  t.equal(answer.condition, "reachable_no_rows", "nothing was measured");
  t.equal(answer.body.window.coverage, "not_covered", "the window holds nothing");
});
