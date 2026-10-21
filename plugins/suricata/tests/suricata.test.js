// The suricata plugin's own tests: its probe, its pass into its own table with the watermark as
// its state and a lost rotation as a gap, and its rule-info derivation against the installed-rules
// grid.
var NOW = 1790000400;

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

// The pass: the recorded alerts become rows of the plugin's table, keyed by file and offset, and the
// watermark per file is the state handed to the next pass, which reads past it.

test("the recorded alerts are read into the plugin's table with their file and offset, and the watermarks advance",
  function (t) {
    t.clock(NOW);
    t.answer("alert_logs", { fixture: "get_alert_logs.json" });
    t.answer("query_alerts", { fixture: "query_alerts.json" });
    var read = t.call("security_event", "collect", {});
    var rows = read.rows && read.rows.security_event;
    if (read.error !== null || !rows || rows.length === 0) {
      t.fatal("reading the fixture gave " + JSON.stringify(read));
    }
    rows.forEach(function (event) {
      t.assert(/^[0-9]+:[0-9]+$/.test(event.provider_event_key), "an event carries no coordinate: " + event.provider_event_key);
      t.equal(event.normalised_severity, null, "an event carries a severity the feed never gives");
      t.equal(typeof event.provider_id, "number", "the provider's registry id");
      t.equal(event.ingested_at, NOW, "the pass's instant");
    });
    var first = rows[0];
    t.equal([first.provider_event_key, first.rule_identity, first.event_action, first.src_address, first.dst_port, first.protocol],
      ["3:8192", "2000001", "blocked", "198.51.100.10", 443, "TCP"], "the first record's fields");
    t.equal(read.state["file.3"], "8192:" + NOW + ":current", "the current file's watermark, at its highest offset");
    t.equal(read.state["file.2"], "2048:" + NOW + ":rotated", "a rotated file's watermark");
    t.equal(read.gaps, [], "no gap");

    // The next pass, handed that state, reads nothing new.
    var again = t.call("security_event", "collect", read.state);
    t.equal((again.rows && again.rows.security_event) || [], [], "nothing past the watermarks");
    t.equal(again.state["file.3"], read.state["file.3"], "the watermark is kept");
  });

test("a watermarked file the rotation list no longer carries is lost: a gap, once", function (t) {
  t.clock(NOW);
  t.answer("alert_logs", { fixture: "get_alert_logs.json" });
  t.answer("query_alerts", { fixture: "query_alerts.json" });
  var read = t.call("security_event", "collect", { "file.9": "100:1700000000:rotated" });
  t.equal(read.gaps.length, 1, "one gap");
  t.equal([read.gaps[0].from, read.gaps[0].to, read.gaps[0].reason], [1700000000, NOW, "eve_rotation_lost"], "the gap");
  t.assert(read.gaps[0].detail.indexOf("event file 9") >= 0, "the detail names the file");
  t.equal(read.state["file.9"], "100:" + NOW + ":lost", "the file is marked lost");
  var again = t.call("security_event", "collect", read.state);
  t.equal(again.gaps, [], "a lost file is reported once");
});

test("when the rotation list does not answer, nothing is declared lost", function (t) {
  t.clock(NOW);
  t.answer("alert_logs", { status: 503, json: {} });
  t.answer("query_alerts", { fixture: "query_alerts.json" });
  var read = t.call("security_event", "collect", { "file.9": "100:1700000000:rotated" });
  t.equal(read.error, null, "the alerts are still read");
  t.equal(read.gaps, [], "no gap without the list");
  t.equal(read.state["file.9"], "100:1700000000:rotated", "the watermark is untouched");
});

// The rule-info derivation: the installed-rules grid, one sid per request, for the rules that fired.

function providerID(t) {
  return t.query("SELECT id FROM provider WHERE kind = 'security_event' AND provider_key = 'suricata'")[0].id;
}

