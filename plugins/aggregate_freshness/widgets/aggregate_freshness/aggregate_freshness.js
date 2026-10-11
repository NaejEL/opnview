// aggregate_freshness: one row per pre-computed period -- slot count, earliest and latest period
// covered, last computation and bytes held -- flagged stale when it holds no slot or a slot's
// computation is behind its flows by the refresh contract's recognition rule
// (docs/widget-catalogue.md, "Aggregate freshness").
//
// It reads the slots a widget can read: those of the last 31 days -- the longest rolling window a
// widget asks for, and one more day, rounded out to whole slots of each period -- so every read is
// an index search, never a scan of a growing table.

// LOOKBACK_SECONDS is how far back the freshness is read: the 30-day window and one more day, so the
// oldest slot a 30-day window touches is included.
var LOOKBACK_SECONDS = 31 * 86400;

exports.widget = {
  serve: function (host) {
    var now = host.request.now;
    var periods = [];
    var hasRows = false;
    host.periods.names().forEach(function (period) {
      if (!host.params.has("periods", period)) {
        return;
      }
      var from = host.periods.slotStart(period, now - LOOKBACK_SECONDS);
      var bounds = { from: from, to: now + 1 };
      var coverage = host.query.row("widget_period_coverage", bounds, period);
      var stale = host.query.row("widget_period_stale_slots", bounds, period);
      // The members in the order the table's rows have always been written in.
      var row = {
        period: period,
        slots: coverage.slots,
        earliest_period_start_at: coverage.earliest_period_start_at,
        latest_period_end_at: coverage.latest_period_end_at,
        last_computed_at: coverage.last_computed_at,
        stale_slots: stale.stale_slots,
        stale: coverage.slots === 0 || stale.stale_slots > 0
      };
      if (host.params.flag("show_bytes")) {
        // A period holding no slot holds no byte: the sum over no row is null, and the figure is 0.
        row.bytes = host.figures.loggedCount(coverage.bytes === null ? 0 : coverage.bytes);
      }
      row.read_from = from;
      if (coverage.slots > 0) {
        hasRows = true;
      }
      periods.push(row);
    });
    return { data: { periods: periods }, has_rows: hasRows };
  }
};
