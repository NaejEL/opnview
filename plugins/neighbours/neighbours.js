// The firewall's neighbour tables: one implementation of the neighbour kind.
//
// What it reads, verified against opnsense/core 26.7.3:
//   - GET /api/diagnostics/interface/get_arp -- InterfaceController::getArpAction runs the configd
//     action `interface list arp json`, which is scripts/interfaces/list_arp.py: one row per
//     complete ARP entry, with mac, ip, intf, expired, expires, permanent, type, manufacturer and
//     hostname; the controller adds intf_description. It is read without `resolve`, so the
//     firewall runs no name lookup for it.
//   - GET /api/diagnostics/interface/get_ndp -- getNdpAction runs `interface list ndp json`, which
//     is scripts/interfaces/list_ndp.py: one row per complete NDP entry, with ip, mac, intf and
//     manufacturer; the controller adds intf_description.
//
// The fields read are mac, ip, intf and manufacturer: the hardware address, the address, the
// network device the entry was learned on, and the vendor string, which is context and never a
// basis for classifying a machine. What the KIND does with them -- the identity cascade at the MAC
// level, never making this firewall a client, leaving the neighbours of an upstream interface out
// -- is opnview's core (internal/collect/kinds/neighbours).
//
// AN ANSWER THAT CANNOT BE READ IS REPORTED, NEVER WORKED AROUND. A 404, a 403 or a body that holds
// no rows ends the read with the broker's outcome in the provider's availability, and nothing is
// inferred from any other data.

var d = opnview.decode;

// tables are the two endpoints, in the order a pass reads them.
var tables = [
  { endpoint: "arp", path: "/api/diagnostics/interface/get_arp" },
  { endpoint: "ndp", path: "/api/diagnostics/interface/get_ndp" }
];

// readTable reads one table: its rows, or the result and error the answer implies.
function readTable(host, table) {
  var result = { probe: table.endpoint, state: "unavailable", outcome: "request_not_made" };
  var response = host.call(table.endpoint);
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail;
    return { result: result, error: "collect: " + table.path + " answered " + response.outcome };
  }
  var rows = response.decodable ? d.rows(response.value) : null;
  if (rows === null) {
    result.outcome = "undecodable";
    result.detail = "the neighbour table could not be read";
    return { result: result, error: "collect: reading " + table.path + ": the body holds no rows" };
  }
  result.state = "reachable";
  result.outcome = rows.length === 0 ? "healthy_empty" : "healthy";
  result.separable = true;
  return { result: result, rows: rows };
}

// neighbourOf is one row as a neighbour_record, or null for a row with no address. The hardware
// address is handed over as the table spells it: the core normalises it, because the identity
// cascade that keys on it is the core's.
function neighbourOf(row) {
  var address = d.string(row, "ip");
  if (address === null) {
    return null;
  }
  return {
    address: address,
    mac: d.rawString(row, "mac"),
    interface_device: d.rawString(row, "intf"),
    vendor_hint: d.string(row, "manufacturer")
  };
}

exports.neighbour = {
  // probe reads the IPv4 table: it is the one every firewall has, and an answer that cannot be read
  // there is one the pass could not read either.
  probe: function (host) {
    var read = readTable(host, tables[0]);
    if (read.result.outcome === "request_not_made") {
      // The call itself could not be made: the probe could not finish.
      return { result: read.result, error: read.error };
    }
    // An answer that cannot be read is a state of the source, recorded with the broker's outcome.
    return { result: read.result };
  },

  // neighbours reads both tables. Either failing ends the read with its outcome: a pass that kept
  // one family would report a complete set it does not have.
  neighbours: function (host) {
    var neighbours = [];
    var result = null;
    for (var i = 0; i < tables.length; i++) {
      var read = readTable(host, tables[i]);
      if (read.error) {
        return { result: read.result, error: read.error };
      }
      for (var j = 0; j < read.rows.length; j++) {
        var neighbour = neighbourOf(read.rows[j]);
        if (neighbour !== null) {
          neighbours.push(neighbour);
        }
      }
      if (result === null || read.result.outcome === "healthy") {
        result = read.result;
      }
    }
    return { result: result, neighbours: neighbours };
  }
};

// The pure function above, exported for the plugin's own tests.
exports.helpers = {
  neighbour_of: neighbourOf
};
