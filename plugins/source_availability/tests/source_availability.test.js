// The source_availability widget's own tests, run by `opnview plugin test plugins/source_availability`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

test("the providers listed are those of the response's availability, in its order", function (t) {
  t.clock(AT);
  var answer = t.widget("source_availability");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.body.data.providers, answer.body.availability, "every provider of every kind, as available");
});

test("kinds narrows the list to the kinds selected", function (t) {
  t.clock(AT);
  var answer = t.widget("source_availability", { kinds: ["firewall_log"] });
  t.assert(answer.body.data.providers.every(function (provider) { return provider.kind === "firewall_log"; }),
    "only the filter log's providers");
  t.golden("firewall_log.json", answer.raw);
});

test("show_inactive = false leaves out every provider that is not active", function (t) {
  t.clock(AT);
  var answer = t.widget("source_availability", { show_inactive: false });
  t.assert(answer.body.data.providers.every(function (provider) { return provider.active; }), "only the active");
  t.equal(answer.body.data.providers.length,
    answer.body.availability.filter(function (provider) { return provider.active; }).length, "every active one");
});
