/*
 * opnview front -- the `sankey` shape: flows between nodes (docs/widget-catalogue.md, Part 1,
 * "Sankey"). Draws {nodes, ribbons, other, total, unmeasured}: sources down the left, targets down the
 * right, a ribbon per pair as wide as its figure, and the Other band carrying its own total.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "sankey",
    render: function (input) {
      var kit = input.kit, data = input.data || {};
      var labels = {};
      (data.nodes || []).forEach(function (node) { labels[node.key] = node.label || String(node.key); });
      return kit.sankey({
        ribbons: (data.ribbons || []).map(function (ribbon) {
          return { left: String(ribbon.source), right: String(ribbon.target), value: kit.amount(ribbon.value) || 0 };
        }),
        label: function (key) { return labels[key] || key; },
        other: data.other && data.other.members ? kit.amount(data.other.value) : null,
        total: data.total || null
      });
    }
  });
})(this);
