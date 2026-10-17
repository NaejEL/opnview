/*
 * opnview front -- the `table` shape: rows and columns (docs/widget-catalogue.md, Part 1, "Table").
 * Draws {columns, rows, remainder}: the query's columns in its order, every row, then the rows the
 * limit dropped, counted. A cell with no value is a dash, never a zero.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "table",
    render: function (input) {
      var kit = input.kit, data = input.data || {};
      var columns = (data.columns || []).map(function (name) { return { key: String(name), label: String(name) }; });
      var rows = (data.rows || []).map(function (cells, index) {
        return { key: String(index), cells: cells.map(function (cell) {
          if (cell === null || cell === undefined) { return kit.none(); }
          if (kit.isFigure(cell)) { return kit.value(cell); }
          return typeof cell === "number" ? kit.text(cell) : String(cell);
        }) };
      });
      return kit.table(columns, rows, { remainder: data.remainder });
    }
  });
})(this);
