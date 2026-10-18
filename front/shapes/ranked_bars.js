/*
 * opnview front -- the `ranked_bars` shape: members ranked by a figure (docs/widget-catalogue.md,
 * Part 1, "Ranked bars"). Draws {bars, remainder, total, unmeasured}: the bars longest first, then the
 * remainder with its own count and figure, then the total, so bars plus remainder read as the total.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "ranked_bars",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var remainder = data.remainder || { members: 0 };
      return h("div", { "class": "shape-ranked-bars" }, [
        kit.bars((data.bars || []).map(function (bar) {
          var label = bar.label || String(bar.key);
          return { key: String(bar.key), label: label, value: kit.amount(bar.value), valueNode: kit.value(bar.value),
            subject: kit.subject("member", bar.key, label, [[t("detail.fact.value"), kit.value(bar.value)]]) };
        }), {
          remainder: remainder.members ? { members: remainder.members, value: kit.amount(remainder.value),
            valueNode: kit.value(remainder.value) } : null,
          total: data.total ? { label: t("shape.total"), value: kit.amount(data.total), valueNode: kit.value(data.total) } : null
        }),
        data.unmeasured ? kit.note(input.tp("shape.unmeasured", data.unmeasured), "unmeasured") : null,
        kit.measures([data.total])
      ]);
    }
  });
})(this);
