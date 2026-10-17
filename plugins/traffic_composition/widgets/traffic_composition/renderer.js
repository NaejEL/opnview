/*
 * The renderer of `traffic_composition` (docs/widget-catalogue.md, "Traffic composition, now";
 * docs/widget-api.md, "Live figures"): what the traffic is made of, and in what proportion. A ring per
 * whole -- the period's whole, and one per direction when the answer splits them -- each closing on its
 * own total, its Other slice explicit; a legend ranking the slices with their figure, their share and,
 * where the answer carries one, their current rate; the current rate of each direction beside the
 * period's volume; and the recent strip when the answer carries one.
 *
 * THE GROUPING DIMENSION IS ALWAYS ON SCREEN: a share means nothing until it says what it is a share
 * of. A current rate is the `sampled` measure, never added to a logged volume; a rate with no current
 * sample is a dash, never a zero.
 *
 * The rendering parameters: `show_other` draws the Other slice in the ring, or, when false, leaves it
 * out of the ring and states its figure in the legend -- the remainder is never silent;
 * `line_interpolation` (`points` | `linear` | `smooth`) is how the recent strip joins its samples.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var INTERPOLATIONS = ["points", "linear", "smooth"];

  opnview.widgets.register({
    type: "traffic_composition",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {}, format = input.format;
      var parameters = input.parameters || {};
      var showOther = parameters.show_other !== false;
      var interpolation = INTERPOLATIONS.indexOf(parameters.line_interpolation) >= 0 ? parameters.line_interpolation : "linear";
      var connections = data.measure === "connections";
      var field = connections ? "connections" : "bytes";
      var unit = connections ? null : "bytes";
      var rates = data.current_rates || {};
      var memberRate = {};
      (rates.by_member || []).forEach(function (entry) { memberRate[entry.key] = entry.rate; });

      function rateText(rate, side) {
        var value = rate ? rate[side] : null;
        if (!rate || rate.state === "no_current_sample" || value === null || value === undefined) {
          return kit.none(t("reason.no_current_sample"));
        }
        return kit.value({ measure: rate.measure, lower_bound: rate.lower_bound, value: value }, "bits_per_second");
      }

      function sliceLabel(slice) {
        if (slice["interface"]) { return kit.interfaceLabel(slice["interface"]); }
        if (slice.client) { return kit.clientLabel(slice.client); }
        if (slice.decision) { return tk("plugin.traffic_composition.widget.traffic_composition.decision.", slice.decision); }
        if (slice.port !== undefined && slice.port !== null) { return slice.protocol ? t("plugin.traffic_composition.widget.traffic_composition.port_protocol",
          { port: String(slice.port), protocol: String(slice.protocol) }) : String(slice.port); }
        if (slice.protocol) { return slice.protocol; }
        if (/^port:/.test(slice.key)) { return t("plugin.traffic_composition.widget.traffic_composition.port_not_recorded"); }
        return t("plugin.traffic_composition.widget.traffic_composition.not_recorded");
      }

      function subjectOf(slice, label, total) {
        var facts = [
          [t("detail.fact.value"), kit.value(slice.volume, unit, { field: field })],
          [t("detail.fact.share"), total > 0 ? format.percent((slice.value || 0) / total) : kit.none()],
          [t("column.blocked"), kit.value(slice.volume, unit, { field: connections ? "blocked_connections" : "blocked_bytes" })]
        ];
        if (memberRate[slice.key]) {
          facts.push([t("column.direction.in"), rateText(memberRate[slice.key], "in_bits_per_second")]);
          facts.push([t("column.direction.out"), rateText(memberRate[slice.key], "out_bits_per_second")]);
        }
        if (slice["interface"]) { return kit.interfaceSubject(slice["interface"], facts); }
        if (slice.client) { return kit.clientSubject(slice.client, facts); }
        return kit.subject("slice", slice.key, label, facts);
      }

      /* One ring: its slices, its Other, its total. ringKey names it ("whole" or a direction). */
      /* A direction in the core's words, and the firewall's own two in this widget's. */
      function directionName(key) {
        return kit.direction(key) || tk("plugin.traffic_composition.widget.traffic_composition.direction.", key);
      }
      function ring(ringKey, part, ringLabel) {
        var total = kit.amount(part.total, field) || 0;
        var slices = (part.slices || []).map(function (slice, index) {
          var label = sliceLabel(slice);
          return { key: ringKey + ":" + slice.key, label: label, value: slice.value || 0, cls: kit.seriesClass(index),
            slice: slice, subject: subjectOf(slice, label, total) };
        });
        var other = part.other || { members: 0 };
        var otherValue = kit.amount(other.volume, field) || 0;
        var otherEntry = other.members ? { key: ringKey + ":other", label: input.tp("shape.remainder", other.members),
          value: otherValue, cls: "other", other: other,
          subject: kit.subject("slice", ringKey + ":other", input.tp("shape.remainder", other.members),
            [[t("detail.fact.value"), kit.value(other.volume, unit, { field: field })],
             [t("detail.fact.share"), total > 0 ? format.percent(otherValue / total) : kit.none()]]) } : null;
        var inRing = showOther && otherEntry ? slices.concat([otherEntry]) : slices;
        var centre = kit.text(total, unit) || "";
        return { key: ringKey, total: total, slices: slices, other: otherEntry, part: part,
          node: h("div", { key: "ring-" + ringKey, "class": "comp-ring", "data-ring": ringKey, "data-total": String(total) }, [
            kit.donut(inRing, { total: showOther ? total : total - (otherEntry ? otherValue : 0), centreValue: centre,
              centreLabel: ringLabel, label: ringLabel }),
            h("span", { "class": "comp-ring-label" }, ringLabel)
          ]) };
      }

      var groupLabel = tk("plugin.traffic_composition.widget.traffic_composition.group_by.", data.group_by);
      var whole = ring("whole", data, tk("plugin.traffic_composition.widget.traffic_composition.whole.", data.whole));
      var rings = [whole];
      (data.rings || []).forEach(function (part) {
        rings.push(ring(part.direction, part, directionName(part.direction)));
      });

      var legendRows = whole.slices.map(function (entry) {
        var memberRateNode = memberRate[entry.slice.key] ? rateText(memberRate[entry.slice.key], "out_bits_per_second") : null;
        return h("div", kit.attrs({ key: entry.key, "class": "comp-row", "data-slice": entry.slice.key }, kit.pick(entry.subject)), [
          h("span", { "class": "swatch sw-" + entry.cls }),
          h("span", { "class": "cl" }, memberRateNode ? [entry.label, " · ", memberRateNode] : entry.label),
          h("span", { "class": "cv" }, kit.value(entry.slice.volume, unit, { field: field })),
          h("span", { "class": "cp" }, whole.total > 0 ? format.percent(entry.value / whole.total, 0) : kit.none())
        ]);
      });
      if (whole.other) {
        legendRows.push(h("div", kit.attrs({ key: "other", "class": "comp-row" + (showOther ? "" : " is-state"),
          "data-slice": "other", "data-in-ring": showOther ? "true" : "false" }, kit.pick(whole.other.subject)), [
          h("span", { "class": "swatch " + (showOther ? "sw-other" : "swatch-none") }),
          h("span", { "class": "cl" }, showOther ? whole.other.label
            : [whole.other.label, " · ", t("plugin.traffic_composition.widget.traffic_composition.not_in_ring")]),
          h("span", { "class": "cv" }, kit.value(whole.other.other.volume, unit, { field: field })),
          h("span", { "class": "cp" }, whole.total > 0 ? format.percent(whole.other.value / whole.total, 0) : kit.none())
        ]));
      }
      legendRows.push(h("div", { key: "total", "class": "comp-row is-total", "data-total": String(whole.total) }, [
        h("span", {}),
        h("span", { "class": "cl" }, t("shape.total")),
        h("span", { "class": "cv" }, kit.value(data.total, unit, { field: field })),
        h("span", { "class": "cp" }, "")
      ]));

      var directionRates = (rates.by_direction || []).map(function (entry) {
        var name = directionName(entry.key);
        return kit.figure("rate-" + entry.key, rateText(entry.rate, "out_bits_per_second"), name,
          h("span", { "class": "figure-previous" }, [t("column.direction.in"), " ",
            rateText(entry.rate, "in_bits_per_second")]));
      });

      var aside = (data.aside || []).map(function (entry) {
        return h("div", { key: "aside-" + entry.key, "class": "comp-row is-state", "data-aside": entry.key }, [
          h("span", {}),
          h("span", { "class": "cl" }, tk("plugin.traffic_composition.widget.traffic_composition.aside.", entry.key)),
          h("span", { "class": "cv" }, entry.condition === "reachable" || entry.condition === "reachable_no_rows"
            ? kit.value(entry.count) : kit.stateBadge(entry.condition)),
          h("span", { "class": "cp" }, "")
        ]);
      });

      var recent = data.recent;
      var strip = null;
      if (recent && recent.series && recent.series.length) {
        var labels = {};
        whole.slices.forEach(function (entry) { labels[entry.slice.key] = { label: entry.label, cls: entry.cls }; });
        strip = h("div", { key: "recent", "class": "comp-recent" }, [
          kit.chart({ from: recent.from, to: recent.to, stacked: true, interpolation: interpolation,
            series: recent.series.map(function (one, index) {
              var known = labels[one.key] || { label: one.key === "other" ? t("plugin.traffic_composition.widget.traffic_composition.other")
                : one.key, cls: one.key === "other" ? "other" : kit.seriesClass(index) };
              return { key: one.key, label: known.label, cls: known.cls, measure: data.total ? data.total.measure : null,
                points: one.buckets.map(function (bucket) {
                  return { from: bucket.from, to: bucket.to, gap: !!bucket.gap, value: bucket.value };
                }) };
            }),
            format: { left: function (value) { return kit.text(value, unit); } }, width: input.size.width, height: 130,
            label: t("plugin.traffic_composition.widget.traffic_composition.recent") }),
          kit.note(t("plugin.traffic_composition.widget.traffic_composition.recent_window",
            { duration: format.duration(recent.seconds) }), "recent-window")
        ]);
      }

      return h("div", { "class": "traffic-composition", "data-group-by": data.group_by, "data-show-other": showOther ? "true" : "false",
        "data-line-interpolation": interpolation }, [
        h("div", { key: "grouping", "class": "legend" }, [
          h("span", { "class": "legend-item" }, t("plugin.traffic_composition.widget.traffic_composition.grouped_by", { group: groupLabel })),
          kit.measure(data.total ? data.total.measure : null),
          directionRates.length ? kit.measure("sampled") : null
        ]),
        directionRates.length ? kit.figures(directionRates) : null,
        h("div", { key: "rings", "class": rings.length > 1 ? "comp-rings" : "comp" }, rings.length > 1
          ? rings.map(function (entry) { return entry.node; })
          : [whole.node, h("div", { "class": "comp-legend" }, legendRows)]),
        rings.length > 1 ? h("div", { key: "legend", "class": "comp-legend" }, legendRows) : null,
        aside.length ? h("div", { key: "aside", "class": "comp-legend" }, aside) : null,
        strip
      ]);
    }
  });
})(this);
