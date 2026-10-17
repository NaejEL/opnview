// The window's total, built through host.figures from the widget's own statement: it names no measure.
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var row = host.query.row("widget_example_reported_total", { from: window.from, to: window.to });
    var total = host.figures.add(host.figures.zero(), row);
    return { data: { total: host.figures.volume(total) }, has_rows: row.allowed_connections > 0 };
  }
};
