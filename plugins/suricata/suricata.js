// Suricata: one implementation of the security_event kind, and the owner of the security events
// and of the rule-info cache (ADR 0019, ADR 0020 wave 4).
//
// Everything here is a fact about this engine and about the one endpoint that opens its event log
// (docs/opnsense-api-survey.md, data source 2). The watermark over the feed is this provider's
// durable state, kept per file; a rotation that discarded a watermarked file is a collection gap
// the pass reports; which client an event names is the clients plugin's placement.
//
// The backend returns a record only if it carries a top-level `alert` key, and it has already
// overwritten that key with the signature text, so category, severity and rule metadata are gone
// before opnview sees them. That is why no record below carries a severity: severity is resolved
// through the per-provider rule-info cache and from nowhere else.
//
// The rule-info cache is filled by the derivation rule_info, from the installed-rules grid (survey,
// data source 2, "Rule metadata, read from source for step 6"). That grid serves a rule's classtype,
// its ruleset file and the metadata tags it declares, and NO FIELD HOLDING SURICATA'S SEVERITY: the
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

// THE WATERMARK is the provider's durable state, one entry per event file: `file.<fileid>` holds
// the highest byte offset read, the instant it was read, and the file's rotation state (current,
// rotated or lost), as `<offset>:<observed_at>:<state>`. It is what the next pass reads past, and
// what a lost rotation is detected against.
var watermarkPrefix = "file.";

function readWatermarks(state) {
  var marks = {};
  Object.keys(state || {}).forEach(function (key) {
    if (key.indexOf(watermarkPrefix) !== 0) {
      return;
    }
    var parts = String(state[key]).split(":");
    if (parts.length !== 3) {
      return;
    }
    marks[key.slice(watermarkPrefix.length)] = { byte_offset: parseInt(parts[0], 10), observed_at: parseInt(parts[1], 10),
      rotation_state: parts[2] };
  });
  return marks;
}

function writeWatermarks(marks) {
  var state = {};
  Object.keys(marks).forEach(function (fileID) {
    var mark = marks[fileID];
    state[watermarkPrefix + fileID] = mark.byte_offset + ":" + mark.observed_at + ":" + mark.rotation_state;
  });
  return state;
}

