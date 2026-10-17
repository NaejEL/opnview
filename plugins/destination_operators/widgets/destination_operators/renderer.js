/*
 * The renderer of `destination_operators` (docs/widget-catalogue.md, "Destination countries";
 * docs/widget-api.md): which operators and autonomous systems the traffic reaches. One row per AS
 * number, or per operator name when grouped by name: its bytes, the allowed and the blocked parts as the `series` parameter asks, the outside addresses
 * behind it. The destinations no operator could be given are first-class rows, one per reason, never
 * dropped; the operators past the limit are a row of their own, and the total closes the table.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "destination_operators",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp;
      var data = input.data || {};
      var series = input.parameters.series || ["allowed", "blocked"];
      var showAllowed = series.indexOf("allowed") >= 0, showBlocked = series.indexOf("blocked") >= 0;

      function reasonOf(reason) {
        switch (reason) {
          case "miss": return t("plugin.destination_operators.widget.destination_operators.unresolved.miss");
          case "pending": return t("plugin.destination_operators.widget.destination_operators.unresolved.pending");
          case "no_row": return t("plugin.destination_operators.widget.destination_operators.unresolved.no_row");
          case "no_coordinates": return t("plugin.destination_operators.widget.destination_operators.unresolved.no_coordinates");
          case "east_west": return t("plugin.destination_operators.widget.destination_operators.unresolved.east_west");
          case "this_firewall": return t("plugin.destination_operators.widget.destination_operators.unresolved.this_firewall");
          default: return String(reason);
        }
      }
      var columns = [{ key: "operator", label: t("column.operator") }, { key: "bytes", label: t("column.bytes"), num: true }];
      if (showAllowed) { columns.push({ key: "allowed", label: t("column.allowed"), num: true }); }
      if (showBlocked) { columns.push({ key: "blocked", label: t("column.blocked"), num: true }); }
      columns.push({ key: "peers", label: t("plugin.destination_operators.widget.destination_operators.addresses"), num: true });

      function cellsOf(first, bytes, allowed, blocked, peers) {
        var cells = [first, bytes];
        if (showAllowed) { cells.push(allowed); }
        if (showBlocked) { cells.push(blocked); }
        cells.push(peers);
        return cells;
      }
      var rows = (data.rows || []).map(function (row) {
        var byAsn = row.asn !== undefined && row.asn !== null;
        var name = byAsn ? (row.operator ? t("plugin.destination_operators.widget.destination_operators.operator_asn", { operator: row.operator, asn: String(row.asn) })
          : t("plugin.destination_operators.widget.destination_operators.asn", { asn: String(row.asn) }))
          : (row.operator || row.key);
        var facts = [
          [t("column.bytes"), kit.value(row.bytes, "bytes")],
          [t("column.allowed"), row.allowed ? kit.value(row.allowed, "bytes", { field: "bytes" }) : kit.none()],
          [t("column.blocked"), row.blocked ? kit.value(row.blocked, "bytes", { field: "bytes" }) : kit.none()],
          [t("column.connections"), row.allowed && row.blocked ? kit.text(kit.add(row.allowed, row.blocked, "connections")) : kit.none()],
          [t("plugin.destination_operators.widget.destination_operators.addresses"), kit.text(row.peers)]
        ];
        return {
          key: row.key,
          subject: kit.subject("operator", row.key, name, facts, {
            reference: byAsn ? { kind: "operator", by: "asn", value: row.asn, label: row.operator || name } : null }),
          cells: cellsOf(name, kit.value(row.bytes, "bytes"),
            row.allowed ? kit.value(row.allowed, "bytes", { field: "bytes" }) : kit.none(),
            row.blocked ? kit.value(row.blocked, "bytes", { field: "bytes" }) : kit.none(),
            kit.text(row.peers))
        };
      });
      (data.unresolved || []).forEach(function (unresolved) {
        var volume = unresolved.volume || {};
        rows.push({ key: "unresolved-" + unresolved.reason, className: "is-flagged",
          subject: kit.subject("unplaced", unresolved.reason, reasonOf(unresolved.reason), [
            [t("column.bytes"), kit.value(volume, "bytes", { field: "bytes" })],
            [t("plugin.destination_operators.widget.destination_operators.addresses"), kit.text(unresolved.addresses)]]),
          cells: cellsOf(h("span", {}, [reasonOf(unresolved.reason), " ", h("span", { "class": "ident" },
            tp("plugin.destination_operators.widget.destination_operators.address_count", unresolved.addresses || 0))]),
          kit.value(volume, "bytes", { field: "bytes" }),
          kit.value(volume, "bytes", { field: "allowed_bytes" }),
          kit.value(volume, "bytes", { field: "blocked_bytes" }),
          kit.text(unresolved.addresses)) });
      });
      var remainder = data.remainder || { members: 0 };
      var restCells = function (volume) {
        var cells = [kit.value(volume, "bytes", { field: "bytes" })];
        if (showAllowed) { cells.push(kit.value(volume, "bytes", { field: "allowed_bytes" })); }
        if (showBlocked) { cells.push(kit.value(volume, "bytes", { field: "blocked_bytes" })); }
        cells.push("");
        return cells;
      };
      return h("div", { "class": "destination-countries" }, [
        kit.table(columns, rows, {
          remainder: remainder.members ? { members: remainder.members, cells: restCells(remainder.volume || {}) } : null,
          total: data.total ? { label: t("shape.total"), cells: restCells(data.total) } : null,
          empty: t("table.no_rows")
        })
      ]);
    }
  });
})(this);
