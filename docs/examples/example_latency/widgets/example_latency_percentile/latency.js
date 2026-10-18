// Each target's latency at a percentile, by nearest rank: the smallest sample at or above that
// share of the target's samples. A percentile is no aggregate a declarative query returns per
// member in one pass, so the script computes it.
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var rank = host.params.integer("percentile");
    var limit = host.params.integer("limit");
    var rows = host.query.rows("widget_example_latency_samples", { from: window.from, to: window.to });
    var samples = {};
    rows.forEach(function (row) {
      (samples[row.target] = samples[row.target] || []).push(row.latency_ms);
    });
    var bars = Object.keys(samples).map(function (target) {
      var sorted = samples[target];
      var index = Math.max(0, Math.ceil(rank / 100 * sorted.length) - 1);
      return { target: target, samples: sorted.length, value: sorted[index] };
    });
    // Slowest first; equal values in the byte order of their names, as the core ranks a tie.
    bars.sort(function (a, b) { return b.value - a.value || host.figures.compare(a.target, b.target); });
    var shown = bars.slice(0, limit);
    var dropped = bars.slice(limit).reduce(function (sum, bar) { return sum + bar.samples; }, 0);
    host.envelope.cover(rows.length > 0 ? window : null);
    return {
      data: {
        percentile: rank,
        bars: shown,
        remainder: { members: bars.length - shown.length, samples: dropped }
      },
      has_rows: bars.length > 0
    };
  }
};
