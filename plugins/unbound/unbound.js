// Unbound: one implementation of three kinds -- the resolver's query report (dns_lookup), its
// cache (resolver_cache) and its local data (resolver_local_data).
//
// Everything here is a fact about Unbound and about OPNsense's Unbound controllers, read at
// opnsense/core 26.7.3 (docs/opnsense-api-survey.md, data source 5, and "Resolver cache and local
// data, read from source for the resolver-cache cycle"). The lookups, the records and the site
// attributions are this plugin's own tables (ADR 0019, ADR 0020 wave 4): the passes below store
// them, a gap of the ring buffer is reported as such, and what came back is never presented as
// coverage of a window; which machine a logged host name is, and which site a flow was for, are the
// plugin's derivations.

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

// readLookupPage reads one page of the ring buffer: { result, lookups, error }.
function readLookupPage(host, page) {
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

// isAddress reports whether the report's client is an address rather than a host name a reverse
// lookup returned: four dotted decimal octets, or a text with a colon, which no host name holds.
function isAddress(text) {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(text) || text.indexOf(":") >= 0;
}

// lookupRow is the row of dns_resolution one lookup becomes. A client logged as a host name is kept
// as both the address and the name, unresolved, until the derivation resolve names the machine.
function lookupRow(lookup, provider, now) {
  var hostname = isAddress(lookup.client_address) ? null : lookup.client_address;
  return {
    lookup_key: lookup.deduplication_key,
    client_address: lookup.client_address,
    client_hostname: hostname,
    client_resolution: hostname === null ? "logged_address" : "unknown_hostname",
    client_family: lookup.client_family,
    domain: lookup.domain,
    resolver: provider.key,
    action: lookup.action,
    answer_source: lookup.answer_source,
    rcode: lookup.rcode,
    dnssec_status: lookup.dnssec_status,
    blocklist_name: lookup.blocklist_name,
    looked_up_at: lookup.looked_up_at,
    ingested_at: now
  };
}

// THE RESOLVER'S RECORDS. A record of the cache dump or of the local data is normalised -- the name
// lower-cased with no trailing dot, an A or AAAA value checked to be an address of its family, a PTR
// owner decoded to the address it names, a cache TTL a whole number of seconds -- and stored as an
// observation covering an interval: a record seen at two consecutive polls is one observation
// extended, a record seen again after a gap is a new one. The types kept are A, AAAA and CNAME of
// the cache, A, AAAA and PTR of the local data; every other type is counted and not stored.

// dnsName is a name as the records store it: lower-cased, trailing dot removed.
function dnsName(name) {
  return String(name || "").trim().replace(/\.+$/, "").toLowerCase();
}

// hostLabel is the first label of a host name, lower-cased, a trailing dot removed: what a host name
// the resolver logged as a lookup's client is matched on.
function hostLabel(name) {
  var trimmed = String(name || "").replace(/\.+$/, "");
  var dot = trimmed.indexOf(".");
  return (dot >= 0 ? trimmed.slice(0, dot) : trimmed).toLowerCase();
}

// canonicalAddress is an address text with no zone, lower-cased, or null for a text that is not one.
function canonicalAddress(text) {
  var value = String(text || "").trim().toLowerCase();
  if (value.indexOf("%") >= 0) {
    return null;
  }
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) {
    var octets = value.split(".");
    for (var i = 0; i < 4; i++) {
      if (parseInt(octets[i], 10) > 255 || (octets[i].length > 1 && octets[i].charAt(0) === "0")) {
        return null;
      }
    }
    return octets.map(function (o) { return String(parseInt(o, 10)); }).join(".");
  }
  if (/^[0-9a-f:]+(:\d{1,3}(\.\d{1,3}){3})?$/.test(value) && value.indexOf(":") >= 0 && value.indexOf(":::") < 0 &&
    value.split("::").length <= 2) {
    return value;
  }
  return null;
}

