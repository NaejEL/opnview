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
//
// The rule-info cache is filled by rule_info, from the installed-rules grid (survey, data source
// 2, "Rule metadata, read from source for step 6"). That grid serves a rule's classtype, its
// ruleset file and the metadata tags it declares, and NO FIELD HOLDING SURICATA'S SEVERITY: the
// rule's `priority` keyword, from which Suricata derives an alert's severity, is parsed by the
// firewall's rule cache and then dropped (scripts/suricata/lib/rulecache.py stores sid, msg, rev,
// gid, source, enabled, reference and action, and classtype as a property). So every rule this
// implementation finds is found with no severity, which opnview serves as unknown with the reason
// not_reported_by_provider. A severity is never derived from the classtype, the ruleset's name, a
// ruleset's own metadata convention or the signature's wording.

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
  },

  // rule_info answers what the installed-rules grid says of each sid asked: found, with its
  // classtype and its ruleset file, or not found. One request per sid: the grid filters on one
  // value per field (SettingsController::searchInstalledRulesAction). A call that fails stops the
  // answer there; the sids not answered are asked again at a later pass.
  rule_info: function (host, ruleIdentities) {
    var result = { probe: "installed_rules", state: "unavailable", outcome: "request_not_made" };
    var rules = [];
    for (var i = 0; i < ruleIdentities.length; i++) {
      var identity = String(ruleIdentities[i]);
      if (!isSid(identity)) {
        // A Suricata sid is a number (the eve record's alert.signature_id); an identity that is
        // not one names no rule of this engine.
        rules.push({ rule_identity: identity, lookup_state: "not_found" });
        continue;
      }
      var response = host.call("installed_rules", { form: { current: 1, rowCount: 2, sid: identity } });
      if (response.error) {
        return { result: result, rules: rules, error: response.error };
      }
      if (!response.ok) {
        result.outcome = response.availability_outcome;
        result.detail = response.detail;
        return { result: result, rules: rules, error: "collect: the installed-rules grid answered " + response.outcome };
      }
      var body = objectBody(response);
      if (body === null || !Array.isArray(body.rows)) {
        // The grid answers an empty list, not a grid, when its backend gave nothing it could
        // decode: that is a failure to ask, never a rule found missing.
        result.outcome = "undecodable";
        result.detail = "the installed-rules grid answered no rows array";
        return { result: result, rules: rules, error: "collect: reading the installed-rules grid: no rows array" };
      }
      rules.push(ruleAnswer(identity, body.rows));
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    return { result: result, rules: rules };
  }
};

// isSid reports whether a rule identity can be a Suricata sid: a whole number.
function isSid(identity) {
  return /^[0-9]+$/.test(identity);
}

// noClasstype is what the firewall's rule cache records for a rule that declares no classtype
// (scripts/suricata/lib/rulecache.py, list_rules: 'classtype': '##none##'): the absence of a
// category, not a category.
var noClasstype = "##none##";

// ruleAnswer is the answer about one sid from the grid's rows: the row carrying that sid, or not
// found. The grid matches the sid with a LIKE and no wildcard, which is equality, and the row's own
// sid is checked all the same.
function ruleAnswer(identity, rows) {
  for (var i = 0; i < rows.length; i++) {
    var row = rows[i];
    if (row === null || typeof row !== "object" || d.rawString(row, "sid") !== identity) {
      continue;
    }
    var answer = { rule_identity: identity, lookup_state: "found" };
    var classtype = d.string(row, "classtype");
    if (classtype !== null && classtype !== noClasstype) {
      answer.category = classtype;
    }
    var source = d.string(row, "source");
    if (source !== null) {
      answer.rule_source = source;
    }
    // No severity: the grid serves none (see the head of this file).
    return answer;
  }
  return { rule_identity: identity, lookup_state: "not_found" };
}

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  normalise_event_action: normaliseEventAction,
  rule_answer: ruleAnswer
};
