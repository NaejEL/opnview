/*
 * opnview front -- the canvas grid: where each card sits (F7-18; docs/dashboard-format.md,
 * "Placement").
 *
 * THE COLUMN COUNT FOLLOWS THE CANVAS, NOT THE WINDOW: twelve, eight or one column, from the width of
 * the canvas's own container, by the step-3 mockup's arithmetic (docs/mockups/canvas-mockup.html,
 * "THE COLUMN COUNT FOLLOWS THE CANVAS, NOT THE WINDOW"):
 *
 *   - twelve when the widest authored card -- seven of twelve -- still clears WIDEST_CARD_FLOOR;
 *   - eight when a half card clears MIN_CARD_WIDTH, where an authored width is snapped up to a
 *     whole quarter and never below a half;
 *   - one otherwise, every widget full width in y order.
 *
 * Y IS ADVISORY: every card floats upward into empty space (y compaction), in the order of its y
 * then its x.
 *
 * NOTHING HERE WRITES BACK TO THE DOCUMENT. The placement is read and never assigned: the derived
 * columns, the compacted rows and the narrow layout are this window's, so the same document
 * re-exports unchanged at every width.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var MIN_CARD_WIDTH = 420;
  var WIDEST_CARD_FLOOR = 898;
  /* The grid's gap between columns, in CSS pixels: var(--spacing) of the stylesheet. */
  var GAP = 16;

  /* The canvas width at which a card spanning span of cols columns is card pixels wide. */
  function canvasWidthFor(cols, span, card, gap, padding) {
    var columnWidth = (card - (span - 1) * gap) / span;
    return columnWidth * cols + (cols - 1) * gap + padding;
  }

  function columnsFor(width, spacing) {
    var gap = spacing && spacing.gap !== undefined ? spacing.gap : GAP;
    var padding = spacing && spacing.padding !== undefined ? spacing.padding : 0;
    if (width >= canvasWidthFor(12, 7, WIDEST_CARD_FLOOR, gap, padding)) { return 12; }
    if (width >= canvasWidthFor(8, 4, MIN_CARD_WIDTH, gap, padding)) { return 8; }
    return 1;
  }

  /* An authored twelve-column width on eight columns: snapped up to a whole quarter, never below a
   * half. */
  function widthOnEight(w) {
    var quarters = Math.ceil((w * 8 / 12) / 2);
    return Math.max(2, Math.min(4, quarters)) * 2;
  }

  function integer(value, fallback) {
    return typeof value === "number" && isFinite(value) ? Math.max(0, Math.floor(value)) : fallback;
  }

  /* The placement of a widget, its values clamped to the grid: a w above the columns is clamped
   * rather than overflowing, as the format says. */
  function placementOf(widget, columns) {
    var p = widget && widget.placement ? widget.placement : {};
    var w = Math.max(1, Math.min(columns, integer(p.w, columns)));
    var x = Math.min(integer(p.x, 0), columns - w);
    return { x: x, y: integer(p.y, 0), w: w, h: Math.max(1, integer(p.h, 4)) };
  }

  /* Lays the widgets out on cols columns. Answers, in drawing order, one cell per widget: {index,
   * column (1-based), span, row (1-based), rows}. */
  function layout(widgets, cols, authoredColumns) {
    var authored = authoredColumns || 12;
    var items = (widgets || []).map(function (widget, index) {
      return { index: index, p: placementOf(widget, authored) };
    }).sort(function (a, b) { return a.p.y - b.p.y || a.p.x - b.p.x || a.index - b.index; });
    var cells = [];
    if (cols === 1) {
      var next = 1;
      items.forEach(function (item) {
        cells.push({ index: item.index, column: 1, span: 1, row: next, rows: item.p.h });
        next += item.p.h;
      });
      return cells;
    }
    /* The height of each column so far: a card lands at the lowest row its columns leave free. */
    var heights = [];
    for (var c = 0; c < cols; c++) { heights.push(0); }
    var cursor = 0;
    items.forEach(function (item) {
      var span, start;
      if (cols === authored) {
        span = item.p.w;
        start = item.p.x;
      } else {
        /* Eight columns cannot hold twelve columns' offsets: cards flow in reading order, each
         * starting where the last one ended, wrapping when it does not fit. */
        span = Math.min(cols, Math.round(widthOnEight(item.p.w) * cols / 8));
        if (cursor + span > cols) { cursor = 0; }
        start = cursor;
        cursor = (cursor + span) % cols;
      }
      var top = 0;
      for (var k = start; k < start + span; k++) { top = Math.max(top, heights[k]); }
      for (var j = start; j < start + span; j++) { heights[j] = top + item.p.h; }
      cells.push({ index: item.index, column: start + 1, span: span, row: top + 1, rows: item.p.h });
    });
    return cells;
  }

  opnview.grid = {
    MIN_CARD_WIDTH: MIN_CARD_WIDTH,
    WIDEST_CARD_FLOOR: WIDEST_CARD_FLOOR,
    columnsFor: columnsFor,
    widthOnEight: widthOnEight,
    placementOf: placementOf,
    layout: layout
  };
})(this);
