// A third party's plugin: "REPORT <src> <src_port> <dst> <dst_port> <bytes> <packets> <estimated>" datagrams,
// decoded here into the plugin's own table, and a derivation counting them as the measure example_reported
// of the logged connections they describe.

exports.push = {
  // One datagram is one report, or nothing when it is not one.
  report: function (message) {
    var parts = message.text.trim().split(" ");
    if (parts.length !== 8 || parts[0] !== "REPORT") {
      return [];
    }
    return [{
      src_address: parts[1], src_port: parseInt(parts[2], 10),
      dst_address: parts[3], dst_port: parseInt(parts[4], 10),
      bytes: parseInt(parts[5], 10), packets: parseInt(parts[6], 10),
      estimated: parts[7] === "1" ? 1 : 0
    }];
  }
};

// tuple is a report's or a leg's four-part key.
function tuple(srcAddress, srcPort, dstAddress, dstPort) {
  return [srcAddress, srcPort, dstAddress, dstPort].join("|");
}

// direction is one direction's row from the reports counting it: reported, or unreported with nothing.
function direction(leg, name, reports) {
  var row = { connection: leg.flow_id, connection_start_at: leg.observed_at, direction: name,
    state: "unreported", bytes: 0, packets: 0, records: 0, markers: [] };
  reports.forEach(function (report) {
    row.state = "reported";
    row.bytes += report.bytes;
    row.packets += report.packets;
    row.records += 1;
    if (report.estimated === 1 && row.markers.length === 0) {
      row.markers.push("estimated");
    }
  });
  return row;
}

exports.derivations = {
  // A function of what is stored: every report kept, joined to its logged connection by its tuple, so the
  // order the reports arrived in changes nothing, and a run over the same reports writes nothing.
  attribute: function (host, run) {
    var reports = host.query("SELECT CAST(extract(epoch FROM received_at) AS BIGINT) AS received_at, " +
      "src_address, src_port, dst_address, dst_port, bytes, packets, estimated " +
      "FROM plugin_example_reporter.reports " +
      "ORDER BY received_at, src_address, src_port, dst_address, dst_port, bytes", {});
    if (reports.length === 0) {
      return {};
    }
    var since = reports[0].received_at - host.settings.lookback_seconds;
    var firewall = {};
    host.datasets.rows("this_firewall_address", { columns: ["address"] }).rows.forEach(function (row) {
      firewall[row.address] = true;
    });
    var interfaces = {};
    host.datasets.rows("interface", { columns: ["identifier"] }).rows.forEach(function (row) {
      interfaces[row.identifier] = true;
    });
    // Every report keyed both ways: a report counts the leg it names, or the leg's reverse direction.
    var byTuple = {};
    var keys = [];
    reports.forEach(function (report) {
      var key = tuple(report.src_address, report.src_port, report.dst_address, report.dst_port);
      (byTuple[key] = byTuple[key] || []).push(report);
      keys.push([report.src_address, report.src_port, report.dst_address, report.dst_port]);
      keys.push([report.dst_address, report.dst_port, report.src_address, report.src_port]);
    });
    // One read of the legs, whatever the number of reports.
    var legs = host.datasets.rows("leg", {
      keys: { columns: ["src_address", "src_port", "dst_address", "dst_port"], values: keys },
      from: since, to: run.now + 1,
      columns: ["flow_id", "observed_at", "interface", "pair_outcome", "src_address", "src_port",
        "dst_address", "dst_port"]
    }).rows;
    var rows = [];
    legs.forEach(function (leg) {
      if (leg.pair_outcome === "second_leg" || !interfaces[leg.interface] ||
          firewall[leg.src_address] || firewall[leg.dst_address]) {
        return;
      }
      rows.push(direction(leg, "forward",
        byTuple[tuple(leg.src_address, leg.src_port, leg.dst_address, leg.dst_port)] || []));
      rows.push(direction(leg, "reverse",
        byTuple[tuple(leg.dst_address, leg.dst_port, leg.src_address, leg.src_port)] || []));
    });
    rows.sort(function (a, b) {
      return a.connection - b.connection || (a.direction < b.direction ? -1 : a.direction > b.direction ? 1 : 0);
    });
    return {
      measures: { example_reported: rows },
      intervals: { example_reported: [{ starts_at: since, ends_at: null, state: "reporting" }] }
    };
  }
};
