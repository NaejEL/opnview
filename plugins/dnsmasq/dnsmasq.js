// Dnsmasq: one implementation of four kinds. As a DHCP backend it is read; as a resolver it is
// registered, probed and never read, because OPNsense exposes neither its lookups, nor its cache,
// nor its local data through the API (docs/opnsense-api-survey.md, data sources 4 and 5, and
// "Resolver cache and local data, read from source for the resolver-cache cycle").
//
// The four share a product and nothing else -- one of them can be read and the others cannot --
// which is exactly why the seam is the kind rather than the product.

var d = opnview.decode;

// readLeaseRows reads one page of the standard grid-search envelope.
function readLeaseRows(host, endpoint, path, pageSize) {
  var result = { probe: endpoint, state: "unavailable", outcome: "request_not_made" };
  var response = host.call(endpoint, { form: host.pagination(1, pageSize) });
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail;
    return { result: result, error: "collect: " + path + " answered " + response.outcome };
  }
  var rows = response.decodable ? d.rows(response.value) : null;
  if (rows === null) {
    result.outcome = "undecodable";
    result.detail = "the lease table could not be read";
    return { result: result, error: "collect: reading " + path + ": the body holds no rows" };
  }
  result.state = "reachable";
  result.outcome = "healthy";
  result.separable = true;
  if (rows.length === 0) {
    result.outcome = "healthy_empty";
  }
  return { result: result, rows: rows };
}

// serviceResult probes the Dnsmasq service, which serves both DHCP and DNS. It returns the
// answer to give back when the probe ends there, or the result to go on from.
function serviceResult(host) {
  var result = { probe: "status" };
  var service = host.serviceState("status");
  if (service.error) {
    return { done: { result: result, error: service.error } };
  }
  result.state = service.state;
  result.outcome = service.outcome;
  result.detail = service.detail;
  if (!service.running) {
    return { done: { result: result } };
  }
  return { result: result };
}

// noReadProbe is the probe of a resolver read Dnsmasq does not offer: the service state, and when
// the service runs, the named absence instead. Never separable: there is nothing to read.
function noReadProbe(host, state, absence) {
  var result = { probe: "status" };
  var service = host.serviceState("status");
  if (service.error) {
    return { result: result, error: service.error };
  }
  result.state = service.state;
  result.outcome = service.outcome;
  result.detail = service.detail;
  if (service.running) {
    result.state = "unavailable";
    result.outcome = absence;
    result.detail = "the service runs, and " + state;
  }
  return { result: result };
}

var noCacheRead = "this resolver offers no cache read through the API";
var noLocalDataRead = "this resolver offers no local-data read through the API";

// decodeLease decodes one row of /api/dnsmasq/leases/search (survey, data source 4, "Response
// shape": hwaddr, address, hostname, client_id, expire, is_reserved, mac_info, and the interface
// under if_name, device and if_descr) into a dhcp_lease_record, or null for a row with no address,
// which names no machine and keys nothing. Dnsmasq reports no lease state beside is_reserved, so a
// lease that is not a reservation is `unknown` rather than a guess. The MAC is handed over as
// Dnsmasq spells it: the core normalises it, because the identity cascade that keys on it is the
// core's.
function decodeLease(row) {
  var address = d.string(row, "address");
  if (address === null) {
    return null;
  }
  return {
    address: address,
    mac: d.rawString(row, "hwaddr"),
    hostname: d.string(row, "hostname"),
    dhcp_client_id: d.string(row, "client_id"),
    duid: d.string(row, "duid"),
    iaid: d.string(row, "iaid"),
    vendor_hint: d.string(row, "mac_info"),
    lease_state: d.flag(row, "is_reserved") === true ? "reserved" : "unknown",
    interface_identifier: d.rawString(row, "if_name"),
    interface_device: d.rawString(row, "device"),
    starts_at: null,
    expires_at: d.epochPointer(row, "expire")
  };
}

