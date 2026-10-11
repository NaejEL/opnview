// pf_states: the current rate by protocol and destination port, from the pf state table.
//
// WHY THE STATE TABLE. traffic/top runs iftop without -P, so no sample of the insight plugin carries a
// port or a protocol (docs/opnsense-api-survey.md, "Live rate endpoints, read from source for C3").
// The state table does: /api/diagnostics/firewall/query_states is the configd action
// `filter list states`, which runs `/sbin/pfctl -vvs state` and answers one row per state with its
// interface, protocol, direction, addresses and ports, its age, and its packets and bytes as
// cumulative [0, 1] pairs since the state was created. It is a read action: the commands that change
// the table -- kill_states, flush_states, flush_sources, del_state -- are other commands, which the
// client refuses.
//
// A RATE NEEDS TWO READS. A state's bytes are cumulative, so its rate is the difference between two
// reads of the same state (by its id, the state id and its creator id) over the seconds between
// them. Each pass keeps the counters it read as the provider's durable state; the next pass
// subtracts them. A state created since the previous read, which pfctl reports with an age no longer
// than the span, counts its whole bytes; one whose counters went down, or that is older than the
// span and was not in the previous read (a read the max_states bound truncated), is left out. A
// state that ended between two reads is gone with its last bytes. The first pass after a start only
// takes the first read. Every figure is therefore a lower bound, and sampled: a second measure,
// never summed with the logged volume.
//
// [0] AND [1]. pf counts a packet in bytes[0] when it travels in the state's own direction -- from
// the initiator, list_states.py's src_addr -- and in bytes[1] otherwise (sys/netpfil/pf/pf.c,
// s->bytes[s_dir_rev]). They are stored as forward and reverse, the words opnview uses for the two
// directions of one connection.
//
// TWO STATES FOR ONE FORWARDED CONNECTION. pf keeps a state on the interface a forwarded connection
// enters (direction in) and another on the one it leaves by (direction out), so every row keeps its
// interface and direction: a reader filters direction = in to count a forwarded connection once, as
// the dataset's description says. Traffic the firewall itself originates has an out state only.

var d = opnview.decode;

// PORTED are the protocols whose rows carry a port; every other protocol's port is null, never 0.
var PORTED = { tcp: true, udp: true, sctp: true };

// SNAPSHOT is the key of the durable state holding the previous read.
var SNAPSHOT = "snapshot";

// COLLECT_MAX_AGE is the max age a pass asks the broker for: a pass needs a read of its own, newer
// than the previous pass's, while a reader asking at the same moment shares the call.
var COLLECT_MAX_AGE = 5;

// PROBE_MAX_AGE lets a probe round reuse the last pass's answer rather than walk the table again.
var PROBE_MAX_AGE = 300;

// maxStates is the most rows one pass asks for.
function maxStates(host) {
  var settings = host.settings || {};
  var value = settings.max_states;
  return typeof value === "number" && value > 0 ? value : 50000;
}

