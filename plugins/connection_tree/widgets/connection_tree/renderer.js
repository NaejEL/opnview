/*
 * The renderer of `connection_tree` (docs/widget-catalogue.md, "Connection tree"): where a chosen
 * subject -- an interface, a machine or a person -- goes preferentially. Two trees from the answer:
 * inside, the subjects at the chosen root and their machines; outside, the operators, the unplaced
 * destinations, the firewall itself and the site names below them. Every node carries its own figure
 * and a bar of its share; a level cut by limit_per_level ends in its remainder node, so a node's
 * children read as the node and the outside's first level as the total. No node is dropped or merged.
 *
 * Every node element carries data-tree-node, data-side, data-level, data-node, data-parent and
 * data-value (the value in the answer's measure), which the reconciliation check reads.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "connection_tree",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      var total = data.total && typeof data.total.value === "number" ? data.total.value : 0;

      function label(node) {
        var kind = node.key.split(":")[0], rest = node.key.slice(kind.length + 1);
        if (node.key === "remainder") { return tp("plugin.connection_tree.widget.connection_tree.remainder", node.remainder_members || 0); }
        if (node.interface) { return kit.interfaceLabel(node.interface); }
        if (node.client) { return kit.clientLabel(node.client); }
        if (node.owner) { return node.owner.display_name; }
        if (node.key === "owner:unassigned") { return t("plugin.connection_tree.widget.connection_tree.unassigned"); }
        if (node.key === "this_firewall") { return t("plugin.connection_tree.widget.connection_tree.this_firewall"); }
        if (kind === "operator") { return node.operator || rest; }
        if (kind === "unplaced") { return tk("plugin.connection_tree.widget.connection_tree.unplaced.", rest); }
        if (node.key === "site:none") { return t("plugin.connection_tree.widget.connection_tree.no_site_name"); }
        if (kind === "site") { return node.site_name || rest; }
        return rest || node.key;
      }

      function subject(node, text) {
        var facts = [
          [t("column.bytes"), kit.value(node.bytes, "bytes")],
          [t("column.connections"), kit.value(node.connections)],
          [t("column.blocked"), kit.value(node.blocked_connections)],
          [t("column.last_seen"), node.last_seen_at ? input.format.instant(node.last_seen_at) : kit.none()]
        ];
        if (node.interface) { return kit.interfaceSubject(node.interface, facts); }
        if (node.client) { return kit.clientSubject(node.client, facts); }
        if (node.owner) { return kit.ownerSubject(node.owner, facts); }
        return kit.subject("tree", node.key, text, facts);
      }

      function list(nodes, side, level, parent) {
        return h("ul", { "class": "tree-list depth-" + level }, (nodes || []).map(function (node) {
          var text = label(node);
          var figure = node[field] || null;
          var attrs = { "class": "tree-row" + (node.key === "remainder" ? " is-remainder" : ""), "data-tree-node": "true",
            "data-side": side, "data-level": String(level), "data-node": node.key, "data-parent": parent,
            "data-value": String(node.value) };
          if (node.key !== "remainder") { attrs = kit.attrs(attrs, kit.pick(subject(node, text))); }
          return h("li", { key: node.key, "class": node.key === "remainder" ? "is-remainder" : null }, [
            h("span", attrs, [
              h("span", { "class": "tree-name" }, text),
              h("span", { "class": "tree-bar" }, kit.track([
                { value: Math.max(0, node.value - ((node.blocked_connections && field === "connections") ? node.blocked_connections.value : 0)), cls: "allowed" },
                { value: field === "connections" && node.blocked_connections ? node.blocked_connections.value : 0, cls: "blocked" }], total || 1)),
              h("span", { "class": "tree-value" }, figure ? kit.value(figure, unit) : kit.text(node.value, unit))
            ]),
            node.children && node.children.length ? list(node.children, side, level + 1, node.key) : null
          ]);
        }));
      }

      return h("div", { "class": "connection-tree" }, [
        h("div", { key: "layout", "class": "tree-layout" }, [
          h("div", { key: "inside", "class": "tree-side", "data-side": "inside" }, [
            h("div", { "class": "tree-side-title" }, tk("plugin.connection_tree.widget.connection_tree.root.", data.root || "interface")),
            list(data.inside, "inside", 1, "")
          ]),
          h("div", { key: "outside", "class": "tree-side", "data-side": "outside" }, [
            h("div", { "class": "tree-side-title" }, t("plugin.connection_tree.widget.connection_tree.outside")),
            list(data.outside, "outside", 1, "")
          ])
        ]),
        data.total ? h("div", { key: "total", "class": "remainder-row is-total", "data-total": String(total) }, [
          h("span", {}, t("shape.total")), kit.value(data.total[field] || null, unit)]) : null
      ]);
    }
  });
})(this);