// reverseNameAddress decodes the address a PTR owner names, or null.
function reverseNameAddress(name) {
  if (name.slice(-13) === ".in-addr.arpa") {
    var labels = name.slice(0, -13).split(".");
    if (labels.length !== 4) {
      return null;
    }
    var octets = [];
    for (var i = 3; i >= 0; i--) {
      if (!/^\d{1,3}$/.test(labels[i]) || parseInt(labels[i], 10) > 255 || (labels[i].length > 1 && labels[i].charAt(0) === "0")) {
        return null;
      }
      octets.push(String(parseInt(labels[i], 10)));
    }
    return octets.join(".");
  }
  if (name.slice(-9) === ".ip6.arpa") {
    var nibbles = name.slice(0, -9).split(".");
    if (nibbles.length !== 32) {
      return null;
    }
    var groups = [];
    for (var g = 0; g < 8; g++) {
      var group = "";
      for (var n = 0; n < 4; n++) {
        var nibble = nibbles[31 - (g * 4 + n)];
        if (!/^[0-9a-f]$/.test(nibble)) {
          return null;
        }
        group += nibble;
      }
      groups.push(group.replace(/^0+(?=.)/, ""));
    }
    return compressIPv6(groups);
  }
  return null;
}

// compressIPv6 writes eight groups as the canonical text: the longest run of zero groups, of two or
// more, written as ::.
function compressIPv6(groups) {
  var bestStart = -1, bestLength = 0, start = -1;
  for (var i = 0; i <= 8; i++) {
    if (i < 8 && groups[i] === "0") {
      if (start < 0) { start = i; }
    } else if (start >= 0) {
      if (i - start > bestLength) { bestStart = start; bestLength = i - start; }
      start = -1;
    }
  }
  if (bestLength < 2) {
    return groups.join(":");
  }
  var head = groups.slice(0, bestStart).join(":");
  var tail = groups.slice(bestStart + bestLength).join(":");
  return head + "::" + tail;
}

// normaliseRecords turns the raw records of one read into the observations to store, counting what
// was not stored and why: { records, counts }.
function normaliseRecords(raw, heldIn) {
  var kept = heldIn === "cache" ? { A: true, AAAA: true, CNAME: true } : { A: true, AAAA: true, PTR: true };
  var counts = { total: 0, stored: 0, byType: {}, badTTL: 0, badName: 0, badValue: 0 };
  var records = [];
  for (var i = 0; i < raw.length; i++) {
    var record = raw[i];
    counts.total++;
    var rrtype = String(record.rrtype || "").trim();
    counts.byType[rrtype] = (counts.byType[rrtype] || 0) + 1;
    if (!kept[rrtype]) {
      continue;
    }
    var owner = dnsName(record.owner_name);
    if (owner === "") {
      counts.badName++;
      continue;
    }
    var normalised = { owner_name: owner, rrtype: rrtype, ttl: 0, address: null, host_label: null };
    if (heldIn === "cache") {
      if (record.ttl_shape !== "string" || !/^\d+$/.test(String(record.ttl || "").trim())) {
        counts.badTTL++;
        continue;
      }
      normalised.ttl = parseInt(String(record.ttl).trim(), 10);
    }
    if (rrtype === "A" || rrtype === "AAAA") {
      var address = canonicalAddress(record.value);
      if (address === null || (rrtype === "A") !== (address.indexOf(":") < 0)) {
        counts.badValue++;
        continue;
      }
      normalised.value = address;
      normalised.address = address;
      if (heldIn === "local_data") {
        normalised.host_label = hostLabel(owner);
      }
    } else if (rrtype === "CNAME") {
      var target = dnsName(record.value);
      if (target === "") {
        counts.badValue++;
        continue;
      }
      normalised.value = target;
    } else {
      var ptrTarget = dnsName(record.value);
      var named = reverseNameAddress(owner);
      if (named === null || ptrTarget === "") {
        counts.badValue++;
        continue;
      }
      normalised.value = ptrTarget;
      normalised.address = named;
      normalised.host_label = hostLabel(ptrTarget);
    }
    if (heldIn === "local_data" && normalised.host_label === "") {
      counts.badValue++;
      continue;
    }
    records.push(normalised);
    counts.stored++;
  }
  return { records: records, counts: counts };
}

