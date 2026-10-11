// The firewall's own volume and telemetry: one implementation of the measurement_sample kind.
//
// IT SAMPLES BECAUSE NOTHING UPSTREAM KEEPS PER-PAIR HISTORY. /api/diagnostics/netflow/top,
// netflow/get_metadata and netflow/aggregate all answered 404 on a live 26.7.3_11; the per-pair
// data is at /api/diagnostics/traffic/top/<interface names> and it is a LIVE RATE SNAPSHOT
// (docs/opnsense-api-survey.md, "Verified against a live firewall"). So no endpoint answers "what
// did these two talk about last Tuesday", and opnview builds that history by sampling.
//
// IT ALSO READS THE FIREWALL'S OWN GAUGES, because a gauge and a sampled pair volume are the same
// five facts -- a subject, a measure, a unit, a value and an instant -- and one table carries both.
// The gauges name no provider (`attributed: false`): the machine reporting on itself implements no
// external contract, and attributing them to the volume source would say it measured the
// temperature.
//
// AND IT REFUSES THE WRONG ARGUMENT. traffic/top takes interface NAMES, the configuration keys. A
// DEVICE name returns an empty array with HTTP 200, which is indistinguishable from an absence of
// traffic, so a wrong argument would not fail: it would quietly record silence.
//
// A READING THAT DID NOT ANSWER IS NEVER A ZERO: it is named in `missing`, and the kind records it
// in the availability detail.

var d = opnview.decode;
var samples = opnview.measurement_sample;

// The candidate field names of the telemetry endpoints. UNVERIFIED: every list below. The survey
// establishes that these endpoints answer on a live firewall and does NOT establish their field
// names, so each reading tries the plausible keys and reports the reading as ABSENT when none of
// them answers.
var memoryTotalKeys = ["total", "memory_total", "total_bytes", "physmem"];
var memoryUsedKeys = ["used", "memory_used", "used_bytes", "active"];
var memoryRatioKeys = ["used_percent", "usage", "memory_usage_percent"];
var temperatureKeys = ["temperature", "temp", "value"];
var sensorNameKeys = ["device", "device_seq", "type", "name"];
var uptimeKeys = ["uptime", "uptime_seconds", "seconds"];
var loadKeys = ["loadavg", "load_average", "load"];
var diskRatioKeys = ["used_pct", "capacity", "used_percent", "percent"];
var mountKeys = ["mountpoint", "mount", "filesystem", "device"];
var diskWrapperKeys = ["devices", "rows", "filesystems"];
var bytesInKeys = ["bytes received", "bytes_received", "bytes_in", "ibytes", "in_bytes"];
var bytesOutKeys = ["bytes transmitted", "bytes_transmitted", "bytes_out", "obytes", "out_bytes"];
var packetsInKeys = ["packets received", "packets_received", "packets_in", "ipackets", "in_packets"];
var packetsOutKeys = ["packets transmitted", "packets_transmitted", "packets_out", "opackets", "out_packets"];

// The field names VERIFIED against the source for step 5 (survey, "Telemetry field names, read
// from source for step 5"). The per-interface error counters are the lines `ifinfo` prints,
// "input errors: %lu" and "output errors: %lu" (opnsense/core 26.7.3, legacy_interface_stats in
// interfaces.lib.inc; opnsense/ports, ifinfo.c). A gateway item's delay and stddev are
// sprintf('%0.1f ms') and its loss sprintf('%0.1f %%'), "~" where dpinger has no figure
// (scripts/routes/gateway_status.php and dpinger.inc). The swap list's total and used are strings
// in KiB, as swapinfo -k writes them (SystemController::systemSwapAction, scripts/system/swapinfo.py).
var inputErrorsKey = "input errors";
var outputErrorsKey = "output errors";
var kibibyte = 1024;

// The processor's event stream (survey, "Processor stream, read from source for the
// resolver-cache cycle"): scripts/system/cpu.py runs `iostat -w 1 cpu` and prints one event per
// line, {total, user, nice, sys, intr, idle}. It never ends, and iostat's FIRST line is the
// average since boot (iostat(8)), so two events are read: the second is the first one-second
// interval. Five seconds covers that second, the script's start-up and the network, far below the
// client's timeout and the sampling interval.
var processorStreamEvents = 2;
var processorStreamWithin = 5;
var processorPrivilege = "\"Lobby: Dashboard\" (page-system-login-logout)";

