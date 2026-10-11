// unresolved_joins: four counters -- flows whose interface device matched no interface-map entry,
// flows whose rid matched no known rule, clients known only by an address behind an interface, and
// addresses whose geolocation is a miss, pending or was never made -- so the quality of every other
// widget is visible rather than assumed (docs/widget-catalogue.md, "Unresolved joins"). Reachable
// and no rows is the one condition that may read 0 here, because there it genuinely is one.

// RECORD_LIMIT is how many records a counter lists when show_records is set: the newest, the rest
// being reachable from the counter's own figure.
var RECORD_LIMIT = 20;

// count is a count of one measure, a lower bound like every count opnview reads.
function count(measure, value) {
  return { measure: measure, lower_bound: true, value: value };
}

// coverFlows records the coverage of a reading of the flows alone, and the named state when part
// of the window lies past the retention horizon.
function coverFlows(host, state) {
  host.envelope.coverFrom(host.envelope.earliest("widget_earliest_flow"));
  host.envelope.coverFlowsState(state);
}

// withRecords sets a counter's records when there are any: an empty list is left out.
function withRecords(entry, records) {
  if (records.length > 0) {
    entry.records = records;
  }
  return entry;
}

exports.widget = {
  serve: function (host) {
    coverFlows(host, "joins_beyond_flow_horizon");
    var window = host.request.window;
    var interfaceIds = host.refs.ids("interfaces");
    var filters = { from: window.from, to: window.to, interface_ids: interfaceIds };
    var joins = host.query.row("widget_unresolved_flow_joins", filters);
    var interfaceMisses = joins === null ? 0 : joins.interface_misses;
    var ruleMisses = joins === null ? 0 : joins.rule_misses;
    var flows = joins === null ? 0 : joins.flows;
    var show = host.params.flag("show_records");

    // flowRecords are the newest flows behind the interface or the rule counter.
    function flowRecords(counter) {
      return host.query.rows("widget_unresolved_flow_records", {
        from: window.from,
        to: window.to,
        counter: counter,
        interface_ids: interfaceIds,
        limit: RECORD_LIMIT
      }).map(function (row) {
        var record = { flow_id: row.id, observed_at: row.observed_at, interface_device: row.interface_device };
        if (row.rid !== null) {
          record.rid = row.rid;
        }
        record.state = counter === "rule" ? row.rule_lookup_state : row.interface_lookup_state;
        return record;
      });
    }

    var counters = host.params.set("counters").map(function (counter) {
      var entry = { counter: counter };
      switch (counter) {
        case "interface":
        case "rule":
          entry.unresolved = count("logged", counter === "rule" ? ruleMisses : interfaceMisses);
          entry.of = count("logged", flows);
          return show ? withRecords(entry, flowRecords(counter)) : entry;
        case "client_identity": {
          var clients = 0;
          var addressOnly = 0;
          var clientRecords = [];
          host.query.rows("widget_window_clients", filters).forEach(function (row) {
            clients++;
            if (row.identity_kind === "address_in_interface") {
              addressOnly++;
              if (show && clientRecords.length < RECORD_LIMIT) {
                var record = { state: row.identity_kind, client_id: row.client_id };
                if (row.last_address !== null) {
                  record.address = row.last_address;
                }
                clientRecords.push(record);
              }
            }
          });
          entry.unresolved = count("clients", addressOnly);
          entry.of = count("clients", clients);
          return show ? withRecords(entry, clientRecords) : entry;
        }
        case "geo": {
          var peers = 0;
          var unplaced = 0;
          var peerRecords = [];
          host.query.rows("widget_window_peers", filters).forEach(function (row) {
            peers++;
            if (row.lookup_state !== "resolved") {
              unplaced++;
              if (show && peerRecords.length < RECORD_LIMIT) {
                peerRecords.push({ state: row.lookup_state, address: row.peer_address });
              }
            }
          });
          entry.unresolved = count("addresses", unplaced);
          entry.of = count("addresses", peers);
          return show ? withRecords(entry, peerRecords) : entry;
        }
      }
      // A counter the manifest does not list is refused before the script runs; it would count
      // nothing.
      entry.unresolved = { measure: "", lower_bound: false, value: 0 };
      entry.of = { measure: "", lower_bound: false, value: 0 };
      return entry;
    });
    return { data: { counters: counters }, has_rows: flows > 0 };
  }
};
