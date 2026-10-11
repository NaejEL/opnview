// The insight plugin's own tests.

var deviceNameRefusal = "collect: refusing to send a network device name to an endpoint that takes " +
  "interface names";

// The sampler refuses rather than samples when an argument is a device name. It is driven by a
// discovery snapshot in which the identifiers were, wrongly, filled with device names -- what a
// decoder reading the wrong key, or a refactor, would produce. The call is refused, because the
// alternative is an empty answer that reads as a quiet network.
test("the sampler refuses rather than samples when an argument is a device name", function (t) {
  var wrong = t.discovery({ interfaces: { example_if_a: "exdev0" }, identifiers: ["exdev0"] });
  var refused = t.helper("guard_interface_names", wrong);
  if (!refused.error || refused.error.indexOf(deviceNameRefusal) !== 0) {
    t.fatal("the guard returned " + JSON.stringify(refused) + ", not the refusal");
  }
  var right = t.discovery({ interfaces: { example_if_a: "exdev0" }, identifiers: ["example_if_a"] });
  var passed = t.helper("guard_interface_names", right);
  if (passed.error) {
    t.fatal("the guard refused a correct snapshot: " + passed.error);
  }
  t.equal(passed.names, ["example_if_a"], "the guard's names");
});

test("the Insight implementation declares itself as a measurement source", function (t) {
  t.assert(t.registered("measurement_sample"),
    "the insight implementation is not registered in the measurement_sample kind");
});

test("local collection is reachable and separable", function (t) {
  t.answer("netflow_is_enabled", { fixture: "netflow_is_enabled_local.json" });
  var probe = t.call("measurement_sample", "probe");
  t.assert(probe.error === null && probe.result.state === "reachable" && probe.result.separable,
    "local collection gave " + JSON.stringify(probe));
});

test("export-only collection is present but disabled", function (t) {
  t.answer("netflow_is_enabled", { fixture: "netflow_is_enabled_export_only.json" });
  var probe = t.call("measurement_sample", "probe");
  t.assert(probe.error === null && probe.result.state === "present_but_disabled" &&
    probe.result.outcome === "present_but_disabled", "export-only collection gave " + JSON.stringify(probe));
});

test("nothing is sampled before any interface is discovered, and no call is made", function (t) {
  var sample = t.call("measurement_sample", "sample", t.discovery({}), t.now());
  if (sample.error !== null) {
    t.fatal("sampling an undiscovered firewall: " + sample.error);
  }
  t.equal(t.requests("traffic_top").length, 0, "calls to the per-pair endpoint with no interface name to give it");
  t.equal(sample.result.outcome, "healthy_empty", "the outcome");
});

test("dpinger figures are read, and the tilde is no figure", function (t) {
  ["1.2 ms", "0.0 ms"].forEach(function (text) {
    t.assert(t.helper("parse_milliseconds", text) !== null, JSON.stringify(text) + " was not read");
  });
  ["~", "", "1.2", "-3 ms"].forEach(function (text) {
    t.assert(t.helper("parse_milliseconds", text) === null, JSON.stringify(text) + " was read as a round-trip time");
  });
  t.equal(t.helper("parse_loss_ratio", "12.5 %"), 0.125, "12.5 %");
  t.assert(t.helper("parse_loss_ratio", "~") === null, "~ was read as a loss");
});
