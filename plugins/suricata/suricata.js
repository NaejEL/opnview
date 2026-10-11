// Suricata: one implementation of the security_event kind.
//
// Everything here is a fact about this engine and about the one endpoint that opens its event log
// (docs/opnsense-api-survey.md, data source 2). What the KIND does -- keeping the watermark,
// detecting a lost rotation, resolving a client -- is opnview's core (internal/collect,
// securityevent.go).
//
// The backend returns a record only if it carries a top-level `alert` key, and it has already
// overwritten that key with the signature text, so category, severity and rule metadata are gone
// before opnview sees them. That is why no record below carries a severity: severity is resolved
// through the per-provider rule-info cache and from nowhere else.

var d = opnview.decode;

// objectBody decodes a body that must be a JSON object, as encoding/json reads it into a map: a
// `null` body is an empty object, and anything else that is not an object is undecodable.
function objectBody(response) {
  var body;
  try {
    if (!response.decodable) {
      throw new Error("the body is not JSON");
    }
    body = response.value;
  } catch (e) {
    return null;
  }
  if (body === null) {
    return {};
  }
  if (typeof body !== "object" || Array.isArray(body)) {
    return null;
  }
  return body;
}

// normaliseEventAction maps the feed's `alert_action` onto the closed vocabulary. An action
// outside it is unknown: presenting an event as blocked when opnview cannot tell would claim the
// firewall stopped something it may have let through.
function normaliseEventAction(raw) {
  switch (d.lower(raw)) {
    case "blocked": case "drop": case "dropped": case "reject": case "rejected":
      return "blocked";
    case "allowed": case "alert": case "pass": case "passed":
      return "allowed";
    default:
      return "unknown";
  }
}

exports.security_event = {
  // probe separates absent from installed-but-stopped from running. A reachable engine with a
  // narrow ruleset and nothing to report is a NORMAL state, and distinguishing it from stopped and
  // from "we could not ask" is the whole point of the availability table. The implementation is
  // present either way, so it is the one opnview reads for this kind: whether it has anything to
  // say is the availability's business, not activation's.
  probe: function (host) {
    var result = { probe: "status", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("status");
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result };
    }
    var body = objectBody(response);
    if (body === null) {
      result.outcome = "undecodable";
      result.detail = "the status body could not be read";
      return { result: result };
    }
    var status = d.rawString(body, "status");
    result.separable = true;
    if (d.lower(status) === "running") {
      result.state = "reachable";
      result.outcome = "healthy";
      return { result: result };
    }
    result.state = "present_but_disabled";
    result.outcome = "service_not_running";
    result.detail = "the service reported " + opnview.quoteForDetail(status);
    return { result: result };
  },

  // rotation enumerates the rotated event files. `fileid` is how query_alerts names a file and
  // `sequence` is how get_alert_logs enumerates those values, so the two are joined on that token,
  // compared as a number.
  rotation: function (host) {
    var result = { probe: "alert_logs", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("alert_logs");
    if (response.error) {
      return { result: result, known: false, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      // The rotation view is unavailable, so a lost file cannot be told from a present one: known
      // stays false, and the kind declares nothing lost rather than invent a permanent gap.
      result.detail = response.detail + "; the rotated-file list did not answer, so a lost rotation " +
        "cannot be detected this pass";
      return { result: result, known: false };
    }
    var rows = response.decodable ? d.rows(response.value) : null;
    if (rows === null) {
      result.outcome = "undecodable";
      result.detail = "the rotated-file list could not be read";
      return { result: result, known: false };
    }
    var present = [];
    var highest = null;
    for (var i = 0; i < rows.length; i++) {
      var sequence = d.int(rows[i], "sequence");
      if (sequence === null) {
        continue;
      }
      present.push(String(sequence));
      if (highest === null || sequence > highest) {
        highest = sequence;
      }
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    return { result: result, known: true, present: present, highest_sequence: highest };
  },

  // alerts reads the most recent events, newest first.
  alerts: function (host, pageSize) {
    var result = { probe: "query_alerts", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("query_alerts", { form: host.pagination(1, pageSize) });
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result, error: "collect: the alert feed answered " + response.outcome };
    }
    var rows = response.decodable ? d.rows(response.value) : null;
    if (rows === null) {
      result.outcome = "undecodable";
      result.detail = "the alert feed body could not be read";
      return { result: result, error: "collect: reading the alert feed: the body holds no rows" };
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    if (rows.length === 0) {
      result.outcome = "healthy_empty";
      result.detail = "reachable with nothing to report, which for a narrow ruleset is a true reading and " +
        "not a fault";
      return { result: result };
    }
    var normaliser = host.normaliser();
    if (normaliser.error) {
      return { result: result, error: normaliser.error };
    }
    var alerts = [];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var fileID = d.string(row, "fileid");
      if (fileID === null) {
        // Without the file the offset names nothing, so the record cannot be given the stable
        // key the de-duplication rests on.
        continue;
      }
      var filepos = d.int(row, "filepos");
      if (filepos === null) {
        continue;
      }
      var stamp = d.string(row, "timestamp");
      if (stamp === null) {
        return { result: result, error: "collect: an alert record carried no timestamp" };
      }
      var occurredAt = normaliser.iso(stamp);
      if (occurredAt.error !== null) {
        return { result: result, error: "collect: normalising an alert timestamp: " + occurredAt.error };
      }
      var ruleIdentity = d.string(row, "alert_sid");
      if (ruleIdentity === null) {
        return { result: result, error: "collect: an alert record carried no signature id" };
      }
      // This key is the whole of what survives the backend's overwrite. Empty means the record
      // arrived without it, and the column is NOT NULL.
      var signature = d.string(row, "alert");
      alerts.push({
        file_id: fileID,
        byte_offset: filepos,
        occurred_at: occurredAt.value,
        rule_identity: ruleIdentity,
        signature: signature === null ? "" : signature,
        event_action: normaliseEventAction(d.rawString(row, "alert_action")),
        src_address: d.rawString(row, "src_ip"),
        dst_address: d.rawString(row, "dest_ip"),
        src_port: d.port(row, "src_port"),
        dst_port: d.port(row, "dest_port"),
        protocol: d.string(row, "proto"),
        in_interface_device: d.string(row, "in_iface")
      });
    }
    return { result: result, alerts: alerts };
  }
};

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  normalise_event_action: normaliseEventAction
};