// observationRows is the extend-or-insert over what the previous poll saw: a record the previous poll
// saw is its observation extended to this poll, any other starts one. { rows, inserted, extended }
function observationRows(host, provider, heldIn, records, polledAt) {
  var previousRead = host.query("SELECT read_at FROM plugin_unbound.resource_record_read WHERE provider_id = :p",
    { p: provider.id });
  var previous = previousRead.length > 0 ? Math.min(previousRead[0].read_at, polledAt) : null;
  var coveredFrom = previous === null ? polledAt : previous;
  var current = {};
  if (previous !== null) {
    host.query("SELECT owner_name, rrtype, value, first_seen_at, covered_from_at, covered_until_at " +
      "FROM plugin_unbound.resource_record_observation WHERE provider_id = :p AND last_seen_at = :prev " +
      "ORDER BY first_seen_at", { p: provider.id, prev: previous }).forEach(function (row) {
      // Of two observations the previous poll saw, the one started last is extended.
      current[row.owner_name + "\u001f" + row.rrtype + "\u001f" + row.value] = row;
    });
  }
  var deduped = {};
  records.forEach(function (record) {
    var key = record.owner_name + "\u001f" + record.rrtype + "\u001f" + record.value;
    if (!deduped[key] || record.ttl > deduped[key].ttl) {
      deduped[key] = record;
    }
  });
  var rows = [];
  var inserted = 0, extended = 0;
  Object.keys(deduped).sort().forEach(function (key) {
    var record = deduped[key];
    var until = heldIn === "cache" && record.ttl > 0 ? polledAt + record.ttl : polledAt;
    var held = current[key];
    var row = { provider_id: provider.id, held_in: heldIn, owner_name: record.owner_name, rrtype: record.rrtype,
      value: record.value, address: record.address, host_label: record.host_label };
    if (held) {
      row.first_seen_at = held.first_seen_at;
      row.covered_from_at = held.covered_from_at;
      row.last_seen_at = Math.max(held.first_seen_at, polledAt);
      row.covered_until_at = Math.max(held.covered_until_at, until);
      extended++;
    } else {
      row.first_seen_at = polledAt;
      row.covered_from_at = Math.min(coveredFrom, polledAt);
      row.last_seen_at = polledAt;
      row.covered_until_at = until;
      inserted++;
    }
    rows.push(row);
  });
  return { rows: rows, inserted: inserted, extended: extended };
}

