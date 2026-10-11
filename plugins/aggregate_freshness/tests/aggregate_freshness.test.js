// The aggregate_freshness widget's own tests, run by `opnview plugin test plugins/aggregate_freshness`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC, inside an hour, a day, a week and
// a month alike.
var AT = 1790000400;

// hourStart is the start of the 1 h slot holding an instant.
function hourStart(instant) {
  return instant - (instant % 3600);
}

test("an installation with no aggregate holds no slot, and every period says it is stale", function (t) {
  t.clock(AT);
  var answer = t.widget("aggregate_freshness");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  var periods = answer.body.data.periods;
  t.equal(periods.map(function (row) { return row.period; }), ["1h", "24h", "7d", "30d"], "the periods");
  periods.forEach(function (row) {
    t.equal(row.slots, 0, row.period + " slots");
    t.equal(row.earliest_period_start_at, null, row.period + " has no earliest slot, not a zero");
    t.equal(row.stale, true, row.period + " is stale with no slot");
    t.equal(row.bytes, { measure: "logged", lower_bound: true, value: 0 }, row.period + " bytes");
  });
});

test("a computed hour is counted with its bytes, and the answer is the recorded one", function (t) {
  t.clock(AT);
  var start = hourStart(AT) - 3600;
  t.sql("INSERT INTO volume_aggregate_1h (id, period_start_at, period_end_at, traffic_scope, " +
    "traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, " +
    "blocked_connections, unknown_connections, computed_at) VALUES (1, :start, :end, 'this_firewall', " +
    "'to_this_firewall', 1500, 1500, 0, 0, 3, 0, 0, :computed)",
    { start: start, end: start + 3600, computed: start + 3660 });
  var answer = t.widget("aggregate_freshness", { periods: ["1h"] });
  t.equal(answer.condition, "reachable", "the condition");
  var hour = answer.body.data.periods[0];
  t.equal([hour.slots, hour.stale_slots, hour.stale], [1, 0, false], "one fresh slot");
  t.equal(hour.bytes.value, 1500, "the bytes it holds");
  t.golden("one_computed_hour.json", answer.raw);
});

test("show_bytes = false leaves the bytes out, and an unknown key is reported, not applied", function (t) {
  t.clock(AT);
  var answer = t.widget("aggregate_freshness", { show_bytes: false, colour: "red" });
  t.assert(answer.body.data.periods.every(function (row) { return !("bytes" in row); }), "no row carries bytes");
  t.equal(answer.body.unknown_parameters, { colour: "red" }, "the unknown key");
});
