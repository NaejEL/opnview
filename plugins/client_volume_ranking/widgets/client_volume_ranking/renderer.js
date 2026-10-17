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
          label: [kit.clientLabel(client), client.unstable_identity ? kit.badge(t("plugin.client_volume_ranking.widget.client_volume_ranking.unstable"), "gap") : null],
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
      return h("div", { "class": "client-volume-ranking" }, [
        kit.bars(bars, {
          max: bars.reduce(function (most, bar) { return Math.max(most, bar.value || 0); }, 0) || 1,
          remainder: remainder.members ? { members: remainder.members, value: remainder.volume ? remainder.volume[field] : 0,
            valueNode: kit.value(remainder.volume, unit, { field: field }) } : null
        }),
        excluded.members ? h("div", { key: "excluded", "class": "bar-row is-remainder", "data-excluded": "true",
          "data-value": String(excluded.volume ? excluded.volume[field] : 0) }, [
          h("span", { "class": "bar-label" }, tp("plugin.client_volume_ranking.widget.client_volume_ranking.excluded", excluded.members)),
          h("span", { "class": "bar-track-blank" }),
          h("span", { "class": "bar-value" }, kit.value(excluded.volume, unit, { field: field }))
        ]) : null,
        total ? h("div", { key: "total", "class": "bar-row is-total", "data-total": String(total[field]) }, [
          h("span", { "class": "bar-label" }, t("shape.total")),
          h("span", { "class": "bar-track-blank" }),
          h("span", { "class": "bar-value" }, kit.value(total, unit, { field: field }))
        ]) : null,
        kit.legend([{ key: "allowed", cls: "allowed", label: t("column.allowed") },
          { key: "blocked", cls: "blocked", label: t("column.blocked") }])
      ]);
    }
  });
})(this);
