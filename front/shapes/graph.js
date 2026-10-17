/*
 * opnview front -- the `graph` shape: node-link data (docs/widget-catalogue.md, Part 1, "Graph").
 * Draws {nodes, edges, remainder}: the nodes on a circle, each edge as wide as its figure, no particle
 * and no motion (docs/ui-references.md, "Connection tree -- rejected"); the edges the limit dropped
 * counted.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "graph",
    render: function (input) {
      var kit = input.kit, h = kit.h, data = input.data || {};
      var nodes = data.nodes || [];
      var width = Math.max(240, Math.round(input.size.width));
      var height = Math.max(200, Math.min(480, Math.round(input.size.height - 30)));
      var cx = width / 2, cy = height / 2, radius = Math.max(20, Math.min(cx, cy) - 40);
      var at = {};
      nodes.forEach(function (node, index) {
        var angle = 2 * Math.PI * index / Math.max(1, nodes.length) - Math.PI / 2;
        at[node.key] = [cx + radius * Math.cos(angle), cy + radius * Math.sin(angle)];
      });
      var edges = (data.edges || []).filter(function (edge) { return at[edge.source] && at[edge.target]; });
      var largest = edges.reduce(function (most, edge) { return Math.max(most, kit.amount(edge.value) || 0); }, 0) || 1;
      var children = edges.map(function (edge, index) {
        var value = kit.amount(edge.value);
        return h("line", { key: "e" + index, "class": "graph-edge" + (value === null ? " is-unmeasured" : ""),
          x1: at[edge.source][0].toFixed(1), y1: at[edge.source][1].toFixed(1), x2: at[edge.target][0].toFixed(1),
          y2: at[edge.target][1].toFixed(1), "stroke-width": value === null ? "1" : (1 + 5 * value / largest).toFixed(1),
          "data-value": value === null ? "" : String(value) });
      });
      nodes.forEach(function (node) {
        var label = node.label || String(node.key);
        children.push(h("circle", kit.attrs({ key: "n-" + node.key, "class": "graph-node", cx: at[node.key][0].toFixed(1),
          cy: at[node.key][1].toFixed(1), r: "6" }, kit.pick(kit.subject("node", node.key, label, [])))));
        children.push(h("text", { key: "t-" + node.key, x: at[node.key][0].toFixed(1), y: (at[node.key][1] - 9).toFixed(1),
          "text-anchor": "middle" }, label));
      });
      return h("div", { "class": "shape-graph" }, [
        h("svg", { "class": "graph-svg", viewBox: "0 0 " + width + " " + height, width: String(width), height: String(height),
          role: "group", "aria-label": input.t("graph.label") }, children),
        kit.remainder(data.remainder ? data.remainder.members : 0)
      ]);
    }
  });
})(this);
