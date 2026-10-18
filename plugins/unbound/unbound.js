// Unbound: one implementation of three kinds -- the resolver's query report (dns_lookup), its
// cache (resolver_cache) and its local data (resolver_local_data).
//
// Everything here is a fact about Unbound and about OPNsense's Unbound controllers, read at
// opnsense/core 26.7.3 (docs/opnsense-api-survey.md, data source 5, and "Resolver cache and local
// data, read from source for the resolver-cache cycle"). What the KINDS do with the records --
// resolving the querying machine, detecting a gap, storing the A, AAAA and CNAME records as
// observations, refusing to present what came back as coverage of a window -- is opnview's core.

var d = opnview.decode;

// The query report.
//
// TWO MEASURED FACTS ABOUT THIS ENDPOINT, PULLING IN OPPOSITE DIRECTIONS. THE CALL FORM IS
// LOAD-BEARING: it keeps timeStart and timeEnd only if is_int() holds, which they can be only
// inside a Content-Type: application/json body; any other encoding silently degrades to the
// unwindowed branch with HTTP 200 and no diagnostic. AND THE WINDOW IS IGNORED ANYWAY: measured on
// a live firewall, a 5-minute request and a 24-hour request returned the same ~410-second span,
// `total` is 1000 whatever is asked, and pages walk further back. It is a ring buffer of the last
// 1000 lookups. So the correct form is sent, and the kind never presents what comes back as
// coverage of the window asked for.

// pageSize is how many lookups one page asks for; pageBound bounds how far back one pass walks:
// the buffer holds 1000 records whatever is asked, so five pages of 200 reach the whole of it.
var pageSize = 200;
var pageBound = 5;
// requestedSpan is the window asked for. It is sent, and it is not believed: it exists so the call
// is the correct form, and so the out-of-window rows a pass receives are visible as evidence that
// the endpoint still ignores it.
var requestedSpan = 3600;

// keyPart is the provider key as it is composed into a de-duplication key. It must never change:
// a stored key would no longer match.
var keyPart = "unbound";

// lookupKey composes the de-duplication key from the row's own content. `uuid` is null on every
// row the firewall returns (measured 2026-09-27), and the endpoint is a ring buffer with no
// cursor, so every pass re-reads rows already stored: without a content-addressed key each pass
// would insert them again. THE TRADE, STATED: two genuinely distinct lookups by the same client,
// for the same domain, with the same verdict, in the same second collapse into one row -- the
// direction that cannot invent traffic. The fields are joined with a unit separator, which cannot
// occur in any of them, and hashed so the column stays bounded.
function lookupKey(row, lookedUpAt) {
  var parts = [
    keyPart,
    String(lookedUpAt),
    d.rawString(row, "client"),
    d.rawString(row, "domain"),
    d.rawString(row, "action"),
    d.rawString(row, "source"),
    d.rawString(row, "rcode"),
    d.rawString(row, "dnssec_status"),
    d.rawString(row, "blocklist")
  ];
  return "content:" + opnview.sha256Hex(parts.join("\u001f"));
}

// queryFamily turns a row's `family` into the querying address's family: ip4 is 4, ip6 is 6,
// anything else no evidence. The field survives the host name the report puts in `client`
// (opnsense/core 26.7.3, scripts/unbound/stats.py; survey, "The querying family behind a logged
// host name, read from source for step 5B").
function queryFamily(value) {
  switch (value) {
    case "ip4": return 4;
    case "ip6": return 6;
    default: return null;
  }
}

// The two record reads share the diagnostics controller: Unbound/Api/DiagnosticsController.php
// runs `unbound dumpcache` and `unbound listlocaldata`, wrapper.py over unbound-control, and
// answers {"status": "ok", "data": [...]}, or {"status": "failed"} with no data when the script
// printed nothing -- which it does when Unbound is not running. The path is admitted by the ACL
// page "Services: Unbound" (models/OPNsense/Unbound/ACL/ACL.xml).
var privilege = "\"Services: Unbound\" (page-services-unbound)";

// A ROW WHOSE TTL IS NULL IS NOT A RECORD, in the cache dump: it is a line of the dump's
// message-cache section naming an rrset of a cached answer (dump_msg_ref, Unbound's
// daemon/cachedump.c), with no TTL and no record data, which wrapper.py's regular expression
// matches with its optional TTL group absent. It is counted under this wording.
var messageCacheReferenceWording = "message-cache references: lines of the dump's message-cache " +
  "section naming an rrset of a cached answer, with no ttl and no record data, so not records " +
  "and not counted among them";

