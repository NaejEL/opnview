/*
 * opnview front -- the `map` shape: a figure per country (docs/widget-catalogue.md, Part 1, "Map").
 * Draws {points, unplaced, remainder, unmeasured}: point marks on the basemap, and the unplaced
 * counter always, at zero too: placed plus unplaced is the whole.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "map",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var unplaced = data.unplaced || { members: 0, value: null };
      var points = (data.points || []).map(function (point, index) {
        return { key: String(point.country) + ":" + index, latitude: point.latitude, longitude: point.longitude,
          size: kit.amount(point.value) || 0, cls: "allowed",
          subject: kit.subject("country", point.country, point.label || String(point.country),
            [[t("detail.fact.value"), kit.value(point.value)]]) };
      });
      return h("div", { "class": "map-layout" }, [
        h("div", { "class": "map-frame" }, kit.map(points, {})),
        h("div", { "class": "map-side" }, [
          h("div", { "class": "counter", "data-unplaced": String(unplaced.members || 0) }, [
            h("span", { "class": "counter-value" }, kit.value(unplaced.value === undefined ? null : unplaced.value)),
            h("span", { "class": "counter-second" }, input.tp("map.unplaced_rows", unplaced.members || 0))
          ]),
          data.remainder && data.remainder.members ? kit.remainder(data.remainder.members, kit.value(data.remainder.value)) : null
        ])
      ]);
    }
  });
})(this);
