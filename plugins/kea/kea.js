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
  leases: function (host, pageSize) {
    var read = readLeaseRows(host, "leases", "/api/kea/leases4/search", pageSize);
    if (read.error) {
      return { result: read.result, error: read.error };
    }
    var leases = [];
    for (var i = 0; i < read.rows.length; i++) {
      var row = read.rows[i];
      var lease = opnview.dhcp_lease.decodeLease(row);
      if (lease === null) {
        continue;
      }
      // Where `valid_lifetime` is absent the start stays absent rather than falling back to the
      // expiry: "this row does not know" is expressible.
      if (lease.expires_at !== null) {
        var lifetime = d.int(row, "valid_lifetime");
        if (lifetime !== null && lifetime > 0) {
          var startsAt = lease.expires_at - lifetime;
          lease.starts_at = startsAt < 0 ? 0 : startsAt;
        }
      }
      leases.push(lease);
    }
    return { result: read.result, leases: leases };
  }
};