// recordsProbe is the probe both record reads share: the service runs and unbound.general.enabled
// is set. The cache exists whenever the resolver runs, so the two are what makes it separable.
function recordsProbe(host) {
  var result = { probe: "status" };
  var service = host.serviceState("status");
  if (service.error) {
    return { result: result, error: service.error };
  }
  result.state = service.state;
  result.outcome = service.outcome;
  result.detail = service.detail;
  if (!service.running) {
    return { result: result };
  }
  var settings = host.readObject("settings");
  if (settings.error) {
    return { result: result, error: settings.error };
  }
  if (!settings.ok || d.nestedFlag(settings.object, "unbound", "general", "enabled") !== true) {
    result.state = "present_but_disabled";
    result.outcome = "present_but_disabled";
    result.detail = "the service runs and unbound.general.enabled is not set";
    return { result: result };
  }
  result.separable = true;
  return { result: result };
}

// envelope decodes the {status, data} envelope the way encoding/json reads it into a status
// pointer and a data list of objects: a body that is not an object, a status that is not a
// string, and a data that is not a list of objects are undecodable; an absent or null status
// is undecodable too, and an absent or null data list is reported on its own.
function envelope(response) {
  var body;
  try {
    if (!response.decodable) {
      throw new Error("the body is not JSON");
    }
    body = response.value;
  } catch (e) {
    return null;
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return null;
  }
  var status = body.status;
  if (status === undefined || status === null || typeof status !== "string") {
    return null;
  }
  var data = body.data;
  if (data === undefined || data === null) {
    return { status: status, data: null };
  }
  if (!Array.isArray(data)) {
    return null;
  }
  for (var i = 0; i < data.length; i++) {
    if (data[i] !== null && (typeof data[i] !== "object" || Array.isArray(data[i]))) {
      return null;
    }
  }
  return { status: status, data: data };
}

// readRecords reads one of the controller's record lists. Each absence is named on its own and
// none stores anything.
function readRecords(host, endpoint, path, ownerField, messageCacheReferences) {
  var result = { probe: endpoint, state: "unavailable", outcome: "request_not_made" };
  var response = host.call(endpoint);
  if (response.error) {
    return { result: result, error: response.error };
  }
  result.outcome = response.availability_outcome;
  switch (response.outcome) {
    case "ok":
      break;
    case "forbidden":
      if (response.status_code === 401) {
        result.detail = opnview.authenticationFailed + " for " + path + "; nothing was stored";
        return { result: result, error: "collect: " + path + ": authentication failed" };
      }
      result.detail = "denied (HTTP " + response.status_code + "): the API key's user lacks the privilege " +
        privilege + ", whose pattern api/unbound/* admits " + path + "; nothing was stored";
      return { result: result, error: "collect: " + path + " was refused" };
    case "not_found":
      result.detail = "not found (HTTP 404): this firewall does not answer " + path + "; nothing was stored";
      return { result: result, error: "collect: " + path + " was not found" };
    default:
      result.detail = path + " answered " + response.outcome + ": " + response.detail + "; nothing was stored";
      return { result: result, error: "collect: " + path + " answered " + response.outcome };
  }

  var decoded = envelope(response);
  if (decoded === null) {
    result.outcome = "undecodable";
    result.detail = "undecodable: the answer of " + path + " is not the {status, data} envelope the " +
      "controller returns; nothing was stored";
    return { result: result, error: "collect: reading " + path + ": the envelope is not {status, data}" };
  }
  if (decoded.status !== "ok") {
    result.state = "unavailable";
    result.outcome = "status_not_ok";
    result.detail = "the resolver answered status " + JSON.stringify(decoded.status) + ": the controller " +
      "answers \"failed\" when the script printed nothing, which it does when Unbound is not running; " +
      "nothing was stored";
    return { result: result, error: "collect: " + path + " answered status " + JSON.stringify(decoded.status) };
  }
  if (decoded.data === null) {
    result.outcome = "undecodable";
    result.detail = "undecodable: " + path + " answered status \"ok\" with no data list; nothing was stored";
    return { result: result, error: "collect: " + path + " answered no data list" };
  }

  var records = [];
  var references = 0;
  for (var i = 0; i < decoded.data.length; i++) {
    var row = decoded.data[i] || {};
    // `type` is the record's class and is never read: the type is `rrtype`. The ttl is read with
    // its shape, because in the cache dump a null one is a message-cache reference.
    var ttl = d.textField(row, "ttl");
    if (messageCacheReferences && ttl.shape === "null") {
      references++;
      continue;
    }
    records.push({
      owner_name: d.rawString(row, ownerField),
      ttl: ttl.text,
      ttl_shape: ttl.shape,
      rrtype: d.rawString(row, "rrtype"),
      value: d.rawString(row, "value")
    });
  }
  var answer = { result: result, records: records, response_bytes: response.body_bytes };
  if (references > 0) {
    answer.not_records = [{ count: references, wording: messageCacheReferenceWording }];
  }
  result.state = "reachable";
  result.outcome = "healthy";
  result.separable = true;
  return answer;
}

