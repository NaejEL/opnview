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

// The live pass (docs/plugins.md, "Live pass"): what the host runs at live_cadence_seconds while
// somebody looks at a live figure. It reads the per-pair snapshot and the interface counters and
// nothing else -- no gauge, no processor stream -- and stamps every reading with the instant its
// answer was fetched.
function liveWorld(t) {
  t.answer("traffic_top", { fixture: "traffic_top.json" });
  t.answer("traffic_interface", { fixture: "traffic_interface.json" });
  return t.discovery({ interfaces: { example_if_a: "exdev0", example_if_b: "exdev1" },
    identifiers: ["example_if_a", "example_if_b"] });
}

test("the live pass reads only the live endpoints", function (t) {
  var live = t.call("measurement_sample", "live", liveWorld(t), t.now());
  t.equal(live.error, null, "the live pass's error");
  t.equal(t.requests("traffic_top").length, 1, "calls to the per-pair endpoint");
  t.equal(t.requests("traffic_interface").length, 1, "calls to the interface counters");
  ["system_resources", "system_temperature", "system_time", "system_disk", "gateway_status", "system_swap",
    "netflow_is_enabled"].forEach(function (endpoint) {
    t.equal(t.requests(endpoint).length, 0, "calls to " + endpoint);
  });
  var subjects = {};
  live.readings.forEach(function (reading) {
    subjects[reading.subject_kind] = true;
  });
  t.equal(Object.keys(subjects).sort(), ["interface", "interface_endpoint", "interface_endpoint_pair"],
    "the subjects a live pass reads");
});

test("every live reading carries the instant its answer was fetched", function (t) {
  t.clock(1790000400);
  var live = t.call("measurement_sample", "live", liveWorld(t), t.now());
  t.assert(live.readings.length > 0, "the live pass read nothing");
  live.readings.forEach(function (reading) {
    t.assert(reading.sampled_at >= 1790000400 && reading.sampled_at <= 1790000405, "the instant of " +
      reading.subject_kind + " " + reading.measure + " is " + reading.sampled_at + ", not the answer's");
  });
});

test("a live pass and a baseline pass read the same capture at the same instant", function (t) {
  t.clock(1790000400);
  var discovery = liveWorld(t);
  t.answer("netflow_is_enabled", { fixture: "netflow_is_enabled_local.json" });
  var live = t.call("measurement_sample", "live", discovery, t.now());
  var baseline = t.call("measurement_sample", "sample", discovery, t.now());
  var key = function (reading) {
    return [reading.subject_kind, reading.subject_key, reading.measure, reading.sampled_at, reading.value].join(" ");
  };
  var baselineKeys = {};
  baseline.readings.forEach(function (reading) {
    baselineKeys[key(reading)] = true;
  });
  live.readings.forEach(function (reading) {
    t.assert(baselineKeys[key(reading)] === true, "the baseline pass did not read " + key(reading) +
      " as the live pass did, so the store would keep the capture twice rather than promote it");
  });
});
