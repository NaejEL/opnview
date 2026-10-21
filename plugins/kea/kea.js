// Kea: one implementation of the dhcp_lease kind.
//
// Everything here is a fact about Kea and about the way OPNsense exposes it
// (docs/opnsense-api-survey.md, data source 4). What the KIND does with the leases -- the
// identity cascade, resolving the interface, writing the generation -- is opnview's core
// (internal/collect, dhcplease.go).
//
// IT IS THE ONLY BACKEND THAT REPORTS A REAL VALIDITY START: `valid_lifetime` is published, so the
// start is `expire` minus it. Every other backend leaves the start absent rather than substitute
// one.

var d = opnview.decode;

// readLeaseRows reads one page of the standard grid-search envelope: what is the same for every
// readable lease backend, and the availability state the answer implies.
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

// leaseState maps Kea's `state` onto the closed vocabulary of the dhcp_lease contract. The control
// agent reports the state as a word or as its number; anything else is `unknown`, never a guess.
function leaseState(row) {
  var state = d.string(row, "state");
  if (state === null) {
    return "unknown";
  }
  switch (d.lower(state)) {
    case "active": case "assigned": case "1":
      return "active";
    case "expired": case "released": case "free": case "backup": case "0":
      return "expired";
    case "reserved": case "static":
      return "reserved";
  }
  return "unknown";
}

// decodeLease decodes one row of /api/kea/leases4/search (survey, data source 4, "Response shape":
// hostname, hwaddr, address, prefix_len, state, expire, valid_lifetime, if_name, if_descr, client_id,
// duid, iaid, mac_info) into a dhcp_lease_record, or null for a row with no address, which names no
// machine and keys nothing. The MAC is handed over as Kea spells it: the core normalises it, because
// the identity cascade that keys on it is the core's.
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
    lease_state: leaseState(row),
    interface_identifier: d.rawString(row, "if_name"),
    interface_device: "",
    starts_at: null,
    expires_at: d.epochPointer(row, "expire")
  };
}

exports.dhcp_lease = {
  // probe reads the service state and then the configured state behind it. Both are needed and
  // neither is enough: a running daemon with DHCPv4 switched off serves no client, and a
  // configured backend that is not running serves none either.
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
    var settings = host.readObject("dhcpv4");
    if (settings.error) {
      return { result: result, error: settings.error };
    }
    var configured = settings.ok && d.nestedFlag(settings.object, "dhcpv4", "general", "enabled") === true;
    if (!configured) {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "the service runs and dhcpv4.general.enabled is not set";
      return { result: result };
    }
    result.separable = true;
    return { result: result };
  },

  // leases reads the lease table from the running daemon through its control agent.
  // collect reads the lease table and answers one row per lease generation for the leases plugin's
  // table, which grants this plugin the table (ADR 0020 wave 4): what the KIND once did with the leases
  // in Go -- the identity cascade, the interface, the generation -- is the leases and clients plugins'
  // SQL now. The pass's page size and the provider's registry id come with the provider.
  collect: function (host, state, provider) {
    var read = readLeaseRows(host, "leases", "/api/kea/leases4/search", provider.page_size);
    if (read.error) {
      return { result: read.result, error: read.error };
    }
    var rows = [];
    for (var i = 0; i < read.rows.length; i++) {
      var row = read.rows[i];
      var lease = decodeLease(row);
      if (lease === null) {
        continue;
      }
      if (lease.expires_at !== null) {
        var lifetime = d.int(row, "valid_lifetime");
        if (lifetime !== null && lifetime > 0) {
          var startsAt = lease.expires_at - lifetime;
          lease.starts_at = startsAt < 0 ? 0 : startsAt;
        }
      }
      rows.push(leaseRow(lease, provider, host.now()));
    }
    return { result: read.result, rows: { "leases.dhcp_lease": rows }, state: {} };
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
