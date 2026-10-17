/*
 * opnview front -- the `matrix` shape: a figure at row x column (docs/widget-catalogue.md, Part 1,
 * "Matrix"). Draws {rows, columns, cells, unmeasured}: the grid with its axes in every condition; a
 * pair the answer did not produce is absent, never zero.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "matrix",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var cells = {};
      (data.cells || []).forEach(function (cell) { cells[cell.row + "\u0000" + cell.column] = cell; });
      var labelOf = {};
      var rows = (data.rows || []).map(function (row) {
        labelOf["r" + row.key] = row.label || String(row.key);
        return { key: String(row.key), label: labelOf["r" + row.key] };
      });
      var columns = (data.columns || []).map(function (column) {
        labelOf["c" + column.key] = column.label || String(column.key);
        return { key: String(column.key), label: labelOf["c" + column.key] };
      });
      return h("div", { "class": "shape-matrix" }, [
        kit.matrix(rows, columns, function (row, column) {
          var cell = cells[row + "\u0000" + column];
          if (!cell) { return null; }
          return { node: kit.value(cell.value), subject: kit.subject("cell", row + "|" + column,
            labelOf["r" + row] + " · " + labelOf["c" + column], [[t("detail.fact.value"), kit.value(cell.value)]]) };
        }),
        data.unmeasured ? kit.note(input.tp("shape.unmeasured", data.unmeasured), "unmeasured") : null
      ]);
    }
  });
})(this);
