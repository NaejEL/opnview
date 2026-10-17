/*
 * The renderer of `connection_tree` (docs/widget-catalogue.md, "Connection tree"): where a chosen
 * subject -- an interface, a machine or a person -- goes preferentially. Drawn as the approved mockup
 * draws it (docs/mockups/canvas-mockup.html, RENDER.connection_tree): two mirrored trees from the
 * firewall, the inside on the left and the outside on the right, every node a bar sized by the square
 * root of its share of the larger side, a hatched-free cap for its blocked share, the rest of a level
 * cut by limit_per_level a hollow node of its own, every node and every label opening the node's
 * detail; beside the drawing its legend, and under it the same branches as text, shown when the
 * drawing's labels are too small to read.
 *
 * Every node element carries data-tree-node, data-side, data-level, data-node (its key), data-path
 * (its place: one site reached through two operators is two nodes), data-parent (the place of its
 * parent) and data-value (its value in the answer's measure), which the reconciliation check reads.
 * No label is cut: the drawing is made as wide as its longest label needs.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var ROW = 20, NODE_H = 12, ROOT_W = 60, BASE = 46, STEP = 62, MAX_NODE = 46, FONT = 9;

  opnview.widgets.register({
    type: "connection_tree",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var field = data.measure === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      var total = data.total && typeof data.total.value === "number" ? data.total.value : 0;
      var prefix = "plugin.connection_tree.widget.connection_tree.";

      function label(node) {
        var kind = node.key.split(":")[0], rest = node.key.slice(kind.length + 1);
        if (node.key === "remainder") { return tp(prefix + "more", node.remainder_members || 0); }
        if (node.interface) { return kit.interfaceLabel(node.interface); }
        if (node.client) { return kit.clientLabel(node.client); }
        if (node.owner) { return node.owner.display_name; }
        if (node.key === "owner:unassigned") { return t("people.nobody"); }
        if (node.key === "this_firewall") { return t("column.scope.this_firewall"); }
        if (kind === "operator") { return node.operator || rest; }
        if (kind === "unplaced") { return t("reason.not_placed", { reason: tk("reason.", rest) }); }
        if (node.key === "site:none") { return t("reason.no_site_name"); }
        if (kind === "site") { return node.site_name || rest; }
        if (node.key === "port:not_recorded") { return t(prefix + "port_not_recorded"); }
        if (kind === "address" && node.address) { return node.address; }
        return rest || node.key;
      }

      function subject(node, text, path) {
        var facts = [
          [t("column.bytes"), kit.value(node.bytes, "bytes")],
          [t("column.connections"), kit.value(node.connections)],
          [t("column.blocked"), kit.value(node.blocked_connections)],
          [t("column.last_seen"), node.last_seen_at ? input.format.instant(node.last_seen_at) : kit.none()]
        ];
        if (node.interface) { return kit.interfaceSubject(node.interface, facts); }
        if (node.client) { return kit.clientSubject(node.client, facts); }
        if (node.owner) { return kit.ownerSubject(node.owner, facts); }
        return kit.subject("tree", path, text, facts);
      }

      function amount(figure) {
        return figure && typeof figure.value === "number" ? figure.value : 0;
      }

      /* Each side as the rows it is drawn in: a node, then its children, depth first. */
      function rowsOf(nodes, side) {
        var rows = [];
        (function walk(list, level, parent) {
          (list || []).forEach(function (node) {
            var path = (parent || side) + "/" + node.key;
            var connections = amount(node.connections);
            rows.push({ node: node, side: side, level: level, path: path, parent: parent, label: label(node),
              value: node.value || 0, remainder: node.key === "remainder",
              blockedShare: connections > 0 ? Math.min(1, amount(node.blocked_connections) / connections) : 0 });
            walk(node.children, level + 1, path);
          });
        })(nodes, 1, "");
        return rows;
      }
      var left = rowsOf(data.inside, "inside");
      var right = rowsOf(data.outside, "outside");
      var levels = Math.max(1, Math.max.apply(null, left.concat(right).map(function (row) { return row.level; })));

      /* The drawing is as wide as its labels need: the mockup's 640 units, widened on the side whose
       * longest label would run past the edge. */
      function room(rows, sign) {
        var need = 0;
        rows.forEach(function (row) {
          var reach = BASE + row.level * STEP + MAX_NODE + 4 + kit.textWidth(row.label, FONT);
          need = Math.max(need, reach);
        });
        return need;
      }
      var half = Math.max(320, room(left, -1), room(right, 1));
      var W = Math.round(half * 2), cx = W / 2;
      var H = Math.max(left.length, right.length, 1) * ROW + 24;
      var level1 = function (rows) { return rows.filter(function (row) { return row.level === 1; })
        .reduce(function (sum, row) { return sum + row.value; }, 0); };
      var largest = Math.max(level1(left), level1(right), 1);

      var children = [
        h("rect", { key: "root", "class": "tree-node tree-node-root", x: cx - ROOT_W / 2, y: H / 2 - 9, width: ROOT_W, height: 18 }),
        h("text", { key: "root-label", "class": "tree-root-label", x: cx, y: H / 2 + 3, "text-anchor": "middle" }, t(prefix + "root_node"))
      ];
      function draw(rows, sign, cls) {
        rows.forEach(function (row, index) {
          var y = 14 + index * ROW;
          var x = cx + sign * (BASE + row.level * STEP);
          var width = Math.max(6, MAX_NODE * Math.sqrt(row.value / largest));
          var nodeX = sign < 0 ? x - width : x;
          var attrs = { key: "node-" + row.path, "class": "tree-node " + cls + (row.remainder ? " tree-node-remainder" : "") +
            (row.blockedShare > 0 ? " tree-node-blocked" : ""),
            x: kit.fixed(nodeX), y: y - NODE_H / 2, width: kit.fixed(width), height: NODE_H,
            "data-tree-node": "true", "data-side": row.side, "data-level": String(row.level), "data-node": row.node.key,
            "data-path": row.path, "data-parent": row.parent, "data-value": String(row.value) };
          var picked = row.remainder ? null : kit.pick(subject(row.node, row.label, row.path));
          children.push(h("line", { key: "edge-" + row.path, "class": "tree-edge", x1: cx + sign * (ROOT_W / 2), y1: H / 2,
            x2: kit.fixed(x), y2: y }));
          children.push(h("rect", picked ? kit.attrs(attrs, picked) : attrs));
          if (row.blockedShare > 0) {
            var blocked = Math.max(2, width * row.blockedShare);
            children.push(h("rect", { key: "blocked-" + row.path, "class": "tree-blocked-share",
              x: kit.fixed(sign < 0 ? nodeX : nodeX + width - blocked), y: y - NODE_H / 2, width: kit.fixed(blocked), height: NODE_H }));
          }
          var text = { key: "label-" + row.path, "class": "tree-label", x: kit.fixed(sign < 0 ? nodeX - 4 : nodeX + width + 4),
            y: y + 3, "text-anchor": sign < 0 ? "end" : "start" };
          children.push(h("text", picked ? kit.attrs(text, picked) : text, row.label));
        });
      }
      draw(left, -1, "tree-node-left");
      draw(right, 1, "tree-node-right");

      /* The branches as text, for a width at which the drawing's labels are too small to read. */
      function branchList(rows, cap) {
        var shown = rows.slice(0, cap);
        return [
          h("ul", { key: "list" }, shown.map(function (row) {
            return h("li", { key: row.path, "class": row.level > 1 ? "fb-child" : null }, [
              h("span", { "class": row.remainder ? "muted" : null }, row.label),
              h("span", {}, kit.text(row.value, unit))
            ]);
          })),
          rows.length > cap ? h("div", { key: "more", "class": "clip-note" }, tp(prefix + "not_listed", rows.length - cap)) : null
        ];
      }

      return h("div", { "class": "connection-tree", "data-total": String(total) }, [
        /* The drawing takes the whole width of the card and its legend sits under it: beside a
         * column, a tree of long names is scaled until its labels are too small to read. */
        h("div", { key: "layout", "class": "tree-stack" }, [
          h("div", { key: "drawing", "class": "tree-drawing" }, [
            h("svg", { key: "svg", "class": "tree-svg", viewBox: "0 0 " + W + " " + H, role: "group",
              "aria-label": tk(prefix + "root.", data.root || "interface") }, children),
            h("div", { key: "fallback", "class": "chart-fallback", hidden: true }, [
              h("div", { key: "inside", "class": "fb-head" }, tk(prefix + "root.", data.root || "interface")),
              branchList(left, 6),
              h("div", { key: "outside", "class": "fb-head" }, t("column.scope.north_south")),
              branchList(right, 5)
            ])
          ]),
          h("div", { key: "side", "class": "map-side" }, [
            kit.legend([{ key: "blocked", cls: "blocked", label: t(prefix + "legend.blocked") },
              { key: "remainder", cls: "remainder", label: t(prefix + "legend.remainder") }]),
            kit.measures([data.total])
          ])
        ])
      ]);
    }
  });
})(this);
