// The insight plugin's own tests: its probe, its pass into its own table, and its live pass.
var NOW = 1790000400;

var deviceNameRefusal = "collect: refusing to send a network device name to an endpoint that takes " +
  "interface names";

// The sampler refuses rather than samples when an argument is a device name. It is driven by an
// interface view in which the identifiers were, wrongly, filled with device names -- what a decoder
// reading the wrong column, or a refactor, would produce. The call is refused, because the
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

// interfaces seeds the installation model's interfaces the samplers read: identifier and device.
function interfaces(t, pairs) {
  Object.keys(pairs).forEach(function (identifier) {
    t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
      "first_seen_at, last_seen_at) VALUES (:identifier, :device, :identifier, 'ethernet', 'physical', :at, :at)",
      { identifier: identifier, device: pairs[identifier], at: NOW - 3600 });
  });
}

test("nothing is sampled before any interface is discovered, and no call is made", function (t) {
  t.clock(NOW);
  var sample = t.call("measurement_sample", "collect", {});
  if (sample.error !== null) {
    t.fatal("sampling an undiscovered firewall: " + sample.error);
  }
  t.equal(t.requests("traffic_top").length, 0, "calls to the per-pair endpoint with no interface name to give it");
  t.equal(sample.result.outcome, "partial_readings", "the outcome: the gauges did not answer either");
  t.assert(sample.result.detail.indexOf("no interface has been discovered yet") === 0, "the detail says why");
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
// nothing else -- no gauge, no processor stream -- and stamps every row with the instant its
// answer was fetched and with the cadence it was taken at.
function liveWorld(t) {
  t.clock(NOW);
  t.answer("traffic_top", { fixture: "traffic_top.json" });
  t.answer("traffic_interface", { fixture: "traffic_interface.json" });
  interfaces(t, { example_if_a: "exdev0", example_if_b: "exdev1" });
}

function liveRows(t) {
  var live = t.call("measurement_sample", "live");
  t.equal(live.error, null, "the live pass's error");
  return live;
}

test("the live pass reads only the live endpoints, into the plugin's table, stamped with its cadence", function (t) {
  liveWorld(t);
  var live = liveRows(t);
  t.equal(t.requests("traffic_top").length, 1, "calls to the per-pair endpoint");
  t.equal(t.requests("traffic_interface").length, 1, "calls to the interface counters");
  ["system_resources", "system_temperature", "system_time", "system_disk", "gateway_status", "system_swap",
    "netflow_is_enabled"].forEach(function (endpoint) {
    t.equal(t.requests(endpoint).length, 0, "calls to " + endpoint);
  });
  var rows = live.rows.measurement_sample;
  var subjects = {};
  rows.forEach(function (row) {
    subjects[row.subject_kind] = true;
    t.equal(row.live_cadence_seconds, 10, "the cadence of " + row.subject_key);
  });
  t.equal(Object.keys(subjects).sort(), ["interface", "interface_endpoint", "interface_endpoint_pair"],
    "the subjects a live pass reads");
  t.assert(live.latest >= NOW, "the newest instant sampled");
});

test("every live row carries the instant its answer was fetched, and the pair readings name the provider", function (t) {
  liveWorld(t);
  var rows = liveRows(t).rows.measurement_sample;
  t.assert(rows.length > 0, "the live pass read nothing");
  rows.forEach(function (row) {
    t.assert(row.sampled_at >= NOW && row.sampled_at <= NOW + 5, "the instant of " +
      row.subject_kind + " " + row.measure + " is " + row.sampled_at + ", not the answer's");
    if (row.subject_kind === "interface") {
      t.equal(row.provider_id, null, "a counter names no provider");
    } else {
      t.equal(typeof row.provider_id, "number", "a pair reading names the provider");
    }
  });
});

test("a live pass and a baseline pass read the same capture at the same instant", function (t) {
  liveWorld(t);
  t.answer("netflow_is_enabled", { fixture: "netflow_is_enabled_local.json" });
  var live = liveRows(t).rows.measurement_sample;
  var baseline = t.call("measurement_sample", "collect", {});
  t.equal(baseline.error, null, "the baseline pass's error");
  var key = function (row) {
    return [row.subject_kind, row.subject_key, row.measure, row.sampled_at, row.value].join(" ");
  };
  var baselineKeys = {};
  baseline.rows.measurement_sample.forEach(function (row) {
    baselineKeys[key(row)] = true;
    t.equal(row.live_cadence_seconds, null, "a baseline row carries no cadence");
  });
  live.forEach(function (row) {
    t.assert(baselineKeys[key(row)] === true, "the baseline pass did not read " + key(row) +
      " as the live pass did, so the store would keep the capture twice rather than promote it");
  });
});

// The subjects read on one interface are reported by their parts (PA11 of the plugin-API amendment):
// the device, the local address and, for a pair, the peer -- which is what the host reads of them. The
// subject key is this plugin's own name for the subject, and nothing in the core composes or parses it.
test("every reading of an address on an interface carries its parts beside its key", function (t) {
  liveWorld(t);
  var rows = liveRows(t).rows.measurement_sample;
  var seen = { interface_endpoint: 0, interface_endpoint_pair: 0 };
  rows.forEach(function (row) {
    if (!(row.subject_kind in seen)) {
      t.assert(row.interface_device === null && row.local_address === null && row.peer_address === null,
        row.subject_kind + " carries no part");
      return;
    }
    seen[row.subject_kind]++;
    var pair = row.subject_kind === "interface_endpoint_pair";
    t.assert(typeof row.interface_device === "string" && row.interface_device !== "", "the device of " + row.subject_key);
    t.assert(typeof row.local_address === "string" && row.local_address !== "", "the local address of " + row.subject_key);
    t.equal(pair ? typeof row.peer_address : row.peer_address, pair ? "string" : null, "the peer of " + row.subject_key);
    var parts = [row.interface_device, row.local_address].concat(pair ? [row.peer_address] : []);
    t.equal(row.subject_key, parts.join(" "), "the key the plugin names the subject by");
  });
  t.assert(seen.interface_endpoint > 0 && seen.interface_endpoint_pair > 0, "the snapshot read no address");
});