// normaliseAction maps the query report's `action` onto the closed vocabulary of the dns_lookup
// contract. Anything else is `unknown`: a lookup whose verdict opnview cannot name must not be
// counted as allowed.
function normaliseAction(raw) {
  switch (d.lower(raw)) {
    case "pass": case "passed": case "allowed":
      return "pass";
    case "block": case "blocked":
      return "block";
    case "drop": case "dropped":
      return "drop";
  }
  return "unknown";
}

exports.dns_lookup = {
  page_bound: pageBound,
  requested_span_seconds: requestedSpan,

  // probe reads the service state, the configured state, and whether query reporting is on.
  // Reporting off is present-but-disabled -- a setting for the user to turn on -- and is never an
  // absence of lookups. All three have to hold, so only the combination makes it separable.
  probe: function (host) {
    var result = { probe: "status" };
    var service = host.serviceState("status");
    if (service.error) {
      return { result: result, error: service.error };
    }
    result.state = service.state;
    result.outcome = service.outcome;
    result.detail = service.detail;
    if (!service.running) {
      return { result: result };
    }
    var settings = host.readObject("settings");
    if (settings.error) {
      return { result: result, error: settings.error };
    }
    var configured = settings.ok && d.nestedFlag(settings.object, "unbound", "general", "enabled") === true;
    if (!configured) {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "the service runs and unbound.general.enabled is not set";
      return { result: result };
    }
    var overview = host.readObject("is_enabled");
    if (overview.error) {
      return { result: result, error: overview.error };
    }
    if (!overview.ok || d.flag(overview.object, "enabled") !== true) {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "query reporting is off, which is a setting to turn on and not an absence of lookups";
      return { result: result };
    }
    result.separable = true;
    return { result: result };
  },

  // lookups reads one page of the ring buffer.
  lookups: function (host, page) {
    var result = { probe: "search_queries", state: "unavailable", outcome: "request_not_made" };
    var now = host.now();
    // Integers, in a JSON body: a string would reach the controller as a PHP string, is_int()
    // would fail, and the call would silently degrade to the unwindowed branch.
    var response = host.call("search_queries", {
      json: { current: page, rowCount: pageSize, timeStart: now - requestedSpan, timeEnd: now }
    });
    if (response.error) {
      return { result: result, error: response.error };
    }
    if (!response.ok) {
      result.outcome = response.availability_outcome;
      result.detail = response.detail;
      return { result: result, error: "collect: the resolver query report answered " + response.outcome };
    }
    var rows = response.decodable ? d.rows(response.value) : null;
    if (rows === null) {
      result.outcome = "undecodable";
      result.detail = "the query report body could not be read";
      return { result: result, error: "collect: reading the resolver query report: the body holds no rows" };
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;

    var lookups = [];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var lookedUpAt = d.epoch(row, "time");
      if (lookedUpAt === null) {
        continue;
      }
      var domain = d.string(row, "domain");
      if (domain === null) {
        continue;
      }
      var action = normaliseAction(d.rawString(row, "action"));
      var blocklistName = "";
      if (action === "block" || action === "drop") {
        // The endpoint does not attribute every block. An unattributed one stays "blocked, list
        // not recorded", which is its own state, so the name is taken only when given.
        blocklistName = d.rawString(row, "blocklist");
      }
      lookups.push({
        deduplication_key: lookupKey(row, lookedUpAt),
        client_address: d.rawString(row, "client"),
        client_family: queryFamily(d.rawString(row, "family")),
        domain: domain,
        action: action,
        answer_source: d.string(row, "source"),
        rcode: d.string(row, "rcode"),
        dnssec_status: d.string(row, "dnssec_status"),
        blocklist_name: blocklistName,
        looked_up_at: lookedUpAt
      });
    }
    return { result: result, lookups: lookups };
  }
};

exports.resolver_cache = {
  probe: recordsProbe,
  // cache reads the dump: every record Unbound holds and has not expired, `host` naming the owner.
  cache: function (host) {
    return readRecords(host, "dump_cache", "/api/unbound/diagnostics/dumpcache", "host", true);
  }
};

exports.resolver_local_data = {
  probe: recordsProbe,
  // local_data reads the local data, host overrides among them: OPNsense writes host overrides as
  // `local-data:` lines (src/etc/inc/plugins.inc.d/unbound.inc), so this is where a host with no
  // DHCP lease is named. `name` names the owner, and `ttl` is the configured TTL.
  local_data: function (host) {
    return readRecords(host, "list_local_data", "/api/unbound/diagnostics/listlocaldata", "name", false);
  }
};

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  lookup_key: lookupKey,
  query_family: queryFamily,
  normalise_action: normaliseAction
};