// The absences of a gateway reading, each named on its own (step-5A live corrections, item 4.2).
// The privilege is OPNsense's own name for the ACL page covering the path ("System: Gateways",
// page-system-gateways, models/OPNsense/Core/ACL/ACL.xml). GatewayController::statusAction starts
// from {"items": [], "status": "failed"} and sets "ok" only when the configd action returned
// something, so a firewall with no gateway answers "failed" too.
var gatewayAbsenceNotFound = "gateway latency and loss (not found, HTTP 404: this firewall has no " +
  "/api/routes/gateway/status)";
var gatewayAbsenceFailed = "gateway latency and loss (the endpoint answered \"status\": \"failed\" with " +
  "no item, which it does both when no gateway is configured and when the configd action " +
  "interface gateways status returned nothing)";
var gatewayAbsenceNoGateway = "gateway latency and loss (the endpoint answered \"status\": \"ok\" and listed " +
  "no gateway)";

function gatewayAbsenceDenied(status) {
  return "gateway latency and loss (denied, HTTP " + status + ": the API key's user needs the privilege " +
    "\"System: Gateways\", which covers /api/routes/gateway/status)";
}

function gatewayAbsenceNoFigure(without, total) {
  return "gateway latency and loss (" + without + " of " + total + " gateways listed with no figure: " +
    "dpinger.inc reports \"~\" for delay, stddev and loss when no dpinger process monitors the gateway -- " +
    "gateway monitoring disabled -- when its socket cannot be read, or before it has measured anything)";
}

function gatewayAbsenceUnanswered(detail) {
  return "gateway latency and loss (the endpoint did not answer usably: " + detail + ")";
}

// quote renders a value as Go's %q renders a plain string.
function quote(value) {
  return JSON.stringify(value);
}

// isObject reports whether a decoded value is a JSON object.
function isObject(value) {
  return value !== null && value !== undefined && typeof value === "object" && !Array.isArray(value);
}

// firstString returns the first of several candidate keys holding a non-empty string, or null.
function firstString(object, keys) {
  for (var i = 0; i < keys.length; i++) {
    var value = d.string(object, keys[i]);
    if (value !== null) {
      return value;
    }
  }
  return null;
}

// percentToRatio turns a figure that may be a percentage into a 0-to-1 ratio: above 1 it is read
// as a percentage, which is the only interpretation that makes both shapes storable under one unit.
function percentToRatio(value) {
  return value > 1 ? value / 100 : value;
}

// perInterface returns a decoded body as a map from interface identifier to its entry: a map
// keyed by the identifier, or an envelope holding one.
function perInterface(body) {
  if (!isObject(body)) {
    return null;
  }
  var wrappers = ["interfaces", "records", "rows"];
  for (var i = 0; i < wrappers.length; i++) {
    var nested = body[wrappers[i]];
    if (nested !== undefined && isObject(nested)) {
      return nested;
    }
  }
  return body;
}

// recordsOf returns the records of one interface's entry.
function recordsOf(entry) {
  if (!isObject(entry)) {
    return d.objects(entry);
  }
  if (entry.records !== undefined) {
    return d.objects(entry.records);
  }
  return d.objects(entry);
}

// memoryRatio computes the memory ratio from whichever shape the endpoint used, or null.
function memoryRatio(body) {
  var container = d.nested(body, "memory");
  if (container === null) {
    container = body;
  }
  var ratio = d.firstFloat(container, memoryRatioKeys);
  if (ratio !== null) {
    return percentToRatio(ratio.value);
  }
  var total = d.firstFloat(container, memoryTotalKeys);
  var used = d.firstFloat(container, memoryUsedKeys);
  if (total !== null && used !== null && total.value > 0) {
    return used.value / total.value;
  }
  return null;
}

// parseMilliseconds reads dpinger's "%0.1f ms"; "~" and anything else is unreadable (null).
function parseMilliseconds(text) {
  var trimmed = text.trim();
  if (trimmed.length < 2 || trimmed.slice(-2) !== "ms") {
    return null;
  }
  var value = d.parseFloat(trimmed.slice(0, -2).trim());
  if (value === null || value < 0) {
    return null;
  }
  return value;
}

