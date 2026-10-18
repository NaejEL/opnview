/*
 * The renderer of `live_strip` (docs/widget-api.md, "Live figures"): the rolling strip, drawn with the
 * chart every rolling series of the front draws with (kit.chart, as traffic_composition draws its recent
 * strip). Two charts, never one: the sampled throughput by traffic direction, and the logged decisions per
 * engine -- the two measures are never summed and never share an axis. A bucket with no sample, or past a
 * decision series' complete_until, is drawn as the chart draws a gap, never as a zero.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  function points(buckets) {
    return (buckets || []).map(function (bucket) {
      return { from: bucket.from, to: bucket.to, gap: !!bucket.gap,
        value: bucket.value === null || bucket.value === undefined ? null : bucket.value };
    });
  }

  opnview.widgets.register({
    type: "live_strip",
    render: function (input) {
      var kit = input.kit, h = kit.h, tk = input.tk, t = input.t, data = input.data || {};
      var span = data.window || {};
      var width = input.size && input.size.width;
      var series = function (list) {
        return (list || []).map(function (one, index) {
          return { key: one.key, label: tk("plugin.live_strip.widget.live_strip.series.", one.key),
            cls: kit.seriesClass(index), measure: one.measure, points: points(one.buckets) };
        });
      };
      return h("div", { "class": "live-strip", "data-cadence": String(data.cadence_seconds || "") }, [
        kit.chart({ from: span.from, to: span.to, series: series(data.throughput),
          format: { left: kit.axis("bits_per_second") }, width: width, height: 130,
          label: t("plugin.live_strip.widget.live_strip.throughput") }),
        kit.chart({ from: span.from, to: span.to, series: series(data.decisions), counts: true,
          width: width, height: 130, label: t("plugin.live_strip.widget.live_strip.decisions") })
      ]);
    }
  });
})(this);
