/*
 * opnview front -- the `donut` shape: parts of a whole (docs/widget-catalogue.md, Part 1, "Donut").
 * Draws {slices, other, total, unmeasured}: a ring that closes on its own total, the Other slice
 * explicit, and a ranked legend beside it. A total of zero draws the ring empty.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "donut",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var slices = (data.slices || []).map(function (slice, index) {
        return { key: String(slice.key), label: slice.label || String(slice.key), value: kit.amount(slice.value) || 0,
          cls: kit.seriesClass(index), figure: slice.value };
      });
      var other = data.other || { members: 0 };
      if (other.members) {
        slices.push({ key: "other", label: input.tp("shape.remainder", other.members), value: kit.amount(other.value) || 0,
          cls: "other", figure: other.value });
      }
      var total = kit.amount(data.total) || 0;
      slices.forEach(function (slice) {
        slice.subject = kit.subject("slice", slice.key, slice.label, [[t("detail.fact.value"), kit.value(slice.figure)],
          [t("detail.fact.share"), total > 0 ? input.format.percent(slice.value / total) : t("figure.dash")]]);
      });
      return h("div", { "class": "comp" }, [
        kit.donut(slices, { total: total, centreValue: kit.text(total), centreLabel: t("shape.total") }),
        h("div", { "class": "comp-legend" }, slices.map(function (slice) {
          return h("div", kit.attrs({ key: slice.key, "class": "comp-row" }, kit.pick(slice.subject)), [
            h("span", { "class": "swatch sw-" + slice.cls }),
            h("span", { "class": "cl" }, slice.label),
            h("span", { "class": "cv" }, kit.value(slice.figure)),
            h("span", { "class": "cp" }, total > 0 ? input.format.percent(slice.value / total, 0) : t("figure.dash"))
          ]);
        })),
        kit.measures([data.total])
      ]);
    }
  });
})(this);