// parseLossRatio reads dpinger's "%0.1f %%" as a 0-to-1 ratio; "~" is unreadable (null).
function parseLossRatio(text) {
  var trimmed = text.trim();
  if (trimmed.length < 1 || trimmed.slice(-1) !== "%") {
    return null;
  }
  var value = d.parseFloat(trimmed.slice(0, -1).trim());
  if (value === null || value < 0 || value > 100) {
    return null;
  }
  return value / 100;
}

// guardInterfaceNames turns the discovered identifiers into the argument traffic/top takes,
// refusing if any of them is a device name. It compares the names about to be sent against the
// DEVICE set discovery read from the firewall, so if anything upstream ever put a device where an
// identifier belongs, the call is refused instead of returning an empty array that looks like an
// absence of traffic. Sorted, so the request is byte-identical between passes.
function guardInterfaceNames(discovery) {
  var names = [];
  for (var i = 0; i < discovery.identifiers.length; i++) {
    var name = discovery.identifiers[i];
    if (discovery.isDevice(name) && !discovery.isIdentifier(name)) {
      return { error: "collect: refusing to send a network device name to an endpoint that takes interface " +
        "names: " + quote(name) + " is a device name" };
    }
    names.push(name);
  }
  names.sort();
  return { names: names };
}

// samplePairVolume reads traffic/top and returns the pair readings: { readings, detail, outcome,
// failed, error }.
function samplePairVolume(host, discovery, now) {
  var guard = guardInterfaceNames(discovery);
  if (guard.error) {
    return { readings: [], detail: guard.error, outcome: "request_not_made", failed: true };
  }
  var names = guard.names;
  if (names.length === 0) {
    // Discovery has not run, or the firewall reported no interface. Calling with no argument would
    // reach a different command on the router.
    return { readings: [], detail: "no interface has been discovered yet, so nothing was sampled",
      outcome: "healthy_empty", failed: false };
  }
  // One comma-separated path element, which is how the OPNsense router passes a positional list.
  var response = host.call("traffic_top", { arguments: [names.join(",")] });
  if (response.error) {
    return { readings: [], detail: "", outcome: "request_not_made", failed: true };
  }
  if (!response.ok) {
    return { readings: [], detail: response.detail, outcome: response.availability_outcome, failed: true };
  }
  var body;
  try {
    if (!response.decodable) {
      throw new Error("the body is not JSON");
    }
    body = response.value;
  } catch (e) {
    return { readings: [], detail: "the traffic snapshot body could not be read", outcome: "undecodable",
      failed: true };
  }
  var readings = [];
  var pairs = 0;
  var identifiers = perInterface(body);
  var order = identifiers === null ? [] : Object.keys(identifiers).sort();
  for (var i = 0; i < order.length; i++) {
    var identifier = order[i];
    // The interface the record was read on is kept, under its network device, the token every
    // other interface reading is keyed by. An identifier discovery does not know has no device to
    // file the reading under, and is left out.
    var device = discovery.deviceOf(identifier);
    if (device === "") {
      continue;
    }
    var records = recordsOf(identifiers[identifier]);
    for (var r = 0; r < records.length; r++) {
      var record = records[r];
      var localAddress = d.string(record, "address");
      if (localAddress === null) {
        continue;
      }
      // The record's own totals, over every peer of the local address: the local address's sending
      // is reported only here, so these are what a client's outbound figures come from.
      var endpointKey = samples.interfaceEndpointKey(device, localAddress);
      var totals = [
        ["rate_bits_in", "rate_bits_in", "bit_per_second"],
        ["rate_bits_out", "rate_bits_out", "bit_per_second"],
        ["cumulative_bytes_in", "cumulative_bytes_in", "byte"],
        ["cumulative_bytes_out", "cumulative_bytes_out", "byte"]
      ];
      for (var t = 0; t < totals.length; t++) {
        var total = d.float(record, totals[t][0]);
        if (total === null) {
          continue;
        }
        readings.push({ subject_kind: "interface_endpoint", subject_key: endpointKey, measure: totals[t][1],
          unit: totals[t][2], value: total, sampled_at: now });
      }
      var details = d.objects(record.details);
      for (var p = 0; p < details.length; p++) {
        var detail = details[p];
        var peerAddress = d.string(detail, "address");
        if (peerAddress === null || peerAddress === localAddress) {
          continue;
        }
        var subjectKey = samples.interfaceEndpointPairKey(device, localAddress, peerAddress);
        // A details entry is the peer's half of one iftop line pair: what the PEER sent to the
        // local address, so its figures are the pair's inbound figures relative to the local end.
        var values = [
          [detail, "rate_bits", "rate_bits_in", "bit_per_second"],
          [detail, "cumulative_bytes", "cumulative_bytes_in", "byte"]
        ];
        // The local address's own sending is reported only as its total over every peer. With
        // exactly one peer that total IS the pair's outbound figure; with several, the split is
        // not reported and no outbound reading is written.
        if (details.length === 1) {
          values.push([record, "rate_bits_out", "rate_bits_out", "bit_per_second"]);
          values.push([record, "cumulative_bytes_out", "cumulative_bytes_out", "byte"]);
        }
        var wrote = false;
        for (var v = 0; v < values.length; v++) {
          var value = d.float(values[v][0], values[v][1]);
          if (value === null) {
            continue;
          }
          readings.push({ subject_kind: "interface_endpoint_pair", subject_key: subjectKey,
            measure: values[v][2], unit: values[v][3], value: value, sampled_at: now });
          wrote = true;
        }
        if (wrote) {
          pairs++;
        }
      }
    }
  }
  if (pairs === 0) {
    return { readings: readings, detail: "the traffic snapshot named no endpoint pair, which is not an " +
      "absence of traffic; a wrong interface argument answers exactly this way", outcome: "healthy_empty",
      failed: false };
  }
  return { readings: readings, detail: "sampled " + pairs + " endpoint pairs from a live snapshot of " +
    samples.sampleSeconds + " seconds; no endpoint exposes a per-pair aggregate over a past window, so this " +
    "history is opnview's own", outcome: "healthy", failed: false };
}

