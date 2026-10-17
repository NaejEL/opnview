// Answers the window's slowest sample per probe target.
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var rows = host.query.rows("widget_example_latency_targets", { from: window.from, to: window.to });
    host.envelope.cover(rows.length > 0 ? window : null);
    return { data: { targets: rows }, has_rows: rows.length > 0 };
  }
};
