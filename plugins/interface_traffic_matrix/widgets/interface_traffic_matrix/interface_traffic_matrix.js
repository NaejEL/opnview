// interface_traffic_matrix: source interface by destination interface, each cell carrying the logged
// volume, the allowed and blocked connections and, on request, the rules that matched
// (docs/widget-catalogue.md, "Interface traffic matrix"). The right-hand column is north-south; this
// firewall has its own row and its own column; a paired connection is counted once, on its first
// leg.

// has reports whether an object holds a key of its own.
function has(object, key) {
  return Object.prototype.hasOwnProperty.call(object, key);
}

// placement is the row and the column of a source interface, a destination interface and a
// direction.
function placement(host, src, dst, direction) {
  var keys = host.matrixplacement.keys;
  var directions = host.volume.directions;
  var row = src === null ? keys.outside : host.figures.interfaceKey(src);
  var column = dst === null ? keys.north_south : host.figures.interfaceKey(dst);
  switch (direction) {
    case directions.to_this_firewall:
      column = keys.this_firewall;
      break;
    case directions.from_this_firewall:
      row = keys.this_firewall;
      break;
    case directions.outbound:
      column = keys.north_south;
      break;
    case directions.inbound:
      row = keys.outside;
      break;
  }
  return { row: row, column: column, key: row + "\u0000" + column };
}

exports.widget = {
  serve: function (host) {
    var volume = host.volume.window();
    var interfaces = host.installation.interfaces();
    var keys = host.matrixplacement.keys;
    var scope = host.params.text("scope");
    var measure = host.params.text("measure");
    var showRules = host.params.flag("show_rules");
    var selected = host.refs.ids("interfaces");
    var selectedKeys = selected === null ? null : selected.map(function (id) {
      return host.figures.interfaceKey(id);
    });
    // keep reports whether a cell touches a selected interface, as its row or its column.
    function keep(row, column) {
      return selectedKeys === null || selectedKeys.indexOf(row) >= 0 || selectedKeys.indexOf(column) >= 0;
    }

    var cells = {};
    var cellList = [];
    var total = host.figures.zero();
    volume.rows.forEach(function (row) {
      if (row.exit_leg_device !== null || !host.volume.scopeMatches(scope, row)) {
        return;
      }
      var placed = placement(host, row.src_interface_id, row.dst_interface_id, row.traffic_direction);
      if (!keep(placed.row, placed.column)) {
        return;
      }
      if (!has(cells, placed.key)) {
        cells[placed.key] = { row: placed.row, column: placed.column, figures: host.figures.zero(),
          rules: [] };
        cellList.push(cells[placed.key]);
      }
      cells[placed.key].figures = host.figures.add(cells[placed.key].figures, row);
      total = host.figures.add(total, row);
    });

    if (showRules) {
      host.envelope.coverFlowsState("matrix_rules_beyond_flow_horizon");
      var window = host.request.window;
      host.query.rows("widget_matrix_rules", { from: window.from, to: window.to }).forEach(function (row) {
        var placed = placement(host, row.src_interface_id, row.dst_interface_id, row.traffic_direction);
        if (!has(cells, placed.key)) {
          return;
        }
        cells[placed.key].rules.push({
          rule_id: row.rule_id,
          description: row.description,
          rid: row.rid,
          state: row.rule_id === null ? "rule_not_found" : "resolved",
          connections: host.figures.loggedCount(row.connections)
        });
      });
    }

    var rows = [];
    var columns = [];
    interfaces.forEach(function (info) {
      if (selected !== null && selected.indexOf(info.id) < 0) {
        return;
      }
      var key = host.figures.interfaceKey(info.id);
      rows.push({ key: key, interface: info });
      columns.push({ key: key, interface: info });
    });
    rows.push({ key: keys.outside }, { key: keys.this_firewall });
    columns.push({ key: keys.north_south }, { key: keys.this_firewall });

    var data = cellList.map(function (entry) {
      var cell = {
        row: entry.row,
        column: entry.column,
        value: host.figures.value(measure, entry.figures),
        volume: host.figures.logged(entry.figures)
      };
      if (showRules) {
        // The most connections first. The order of equal counts was never pinned (the Go hook's sort
        // was not stable); it is pinned here by rule, then by label, so an answer is the same on
        // every run.
        cell.rules = entry.rules.sort(function (a, b) {
          return b.connections.value - a.connections.value ||
            (a.rule_id === null ? -1 : 0) - (b.rule_id === null ? -1 : 0) ||
            (a.rule_id !== null && b.rule_id !== null ? a.rule_id - b.rule_id : 0) ||
            host.figures.compare(a.rid === null ? "" : a.rid, b.rid === null ? "" : b.rid);
        });
      }
      return cell;
    });
    // The largest first; equal values by row, then column, in byte order.
    data.sort(function (a, b) {
      return b.value - a.value || host.figures.compare(a.row, b.row) ||
        host.figures.compare(a.column, b.column);
    });
    return {
      data: {
        measure: measure,
        rows: rows,
        columns: columns,
        cells: data,
        total: host.figures.logged(total)
      },
      has_rows: data.length > 0
    };
  }
};
