// The pf filter log: one implementation of the firewall_log kind.
//
// Everything here is a fact about pf and about the way OPNsense exposes its log
// (docs/opnsense-api-survey.md, data source 1). What the KIND does with the records --
// resolving the device and the rule, composing a client identity, detecting a gap -- is
// opnview's core and is written once (internal/collect, firewalllog.go).
//
// `digest` is deliberately never sent. The survey's inferred text treated it as the incremental
// primitive; measured twice on a live firewall, passing it returned byte-identical output and
// the supplied digest did not appear in the response. Sending it would suggest a resume guarantee
// that does not exist and would hide the gap detection that replaces it.

var d = opnview.decode;

// limitQuery is the query string of one poll, asking for `limit` lines: the page size the
// operator configured, not a constant here. The endpoint's own default is 1000 and limit=0 is
// coerced back to 1000.
function limitQuery(limit) {
  return { query: { limit: String(limit) } };
}

// parseArray decodes a body that must be a JSON array. A `null` body decodes as an empty one, as
// encoding/json reads it; anything else that is not an array is undecodable. It returns
// { rows, ok }.
function parseArray(response, objectsOnly) {
  var body;
  try {
    if (!response.decodable) {
      throw new Error("the body is not JSON");
    }
    body = response.value;
  } catch (e) {
    return { rows: null, ok: false };
  }
  if (body === null) {
    return { rows: [], ok: true };
  }
  if (!Array.isArray(body)) {
    return { rows: null, ok: false };
  }
  if (objectsOnly) {
    for (var i = 0; i < body.length; i++) {
      var row = body[i];
      if (row !== null && (typeof row !== "object" || Array.isArray(row))) {
        return { rows: null, ok: false };
      }
    }
  }
  return { rows: body, ok: true };
}

// The detail of a log that answered with nothing.
var silent = "reachable but silent: the log returned no record, which is not an absence of traffic";

// normaliseAction maps pf's action onto the closed vocabulary. An action outside it is unknown,
// never folded into pass or block: the field can also carry a translation action, and rendering
// one of those as a permission or a denial would attribute a decision nothing made.
function normaliseAction(raw) {
  switch (d.lower(raw)) {
    case "pass": return "pass";
    case "block": return "block";
    case "reject": return "reject";
    default: return "unknown";
  }
}

// normaliseDirection maps pf's `dir`; anything else is unknown, because on a blocked record the
// direction is what tells a reader which end is the machine inside.
function normaliseDirection(raw) {
  switch (d.lower(raw)) {
    case "in": return "in";
    case "out": return "out";
    default: return "unknown";
  }
}

// pairingField reads one of the two fields that pair the records of one connection: `id`, the
// IPv4 identification, and `seq`, the TCP sequence number, written by filterlog at fixed
// positions (opnsense/ports 26.7.3, opnsense/filterlog/files/description.txt) and named so by
// read_log.py (opnsense/core 26.7.3, src/opnsense/scripts/filter/read_log.py). A field the
// record does not carry, or one outside its width, is no value -- never a zero a pairing could
// match on.
function pairingField(row, key, applies, maximum) {
  if (!applies) {
    return null;
  }
  var value = d.int(row, key);
  if (value === null || value < 0 || value > maximum) {
    return null;
  }
  return value;
}

exports.firewall_log = {
  // probe asks whether the log can be read. An empty or non-array body, or 401/403, is
  // unavailable; an empty array with a healthy body is reachable but silent -- a warning state,
  // not an absence of traffic -- which is why the body's SHAPE is checked and not its length.
  probe: function (host) {
    var result = { probe: "filter_log", outcome: "request_not_made" };
    var response = host.call("filter_log", limitQuery(1));
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.state = "unavailable";
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result };
    }
    var parsed = parseArray(response, false);
    if (!parsed.ok) {
      result.state = "unavailable";
      result.outcome = "undecodable";
      result.detail = "HTTP 200 with a body that is not an array, which the survey names as a backend " +
        "failure rather than an absence of traffic";
      return { result: result };
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    if (parsed.rows.length === 0) {
      result.outcome = "healthy_empty";
      result.detail = silent;
    }
    return { result: result };
  },

  // records reads one page, newest first: a JSON array, not a paginated envelope.
  records: function (host, pageSize) {
    var result = { probe: "filter_log", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("filter_log", limitQuery(pageSize));
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result, error: "collect: the filter log answered " + response.outcome };
    }
    var parsed = parseArray(response, true);
    if (!parsed.ok) {
      result.outcome = "undecodable";
      result.detail = "HTTP 200 with a body that is not an array of records";
      return { result: result, error: "collect: reading the filter log: the body is not an array of records" };
    }
    var rows = parsed.rows;
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    if (rows.length === 0) {
      result.outcome = "healthy_empty";
      result.detail = silent;
      return { result: result };
    }

    var normaliser = host.normaliser();
    if (normaliser.error) {
      return { result: result, error: normaliser.error };
    }
    var records = [];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i] || {};
      var digest = d.string(row, "__digest__");
      if (digest === null) {
        // Without the identity there is no way to avoid storing the record twice, and a
        // duplicated flow would inflate every volume that sums it.
        continue;
      }
      var stamp = d.string(row, "__timestamp__");
      if (stamp === null) {
        continue;
      }
      // The two timestamp shapes, read here because which shapes a timestamp arrives in is a
      // fact about this log and its syslog format rather than about the kind.
      var observedAt = normaliser.filterLog(stamp);
      if (observedAt.error !== null) {
        return { result: result, error: "collect: normalising a filter-log timestamp: " + observedAt.error };
      }
      var ipVersion = d.int(row, "ipversion");
      if (ipVersion === null || (ipVersion !== 4 && ipVersion !== 6)) {
        return { result: result, error: "collect: a filter-log record reported no usable ipversion" };
      }
      var packetBytes = d.int(row, "length");
      if (packetBytes === null || packetBytes < 0) {
        // The column is NOT NULL and non-negative, and a length the record did not give must
        // not become a zero that a volume sums.
        return { result: result, error: "collect: a filter-log record reported no usable length" };
      }
      // protonum is the numeric twin and is not stored; when the name is absent the honest
      // value is the empty string, which no query interprets.
      var protocol = d.string(row, "protoname");
      records.push({
        digest: digest,
        observed_at: observedAt.value,
        device: d.rawString(row, "interface"),
        src_address: d.rawString(row, "src"),
        dst_address: d.rawString(row, "dst"),
        src_port: d.port(row, "srcport"),
        dst_port: d.port(row, "dstport"),
        protocol: protocol === null ? "" : protocol,
        ip_version: ipVersion,
        action: normaliseAction(d.rawString(row, "action")),
        direction: normaliseDirection(d.rawString(row, "dir")),
        log_reason: d.string(row, "reason"),
        packet_bytes: packetBytes,
        rid: d.string(row, "rid"),
        ip_id: pairingField(row, "id", ipVersion === 4, 65535),
        tcp_seq: pairingField(row, "seq", true, 4294967295)
      });
    }
    return { result: result, records: records };
  }
};

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  normalise_action: normaliseAction,
  normalise_direction: normaliseDirection
};
