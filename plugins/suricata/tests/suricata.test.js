// The suricata plugin's own tests.

test("the Suricata implementation declares itself as the security_event kind", function (t) {
  t.assert(t.registered("security_event"), "the suricata implementation is not registered in the security_event kind");
});

test("an absent engine is unavailable and not separable", function (t) {
  var probe = t.call("security_event", "probe");
  t.assert(probe.error === null && probe.result.state === "unavailable" && !probe.result.separable,
    "an absent engine gave " + JSON.stringify(probe));
});

test("a stopped engine is present but disabled, and separable", function (t) {
  t.answer("status", { fixture: "ids_status_stopped.json" });
  var probe = t.call("security_event", "probe");
  t.assert(probe.error === null && probe.result.state === "present_but_disabled" &&
    probe.result.outcome === "service_not_running" && probe.result.separable,
    "a stopped engine gave " + JSON.stringify(probe));
});

test("a running engine is reachable and separable", function (t) {
  t.answer("status", { fixture: "ids_status_running.json" });
  var probe = t.call("security_event", "probe");
  t.assert(probe.error === null && probe.result.state === "reachable" && probe.result.separable,
    "a running engine gave " + JSON.stringify(probe));
});

test("an event action outside the vocabulary is unknown", function (t) {
  var actions = { "blocked": "blocked", "DROP": "blocked", "alert": "allowed", "x": "unknown" };
  Object.keys(actions).forEach(function (raw) {
    t.equal(t.helper("normalise_event_action", raw), actions[raw], "action " + JSON.stringify(raw));
  });
});

test("the recorded alerts are read with their file and offset", function (t) {
  t.answer("query_alerts", { fixture: "query_alerts.json" });
  var read = t.call("security_event", "alerts", 100);
  if (read.error !== null || !read.alerts || read.alerts.length === 0) {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  read.alerts.forEach(function (alert) {
    t.assert(alert.file_id !== "" && alert.byte_offset >= 0, "an alert carries no coordinate");
    t.assert(alert.normalised_severity === undefined, "an alert carries a severity the feed never gives");
  });
});

// rule_info (S6-6 of specs/SPEC-step-6-alerts.md): the installed-rules grid, one sid per request.

test("a rule found carries its classtype verbatim as the category, its ruleset as the source, and no severity", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  var answer = t.call("security_event", "rule_info", ["9000001"]);
  t.equal(answer.error, undefined, "no error");
  t.equal(answer.rules, [{ rule_identity: "9000001", lookup_state: "found", category: "example-classtype",
    rule_source: "example-ruleset.rules" }], "the answer");
  t.equal([answer.result.state, answer.result.outcome], ["reachable", "healthy"], "the read's result");
  var requests = t.requests("installed_rules");
  t.equal(requests.length, 1, "one request for one sid");
});

test("a metadata tag named like a severity is no severity: the grid serves none, so none is answered", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  var rule = t.call("security_event", "rule_info", ["9000001"]).rules[0];
  t.equal([rule.normalised_severity, rule.provider_severity], [undefined, undefined],
    "neither a normalised nor a raw severity");
});

test("a rule that declares no classtype is found with no category", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_no_classtype.json" });
  t.equal(t.call("security_event", "rule_info", ["9000002"]).rules,
    [{ rule_identity: "9000002", lookup_state: "found", rule_source: "example-ruleset.rules" }],
    "found, with the marker of no classtype left out");
});

test("a sid the grid does not hold is not found", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_none.json" });
  t.equal(t.call("security_event", "rule_info", ["9000003"]).rules,
    [{ rule_identity: "9000003", lookup_state: "not_found" }], "not found");
});

test("a row whose sid is not the one asked is no answer about it", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  t.equal(t.call("security_event", "rule_info", ["9000004"]).rules,
    [{ rule_identity: "9000004", lookup_state: "not_found" }], "not found");
});

test("an identity that is no sid is not found, and is not asked", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  t.equal(t.call("security_event", "rule_info", ["named-rule"]).rules,
    [{ rule_identity: "named-rule", lookup_state: "not_found" }], "not found");
  t.equal(t.requests("installed_rules").length, 0, "no request");
});

test("a 404 or a 403 answers nothing and says why, so nothing is cached", function (t) {
  [404, 403].forEach(function (status) {
    t.answer("installed_rules", { status: status, json: {} });
    var answer = t.call("security_event", "rule_info", ["9000001", "9000002"]);
    t.assert(typeof answer.error === "string" && answer.error !== "", status + ": no error was returned");
    t.equal(answer.rules, [], status + ": no rule answered");
    t.equal(answer.result.state, "unavailable", status + ": the read's state");
  });
});

test("an empty list instead of a grid is a failure to ask, never a rule found missing", function (t) {
  t.answer("installed_rules", { fixture: "installed_rules_backend_failed.json" });
  var answer = t.call("security_event", "rule_info", ["9000001"]);
  t.equal([answer.rules, answer.result.outcome], [[], "undecodable"], "nothing answered");
  t.assert(typeof answer.error === "string", "the failure is said");
});
