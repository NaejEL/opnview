test("the readings are totalled per meter by SQL alone, and the view and the function serve them", function (t) {
  t.deliverSyslog([
    "<14>1 2026-09-27T10:00:00+00:00 example-host example-meter - - - reading meter=kitchen value=40",
    "<14>1 2026-09-27T10:00:01+00:00 example-host example-meter - - - reading meter=kitchen value=60",
    "<14>1 2026-09-27T10:00:02+00:00 example-host example-meter - - - reading meter=garage value=15"
  ]);
  t.equal(t.pushed("readings").length, 3, "the readings stored");
  var run = t.derive("total");
  t.equal(run.error, null, "the run");
  t.equal(run.ran, true, "a run happened");
  var totals = t.query("SELECT meter, readings, total FROM plugin_example_derived_sql.meter_totals ORDER BY meter");
  t.equal(totals, [{ meter: "garage", readings: 1, total: 15 }, { meter: "kitchen", readings: 2, total: 100 }],
    "the totals, through the view");
  var mean = t.query("SELECT plugin_example_derived_sql.mean_reading('kitchen') AS mean");
  t.equal(mean[0].mean, 50, "the function");
});

test("a second run over the same readings changes nothing", function (t) {
  t.deliverSyslog([
    "<14>1 2026-09-27T10:00:00+00:00 example-host example-meter - - - reading meter=kitchen value=40"
  ]);
  t.derive("total");
  var again = t.derive("total");
  t.equal(again.error, null, "the second run");
  t.equal(t.query("SELECT count(*) AS meters FROM plugin_example_derived_sql.totals")[0].meters, 1, "one meter");
});