// collectRecords is one pass of the cache or of the local data into the plugin's table.
function collectRecords(host, provider, heldIn, endpoint, path, ownerField, messageCacheReferences) {
  var polledAt = host.now();
  var read = readRecords(host, endpoint, path, ownerField, messageCacheReferences);
  if (read.error) {
    return { result: read.result, error: read.error };
  }
  var normalised = normaliseRecords(read.records, heldIn);
  var stored = observationRows(host, provider, heldIn, normalised.records, polledAt);
  var counts = normalised.counts;
  var types = Object.keys(counts.byType).sort(function (a, b) {
    return counts.byType[b] - counts.byType[a] || (a < b ? -1 : 1);
  }).map(function (type) { return (type === "" ? "no type" : type) + " " + counts.byType[type]; });
  var detail = "read " + counts.total + " records of " + (heldIn === "cache" ? "the cache" : "the local data") +
    " (" + read.response_bytes + " bytes)" + (types.length > 0 ? ": " + types.join(", ") : "") +
    "; kept " + counts.stored + " of the " + (heldIn === "cache" ? "A, AAAA, CNAME" : "A, AAAA, PTR") +
    " records (" + stored.inserted + " observations started, " + stored.extended + " extended); every other type " +
    "is counted and not stored";
  (read.not_records || []).forEach(function (entry) {
    if (entry.count > 0) { detail += "; skipped " + entry.count + " " + entry.wording; }
  });
  if (counts.badTTL > 0) { detail += "; skipped " + counts.badTTL + " whose ttl is not a whole number of seconds"; }
  if (counts.badValue > 0) { detail += "; skipped " + counts.badValue + " whose value is not valid for their type"; }
  if (counts.badName > 0) { detail += "; skipped " + counts.badName + " with no owner name"; }
  read.result.detail = detail;
  read.result.outcome = counts.total === 0 ? "healthy_empty" : "healthy";
  return { result: read.result, rows: { resource_record_observation: stored.rows,
    resource_record_read: [{ provider_id: provider.id, read_at: polledAt }] }, state: {} };
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

  // readPage reads one page of the ring buffer: { result, lookups, error }.
  readPage: function (host, page) {
    return readLookupPage(host, page);
  },

  // collect walks the ring buffer newest first, page by page, until a page brings nothing new or
  // the bound is reached, and stores the lookups past what the table holds: a lookup whose client
  // the report logged as a host name is stored under that name, unresolved, for the derivation
  // resolve to name the machine; a refused lookup carries its list's name, which the same derivation
  // links. An interval that rotated out of the buffer before it was read is a gap, since the
  // endpoint honours no window and nothing can recover it.
  collect: function (host, state, provider) {
    var now = host.now();
    var purge = host.datasets.latest("retention_purge");
    var purgedBefore = purge && typeof purge.purged_before === "number" ? purge.purged_before : null;
    var newest = host.query("SELECT max(looked_up_at) AS newest FROM plugin_unbound.dns_resolution", {})[0].newest;
    var rows = [];
    var pushed = {};
    var lastResult = null;
    var total = 0;
    var outside = 0;
    var oldestSeen = null;
    var failure = null;
    for (var page = 1; page <= pageBound; page++) {
      var read = readLookupPage(host, page);
      lastResult = read.result;
      if (read.error) {
        if (page === 1) {
          return { result: read.result, error: read.error };
        }
        failure = read.error;
        break;
      }
      if (read.lookups.length === 0) {
        break;
      }
      total += read.lookups.length;
      var parameters = {};
      var marks = [];
      read.lookups.forEach(function (lookup, index) {
        parameters["k" + index] = lookup.deduplication_key;
        marks.push(":k" + index);
      });
      var held = {};
      host.query("SELECT lookup_key FROM plugin_unbound.dns_resolution WHERE lookup_key IN (" + marks.join(", ") + ")",
        parameters).forEach(function (row) { held[row.lookup_key] = true; });
      var fresh = 0;
      for (var i = 0; i < read.lookups.length; i++) {
        var lookup = read.lookups[i];
        if (oldestSeen === null || lookup.looked_up_at < oldestSeen) {
          oldestSeen = lookup.looked_up_at;
        }
        if (requestedSpan > 0 && (lookup.looked_up_at < now - requestedSpan || lookup.looked_up_at > now)) {
          outside++;
        }
        if (held[lookup.deduplication_key] || pushed[lookup.deduplication_key] ||
          (purgedBefore !== null && lookup.looked_up_at < purgedBefore)) {
          continue;
        }
        pushed[lookup.deduplication_key] = true;
        fresh++;
        rows.push(lookupRow(lookup, provider, now));
      }
      if (fresh === 0) {
        break;
      }
    }
    var result = lastResult;
    result.state = "reachable";
    result.outcome = total === 0 ? "healthy_empty" : "healthy";
    result.separable = true;
    result.detail = total === 0 ?
      "reachable and the query report returned no lookup, which is not an absence of lookups" :
      "read " + total + " of the most recent lookups; the endpoint ignores timeStart and timeEnd, so this is not " +
      "coverage of the " + requestedSpan + "-second window that was requested" +
      (outside > 0 ? " (" + outside + " of the rows fell outside it, which is that behaviour showing)" : "") +
      (failure ? "; a later page failed: " + failure : "");
    var gaps = [];
    if (newest !== null && oldestSeen !== null && oldestSeen > newest) {
      gaps.push({ from: newest, to: oldestSeen, reason: "resolver_window_not_honoured",
        detail: "the oldest of the " + total + " lookups read is newer than the newest lookup stored, so the interval " +
          "between them rotated out of the endpoint's ring buffer before it was read; the endpoint honours no " +
          "window, so nothing can recover it" });
    }
    return { result: result, rows: { dns_resolution: rows }, state: {}, gaps: gaps };
  }
};

exports.resolver_cache = {
  probe: recordsProbe,
  // collect reads the dump -- every record Unbound holds and has not expired, `host` naming the
  // owner -- into the plugin's observations.
  collect: function (host, state, provider) {
    return collectRecords(host, provider, "cache", "dump_cache", "/api/unbound/diagnostics/dumpcache", "host", true);
  }
};

exports.resolver_local_data = {
  probe: recordsProbe,
  // collect reads the local data, host overrides among them: OPNsense writes host overrides as
  // `local-data:` lines (src/etc/inc/plugins.inc.d/unbound.inc), so this is where a host with no
  // DHCP lease is named. `name` names the owner, and `ttl` is the configured TTL.
  collect: function (host, state, provider) {
    return collectRecords(host, provider, "local_data", "list_local_data", "/api/unbound/diagnostics/listlocaldata",
      "name", false);
  }
};

// The pure functions above, exported for the plugin's own tests.
exports.helpers = {
  lookup_key: lookupKey,
  query_family: queryFamily,
  normalise_action: normaliseAction,
  canonical_address: canonicalAddress,
  reverse_name_address: reverseNameAddress,
  normalise_records: normaliseRecords
};