// readProcessorRatio reads the processor's use as a 0-to-1 ratio, or the named reason it could
// not: { ratio } or { absent } or { error }. The derivation is total / (total + idle) from the
// second event -- not total / 100, because iostat rounds each column on its own.
function readProcessorRatio(host) {
  var response = host.stream("cpu_usage_stream", { events: processorStreamEvents,
    within_seconds: processorStreamWithin });
  if (response.error) {
    return { error: response.error };
  }
  switch (response.outcome) {
    case "ok":
      break;
    case "forbidden":
      if (response.status_code === 401) {
        return { absent: "processor (" + opnview.authenticationFailed + ")" };
      }
      return { absent: "processor (denied, HTTP " + response.status_code + ": the API key's user lacks the " +
        "privilege " + processorPrivilege + ", whose pattern api/diagnostics/cpu_usage/* admits the stream)" };
    case "not_found":
      return { absent: "processor (not found, HTTP 404: this firewall does not answer " +
        "/api/diagnostics/cpu_usage/stream)" };
    default:
      return { absent: "processor (the stream answered " + response.outcome + ": " + response.detail + ")" };
  }
  var events = response.events;
  if (events.length < processorStreamEvents) {
    var byServer = response.ended_by === "closed_by_server";
    if (events.length === 0 && byServer) {
      return { absent: "processor (the stream ended before any event)" };
    }
    if (events.length === 0) {
      return { absent: "processor (no event within " + processorStreamWithin + " s)" };
    }
    if (byServer) {
      return { absent: "processor (the stream ended after the first event, which is iostat's average since " +
        "boot and not a current reading)" };
    }
    return { absent: "processor (no event after the first within " + processorStreamWithin + " s; the first " +
      "is iostat's average since boot and not a current reading)" };
  }
  var unreadable = { absent: "processor (an event that could not be read as {total, idle})" };
  var event;
  try {
    event = JSON.parse(events[processorStreamEvents - 1]);
  } catch (e) {
    return unreadable;
  }
  if (!isObject(event) || typeof event.total !== "number" || typeof event.idle !== "number" ||
      event.total < 0 || event.idle < 0 || event.total + event.idle <= 0) {
    return unreadable;
  }
  return { ratio: event.total / (event.total + event.idle) };
}

