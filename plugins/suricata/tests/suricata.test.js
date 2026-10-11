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
