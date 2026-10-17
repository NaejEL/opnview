/*
 * opnview front -- the `tree` shape: a hierarchy (docs/widget-catalogue.md, Part 1, "Tree"). Draws
 * {roots, remainder, orphans, cyclic}: every node its own rolled-up figure, each sibling set ending in
 * its remainder child, so a node's children read as the node.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "tree",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      function list(nodes, remainder, depth) {
        var items = (nodes || []).map(function (node) {
          var label = node.label || String(node.id);
          return h("li", { key: String(node.id), "data-node": String(node.id), "data-value": String(kit.amount(node.value)) }, [
            h("span", kit.attrs({ "class": "tree-row" }, kit.pick(kit.subject("node", node.id, label,
              [[t("detail.fact.value"), kit.value(node.value)]]))), [
              h("span", { "class": "tree-name" }, label),
              h("span", { "class": "tree-value" }, kit.value(node.value))
            ]),
            node.children && node.children.length ? list(node.children, node.remainder, depth + 1) : null
          ]);
        });
        if (remainder && remainder.members) {
          items.push(h("li", { key: "remainder", "class": "is-remainder", "data-remainder": String(remainder.members),
            "data-value": String(kit.amount(remainder.value)) },
            h("span", { "class": "tree-row" }, [h("span", { "class": "tree-name" }, input.tp("shape.remainder", remainder.members)),
              h("span", { "class": "tree-value" }, kit.value(remainder.value))])));
        }
        return h("ul", { "class": "tree-list depth-" + depth }, items);
      }
      return h("div", { "class": "shape-tree" }, [
        list(data.roots, data.remainder, 0),
        data.orphans ? kit.note(input.tp("shape.tree.orphans", data.orphans), "orphans") : null,
        data.cyclic ? kit.note(input.tp("shape.tree.cyclic", data.cyclic), "cyclic") : null
      ]);
    }
  });
})(this);
