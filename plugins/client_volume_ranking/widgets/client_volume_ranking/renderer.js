/*
 * The renderer of `client_volume_ranking` (docs/widget-catalogue.md, "Client volume ranking"): which
 * machine is generating the traffic. One bar per client, longest first, each split into its allowed
 * and its blocked part, with the client's interface and its count of destinations under it. The
 * clients the limit dropped are the remainder row, the clients the identity filter left out their own
 * row, and the total closes the list: bars, remainder and excluded read as the total.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "client_volume_ranking",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      var bars = (data.bars || []).map(function (bar) {
        var volume = bar.volume || {};
        var client = bar.client || {};
        var iface = bar.interface;
        var facts = [
          [t("column.bytes"), kit.value(volume, "bytes", { field: "bytes" })],
          [t("column.connections"), kit.value(volume, null, { field: "connections" })],
          [t("column.blocked"), kit.value(volume, null, { field: "blocked_connections" })],
          [t("column.destinations"), kit.text(bar.destinations)]
        ];
        return {
          key: String(client.id),
          label: [kit.clientLabel(client), client.unstable_identity ? kit.badge(t("column.unstable_identity"), "gap") : null],
          sub: [iface ? kit.interfaceLabel(iface) : t("plugin.client_volume_ranking.widget.client_volume_ranking.no_interface"), " · ",
            tp("plugin.client_volume_ranking.widget.client_volume_ranking.destinations", bar.destinations || 0)],
          value: bar.value,
          valueNode: kit.value(volume, unit, { field: field }),
          parts: [{ value: volume["allowed_" + field] || 0, cls: "allowed" },
            { value: volume["blocked_" + field] || 0, cls: "blocked" },
            { value: volume["unknown_" + field] || 0, cls: "other" }],
          subject: kit.clientSubject(client, facts, { related: iface ? [kit.interfaceSubject(iface)] : [] })
        };
      });
      var remainder = data.remainder || { members: 0 };
      var excluded = data.excluded || { members: 0 };
      var total = data.total || null;
      /* THE MOCKUP'S TABLE: the client with its MAC and address under its name, its interface, its
       * figure, its blocked connections and its destinations; the other clients a row of their own,
       * the whole a total, and destinations, which a client can share, never added. */
      var columns = [
        { key: "client", label: t("column.client") },
        { key: "interface", label: t("column.interface") },
        { key: "value", label: field === "bytes" ? t("column.bytes") : t("column.connections"), num: true },
        { key: "blocked", label: t("column.blocked"), num: true },
        { key: "destinations", label: t("column.destinations"), num: true }
      ];
      var notAdditive = h("span", { "class": "muted" }, t("plugin.client_volume_ranking.widget.client_volume_ranking.not_additive"));
      var rows = (data.bars || []).map(function (bar, index) {
        var volume = bar.volume || {};
        var client = bar.client || {};
        var iface = bar.interface;
        var ident = [client.mac, client.last_address].filter(function (part) { return part && part !== kit.clientLabel(client); });
        return { key: String(client.id), subject: bars[index].subject, attrs: { "data-bar": String(client.id), "data-value": String(bar.value) },
          cells: [
            [h("span", { key: "name" }, kit.clientLabel(client)), client.unstable_identity ? [" ", kit.badge(t("column.unstable_identity"), "gap")] : null,
              ident.length ? h("span", { key: "ident", "class": "bar-cidr" }, kit.join(ident)) : null],
            iface ? kit.interfaceLabel(iface) : kit.none(t("plugin.client_volume_ranking.widget.client_volume_ranking.no_interface")),
            kit.value(volume, unit, { field: field }),
            kit.value(volume, null, { field: "blocked_connections" }),
            kit.text(bar.destinations)
          ] };
      });
      if (excluded.members) {
        rows.push({ key: "excluded", className: "is-remainder", attrs: { "data-excluded": "true",
          "data-value": String(excluded.volume ? excluded.volume[field] : 0) },
          cells: [tp("plugin.client_volume_ranking.widget.client_volume_ranking.excluded", excluded.members), "", kit.value(excluded.volume, unit, { field: field }),
            kit.value(excluded.volume, null, { field: "blocked_connections" }), notAdditive] });
      }
      var shown = (data.bars || []).length + (remainder.members || 0);
      return h("div", { "class": "client-volume-ranking" }, kit.table(columns, rows, {
        remainder: remainder.members ? { members: remainder.members, value: remainder.volume ? remainder.volume[field] : 0,
          label: tp("plugin.client_volume_ranking.widget.client_volume_ranking.others", remainder.members),
          cells: ["", kit.value(remainder.volume, unit, { field: field }), kit.value(remainder.volume, null, { field: "blocked_connections" }),
            notAdditive] } : null,
        total: total ? { value: total[field], label: tp("plugin.client_volume_ranking.widget.client_volume_ranking.all_clients", shown),
          cells: ["", kit.value(total, unit, { field: field }), kit.value(total, null, { field: "blocked_connections" }), notAdditive] } : null
      }));
    }
  });
})(this);
