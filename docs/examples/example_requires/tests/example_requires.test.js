// The plugin requires example_derived_sql: the test world loads it first, from the sibling directory,
// exactly as the service loads a required plugin first, so its schema and its view exist here.
test("the widget reads the required plugin's view", function (t) {
  t.clock(1790000400);
  t.sql("INSERT INTO plugin_example_derived_sql.totals (meter, readings, total) VALUES " +
    "('kitchen', 2, 100), ('garage', 1, 15), ('attic', 4, 8)");
  var answer = t.widget("example_meter_totals", { limit: 2 });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.body.data.rows, [{ meter: "kitchen", readings: 2, total: 100 }, { meter: "garage", readings: 1, total: 15 }],
    "the two largest totals");
});
