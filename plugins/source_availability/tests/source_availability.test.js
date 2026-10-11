// The source_availability widget's own tests, run by `opnview plugin test plugins/source_availability`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// withoutSince is a listed provider as the response's availability carries it: the entry less the
// beginning of its state, which only the widget answers.
function withoutSince(provider) {
  var entry = {};
  Object.keys(provider).forEach(function (key) {
    if (key !== "since") {
      entry[key] = provider[key];
    }
  });
  return entry;
}

test("the providers listed are those of the response's availability, in its order", function (t) {
  t.clock(AT);
  var answer = t.widget("source_availability");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.body.data.providers.map(withoutSince), answer.body.availability,
    "every provider of every kind, as available");
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

test("a state never seen changing names its absence rather than an instant", function (t) {
  t.clock(AT);
  var providers = t.widget("source_availability", { kinds: ["firewall_log"] }).body.data.providers;
  t.equal(providers.map(function (provider) { return provider.since; }),
    [{ after_at: null, by_at: null, state: "state_change_not_observed" }], "no bound, the absence named");
});

test("a state seen changing between two checks carries those two checks as its bounds", function (t) {
  t.clock(AT);
  t.sql("UPDATE source_availability SET state = 'unavailable', outcome = 'transport_failure', probe = 'probe', " +
    "checked_at = :by, state_since_after_at = :after, state_since_by_at = :by " +
    "WHERE provider_id = (SELECT id FROM provider WHERE kind = 'firewall_log' AND provider_key = 'pf')",
    { after: AT - 120, by: AT - 60 });
  var providers = t.widget("source_availability", { kinds: ["firewall_log"] }).body.data.providers;
  t.equal(providers[0].since, { after_at: AT - 120, by_at: AT - 60, state: null }, "after one check, by the next");
});
