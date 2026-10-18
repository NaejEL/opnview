/*
 * opnview front -- the `time_series` shape: values over the window (docs/widget-catalogue.md, Part 1,
 * "Time series"). Draws {bucket_seconds, series: [{key, buckets: [{from, to, gap, value}]}]}: one line
 * per series through every bucket that has a value, a bucket with none a labelled gap, never a zero.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "time_series",
    render: function (input) {
      var kit = input.kit, h = kit.h, data = input.data || {};
      var requested = (input.answer && input.answer.window && input.answer.window.requested) || {};
      var series = (data.series || []).map(function (one, index) {
        var buckets = one.buckets || [];
        var measured = buckets.filter(function (bucket) { return kit.isFigure(bucket.value); })[0];
        return { key: String(one.key), label: String(one.key), cls: kit.seriesClass(index),
          measure: measured ? measured.value.measure : null,
          points: buckets.map(function (bucket) {
            return { from: bucket.from, to: bucket.to, gap: !!bucket.gap, value: kit.amount(bucket.value) };
          }) };
      });
      var points = series[0] ? series[0].points : [];
      return h("div", { "class": "shape-time-series" }, [
        kit.chart({ from: requested.from || (points[0] ? points[0].from : 0),
          to: requested.to || (points.length ? points[points.length - 1].to : 1), series: series,
          width: input.size.width, height: Math.max(140, Math.min(320, input.size.height - 40)) }),
        kit.legend(series.map(function (one) { return { key: one.key, cls: one.cls, label: one.label }; }))
      ]);
    }
  });
})(this);