// readRotation enumerates the rotated event files: `fileid` is how query_alerts names a file and
// `sequence` is how get_alert_logs enumerates those values, so the two are joined on that token,
// compared as a number. { known, present, highest, result } -- known false when the list did not
// answer, in which case a lost file cannot be told from a present one and nothing is declared lost.
function readRotation(host) {
  var result = { probe: "alert_logs", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("alert_logs");
  if (response.error) {
    return { known: false, result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail + "; the rotated-file list did not answer, so a lost rotation " +
      "cannot be detected this pass";
    return { known: false, result: result };
  }
  var rows = response.decodable ? d.rows(response.value) : null;
  if (rows === null) {
    result.outcome = "undecodable";
    result.detail = "the rotated-file list could not be read";
    return { known: false, result: result };
  }
  var present = {};
  var highest = null;
  for (var i = 0; i < rows.length; i++) {
    var sequence = d.int(rows[i], "sequence");
    if (sequence === null) {
      continue;
    }
    present[String(sequence)] = true;
    if (highest === null || sequence > highest) {
      highest = sequence;
    }
  }
  return { known: true, present: present, highest: highest, result: result };
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

  // collect reads the rotated-file list, marks lost every watermarked file rotation discarded --
  // reported as a gap, since the records after the watermark are gone and no endpoint returns them
  // -- then reads the most recent events, newest first, and stores those past each file's
  // watermark, which it advances. The rows are the plugin's own table's; the watermarks are the
  // state handed to the next pass.
  collect: function (host, state, provider) {
    var now = host.now();
    var marks = readWatermarks(state);
    var gaps = [];
    var rotation = readRotation(host);
    if (rotation.error) {
      return { result: rotation.result, error: rotation.error };
    }
    if (rotation.known) {
      Object.keys(marks).forEach(function (fileID) {
        var mark = marks[fileID];
        if (rotation.present[fileID] || mark.rotation_state === "lost") {
          return;
        }
        mark.rotation_state = "lost";
        gaps.push({ from: mark.observed_at, to: now, reason: "eve_rotation_lost",
          detail: "rotation discarded the event file " + fileID + ", which the reader had a watermark in at byte " +
            mark.byte_offset + "; the records after that offset are gone and no endpoint can return them" });
        mark.observed_at = now;
      });
    }

    var result = { probe: "query_alerts", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("query_alerts", { form: host.pagination(1, provider.page_size) });
    if (response.error) {
      return { result: result, error: response.error, state: writeWatermarks(marks), gaps: gaps };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result, error: "collect: the alert feed answered " + response.outcome,
        state: writeWatermarks(marks), gaps: gaps };
    }
    var rows = response.decodable ? d.rows(response.value) : null;
    if (rows === null) {
      result.outcome = "undecodable";
      result.detail = "the alert feed body could not be read";
      return { result: result, error: "collect: reading the alert feed: the body holds no rows",
        state: writeWatermarks(marks), gaps: gaps };
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    if (rows.length === 0) {
      result.outcome = "healthy_empty";
      result.detail = "reachable with nothing to report, which for a narrow ruleset is a true reading and " +
        "not a fault";
      return { result: result, rows: { security_event: [] }, state: writeWatermarks(marks), gaps: gaps };
    }
    var normaliser = host.normaliser();
    if (normaliser.error) {
      return { result: result, error: normaliser.error, state: writeWatermarks(marks), gaps: gaps };
    }
    var events = [];
    var highestPerFile = {};
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
      var mark = marks[fileID];
      if (mark && filepos <= mark.byte_offset) {
        continue;
      }
      if (highestPerFile[fileID] === undefined || filepos > highestPerFile[fileID]) {
        highestPerFile[fileID] = filepos;
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
      events.push({
        provider_id: provider.id,
        provider_event_key: fileID + ":" + filepos,
        occurred_at: occurredAt.value,
        ingested_at: now,
        rule_identity: ruleIdentity,
        signature: signature === null ? "" : signature,
        event_action: normaliseEventAction(d.rawString(row, "alert_action")),
        normalised_severity: null,
        src_address: d.rawString(row, "src_ip"),
        dst_address: d.rawString(row, "dest_ip"),
        src_port: d.port(row, "src_port"),
        dst_port: d.port(row, "dest_port"),
        protocol: d.string(row, "proto"),
        in_interface_device: d.string(row, "in_iface")
      });
    }
    Object.keys(highestPerFile).forEach(function (fileID) {
      var sequence = /^[0-9]+$/.test(fileID) ? parseInt(fileID, 10) : null;
      var rotationState = "rotated";
      if (sequence !== null && rotation.known && rotation.highest !== null && sequence === rotation.highest) {
        rotationState = "current";
      }
      marks[fileID] = { byte_offset: highestPerFile[fileID], observed_at: now, rotation_state: rotationState };
    });
    return { result: result, rows: { security_event: events }, state: writeWatermarks(marks), gaps: gaps };
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

// askRuleInfo answers what the installed-rules grid says of each sid asked: found, with its
// classtype and its ruleset file, or not found. One request per sid: the grid filters on one value
// per field (SettingsController::searchInstalledRulesAction). A call that fails stops the answer
// there; the sids not answered are asked again at a later run. { rules, result, error }.
function askRuleInfo(host, ruleIdentities) {
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

exports.derivations = {
  // rule_info resolves, after each pass that stored events and on its cadence, the rules that fired
  // and have no answer fetched within rule_info_recheck_seconds, rule_info_per_pass of them at a
  // time in identity order, and writes what the grid said: a found rule with its category and its
  // source, or not found, each stamped with the run's instant. A grid that fails to answer stops the
  // run's asking; what was answered is written, and the rest is asked again at the next run.
  rule_info: function (host, run) {
    var settings = host.settings;
    var staleBefore = run.now - settings.rule_info_recheck_seconds;
    var due = host.query("SELECT DISTINCT e.provider_id, e.rule_identity FROM plugin_suricata.security_event AS e " +
      "WHERE NOT EXISTS (SELECT 1 FROM plugin_suricata.provider_rule_info AS r " +
      "WHERE r.provider_id = e.provider_id AND r.rule_identity = e.rule_identity AND r.fetched_at >= :stale_before) " +
      "ORDER BY e.provider_id, e.rule_identity LIMIT :bound",
      { stale_before: staleBefore, bound: settings.rule_info_per_pass });
    if (due.length === 0) {
      return { rows: { provider_rule_info: [] } };
    }
    var identities = due.map(function (row) { return row.rule_identity; });
    var asked = askRuleInfo(host, identities);
    if (asked.error) {
      host.log("rule info: " + asked.error + "; " + (identities.length - asked.rules.length) +
        " of the " + identities.length + " rule identities asked stay not fetched and are asked again");
    }
    var providerOf = {};
    due.forEach(function (row) { providerOf[row.rule_identity] = row.provider_id; });
    var rows = asked.rules.map(function (rule) {
      return {
        provider_id: providerOf[rule.rule_identity],
        rule_identity: rule.rule_identity,
        lookup_state: rule.lookup_state,
        normalised_severity: null,
        provider_severity: null,
        category: rule.category === undefined ? null : rule.category,
        rule_source: rule.rule_source === undefined ? null : rule.rule_source,
        fetched_at: run.now
      };
    });
    return { rows: { provider_rule_info: rows } };
  }
};

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  normalise_event_action: normaliseEventAction,
  rule_answer: ruleAnswer
};
