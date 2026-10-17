/*
 * The renderer of `interface_volume_ranking` (docs/widget-catalogue.md, "Interface volume ranking"):
 * which interface is generating the traffic. One bar per interface with its VLAN tag, split into its
 * allowed and its blocked part, and under it the shares that stayed between interfaces, crossed to the
 * outside and reached the firewall itself, with how many of its known clients were seen. A silent
 * interface keeps an empty bar. The remainder, the unattributed volume and the interfaces the
 * parameters left out are rows of their own, and the total closes the list.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "interface_volume_ranking",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      function part(volume) { return volume ? volume[field] || 0 : 0; }
      function share(volume, whole) {
        return whole > 0 ? input.format.percent(part(volume) / whole, 0) : t("figure.dash");
      }
      var listed = 0;
      var bars = (data.bars || []).map(function (bar) {
        var volume = bar.volume || {};
        var iface = bar.interface || {};
        listed += bar.value || 0;
        var whole = part(volume);
        var facts = [
          [t("column.bytes"), kit.value(volume, "bytes", { field: "bytes" })],
          [t("column.connections"), kit.value(volume, null, { field: "connections" })],
          [t("column.blocked"), kit.value(volume, null, { field: "blocked_connections" })],
          [t("column.scope.east_west"), kit.value(bar.east_west, unit, { field: field })],
          [t("plugin.interface_volume_ranking.widget.interface_volume_ranking.north_south"), kit.value(bar.north_south, unit, { field: field })],
          [t("plugin.interface_volume_ranking.widget.interface_volume_ranking.this_firewall"), kit.value(bar.this_firewall, unit, { field: field })],
          [t("plugin.interface_volume_ranking.widget.interface_volume_ranking.clients_seen"), t("plugin.interface_volume_ranking.widget.interface_volume_ranking.of", { seen: kit.text(bar.clients_seen), known: kit.text(bar.clients_known) })]
        ];
        var sub = [
          t("plugin.interface_volume_ranking.widget.interface_volume_ranking.shares", { east_west: share(bar.east_west, whole), north_south: share(bar.north_south, whole),
            this_firewall: share(bar.this_firewall, whole) }),
          " · ",
          t("plugin.interface_volume_ranking.widget.interface_volume_ranking.clients", { seen: kit.text(bar.clients_seen), known: kit.text(bar.clients_known) })
        ];
        if (bar.rules_not_logging) {
          sub.push(" ");
          sub.push(kit.badge(tp("plugin.interface_volume_ranking.widget.interface_volume_ranking.rules_not_logging", bar.rules_not_logging), "gap"));
        }
        return {
          key: String(iface.id),
          label: [kit.interfaceLabel(iface), " ", h("span", { "class": "vlan-tag" },
            iface.vlan_tag === null || iface.vlan_tag === undefined ? t("plugin.interface_volume_ranking.widget.interface_volume_ranking.untagged") : t("plugin.interface_volume_ranking.widget.interface_volume_ranking.vlan", { tag: kit.text(iface.vlan_tag) }))],
          sub: sub,
          value: bar.value,
          valueNode: kit.value(volume, unit, { field: field }),
          parts: [{ value: volume["allowed_" + field] || 0, cls: "allowed" },
            { value: volume["blocked_" + field] || 0, cls: "blocked" },
            { value: volume["unknown_" + field] || 0, cls: "other" }],
          subject: kit.interfaceSubject(iface, facts)
        };
      });
      var remainder = data.remainder || { members: 0 };
      var total = data.total || null;
      var unattributed = part(data.unattributed);
      /* What the parameters left out (unlabelled interfaces): the total less every row drawn, in the
       * total's own measure -- never a sum across measures. */
      var left = total ? part(total) - listed - (remainder.volume ? part(remainder.volume) : 0) - unattributed : 0;
      var extra = [];
      if (unattributed > 0) {
        extra.push(h("div", { key: "unattributed", "class": "bar-row is-remainder", "data-excluded": "true",
          "data-value": String(unattributed) }, [
          h("span", { "class": "bar-label" }, t("plugin.interface_volume_ranking.widget.interface_volume_ranking.unattributed")),
          h("span", { "class": "bar-track-blank" }),
          h("span", { "class": "bar-value" }, kit.value(data.unattributed, unit, { field: field }))
        ]));
      }
      if (left > 0) {
        extra.push(h("div", { key: "left-out", "class": "bar-row is-remainder", "data-excluded": "true", "data-value": String(left) }, [
          h("span", { "class": "bar-label" }, t("plugin.interface_volume_ranking.widget.interface_volume_ranking.left_out")),
          h("span", { "class": "bar-track-blank" }),
          h("span", { "class": "bar-value" }, kit.value({ measure: total.measure, lower_bound: total.lower_bound, value: left }, unit))
        ]));
      }
      var notes = [];
      if (data.floating_rules_not_logging) { notes.push(kit.note(tp("plugin.interface_volume_ranking.widget.interface_volume_ranking.floating_rules_not_logging", data.floating_rules_not_logging), "floating")); }
      if (data.unresolved_rules_not_logging) { notes.push(kit.note(tp("plugin.interface_volume_ranking.widget.interface_volume_ranking.unresolved_rules_not_logging", data.unresolved_rules_not_logging), "unresolved")); }
      if (data.rules_logging_not_reported) { notes.push(kit.note(tp("plugin.interface_volume_ranking.widget.interface_volume_ranking.rules_logging_not_reported", data.rules_logging_not_reported), "unreported")); }
      return h("div", { "class": "interface-volume-ranking" }, [
        kit.bars(bars, {
          max: bars.reduce(function (most, bar) { return Math.max(most, bar.value || 0); }, 0) || 1,
          remainder: remainder.members ? { members: remainder.members, value: part(remainder.volume),
            valueNode: kit.value(remainder.volume, unit, { field: field }) } : null
        }),
        extra,
        total ? h("div", { key: "total", "class": "bar-row is-total", "data-total": String(part(total)) }, [
          h("span", { "class": "bar-label" }, t("shape.total")),
          h("span", { "class": "bar-track-blank" }),
          h("span", { "class": "bar-value" }, kit.value(total, unit, { field: field }))
        ]) : null,
        kit.legend([{ key: "allowed", cls: "allowed", label: t("column.allowed") },
          { key: "blocked", cls: "blocked", label: t("column.blocked") }]),
        notes
      ]);
    }
  });
})(this);