// sampleGateways reads each gateway's round-trip time, its standard deviation and its loss, keyed
// by the gateway's name: { readings, absent } or { error }. A figure reported as "~", or in a form
// this code cannot read, is not written, and a gateway with no figure at all is not written either.
function sampleGateways(host, now) {
  var response = host.call("gateway_status");
  if (response.error) {
    return { error: response.error };
  }
  switch (response.outcome) {
    case "ok":
      break;
    case "forbidden":
      if (response.status_code === 401) {
        return { readings: [], absent: "gateway latency and loss (" + opnview.authenticationFailed + ")" };
      }
      return { readings: [], absent: gatewayAbsenceDenied(response.status_code) };
    case "not_found":
      return { readings: [], absent: gatewayAbsenceNotFound };
    default:
      return { readings: [], absent: gatewayAbsenceUnanswered(response.detail) };
  }
  var body;
  try {
    if (!response.decodable) {
      throw new Error("the body is not JSON");
    }
    body = response.value;
  } catch (e) {
    body = undefined;
  }
  if (body === null) {
    body = {};
  }
  if (!isObject(body)) {
    return { readings: [], absent: gatewayAbsenceUnanswered("the body is not a JSON object") };
  }
  var status = d.string(body, "status");
  if (status !== null && d.lower(status) === "failed") {
    return { readings: [], absent: gatewayAbsenceFailed };
  }
  var items = d.objects(body.items);
  if (items.length === 0) {
    return { readings: [], absent: gatewayAbsenceNoGateway };
  }
  var readings = [];
  var withoutFigure = 0;
  var figures = [
    ["delay", "delay_milliseconds", "millisecond", parseMilliseconds],
    ["stddev", "delay_stddev_milliseconds", "millisecond", parseMilliseconds],
    ["loss", "loss_ratio", "ratio", parseLossRatio]
  ];
  for (var i = 0; i < items.length; i++) {
    var item = items[i];
    var name = d.string(item, "name");
    if (name === null) {
      withoutFigure++;
      continue;
    }
    var wrote = false;
    for (var f = 0; f < figures.length; f++) {
      var text = d.string(item, figures[f][0]);
      if (text === null) {
        continue;
      }
      var value = figures[f][3](text);
      if (value === null) {
        continue;
      }
      readings.push({ subject_kind: "gateway", subject_key: name, measure: figures[f][1], unit: figures[f][2],
        value: value, sampled_at: now, attributed: false });
      wrote = true;
    }
    if (!wrote) {
      withoutFigure++;
    }
  }
  if (withoutFigure > 0) {
    return { readings: readings, absent: gatewayAbsenceNoFigure(withoutFigure, items.length) };
  }
  return { readings: readings, absent: "" };
}

// sampleSwap reads each swap device's size and use, in bytes: { readings, absent } or { error }.
// A firewall with no swap device answers with an empty list, which is a fact about the machine and
// not a zero of use.
function sampleSwap(host, now) {
  var answer = host.readObject("system_swap");
  if (answer.error) {
    return { error: answer.error };
  }
  if (!answer.ok) {
    return { readings: [], absent: "swap (the endpoint did not answer)" };
  }
  var devices = d.objects(answer.object.swap);
  if (devices.length === 0) {
    return { readings: [], absent: "swap (this firewall reports no swap device)" };
  }
  var readings = [];
  for (var i = 0; i < devices.length; i++) {
    var entry = devices[i];
    var device = d.string(entry, "device");
    if (device === null || device === "") {
      continue;
    }
    var figures = [["total", "swap_total_bytes"], ["used", "swap_used_bytes"]];
    for (var f = 0; f < figures.length; f++) {
      var kib = d.float(entry, figures[f][0]);
      if (kib === null || kib < 0) {
        continue;
      }
      readings.push({ subject_kind: "firewall", subject_key: device, measure: figures[f][1], unit: "byte",
        value: kib * kibibyte, sampled_at: now, attributed: false });
    }
  }
  if (readings.length === 0) {
    return { readings: [], absent: "swap (the endpoint answered with no figure this code could read)" };
  }
  return { readings: readings, absent: "" };
}

