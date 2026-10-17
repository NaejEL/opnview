/*
 * The renderer of `custom_chart` (docs/widget-catalogue.md, "Custom chart"; docs/widget-api.md,
 * "Series of custom_chart"): the series the reader named, on a left and a right axis.
 *
 * EACH SERIES IS ITS OWN: its condition, its measure, its unit, its aggregation. A series whose source
 * cannot answer, or whose reference matched nothing, keeps its row under the chart with its condition
 * named -- a chart of four series with one missing never looks like a chart of three. Two measures are
 * never added: series rendered as areas stack only with areas of their own measure.
 *
 * The rendering parameters: `axes` (per axis its unit and its scale, stated under the chart, the unit
 * choosing the axis's formatter), `legend` (`table` | `inline` | `hidden`) and `tooltip` (`single` |
 * `all_series`, Grafana's whole stack at the hovered instant). Annotations are a strip of bars under
 * the chart, one per bucket that holds any.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var DRAWN = { reachable: true, reachable_no_rows: true };

  /* The formatter of a unit: bytes, a bit rate, a ratio as a share, anything else a plain number. */
  function formatter(kit, format, unit) {
    if (unit === "bytes") { return function (value) { return format.bytes(value); }; }
    if (unit === "bits_per_second") { return function (value) { return format.rate(value); }; }
    if (unit === "ratio") { return function (value) { return format.percent(value); }; }
    return function (value) { return kit.text(value); };
  }

  opnview.widgets.register({
    type: "custom_chart",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {}, format = input.format;
      var parameters = input.parameters || {};
      var axes = parameters.axes && typeof parameters.axes === "object" ? parameters.axes : null;
      var legend = parameters.legend === "inline" || parameters.legend === "hidden" ? parameters.legend : "table";
      var tooltip = parameters.tooltip === "all_series" ? "all_series" : "single";
      var requested = (input.answer && input.answer.window && input.answer.window.requested) || {};

      var series = (data.series || []).map(function (one, index) {
        var given = one.parameters || {};
        var label = given.label || (given.source ? t("plugin.custom_chart.widget.custom_chart.series_label",
          { source: String(given.source), measure: String(given.metric || given.measure || "") }) : one.key);
        return { one: one, key: one.key, label: label, cls: kit.seriesClass(index), axis: given.axis === "right" ? "right" : "left",
          render: given.render || "line", measure: one.measure, unit: one.unit };
      });

      /* The unit of each axis: the axes parameter's, else its first series'. */
      var axisUnit = { left: null, right: null };
      series.forEach(function (entry) { if (axisUnit[entry.axis] === null) { axisUnit[entry.axis] = entry.unit; } });
      if (axes) {
        ["left", "right"].forEach(function (side) {
          if (axes[side] && typeof axes[side].unit === "string") { axisUnit[side] = axes[side].unit; }
        });
      }

      /* Areas stack, so an area series whose measure differs from the first area of its axis is drawn
       * as a line: two measures are never added. */
      var areaMeasure = {};
      var drawn = [], gaps = [];
      series.forEach(function (entry) {
        var one = entry.one;
        if (!DRAWN[one.condition] || !one.buckets || one.buckets.length === 0) {
          gaps.push(h("div", { key: entry.key, "class": "series-gap-row", "data-series": entry.key, "data-condition": one.condition }, [
            h("span", { "class": "swatch swatch-none" }),
            h("span", { "class": "series-gap-name" }, entry.label),
            kit.stateBadge(one.condition),
            (one.unresolved_references || []).map(function (reference, index) {
              return h("span", { key: "ref-" + index, "class": "mono state-detail" },
                t("state.unresolved_reference", { kind: String(reference.kind), by: String(reference.by),
                  value: String(reference.value), label: reference.label === null || reference.label === undefined ? "" : String(reference.label) }));
            })
          ]));
          return;
        }
        var render = entry.render;
        if (render === "area") {
          if (areaMeasure[entry.axis] === undefined) { areaMeasure[entry.axis] = entry.measure; }
          if (areaMeasure[entry.axis] !== entry.measure) { render = "line"; }
        }
        drawn.push({ key: entry.key, label: entry.label, cls: entry.cls, axis: entry.axis, render: render, measure: entry.measure,
          points: one.buckets.map(function (bucket) {
            return { from: bucket.from, to: bucket.to, gap: !!bucket.gap,
              value: bucket.value === null || bucket.value === undefined ? null : bucket.value };
          }) });
      });

      var chart = kit.chart({ from: requested.from, to: requested.to, series: drawn, tooltip: tooltip,
        format: { left: formatter(kit, format, axisUnit.left), right: formatter(kit, format, axisUnit.right) },
        width: input.size.width, height: Math.max(140, Math.min(300, input.size.height - 90)), label: t("plugin.custom_chart.widget.custom_chart.title") });

      var axisNotes = axes ? h("div", { key: "axes", "class": "legend custom-axes", "data-axes": "true" },
        ["left", "right"].filter(function (side) { return axes[side]; }).map(function (side) {
          var words = { unit: String(axes[side].unit || axisUnit[side] || ""), scale: String(axes[side].scale || "linear") };
          return h("span", { key: side, "class": "legend-item", "data-axis": side }, side === "left"
            ? t("plugin.custom_chart.widget.custom_chart.axis.left", words)
            : t("plugin.custom_chart.widget.custom_chart.axis.right", words));
        })) : null;

      var annotations = (data.annotations || []).filter(function (annotation) { return annotation.buckets && annotation.buckets.length; });
      var annotationChart = annotations.length ? kit.chart({ from: requested.from, to: requested.to,
        series: annotations.map(function (annotation, index) {
          return { key: "annotation-" + annotation.kind, label: annotation.kind, cls: kit.seriesClass(series.length + index), render: "bars",
            measure: "security_events", points: annotation.buckets.map(function (bucket) {
              return { from: bucket.from, to: bucket.to, gap: !!bucket.gap, value: bucket.value };
            }) };
        }), width: input.size.width, height: 120, label: t("plugin.custom_chart.widget.custom_chart.annotations") }) : null;

      /* A series measure the figures vocabulary names is a measure chip; one it does not -- a gauge, a
       * count of a dataset -- is shown as the code the answer gives. */
      function measureNode(measure) {
        if (kit.MEASURES.indexOf(measure) >= 0) { return kit.measure(measure); }
        return measure ? h("span", { "class": "mono" }, measure) : kit.none();
      }
      function totalOf(entry) {
        var one = entry.one;
        if (one.total === null || one.total === undefined) { return kit.none(); }
        return kit.value({ measure: one.measure, lower_bound: one.lower_bound, value: one.total },
          one.unit === "bytes" ? "bytes" : one.unit === "bits_per_second" ? "bits_per_second" : null);
      }
      var legendNode = null;
      if (legend === "table") {
        legendNode = kit.table([
          { key: "series", label: t("plugin.custom_chart.widget.custom_chart.column.series") },
          { key: "measure", label: t("plugin.custom_chart.widget.custom_chart.column.measure") },
          { key: "aggregation", label: t("plugin.custom_chart.widget.custom_chart.column.aggregation") },
          { key: "total", label: t("plugin.custom_chart.widget.custom_chart.column.total"), num: true }
        ], series.map(function (entry) {
          return { key: entry.key, subject: kit.subject("series", entry.key, entry.label, [
            [t("plugin.custom_chart.widget.custom_chart.column.measure"), measureNode(entry.measure)],
            [t("plugin.custom_chart.widget.custom_chart.column.aggregation"), entry.one.aggregation || kit.none()],
            [t("plugin.custom_chart.widget.custom_chart.column.total"), totalOf(entry)]
          ]), cells: [
            h("span", {}, [h("span", { "class": "swatch sw-" + entry.cls }), " ", entry.label]),
            measureNode(entry.measure),
            entry.one.aggregation || kit.none(),
            totalOf(entry)
          ] };
        }), { compact: true });
      } else if (legend === "inline") {
        legendNode = kit.legend(series.map(function (entry) { return { key: entry.key, cls: entry.cls, label: entry.label }; }));
      }
      return h("div", { "class": "custom-chart", "data-legend": legend, "data-tooltip": tooltip }, [
        chart,
        axisNotes,
        annotationChart,
        gaps.length ? h("div", { key: "gaps", "class": "series-gaps" }, gaps) : null,
        legendNode
      ]);
    }
  });
})(this);
