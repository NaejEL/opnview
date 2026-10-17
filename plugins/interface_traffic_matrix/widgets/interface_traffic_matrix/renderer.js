/*
 * The renderer of `interface_traffic_matrix` (docs/widget-catalogue.md, "Interface traffic matrix"):
 * which interface talks to which, how much, and how much was blocked. One row per interface, one
 * column per interface it reached, then the outside and the firewall itself. A cell carries its volume
 * and, when it holds blocked traffic, a blocked mark; it opens a detail with its allowed and blocked
 * parts and the rules that matched it. A pair the answer did not produce is absent, never zero; the
 * grid keeps its axes in every condition. The total closes it.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "interface_traffic_matrix",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      var labels = {}, subjects = {};
      function axis(entries) {
        return (entries || []).map(function (entry) {
          var iface = entry.interface || {};
          labels[entry.key] = entry.interface ? kit.interfaceLabel(iface)
            : entry.key === "this_firewall" ? t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.this_firewall")
            : t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.north_south");
          subjects[entry.key] = entry.interface ? kit.interfaceSubject(iface) : null;
          return { key: String(entry.key), label: labels[entry.key] };
        });
      }
      var rows = axis(data.rows);
      var columns = axis(data.columns);
      var cells = {};
      var pseudo = { north_south: false, this_firewall: false };
      (data.cells || []).forEach(function (cell) {
        cells[cell.row + "\u0000" + cell.column] = cell;
        if (pseudo[cell.column] !== undefined) { pseudo[cell.column] = true; }
      });
      if (pseudo.north_south) { labels.north_south = t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.north_south"); columns.push({ key: "north_south", label: labels.north_south }); }
      if (pseudo.this_firewall) { labels.this_firewall = t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.this_firewall"); columns.push({ key: "this_firewall", label: labels.this_firewall }); }
      function ruleText(rule) {
        if (rule.state === "rule_not_found") { return t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.rule_not_found", { rid: String(rule.rid) }); }
        return rule.description || String(rule.rid);
      }
      var matrix = kit.matrix(rows, columns, function (row, column) {
        var cell = cells[row + "\u0000" + column];
        if (!cell) { return null; }
        var volume = cell.volume || {};
        var facts = [
          [t("column.bytes"), kit.value(volume, "bytes", { field: "bytes" })],
          [t("column.connections"), kit.value(volume, null, { field: "connections" })],
          [t("column.allowed"), kit.value(volume, unit, { field: "allowed_" + field })],
          [t("column.blocked"), kit.value(volume, unit, { field: "blocked_" + field })]
        ];
        (cell.rules || []).forEach(function (rule, index) {
          facts.push([index === 0 ? t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.rules") : "", h("span", {}, [ruleText(rule), " · ", kit.value(rule.connections)])]);
        });
        var related = [subjects[row], subjects[column]].filter(Boolean);
        return {
          node: kit.value(volume, unit, { field: field }),
          className: (volume.blocked_connections || 0) > 0 ? "has-blocked" : null,
          subject: kit.subject("cell", row + "|" + column, labels[row] + " → " + labels[column], facts, { related: related })
        };
      }, { corner: t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.corner") });
      return h("div", { "class": "interface-traffic-matrix" }, [
        matrix,
        data.total ? h("div", { "class": "remainder-row is-total", "data-total": String(data.total[field]) }, [
          h("span", {}, t("shape.total")), kit.value(data.total, unit, { field: field })]) : null,
        kit.legend([{ key: "blocked", cls: "blocked", label: t("plugin.interface_traffic_matrix.widget.interface_traffic_matrix.has_blocked") }])
      ]);
    }
  });
})(this);