// sampleFirewallTelemetry reads the firewall's gauges: { readings, missing } or { error }. Nothing
// here writes a zero for a reading it could not take.
function sampleFirewallTelemetry(host, discovery, now) {
  var readings = [];
  var missing = [];
  function add(subjectKey, measure, unit, value) {
    readings.push({ subject_kind: "firewall", subject_key: subjectKey, measure: measure, unit: unit,
      value: value, sampled_at: now, attributed: false });
  }

  // Memory: either a ratio the endpoint computed, or a total and a used figure to divide.
  var resources = host.readObject("system_resources");
  if (resources.error) {
    return { error: resources.error };
  }
  if (!resources.ok) {
    missing.push("memory (the endpoint did not answer)");
  } else {
    var ratio = memoryRatio(resources.object);
    if (ratio === null) {
      missing.push("memory (the endpoint answered with no figure this code could read)");
    } else {
      add("", "memory_use_ratio", "ratio", ratio);
    }
  }

  // The processor, from its event stream.
  var processor = readProcessorRatio(host);
  if (processor.error) {
    return { error: processor.error };
  }
  if (processor.absent) {
    missing.push(processor.absent);
  } else {
    add("", "cpu_use_ratio", "ratio", processor.ratio);
  }

  // Temperature, one reading per sensor. A firewall with no sensor answers with nothing, and that
  // is a fact about the hardware rather than a temperature of zero.
  var sensors = host.readCollection("system_temperature");
  if (sensors.error) {
    return { error: sensors.error };
  }
  if (!sensors.ok) {
    missing.push("temperature (the endpoint did not answer)");
  } else {
    var written = 0;
    for (var s = 0; s < sensors.rows.length; s++) {
      var sensor = sensors.rows[s];
      var temperature = d.firstFloat(sensor, temperatureKeys);
      if (temperature === null) {
        continue;
      }
      var label = firstString(sensor, sensorNameKeys);
      if (label === null) {
        label = "sensor-" + s;
      }
      add(label, "temperature_celsius", "celsius", temperature.value);
      written++;
    }
    if (written === 0) {
      missing.push("temperature (this firewall reports no sensor)");
    }
  }

  // Uptime and load. The live answer is text, "17 days, 23:24:10" and "0.07, 0.14, 0.15", as the
  // survey's systemTime table records; a number under one of the keys is read too.
  var time = host.readObject("system_time");
  if (time.error) {
    return { error: time.error };
  }
  if (!time.ok) {
    missing.push("uptime (the endpoint did not answer)");
  } else {
    var body = time.object;
    var uptime = d.firstFloat(body, uptimeKeys);
    var uptimeText = d.string(body, "uptime");
    if (uptime !== null) {
      add("", "uptime_seconds", "second", uptime.value);
    } else if (uptimeText !== null && d.uptimeSeconds(uptimeText) !== null) {
      add("", "uptime_seconds", "second", d.uptimeSeconds(uptimeText));
    } else {
      missing.push("uptime (the endpoint answered with no figure this code could read)");
    }
    var load = d.firstFloat(body, loadKeys);
    var loadText = d.string(body, "loadavg");
    if (load !== null) {
      add("", "load_average", "dimensionless", load.value);
    } else if (loadText !== null && d.firstLoadAverage(loadText) !== null) {
      add("", "load_average", "dimensionless", d.firstLoadAverage(loadText));
    } else {
      missing.push("load average (the endpoint answered with no figure this code could read)");
    }
  }

  // Disk, one reading per filesystem.
  var filesystems = host.readCollection.apply(host, ["system_disk"].concat(diskWrapperKeys));
  if (filesystems.error) {
    return { error: filesystems.error };
  }
  if (!filesystems.ok) {
    missing.push("disk (the endpoint did not answer)");
  } else {
    var disks = 0;
    for (var f = 0; f < filesystems.rows.length; f++) {
      var filesystem = filesystems.rows[f];
      var used = d.firstFloat(filesystem, diskRatioKeys);
      if (used === null) {
        continue;
      }
      var mount = firstString(filesystem, mountKeys);
      if (mount === null) {
        mount = "filesystem-" + f;
      }
      add(mount, "disk_use_ratio", "ratio", percentToRatio(used.value));
      disks++;
    }
    if (disks === 0) {
      missing.push("disk (the endpoint answered with no figure this code could read)");
    }
  }

  // The per-interface counters. Their subject is the network DEVICE name, the token interface_map
  // keys by, so a reading joins to an interface without a foreign key a discovery refresh could
  // break.
  var counters = host.readObject("traffic_interface");
  if (counters.error) {
    return { error: counters.error };
  }
  if (!counters.ok) {
    missing.push("interface counters (the endpoint did not answer)");
  } else {
    var byInterface = perInterface(counters.object);
    var keys = byInterface === null ? [] : Object.keys(byInterface);
    var count = 0;
    var specs = [
      [bytesInKeys, "bytes_in", "byte"],
      [bytesOutKeys, "bytes_out", "byte"],
      [packetsInKeys, "packets_in", "packet"],
      [packetsOutKeys, "packets_out", "packet"],
      [[inputErrorsKey], "errors_in", "packet"],
      [[outputErrorsKey], "errors_out", "packet"]
    ];
    for (var k = 0; k < keys.length; k++) {
      var entry = byInterface[keys[k]];
      if (!isObject(entry)) {
        continue;
      }
      var device = discovery.deviceOf(keys[k]);
      if (device === "") {
        // The reading names an interface discovery does not know: a key nothing could join to.
        continue;
      }
      for (var c = 0; c < specs.length; c++) {
        var counter = d.firstFloat(entry, specs[c][0]);
        if (counter === null) {
          continue;
        }
        readings.push({ subject_kind: "interface", subject_key: device, measure: specs[c][1], unit: specs[c][2],
          value: counter.value, sampled_at: now, attributed: false });
        count++;
      }
    }
    if (count === 0) {
      missing.push("interface counters (the endpoint answered with no figure this code could read)");
    }
  }

  var gateways = sampleGateways(host, now);
  if (gateways.error) {
    return { error: gateways.error };
  }
  readings = readings.concat(gateways.readings);
  if (gateways.absent !== "") {
    missing.push(gateways.absent);
  }

  var swap = sampleSwap(host, now);
  if (swap.error) {
    return { error: swap.error };
  }
  readings = readings.concat(swap.readings);
  if (swap.absent !== "") {
    missing.push(swap.absent);
  }
  return { readings: readings, missing: missing };
}

