/*
 * The renderer of `traffic_sankey` (docs/widget-catalogue.md, "Traffic Sankey"): who talks to whom at a
 * glance. The answer's left side down the left, its right side down the right, a ribbon per pair as
 * wide as its figure, every end labelled with its resolved name -- an interface's label, a client's
 * name, an operator, a country. With show_blocked a ribbon carrying blocked traffic is marked and its
 * blocked share is in its tooltip. The ribbons below min_share are the Other band with its own figure,
 * and the destinations no name was found for are listed by reason; the total closes it.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "traffic_sankey",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      var showBlocked = input.parameters.show_blocked !== false;
      var nodes = {};
      (data.nodes || []).forEach(function (node) { nodes[node.key] = node; });
      function label(key) {
        var node = nodes[key] || { key: key };
        if (node.interface) { return kit.interfaceLabel(node.interface); }
        if (node.client) { return kit.clientLabel(node.client); }
        if (node.operator || node.asn) { return node.operator || String(node.asn); }
        if (node.country_code) { return node.country_code; }
        if (key === "this_firewall") { return t("column.scope.this_firewall"); }
        if (key === "outside") { return t("column.scope.north_south"); }
        return key;
      }
      function subject(key) {
        var node = nodes[key] || {};
        if (node.interface) { return kit.interfaceSubject(node.interface); }
        if (node.client) { return kit.clientSubject(node.client); }
        if (node.asn) { return kit.subject("operator", node.asn, label(key), [[t("column.asn"), kit.text(node.asn)]]); }
        if (node.country_code) { return kit.subject("country", node.country_code, node.country_code, []); }
        return null;
      }
      var other = data.other || null;
      var unnamed = other && other.unnamed ? other.unnamed.filter(function (row) { return row.addresses > 0; }) : [];
      return h("div", { "class": "traffic-sankey" }, [
        kit.sankey({
          ribbons: (data.ribbons || []).map(function (ribbon) {
            return { left: ribbon.left, right: ribbon.right, value: ribbon.value || 0,
              blockedShare: showBlocked ? ribbon.blocked_share : undefined };
          }),
          label: label,
          subject: subject,
          unit: unit,
          field: field,
          other: other && other[field] ? kit.amount(other[field]) : null,
          total: data.total || null
        }),
        unnamed.length ? h("ul", { key: "unnamed", "class": "counter-list" }, unnamed.map(function (row) {
          return h("li", { key: row.reason, "data-reason": row.reason }, [
            h("span", {}, [t("reason.unnamed", { reason: tk("reason.", row.reason) }), " · ", tp("column.address_count", row.addresses)]),
            kit.value(row.volume, unit, { field: field })
          ]);
        })) : null,
        kit.measures([data.total])
      ]);
    }
  });
})(this);
