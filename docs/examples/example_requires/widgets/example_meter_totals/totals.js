exports.widget = {
  serve: function (host) {
    var rows = host.query.rows("widget_example_meter_totals", { limit: host.params.integer("limit") });
    host.envelope.cover(rows.length > 0 ? host.request.window : null);
    return { data: { rows: rows }, has_rows: rows.length > 0 };
  }
};