exports.measurement_sample = {
  // probe reads whether the firewall keeps volume data itself. is_enabled returns {netflow,
  // local}, and local data exists only when `local` is 1: a firewall exporting to an external
  // collector reports netflow 1, local 0 and keeps nothing here -- a configuration state, recorded
  // as present-but-disabled, never as a flat zero line.
  probe: function (host) {
    var result = { probe: "netflow_is_enabled", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("netflow_is_enabled");
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result };
    }
    var body;
    try {
      if (!response.decodable) {
        throw new Error("the body is not JSON");
      }
      body = response.value;
    } catch (e) {
      body = undefined;
    }
    if (body === null) {
      body = {};
    }
    if (!isObject(body)) {
      result.outcome = "undecodable";
      result.detail = "the is_enabled body could not be read";
      return { result: result };
    }
    var local = d.int(body, "local");
    result.separable = true;
    if (local === null) {
      result.state = "present_but_disabled";
      result.outcome = "undecodable";
      result.detail = "is_enabled did not report whether local collection is on";
    } else if (local === 0) {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "local collection is off, so the firewall exports elsewhere and keeps nothing here";
    } else {
      result.state = "reachable";
      result.outcome = "healthy";
    }
    return { result: result };
  },

  // sample reads the per-pair snapshot and the firewall's gauges. When the volume half cannot be
  // read the volume source reads as unavailable, and the gauges are read anyway: they come from
  // different endpoints, and a refusal over the per-pair argument says nothing about the
  // firewall's temperature.
  sample: function (host, discovery, now) {
    var result = { probe: "traffic_top", state: "reachable" };
    var pair = samplePairVolume(host, discovery, now);
    result.detail = pair.detail;
    result.outcome = pair.outcome;
    var readings = pair.readings;
    if (pair.failed) {
      result.state = "unavailable";
      readings = [];
    }
    var gauges = sampleFirewallTelemetry(host, discovery, now);
    if (gauges.error) {
      return { result: result, error: gauges.error };
    }
    return { result: result, readings: readings.concat(gauges.readings), missing: gauges.missing };
  }
};

// The two parsers, exported for the plugin's own tests.
exports.helpers = {
  parse_milliseconds: parseMilliseconds,
  parse_loss_ratio: parseLossRatio,
  guard_interface_names: guardInterfaceNames
};
