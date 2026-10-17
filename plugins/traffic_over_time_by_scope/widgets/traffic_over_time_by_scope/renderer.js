/*
 * The renderer of `traffic_over_time_by_scope` (docs/widget-catalogue.md, "Traffic over time by
 * scope"; docs/widget-api.md): how much traffic left the network, entered it and stayed between
 * interfaces, over time. A lead figure per scope -- each its own total, never added to another --
 * then one chart of every scope's buckets, linear between measured buckets, stacked only when
 * `stacked` asks for it, an area then separating the stacked scopes.
 *
 * DEGRADATION IS PER SERIES: each scope carries its own condition. A scope whose source cannot answer
 * is not drawn as a line at zero; it keeps its row under the chart, named, with its condition.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The conditions under which a scope's buckets are drawn: measured, or measured and empty. */
  var DRAWN = { reachable: true, reachable_no_rows: true };

  opnview.widgets.register({
    type: "traffic_over_time_by_scope",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {};
      var parameters = input.parameters || {};
      var stacked = parameters.stacked === true;
      var requested = (input.answer && input.answer.window && input.answer.window.requested) || {};
      var series = data.series || [];
      var unitOf = function (one) { return one.unit === "bytes" ? "bytes" : null; };
      /* A scope's name in the core's words; a key outside them is shown as the answer gives it. */
      var scopeName = function (key) { return kit.direction(key) || String(key); };

      /* The mockup's figures: the scopes that cross the firewall name the period they total, and the
       * last figure is every scope together, where they share one measure. */
      var period = parameters.period ? tk("period.", parameters.period) : null;
      var figures = series.map(function (one, index) {
        var label = scopeName(one.key);
        var figureLabel = period && (one.key === "outbound" || one.key === "inbound")
          ? t("plugin.traffic_over_time_by_scope.widget.traffic_over_time_by_scope.figure_label", { scope: label, period: period }) : label;
        var total = DRAWN[one.condition] && one.total !== null && one.total !== undefined
          ? { measure: one.measure, lower_bound: one.lower_bound, value: one.total } : null;
        var subject = kit.subject("scope", one.key, label, [
          [t("plugin.traffic_over_time_by_scope.widget.traffic_over_time_by_scope.fact.total"), kit.value(total, unitOf(one))],
          [t("column.allowed"), one.total_volume ? kit.value(one.total_volume, unitOf(one), { field: one.unit === "bytes" ? "allowed_bytes" : "allowed_connections" }) : kit.none()],
          [t("column.blocked"), one.total_volume ? kit.value(one.total_volume, unitOf(one), { field: one.unit === "bytes" ? "blocked_bytes" : "blocked_connections" }) : kit.none()],
          [t("column.connections"), one.total_volume ? kit.value(one.total_volume, null, { field: "connections" }) : kit.none()]
        ]);
        return h("div", kit.attrs({ key: one.key, "class": "figure", "data-series": one.key }, kit.pick(subject)), [
          h("span", { "class": "figure-value" }, kit.value(total, unitOf(one))),
          h("span", { "class": "figure-label" }, [h("span", { "class": "swatch sw-" + kit.seriesClass(index) }), " ", figureLabel])
        ]);
      });
      var measures = {};
      var totals = series.filter(function (one) {
        return DRAWN[one.condition] && one.total !== null && one.total !== undefined;
      });
      totals.forEach(function (one) { measures[one.measure + "|" + one.unit] = true; });
      if (totals.length > 1 && Object.keys(measures).length === 1) {
        var all = { measure: totals[0].measure, lower_bound: true,
          value: totals.reduce(function (sum, one) { return sum + one.total; }, 0) };
        figures.push(h("div", { key: "all", "class": "figure", "data-series": "all" }, [
          h("span", { "class": "figure-value" }, kit.value(all, unitOf(totals[0]))),
          h("span", { "class": "figure-label" }, t("plugin.traffic_over_time_by_scope.widget.traffic_over_time_by_scope.all_scopes"))
        ]));
      }

      var drawn = [], gaps = [];
      series.forEach(function (one, index) {
        var label = scopeName(one.key);
        if (!DRAWN[one.condition] || !one.buckets || one.buckets.length === 0) {
          gaps.push(h("div", { key: one.key, "class": "series-gap-row", "data-series": one.key, "data-condition": one.condition }, [
            h("span", { "class": "swatch swatch-none" }),
            h("span", { "class": "series-gap-name" }, label),
            kit.stateBadge(one.condition)
          ]));
          return;
        }
        drawn.push({ key: one.key, label: label, cls: kit.seriesClass(index), measure: one.measure,
          points: one.buckets.map(function (bucket) {
            return { from: bucket.from, to: bucket.to, gap: !!bucket.gap,
              value: bucket.value === null || bucket.value === undefined ? null : bucket.value };
          }) });
      });
      var unit = series[0] && series[0].unit === "bytes" ? "bytes" : null;
      var axis = kit.axis(unit);
      return h("div", { "class": "traffic-over-time", "data-stacked": stacked ? "true" : "false" }, [
        kit.figures(figures),
        kit.chart({ from: requested.from, to: requested.to, series: drawn, stacked: stacked,
          format: { left: axis }, width: input.size.width,
          height: Math.max(140, Math.min(260, input.size.height - 110)),
          label: t("plugin.traffic_over_time_by_scope.widget.traffic_over_time_by_scope.title") }),
        gaps.length ? h("div", { key: "gaps", "class": "series-gaps" }, gaps) : null,
        kit.legend(drawn.map(function (one) {
          return { key: one.key, cls: one.cls, label: t("plugin.traffic_over_time_by_scope.widget.traffic_over_time_by_scope.legend", { scope: one.label,
            unit: unit === "bytes" ? t("column.figure.bytes") : t("column.figure.connections") }) };
        }))
      ]);
    }
  });
})(this);
