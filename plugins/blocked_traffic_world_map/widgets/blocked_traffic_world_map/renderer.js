/*
 * The renderer of `blocked_traffic_world_map` (docs/widget-catalogue.md, "Blocked-traffic world map"):
 * where the stopped traffic was going, or coming from. Point marks on the basemap: a firewall rule's
 * refusal sized by its volume, a detection engine's by its events, each its own mark and its own hue.
 * The unplaced counter is always drawn, at zero too, with its breakdown per reason under
 * show_unplaced; the map-reconcile element states placed, unplaced and below the minimum against the
 * total of the blocked volume. The security events, the refused lookups and every refusal are stated
 * beside it, each in its own measure, never added to the volume.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "blocked_traffic_world_map",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var parameters = input.parameters || {};
      var field = data.size_by === "connections" ? "connections" : "bytes";
      var unit = field === "bytes" ? "bytes" : null;
      function part(volume) { return volume ? volume[field] || 0 : 0; }
      var placed = 0;
      var points = (data.points || []).map(function (point, index) {
        var geo = point.geo || {};
        var security = point.engine === "security_engine";
        if (point.volume) { placed += part(point.volume); }
        var operator = geo.asn ? kit.subject("operator", geo.asn, geo.operator || String(geo.asn), [[t("column.asn"), kit.text(geo.asn)]]) : null;
        var facts = [
          [t("column.engine"), tk("column.engine.", point.engine)],
          [t("column.country"), geo.country_code ? (geo.country_name || geo.country_code) : kit.none(t("figure.no_country"))],
          [t("column.operator"), geo.operator || kit.none(t("figure.no_operator"))]
        ];
        if (point.volume) {
          facts.push([t("column.bytes"), kit.value(point.volume, "bytes", { field: "bytes" })]);
          facts.push([t("column.connections"), kit.value(point.volume, null, { field: "connections" })]);
        }
        if (point.events) { facts.push([t("collection.kind.security_event"), kit.value(point.events)]); }
        return {
          key: point.engine + ":" + point.address + ":" + index, latitude: geo.latitude, longitude: geo.longitude,
          size: point.size || 0, cls: security ? "security" : "blocked",
          subject: kit.subject("address", point.engine + ":" + point.address, point.address, facts,
            { related: operator ? [operator] : [] })
        };
      });
      var unplacedRows = data.unplaced || [];
      var unplaced = unplacedRows.reduce(function (sum, row) { return sum + part(row.volume); }, 0);
      var addresses = unplacedRows.reduce(function (sum, row) { return sum + (row.addresses || 0); }, 0);
      var below = part(data.below_minimum ? data.below_minimum.volume : null);
      var measure = data.total ? data.total.measure : "logged";
      function figure(value) { return kit.value({ measure: measure, lower_bound: true, value: value }, unit); }
      function breakdown(rows, key) {
        return h("ul", { key: key, "class": "counter-list" }, rows.map(function (row) {
          return h("li", { key: row.reason, "data-reason": row.reason }, [
            h("span", {}, [(kit.placeReason(row.reason) || tk("plugin.blocked_traffic_world_map.widget.blocked_traffic_world_map.reason.", row.reason)), " · ", tp("column.address_count", row.addresses || 0)]),
            kit.value(row.volume, unit, { field: field })
          ]);
        }));
      }
      var counter = h("div", { "class": "counter", "data-unplaced": String(unplaced) }, [
        h("span", { "class": "counter-value" }, figure(unplaced)),
        h("span", { "class": "counter-second" }, tp("column.map.not_placed_addresses", addresses)),
        parameters.show_unplaced === false ? null : breakdown(unplacedRows, "volume"),
        parameters.show_unplaced === false || !(data.security_unplaced || []).length ? null : [
          h("span", { key: "security-head", "class": "counter-second" }, t("plugin.blocked_traffic_world_map.widget.blocked_traffic_world_map.security_unplaced")),
          breakdown(data.security_unplaced, "security")]
      ]);
      var reconcile = h("div", { "class": "map-reconcile", "data-placed": String(placed), "data-unplaced": String(unplaced),
        "data-below-minimum": String(below), "data-below": String(below), "data-total": String(data.total ? part(data.total) : 0) },
        kit.table([{ key: "what", label: t("sankey.column.what") }, { key: "value", label: t("sankey.column.value"), num: true }], [
          { key: "placed", cells: [t("column.map.placed"), figure(placed)] },
          { key: "unplaced", cells: [t("column.map.not_placed"), figure(unplaced)] },
          { key: "below", cells: [tp("column.below_minimum", data.below_minimum ? data.below_minimum.members : 0), figure(below)] }
        ], { total: data.total ? { label: t("shape.total"), cells: [kit.value(data.total, unit, { field: field })] } : null, compact: true }));
      var others = kit.table([{ key: "what", label: t("sankey.column.what") }, { key: "value", label: t("sankey.column.value"), num: true }], [
        { key: "events", cells: [t("collection.kind.security_event"), kit.value(data.security_events)] },
        { key: "lookups", cells: [t("column.refused_lookups"), kit.value(data.dns_blocked_lookups)] },
        { key: "refusals", cells: [t("plugin.blocked_traffic_world_map.widget.blocked_traffic_world_map.total_refusals"), kit.value(data.total_refusals)] }
      ], { compact: true });
      return h("div", { "class": "map-layout" }, [
        h("div", { "class": "map-frame" }, [
          kit.map(points, { cluster: parameters.cluster !== false }),
          parameters.cluster !== false ? h("p", { "class": "table-foot", "data-cluster": "true" }, t("column.map.clustered")) : null,
          kit.legend([{ key: "rule", cls: "blocked", label: tk("column.engine.", "firewall_rule") },
            { key: "security", cls: "5", label: tk("column.engine.", "security_engine") }])
        ]),
        h("div", { "class": "map-side" }, [counter, reconcile, others])
      ]);
    }
  });
})(this);