function event(t, identity, key) {
  t.sql("INSERT INTO plugin_suricata.security_event (provider_id, provider_event_key, occurred_at, ingested_at, " +
    "rule_identity, signature, event_action, src_address, dst_address) VALUES (:provider, :key, :at, :at, :identity, " +
    "'example', 'blocked', '198.51.100.10', '203.0.113.30')", { provider: providerID(t), key: key, at: NOW - 60, identity: identity });
}

function ruleInfo(t) {
  return t.query("SELECT rule_identity, lookup_state, category, rule_source, normalised_severity, provider_severity, fetched_at " +
    "FROM plugin_suricata.provider_rule_info ORDER BY rule_identity");
}

test("a rule found carries its classtype verbatim as the category, its ruleset as the source, and no severity", function (t) {
  t.clock(NOW);
  event(t, "9000001", "1:10");
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  var run = t.derive("rule_info");
  t.equal(run.error, null, "the run");
  t.equal(ruleInfo(t), [{ rule_identity: "9000001", lookup_state: "found", category: "example-classtype",
    rule_source: "example-ruleset.rules", normalised_severity: null, provider_severity: null, fetched_at: NOW }], "the answer");
  t.equal(t.requests("installed_rules").length, 1, "one request for one sid");
  // Answered within the recheck window: not asked again.
  t.derive("rule_info");
  t.equal(t.requests("installed_rules").length, 1, "no second request");
});

test("a rule that declares no classtype is found with no category", function (t) {
  t.clock(NOW);
  event(t, "9000002", "1:10");
  t.answer("installed_rules", { fixture: "installed_rules_no_classtype.json" });
  t.derive("rule_info");
  t.equal(ruleInfo(t).map(function (r) { return [r.lookup_state, r.category, r.rule_source]; }),
    [["found", null, "example-ruleset.rules"]], "found, with no category");
});

test("a sid the grid does not hold, or a row about another sid, is not found", function (t) {
  t.clock(NOW);
  event(t, "9000003", "1:10");
  event(t, "9000004", "1:20");
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  t.derive("rule_info");
  t.equal(ruleInfo(t).map(function (r) { return [r.rule_identity, r.lookup_state, r.category]; }),
    [["9000003", "not_found", null], ["9000004", "not_found", null]], "not found");
});

test("an identity that is no sid is not found, and is not asked", function (t) {
  t.clock(NOW);
  event(t, "named-rule", "1:10");
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  t.derive("rule_info");
  t.equal(ruleInfo(t).map(function (r) { return [r.rule_identity, r.lookup_state]; }), [["named-rule", "not_found"]], "not found");
  t.equal(t.requests("installed_rules").length, 0, "no request");
});

test("a 404 or a 403 answers nothing, so nothing is cached and the rule is asked again", function (t) {
  t.clock(NOW);
  event(t, "9000001", "1:10");
  [404, 403].forEach(function (status) {
    t.answer("installed_rules", { status: status, json: {} });
    var run = t.derive("rule_info");
    t.equal(run.error, null, status + ": the run itself is not a failure");
    t.equal(ruleInfo(t), [], status + ": no rule cached");
  });
  t.equal(t.requests("installed_rules").length, 2, "asked at each run");
});

test("an empty list instead of a grid is a failure to ask, never a rule found missing", function (t) {
  t.clock(NOW);
  event(t, "9000001", "1:10");
  t.answer("installed_rules", { fixture: "installed_rules_backend_failed.json" });
  t.derive("rule_info");
  t.equal(ruleInfo(t), [], "nothing cached");
});

test("the per-run bound caps how many rules are asked, in identity order; the rest wait for the next run", function (t) {
  t.clock(NOW);
  t.setting("rule_info_per_pass", 1);
  event(t, "9000002", "1:10");
  event(t, "9000001", "1:20");
  t.answer("installed_rules", { fixture: "installed_rules_found.json" });
  t.derive("rule_info");
  t.equal(ruleInfo(t).map(function (r) { return r.rule_identity; }), ["9000001"], "the first identity");
  t.derive("rule_info");
  t.equal(ruleInfo(t).map(function (r) { return r.rule_identity; }), ["9000001", "9000002"], "then the second");
});
