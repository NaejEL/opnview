/*
 * The renderer of `interface_throughput` (docs/widget-catalogue.md, "Interface throughput";
 * docs/widget-api.md): how much each interface carries, from the interface's own counters -- the
 * `interface_counter` measure, never NetFlow and never the filter log. Every sample is drawn where it
 * was taken; a run with no sample is a labelled gap, never a line across it.
 *
 * Two rendering parameters change the drawing: `stacked` stacks the series of one axis, an area then
 * separating them; `per_interface_axis` draws one small chart per interface instead of one shared
 * chart. An interface with no sample in the window keeps its row, named, never a line at zero.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The samples of one series as chart points. A sample is an instant; where the spacing between two
   * samples is more than twice the usual one, nothing was read between them: a gap is inserted there,
   * so the line breaks and the band names it. */
  function points(samples) {
    var steps = [];
    for (var i = 1; i < samples.length; i++) { steps.push(samples[i].at - samples[i - 1].at); }
    steps.sort(function (a, b) { return a - b; });
    var usual = steps.length ? steps[Math.floor(steps.length / 2)] : 0;
    var out = [];
    samples.forEach(function (sample, index) {
      if (index > 0 && usual > 0 && sample.at - samples[index - 1].at > 2 * usual) {
        out.push({ from: samples[index - 1].at + usual / 2, to: sample.at - usual / 2, gap: true, value: null });
      }
      out.push({ from: sample.at, to: sample.at, gap: false,
        value: sample.value === null || sample.value === undefined ? null : sample.value });
    });
    return out;
  }

  opnview.widgets.register({
    type: "interface_throughput",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {};
      var parameters = input.parameters || {};
      var stacked = parameters.stacked === true;
      var perInterfaceAxis = parameters.per_interface_axis === true;
      var requested = (input.answer && input.answer.window && input.answer.window.requested) || {};
      var series = data.series || [];
      var unitOf = function (one) {
        return one.unit === "bits_per_second" ? "bits_per_second" : one.unit === "bytes" ? "bytes" : null;
      };
      var unit = series[0] ? unitOf(series[0]) : null;
      var axis = kit.axis(unit);

      var byInterface = {}, order = [], silent = [], drawnCount = 0;
      series.forEach(function (one) {
        var iface = one["interface"] || {};
        var key = String(iface.id);
        if (!byInterface[key]) { byInterface[key] = { iface: iface, series: [] }; order.push(key); }
        var label = t("plugin.interface_throughput.widget.interface_throughput.series_label", { interface: kit.interfaceLabel(iface),
          direction: tk("column.direction.", one.direction) });
        var cls = kit.seriesClass(drawnCount);
        drawnCount += 1;
        var entry = { key: one.key, label: label, cls: cls, measure: one.measure, one: one,
          points: points(one.points || []) };
        if (entry.points.length === 0) {
          silent.push(entry);
        } else {
          byInterface[key].series.push(entry);
        }
      });

      function legendRows(entries) {
        return entries.map(function (entry) {
          var current = entry.one.current
            ? kit.value({ measure: entry.one.measure, lower_bound: entry.one.lower_bound, value: entry.one.current.value },
              unitOf(entry.one)) : kit.none(t("chart.no_sample"));
          return h("div", kit.attrs({ key: entry.key, "class": "comp-row", "data-series": entry.key },
            kit.pick(kit.interfaceSubject(entry.one["interface"] || {}, [
              [t("plugin.interface_throughput.widget.interface_throughput.fact.current"), current],
              [t("plugin.interface_throughput.widget.interface_throughput.fact.resets"), kit.text(entry.one.resets || 0)]
            ]))), [
            h("span", { "class": "swatch sw-" + entry.cls }),
            h("span", { "class": "cl" }, entry.label),
            h("span", { "class": "cv" }, current),
            h("span", { "class": "cp" }, entry.one.resets ? input.tp("plugin.interface_throughput.widget.interface_throughput.resets", entry.one.resets) : "")
          ]);
        });
      }

      function chart(entries, height, key) {
        return h("div", { key: key, "class": "throughput-chart" }, kit.chart({ from: requested.from, to: requested.to,
          series: entries, stacked: stacked, format: { left: axis }, width: input.size.width, height: height,
          label: t("plugin.interface_throughput.widget.interface_throughput.title") }));
      }

      var charts;
      if (perInterfaceAxis) {
        charts = order.filter(function (key) { return byInterface[key].series.length; }).map(function (key) {
          return h("div", { key: "interface-" + key, "class": "throughput-interface", "data-interface": key }, [
            h("div", { "class": "tree-side-title" }, kit.interfaceLabel(byInterface[key].iface)),
            chart(byInterface[key].series, 120, "chart")
          ]);
        });
      } else {
        var all = [];
        order.forEach(function (key) { all = all.concat(byInterface[key].series); });
        charts = [chart(all, Math.max(140, Math.min(280, input.size.height - 120)), "shared")];
      }
      var drawnEntries = [];
      order.forEach(function (key) { drawnEntries = drawnEntries.concat(byInterface[key].series); });
      return h("div", { "class": "interface-throughput", "data-per-interface-axis": perInterfaceAxis ? "true" : "false",
        "data-stacked": stacked ? "true" : "false" }, [
        charts,
        h("div", { key: "legend", "class": "comp-legend" }, legendRows(drawnEntries)),
        silent.length ? h("div", { key: "silent", "class": "series-gaps" }, silent.map(function (entry) {
          return h("div", { key: entry.key, "class": "series-gap-row", "data-series": entry.key }, [
            h("span", { "class": "swatch swatch-none" }),
            h("span", { "class": "series-gap-name" }, entry.label),
            h("span", { "class": "badge badge-gap" }, t("chart.no_sample"))
          ]);
        })) : null
      ]);
    }
  });
})(this);
