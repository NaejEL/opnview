/*
 * The renderer of `custom_chart` (docs/widget-catalogue.md, "Custom chart"; docs/widget-api.md,
 * "Series of custom_chart"): the series the reader named, on a left and a right axis.
 *
 * EACH SERIES IS ITS OWN: its condition, its measure, its unit, its aggregation. A series whose source
 * cannot answer, or whose reference matched nothing, keeps its row under the chart with its condition
 * named -- a chart of four series with one missing never looks like a chart of three. Two measures are
 * never added: series rendered as areas stack only with areas of their own measure.
 *
 * The rendering parameters: `axes` (per axis its unit, choosing the axis's formatter and named under
 * the chart, and its scale, `linear` or `log`, which the axis is drawn on), `legend` (`table` | `inline` | `hidden`) and `tooltip` (`single` |
 * `all_series`, Grafana's whole stack at the hovered instant). Annotations are a strip of bars under
 * the chart, one per bucket that holds any.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var DRAWN = { reachable: true, reachable_no_rows: true };

  /* The words of what a series counts, and of an axis's unit, each a key of this widget's catalogue,
   * written out so a reader of this file sees every one it can ask for. A series of a dataset names a
   * column the catalogue does not hold: it is shown as the answer writes it, as data. */
  var FIGURES = {
    bytes: function (t) { return t("column.figure.bytes"); },
    connections: function (t) { return t("column.figure.connections"); },
    allowed: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.allowed"); },
    blocked: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.blocked"); },
    netflow_bytes: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.netflow_bytes"); },
    netflow_packets: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.netflow_packets"); },
    uptime: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.uptime"); },
    cpu: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.cpu"); },
    memory: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.memory"); },
    swap: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.swap"); },
    disk: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.disk"); },
    temperature: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.temperature"); },
    gateway_latency: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.gateway_latency"); },
    gateway_loss: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.gateway_loss"); },
    bits_per_second: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.bits_per_second"); },
    packets: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.packets"); },
    errors: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.errors"); },
    lookups: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.lookups"); },
    events: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.events"); }
  };
  var UNITS = {
    bytes: function (t) { return t("column.figure.bytes"); },
    bits_per_second: function (t) { return t("plugin.custom_chart.widget.custom_chart.unit.bits_per_second"); },
    ratio: function (t) { return t("plugin.custom_chart.widget.custom_chart.unit.ratio"); },
    lookups: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.lookups"); },
    events: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.events"); },
    connections: function (t) { return t("column.figure.connections"); },
    packets: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.packets"); },
    errors: function (t) { return t("plugin.custom_chart.widget.custom_chart.figure.errors"); },
    seconds: function (t) { return t("unit.second.other"); }
  };
  var ANNOTATIONS = {
    security_events: function (t) { return t("collection.kind.security_event"); },
    blocked_spikes: function (t) { return t("plugin.custom_chart.widget.custom_chart.annotation.blocked_spikes"); }
  };
  function word(table, key, t) {
    return Object.prototype.hasOwnProperty.call(table, key) ? table[key](t) : String(key);
  }

  /* The formatter of a unit: bytes, a bit rate, a ratio as a share, anything else a plain number. */
  function formatter(kit, format, unit) {
    if (unit === "bytes") { return kit.axis("bytes"); }
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
        var figure = given.metric || given.measure;
        var label = given.label || (given.source ? t("plugin.custom_chart.widget.custom_chart.series_label",
          { source: input.tk("series.source.", given.source), measure: figure ? word(FIGURES, figure, t) : "" }) : one.key);
        return { one: one, key: one.key, label: label, cls: kit.seriesClass(index), axis: given.axis === "right" ? "right" : "left",
          render: given.render || "line", measure: one.measure, unit: one.unit };
      });

      /* The unit of each axis: the axes parameter's, else its first series'. A SERIES IS NEVER DRAWN
       * ON AN AXIS OF ANOTHER UNIT -- a CPU share on a byte axis is a flat line at zero -- so a series
       * whose unit is not its axis's moves to the other axis when that one is free or of its unit, and
       * a series no axis can carry is listed under the chart, named, rather than drawn wrong. */
      var axisUnit = { left: null, right: null };
      if (axes) {
        ["left", "right"].forEach(function (side) {
          if (axes[side] && typeof axes[side].unit === "string") { axisUnit[side] = axes[side].unit; }
        });
      }
      var noAxis = {};
      series.forEach(function (entry) {
        if (!DRAWN[entry.one.condition] || !entry.one.buckets || entry.one.buckets.length === 0) { return; }
        var other = entry.axis === "left" ? "right" : "left";
        if (axisUnit[entry.axis] === null || axisUnit[entry.axis] === entry.unit) {
          axisUnit[entry.axis] = entry.unit;
        } else if (axisUnit[other] === null || axisUnit[other] === entry.unit) {
          entry.axis = other;
          axisUnit[other] = entry.unit;
        } else {
          noAxis[entry.key] = true;
        }
      });

      /* Areas stack, so an area series whose measure differs from the first area of its axis is drawn
       * as a line: two measures are never added. */
      var areaMeasure = {};
      var drawn = [], gaps = [];
      series.forEach(function (entry) {
        var one = entry.one;
        if (noAxis[entry.key]) {
          gaps.push(h("div", { key: entry.key, "class": "series-gap-row", "data-series": entry.key, "data-condition": "no_axis" }, [
            h("span", { "class": "swatch swatch-none" }),
            h("span", { "class": "series-gap-name" }, entry.label),
            kit.badge(t("plugin.custom_chart.widget.custom_chart.no_axis", { unit: word(UNITS, entry.unit, t) }), "gap")
          ]));
          return;
        }
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

      /* The scale of each axis: log where the axes parameter asks for it, linear otherwise. */
      var scale = { left: "linear", right: "linear" };
      if (axes) {
        ["left", "right"].forEach(function (side) {
          if (axes[side] && axes[side].scale === "log") { scale[side] = "log"; }
        });
      }

      var chart = kit.chart({ from: requested.from, to: requested.to, series: drawn, tooltip: tooltip, scale: scale,
        format: { left: formatter(kit, format, axisUnit.left), right: formatter(kit, format, axisUnit.right) },
        width: input.size.width, height: Math.max(140, Math.min(300, input.size.height - 90)), label: t("plugin.custom_chart.widget.custom_chart.title") });

      /* Each axis in use is named by its unit; with two, a reader must know which is which. */
      var named = ["left", "right"].filter(function (side) { return (axes && axes[side]) || (axisUnit.right !== null && axisUnit[side] !== null); });
      var axisNotes = named.length ? h("div", { key: "axes", "class": "legend custom-axes", "data-axes": "true" },
        named.map(function (side) {
          var words = { unit: word(UNITS, (axes && axes[side] && axes[side].unit) || axisUnit[side] || "", t) };
          return h("span", { key: side, "class": "legend-item", "data-axis": side, "data-scale": scale[side] }, [
            side === "left" ? t("plugin.custom_chart.widget.custom_chart.axis.left", words)
              : t("plugin.custom_chart.widget.custom_chart.axis.right", words),
            scale[side] === "log" ? [" ", kit.badge(t("chart.scale.log"))] : null]);
        })) : null;

      /* The annotations: their own strip under the chart, titled, each kind in the legend by its name,
       * one bar per bucket as wide as the bucket, on a count axis. */
      var annotations = (data.annotations || []).filter(function (annotation) { return annotation.buckets && annotation.buckets.length; });
      var annotationSeries = annotations.map(function (annotation, index) {
        return { key: "annotation-" + annotation.kind, label: word(ANNOTATIONS, annotation.kind, t),
          cls: kit.seriesClass(series.length + index), render: "bars",
          measure: "security_events", points: annotation.buckets.map(function (bucket) {
            return { from: bucket.from, to: bucket.to, gap: !!bucket.gap, value: bucket.value };
          }) };
      });
      var annotationChart = annotations.length ? h("div", { key: "annotations", "class": "custom-annotations", "data-annotations": "true" }, [
        h("h3", { key: "title", "class": "chart-subtitle" }, t("plugin.custom_chart.widget.custom_chart.annotations")),
        kit.chart({ from: requested.from, to: requested.to, series: annotationSeries, counts: true,
          width: input.size.width, height: 90, label: t("plugin.custom_chart.widget.custom_chart.annotations") }),
        kit.legend(annotationSeries.map(function (entry) { return { key: entry.key, cls: entry.cls, label: entry.label }; }))
      ]) : null;

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
          { key: "total", label: t("shape.total"), num: true }
        ], series.map(function (entry) {
          return { key: entry.key, subject: kit.subject("series", entry.key, entry.label, [
            [t("plugin.custom_chart.widget.custom_chart.column.measure"), measureNode(entry.measure)],
            [t("plugin.custom_chart.widget.custom_chart.column.aggregation"), entry.one.aggregation || kit.none()],
            [t("shape.total"), totalOf(entry)]
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
