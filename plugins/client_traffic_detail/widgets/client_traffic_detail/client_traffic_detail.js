// client_traffic_detail: one client's records over the period, newest first, and which server
// issued each of its leases (docs/widget-catalogue.md, "Client traffic detail"). A record with no
// attribution is a first-class row, with its address, country and operator; an attributed one
// carries its site name, its method and its delay.

// WITHHELD are the fields aggregate_mode = no_domains withholds: the inferred name and what says how,
// or whether, it was inferred.
var WITHHELD = ["records[].site_name", "records[].method", "records[].correlation_delay_seconds"];

// direction is one direction of a connection as NetFlow measured it: its bytes, packets and state
// (connection_measured_volume, PS20), and the leg the figure was measured at, null when no leg of the
// connection was logged at the Observation Point.
function direction(bytes, packets, state, leg) {
  return { bytes: bytes, packets: packets, state: state, measured_at_leg: leg };
}

// peerRates lists a map of rates by peer address as [{peer, rate}], by address in byte order.
function peerRates(host, byPeer) {
  return Object.keys(byPeer).sort(host.figures.compare).map(function (peer) {
    return { peer: peer, rate: byPeer[peer] };
  });
}

exports.widget = {
  serve: function (host) {
    var ids = host.refs.ids("client");
    if (ids === null) {
      return { data: null, has_rows: false };
    }
    var clientId = ids[0];
    var info = host.installation.client(clientId);
    if (info === null) {
      return { data: null, has_rows: false };
    }
    host.envelope.coverFlows("records_beyond_flow_horizon");
    var window = host.request.window;
    var parameters = {
      from: window.from,
      to: window.to,
      client_id: clientId,
      action: host.params.text("action"),
      scope: host.params.text("scope"),
      limit: host.params.integer("limit")
    };
    var noDomains = host.request.no_domains;
    var data = {
      client: info,
      // The client's latest sampled rate, or its named absence.
      current_rate: host.rates.client(clientId),
      // The client's latest sampled rate with each peer it was read with (row 24 of the mockup's
      // coverage): what the share per destination is drawn against now, keyed by the address a
      // record's destination carries. A peer with no current reading is absent, never a rate of 0.
      current_rate_by_peer: peerRates(host, host.rates.clientPeers(clientId)),
      leases: host.query.rows("widget_client_traffic_detail_leases", { client_id: clientId }).map(function (row) {
        return {
          provider_kind: row.provider_kind,
          provider_key: row.provider_key,
          backend: row.backend,
          address: row.address,
          last_observed_at: row.last_observed_at,
          expires_at: row.expires_at
        };
      }),
      records: [],
      remainder: null,
      total: null
    };
    var shownBytes = 0;
    host.query.rows("widget_client_traffic_detail_records", parameters).forEach(function (row) {
      shownBytes += row.bytes;
      var record = {
        flow_id: row.flow_id,
        observed_at: row.observed_at,
        destination: row.destination,
        port: row.port,
        protocol: row.protocol,
        action: row.action,
        scope: row.traffic_scope,
        bytes: host.figures.connectionBytes(row),
        // Only what geo_asn is read for here: the country code, the AS and its operator.
        geo: {
          lookup_state: row.lookup_state === null ? "no_row" : row.lookup_state,
          country_code: row.country_code,
          country_name: null,
          latitude: null,
          longitude: null,
          asn: row.asn,
          operator: row.operator,
          dataset_build_at: row.dataset_build_at
        },
        // The inferred site name, null when no lookup could be correlated -- or when the aggregate
        // mode withholds it, which `withheld` lists.
        site_name: row.site_name,
        method: row.method,
        correlation_delay_seconds: row.correlation_delay_seconds,
        interface_device: row.interface_device,
        interface_description: row.interface_description
      };
      if (noDomains) {
        // Every destination in address form: the record carries no trace of whether a name was
        // inferred, neither the name, nor the method, nor the delay that inferred it (C2).
        record.site_name = null;
        record.method = null;
        record.correlation_delay_seconds = null;
      }
      if (row.measure === host.figures.measures.netflow) {
        // Under the netflow measure, each direction of the record's connection as NetFlow measured it
        // at the single Observation Point; null when it started under no single Observation Point.
        record.directions = row.forward_state === null && row.reverse_state === null ? null : {
          forward: direction(row.forward_bytes, row.forward_packets, row.forward_state, row.forward_measured_at_leg),
          reverse: direction(row.reverse_bytes, row.reverse_packets, row.reverse_state, row.reverse_measured_at_leg)
        };
      }
      data.records.push(record);
    });
    // The totals read the same filter as the records, with no limit.
    var totals = host.query.row("widget_client_traffic_detail_record_totals", {
      from: parameters.from,
      to: parameters.to,
      client_id: parameters.client_id,
      action: parameters.action,
      scope: parameters.scope
    });
    data.total = host.figures.remainder(totals.records, null, host.figures.byteCount(totals));
    // What the limit left out, in the measure of the whole it is part of.
    var left = host.figures.withMeasure({ bytes: totals.bytes - shownBytes }, totals);
    data.remainder = host.figures.remainder(totals.records - data.records.length, null, host.figures.byteCount(left));
    if (noDomains) {
      WITHHELD.forEach(function (path) { host.envelope.withhold(path); });
    }
    return { data: data, has_rows: totals.records > 0 };
  }
};