// read asks for the state table: { result, rows, total, at } or { result, error }, notMade when the
// request was never sent.
function read(host, maxAge) {
  var result = { probe: "query_states", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("query_states", { form: host.pagination(1, maxStates(host)), max_age_seconds: maxAge });
  if (response.error) {
    return { result: result, error: response.error, notMade: true };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail;
    return { result: result, error: "pf_states: query_states answered " + response.outcome };
  }
  var rows = response.decodable ? d.rows(response.value) : null;
  if (rows === null) {
    result.outcome = "undecodable";
    result.detail = "the state table could not be read";
    return { result: result, error: "pf_states: the query_states body holds no rows" };
  }
  var total = response.value.total;
  var at = response.fetched_at;
  result.state = "reachable";
  result.outcome = rows.length === 0 ? "healthy_empty" : "healthy";
  result.separable = true;
  return {
    result: result,
    rows: rows,
    total: typeof total === "number" ? total : rows.length,
    at: typeof at === "number" && at > 0 ? at : host.now()
  };
}

// counterPair reads a cumulative [0, 1] pair, or null.
function counterPair(value) {
  if (value === null || value === undefined || typeof value.length !== "number" || value.length !== 2) {
    return null;
  }
  var first = value[0];
  var second = value[1];
  if (typeof first !== "number" || typeof second !== "number" || first < 0 || second < 0) {
    return null;
  }
  return [first, second];
}

// ageSeconds reads pfctl's age, hours:minutes:seconds, the hours unbounded; null when unreadable.
function ageSeconds(text) {
  if (typeof text !== "string") {
    return null;
  }
  var parts = text.split(":");
  if (parts.length !== 3) {
    return null;
  }
  var seconds = 0;
  for (var i = 0; i < parts.length; i++) {
    if (!/^[0-9]+$/.test(parts[i])) {
      return null;
    }
    seconds = seconds * 60 + parseInt(parts[i], 10);
  }
  return seconds;
}

// portOf is a row's destination port, null for a protocol with none or a port that is not one.
function portOf(protocol, text) {
  if (!PORTED[protocol] || typeof text !== "string" || !/^[0-9]+$/.test(text)) {
    return null;
  }
  var port = parseInt(text, 10);
  return port >= 1 && port <= 65535 ? port : null;
}

// previousOf reads the durable state's previous read: { at, counters } or null.
function previousOf(state) {
  if (state === null || state === undefined || typeof state[SNAPSHOT] !== "string") {
    return null;
  }
  try {
    var parsed = JSON.parse(state[SNAPSHOT]);
    if (typeof parsed.at === "number" && parsed.counters !== null && typeof parsed.counters === "object") {
      return parsed;
    }
  } catch (e) {
    return null;
  }
  return null;
}

// rates turns two reads into rows of the rate table, one per interface, direction, protocol and
// port: { rows, measured, unmeasured }.
function rates(rows, previous, at) {
  var span = at - previous.at;
  var groups = {};
  var order = [];
  var measured = 0;
  var unmeasured = 0;
  rows.forEach(function (row) {
    var id = d.string(row, "id");
    var bytes = counterPair(row.bytes);
    if (id === null || bytes === null) {
      return;
    }
    var prior = previous.counters[id];
    var forward;
    var reverse;
    if (prior !== undefined) {
      forward = bytes[0] - prior[0];
      reverse = bytes[1] - prior[1];
      if (forward < 0 || reverse < 0) {
        unmeasured++;
        return;
      }
    } else {
      var age = ageSeconds(d.string(row, "age"));
      if (age === null || age > span) {
        unmeasured++;
        return;
      }
      forward = bytes[0];
      reverse = bytes[1];
    }
    var protocol = (d.string(row, "proto") || "").toLowerCase();
    var device = d.string(row, "iface") || "";
    var direction = d.string(row, "direction") || "";
    var port = portOf(protocol, d.string(row, "dst_port"));
    var key = [device, direction, protocol, port === null ? "" : String(port)].join(" ");
    if (groups[key] === undefined) {
      groups[key] = { interface_device: device, direction: direction, protocol: protocol, port: port,
        forward: 0, reverse: 0, states: 0 };
      order.push(key);
    }
    var group = groups[key];
    group.forward += forward;
    group.reverse += reverse;
    group.states++;
    measured++;
  });
  order.sort();
  return {
    measured: measured,
    unmeasured: unmeasured,
    rows: order.map(function (key) {
      var group = groups[key];
      var forward = group.forward * 8 / span;
      var reverse = group.reverse * 8 / span;
      return {
        sampled_at: at,
        span_seconds: span,
        interface_device: group.interface_device,
        direction: group.direction,
        protocol: group.protocol,
        port: group.port,
        forward_bits_per_second: forward,
        reverse_bits_per_second: reverse,
        bits_per_second: forward + reverse,
        states: group.states
      };
    })
  };
}

exports.pf_state_rate = {
  // probe reads the state table, sharing the last pass's answer when the broker holds one.
  // An answer that is not the table -- a 404 where the action does not exist, an undecodable body --
  // is the provider's availability, as every probe reports it; only a request never made is an error.
  probe: function (host) {
    var answer = read(host, PROBE_MAX_AGE);
    if (answer.notMade) {
      return { result: answer.result, error: answer.error };
    }
    return { result: answer.result };
  },

  // collect reads the state table, stores the rate since the previous read, and keeps this read's
  // counters for the next pass.
  collect: function (host, state) {
    var answer = read(host, COLLECT_MAX_AGE);
    if (answer.error) {
      return { result: answer.result, error: answer.error, rows: {}, state: state || {} };
    }
    var counters = {};
    answer.rows.forEach(function (row) {
      var id = d.string(row, "id");
      var bytes = counterPair(row.bytes);
      if (id !== null && bytes !== null) {
        counters[id] = bytes;
      }
    });
    var previous = previousOf(state);
    var result = answer.result;
    var next = {};
    next[SNAPSHOT] = JSON.stringify({ at: answer.at, counters: counters });
    var truncated = answer.total > answer.rows.length;
    var truncation = truncated ? "; " + answer.rows.length + " of " + answer.total + " states read (max_states), " +
      "so the rates are those of the states read" : "";
    if (previous === null) {
      result.detail = "the first read of the state table was taken; a rate needs a second one, at the next pass" +
        truncation;
      return { result: result, rows: { rate: [] }, state: next };
    }
    if (answer.at <= previous.at) {
      // The broker served the read the previous pass already used: no time has passed between them.
      result.detail = "the state table answer is the previous pass's, so no rate is computed";
      return { result: result, rows: { rate: [] }, state: state };
    }
    var computed = rates(answer.rows, previous, answer.at);
    if (truncated) {
      result.outcome = "partial_readings";
    }
    result.detail = computed.measured + " states measured over " + (answer.at - previous.at) + " seconds, " +
      computed.unmeasured + " left out (new before the previous read, or a counter that went down)" + truncation;
    return { result: result, rows: { rate: computed.rows }, state: next };
  }
};

exports.helpers = { ageSeconds: ageSeconds, portOf: portOf };
