/*
 * The renderer of `passed_traffic_world_map` (docs/widget-catalogue.md, "Passed-traffic world map"):
 * where the allowed traffic went. Point marks on the basemap, sized by the answer's size_by measure,
 * each opening a detail with its address, operator, AS, country and interfaces -- no choropleth, no
 * arcs. The unplaced counter is always drawn, at zero too, and with show_unplaced its breakdown per
 * reason; below it what fell under the minimum and the total, so placed plus unplaced plus below the
 * minimum reads as the whole (the map-reconcile element states the three figures).
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "passed_traffic_world_map",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var parameters = input.parameters || {};
      var field = data.size_by === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      function part(volume) { return volume ? volume[field] || 0 : 0; }
      var placed = 0;
      var points = (data.points || []).map(function (point) {
        var geo = point.geo || {};
        placed += part(point.volume);
        var operator = geo.asn ? kit.subject("operator", geo.asn, geo.operator || String(geo.asn), [[t("column.asn"), kit.text(geo.asn)]]) : null;
        var country = geo.country_code ? kit.subject("country", geo.country_code, geo.country_name || geo.country_code, []) : null;
        return {
          key: point.address, latitude: geo.latitude, longitude: geo.longitude, size: point.size || 0, cls: "allowed",
          subject: kit.subject("address", point.address, point.address, [
            [t("column.country"), geo.country_code ? (geo.country_name || geo.country_code) : kit.none(t("figure.no_country"))],
            [t("column.operator"), geo.operator || kit.none(t("figure.no_operator"))],
            [t("column.asn"), geo.asn ? kit.text(geo.asn) : kit.none(t("figure.no_operator"))],
            [t("column.bytes"), kit.value(point.volume, "bytes", { field: "bytes" })],
            [t("column.connections"), kit.value(point.volume, null, { field: "connections" })],
            [t("interfaces.title"), kit.text((point.interfaces || []).length)]
          ], { related: [operator, country].filter(Boolean) })
        };
      });
      var unplacedRows = data.unplaced || [];
      var unplaced = unplacedRows.reduce(function (sum, row) { return sum + part(row.volume); }, 0);
      var addresses = unplacedRows.reduce(function (sum, row) { return sum + (row.addresses || 0); }, 0);
      var below = part(data.below_minimum ? data.below_minimum.volume : null);
      var measure = data.total ? data.total.measure : "logged";
      function figure(value) { return kit.value({ measure: measure, lower_bound: true, value: value }, unit); }
      /* THE MOCKUP'S SIDE: the unplaced counter leads -- its figure, what it is, how many connections
       * and addresses it holds -- and the reconciliation is a box of its own under it. */
      var unplacedConnections = unplacedRows.reduce(function (sum, row) { return sum + (row.volume ? row.volume.connections || 0 : 0); }, 0);
      var placedConnections = (data.points || []).reduce(function (sum, point) {
        return sum + (point.volume ? point.volume.connections || 0 : 0); }, 0);
      var counter = h("div", { "class": "counter", "data-unplaced": String(unplaced) }, [
        h("span", { "class": "counter-value" }, figure(unplaced)),
        t(field === "bytes" ? "plugin.passed_traffic_world_map.widget.passed_traffic_world_map.unplaced_volume" : "plugin.passed_traffic_world_map.widget.passed_traffic_world_map.unplaced_connections"),
        h("span", { "class": "counter-second" }, [tp("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.connections_not_placed", unplacedConnections), " · ",
          tp("column.address_count", addresses)]),
        parameters.show_unplaced === false ? null : h("ul", { "class": "counter-list" }, unplacedRows.map(function (row) {
          return h("li", { key: row.reason, "data-reason": row.reason }, [
            /* Why, then how many addresses where there are any, then the figure with what it counts. */
            h("span", {}, [(kit.placeReason(row.reason) || tk("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.reason.", row.reason)),
              row.addresses ? [" · ", tp("column.address_count", row.addresses)] : null]),
            h("span", { "class": "counter-figure" }, [kit.value(row.volume, unit, { field: field }),
              field === "connections" ? [" ", t("column.figure.connections")] : null])
          ]);
        }))
      ]);
      function row(key, label, value) {
        return [h("dt", { key: "t-" + key }, label), h("dd", { key: "d-" + key }, value)];
      }
      var connectionsWord = function (count) { return kit.text(count); };
      var reconcile = h("div", { "class": "popup map-reconcile", "data-placed": String(placed), "data-unplaced": String(unplaced),
        "data-below-minimum": String(below), "data-below": String(below), "data-total": String(data.total ? part(data.total) : 0) }, [
        h("div", { key: "title", "class": "popup-title" }, t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.allowed_traffic")),
        h("dl", { key: "facts" }, [
          row("placed", t("column.map.placed"), figure(placed)),
          row("unplaced", t("column.map.not_placed"), figure(unplaced)),
          below > 0 ? row("below", tp("column.below_minimum", data.below_minimum ? data.below_minimum.members : 0), figure(below)) : null,
          row("total", t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.total_allowed"), data.total ? kit.value(data.total, unit, { field: field }) : kit.none()),
          row("connections-placed", t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.connections_placed"), connectionsWord(placedConnections)),
          row("connections-unplaced", t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.connections_unplaced"), connectionsWord(unplacedConnections)),
          row("connections-total", t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.connections_total"), connectionsWord(placedConnections + unplacedConnections))
        ])
      ]);
      return h("div", { "class": "map-layout" }, [
        h("div", { "class": "map-frame" }, [kit.map(points, { cluster: parameters.cluster !== false }),
          parameters.cluster !== false ? h("p", { "class": "table-foot", "data-cluster": "true" }, t("column.map.clustered")) : null]),
        h("div", { "class": "map-side" }, [counter, reconcile])
      ]);
    }
  });
})(this);
