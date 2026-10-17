/*
 * opnview front -- the `figure` shape: one number (docs/widget-catalogue.md, Part 1, "One figure";
 * docs/widget-authoring.md, "Shape contracts"). Draws {label, unit, figure, previous}: the figure in
 * its unit, its label, and the preceding period beside it when given. No figure is a dash, never a
 * zero.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "figure",
    render: function (input) {
      var kit = input.kit, h = kit.h, data = input.data || {};
      var unit = data.unit === "bytes" || data.unit === "bits_per_second" || data.unit === "seconds" ? data.unit : null;
      var previous = data.previous ? kit.text(kit.amount(data.previous), unit) : null;
      return h("div", { "class": "shape-figure" }, [
        kit.figures([kit.figure("value", kit.value(data.figure, unit), data.label || "",
          previous ? h("span", { "class": "figure-previous" }, input.t("shape.figure.previous", { value: previous })) : null)]),
        kit.measures([data.figure])
      ]);
    }
  });
})(this);
