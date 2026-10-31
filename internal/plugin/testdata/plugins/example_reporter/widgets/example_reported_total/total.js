// The window's total, built through host.figures from the widget's own statement: it names no measure.
exports.widget = {
  serve: function (host) { return host.lib.figures.measure.serve(host, serveInMeasure); }
};

// serveInMeasure builds the answer under the measure the figures library put in force: it is run
// once per measure tried, and never names one (ADR 0021, step 5).
function serveInMeasure(host) {
  var window = host.request.window;
  var row = host.query.row("widget_example_reported_total", { from: window.from, to: window.to });
  var total = host.lib.figures.figures.add(host.lib.figures.figures.zero(), row);
  return { data: { total: host.lib.figures.figures.volume(total) }, has_rows: row.allowed_connections > 0 };
}