exports.dhcp_lease = {
  // probe reads the service state and then whether the backend serves any range. Only the
  // EMPTINESS of the ranges is read: the ranges themselves are the firewall's addressing
  // configuration, which opnview neither stores nor interprets.
  probe: function (host) {
    var service = serviceResult(host);
    if (service.done) {
      return service.done;
    }
    var result = service.result;
    var settings = host.readObject("settings");
    if (settings.error) {
      return { result: result, error: settings.error };
    }
    var ranges = false;
    if (settings.ok) {
      var root = d.nested(settings.object, "dnsmasq");
      if (root !== null) {
        ranges = d.nonEmpty(root.dhcp_ranges);
      }
    }
    if (!ranges) {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "the service runs and dnsmasq.dhcp_ranges is empty, so it serves no lease";
      return { result: result };
    }
    result.separable = true;
    return { result: result };
  },

  // collect reads the lease table and answers one row per lease generation for the leases plugin's
  // table, which grants this plugin the table (ADR 0020 wave 4); the identity cascade and the interface
  // are the clients and leases plugins' SQL. The page size and the registry id come with the provider.
  collect: function (host, state, provider) {
    var read = readLeaseRows(host, "leases", "/api/dnsmasq/leases/search", provider.page_size);
    if (read.error) {
      return { result: read.result, error: read.error };
    }
    var rows = [];
    for (var i = 0; i < read.rows.length; i++) {
      var lease = decodeLease(read.rows[i]);
      if (lease !== null) {
        rows.push(leaseRow(lease, provider, host.now()));
      }
    }
    return { result: read.result, rows: { "leases.dhcp_lease": rows }, state: {} };
  }
};

exports.dns_lookup = {
  // One page, and no page is ever read; no window is ever requested.
  page_bound: 1,
  requested_span_seconds: 0,

  // probe reports the service state and whether query logging is on. opnview reads
  // dnsmasq.log_queries and NEVER writes it: turning it on is a manual operation the user
  // performs in the firewall's own interface. Whether logging is on or off, nothing can be read:
  // the grammar is the named absence either way.
  probe: function (host) {
    var service = serviceResult(host);
    if (service.done) {
      return service.done;
    }
    var result = service.result;
    var settings = host.readObject("settings");
    if (settings.error) {
      return { result: result, error: settings.error };
    }
    var logging = settings.ok && d.nestedFlag(settings.object, "dnsmasq", "log_queries") === true;
    result.state = "present_but_disabled";
    result.outcome = "log_grammar_not_recorded";
    if (logging) {
      result.detail = "query logging is on, but its log grammar is dnsmasq's own and is not recorded, " +
        "so opnview does not parse it";
    } else {
      result.detail = "query logging is off, and its log grammar is not recorded either, so opnview " +
        "does not parse it";
    }
    return { result: result };
  },

  // lookups refuses, with the reason: the lookups exist only as the free-text line of the generic
  // log endpoint, under a grammar that is dnsmasq's own and that nobody has recorded.
  lookups: function () {
    return {
      result: { probe: "settings", state: "present_but_disabled", outcome: "log_grammar_not_recorded" },
      unsupported: "this resolver exposes its lookups only as free text under a grammar nobody has recorded"
    };
  }
};

exports.resolver_cache = {
  probe: function (host) {
    return noReadProbe(host, noCacheRead, "no_cache_read");
  },
  cache: function () {
    return {
      result: { probe: "status", state: "unavailable", outcome: "no_cache_read" },
      unsupported: noCacheRead
    };
  }
};

exports.resolver_local_data = {
  probe: function (host) {
    return noReadProbe(host, noLocalDataRead, "no_local_data_read");
  },
  local_data: function () {
    return {
      result: { probe: "status", state: "unavailable", outcome: "no_local_data_read" },
      unsupported: noLocalDataRead
    };
  }
};

// leaseRow is the row of plugin_leases.dhcp_lease one decoded lease becomes: the provider's registry id
// and key, the lease as decoded, the interface as the server names it, and the pass's instant. The
// hardware address is lower-cased; one that is not six colon-separated octets is stored as absent.
function leaseRow(lease, provider, now) {
  var mac = lease.mac === null || lease.mac === undefined ? null : d.lower(lease.mac);
  if (mac !== null && !/^[0-9a-f]{2}(:[0-9a-f]{2}){5}$/.test(mac)) {
    mac = null;
  }
  return {
    provider_id: provider.id,
    backend: provider.key,
    address: lease.address,
    mac: mac,
    hostname: lease.hostname,
    dhcp_client_id: lease.dhcp_client_id,
    duid: lease.duid === undefined ? null : lease.duid,
    iaid: lease.iaid === undefined ? null : lease.iaid,
    vendor_hint: lease.vendor_hint,
    lease_state: lease.lease_state,
    interface_identifier: lease.interface_identifier,
    interface_device: lease.interface_device === "" ? null : lease.interface_device,
    starts_at: lease.starts_at,
    expires_at: lease.expires_at,
    observed_at: now
  };
}
