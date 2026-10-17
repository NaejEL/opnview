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
      var counter = h("div", { "class": "counter", "data-unplaced": String(unplaced) }, [
        h("span", { "class": "counter-value" }, figure(unplaced)),
        h("span", { "class": "counter-second" }, tp("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.unplaced", addresses)),
        parameters.show_unplaced === false ? null : h("ul", { "class": "counter-list" }, unplacedRows.map(function (row) {
          return h("li", { key: row.reason, "data-reason": row.reason }, [
            h("span", {}, [tk("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.reason.", row.reason), " · ", tp("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.addresses", row.addresses || 0)]),
            kit.value(row.volume, unit, { field: field })
          ]);
        }))
      ]);
      var reconcile = h("div", { "class": "map-reconcile", "data-placed": String(placed), "data-unplaced": String(unplaced),
        "data-below-minimum": String(below), "data-below": String(below), "data-total": String(data.total ? part(data.total) : 0) },
        kit.table([{ key: "what", label: t("sankey.column.what") }, { key: "value", label: t("sankey.column.value"), num: true }], [
          { key: "placed", cells: [t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.placed"), figure(placed)] },
          { key: "unplaced", cells: [t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.unplaced_row"), figure(unplaced)] },
          { key: "below", cells: [tp("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.below_minimum", data.below_minimum ? data.below_minimum.members : 0), figure(below)] }
        ], { total: data.total ? { label: t("shape.total"), cells: [kit.value(data.total, unit, { field: field })] } : null, compact: true }));
      return h("div", { "class": "map-layout" }, [
        h("div", { "class": "map-frame" }, [kit.map(points, { cluster: parameters.cluster !== false }),
          parameters.cluster !== false ? h("p", { "class": "table-foot", "data-cluster": "true" }, t("plugin.passed_traffic_world_map.widget.passed_traffic_world_map.clustered")) : null]),
        h("div", { "class": "map-side" }, [counter, reconcile])
      ]);
    }
  });
})(this);
