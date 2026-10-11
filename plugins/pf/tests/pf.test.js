// The pf plugin's own tests: its probe and its reading of its own recorded answer, through the
// plugin loader and the same adapter the collector runs it through. What the kind does with the
// records is tested in opnview's core.

test("the pf implementation declares itself as the firewall_log kind", function (t) {
  t.assert(t.registered("firewall_log"), "the pf implementation is not registered in the firewall_log kind");
});

test("an unanswered log is unavailable and never separable", function (t) {
  var probe = t.call("firewall_log", "probe");
  if (probe.error !== null) {
    t.fatal("the probe returned " + probe.error + "; an absent endpoint is a state and not an error");
  }
  t.assert(probe.result.state === "unavailable" && !probe.result.separable,
    "an unanswered log read as state " + probe.result.state + ", separable " + probe.result.separable);
  t.equal(probe.result.outcome, "not_found", "the outcome");
});

test("the fixture log is read into records of the closed vocabulary", function (t) {
  t.answer("filter_log", { fixture: "firewall_log.json" });
  var probe = t.call("firewall_log", "probe");
  if (probe.error !== null || !probe.result.separable || probe.result.state !== "reachable") {
    t.fatal("probing the fixture log gave " + JSON.stringify(probe));
  }
  var read = t.call("firewall_log", "records", 100);
  if (read.error !== null) {
    t.fatal("reading the fixture log: " + read.error);
  }
  if (!read.records || read.records.length === 0 || !read.result.separable) {
    t.fatal("read " + (read.records || []).length + " records, separable " + read.result.separable);
  }
  read.records.forEach(function (record) {
    t.assert(record.digest !== "", "a record carries no digest, which is the identity the de-duplication rests on");
    t.assert(["pass", "block", "reject", "unknown"].indexOf(record.action) >= 0,
      "the action " + record.action + " is outside the closed vocabulary");
    t.assert(["in", "out", "unknown"].indexOf(record.direction) >= 0,
      "the direction " + record.direction + " is outside the closed vocabulary");
  });
});

test("an action or direction outside the vocabulary is unknown, never folded", function (t) {
  var actions = { "PASS": "pass", "block": "block", "nat": "unknown", "": "unknown" };
  Object.keys(actions).forEach(function (raw) {
    t.equal(t.helper("normalise_action", raw), actions[raw], "action " + JSON.stringify(raw));
  });
  var directions = { "IN": "in", "out": "out", "any": "unknown" };
  Object.keys(directions).forEach(function (raw) {
    t.equal(t.helper("normalise_direction", raw), directions[raw], "direction " + JSON.stringify(raw));
  });
});
