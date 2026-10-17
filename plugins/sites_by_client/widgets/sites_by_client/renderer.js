/*
 * The renderer of `sites_by_client` (docs/widget-catalogue.md, "Sites by client"): which site was used,
 * and by whom. One row per client: the client, its sites as chips -- each with its flows, and opening a
 * detail carrying its bytes, its operator and the attribution methods its name was inferred by
 * (F7-25) -- then the sites the per-client limit dropped and those under min_flows, counted, and, when
 * asked, the share of the client's flows that could be named at all. A client none of whose flows was
 * named says so in its row rather than disappearing.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var METHODS = ["resolver_cache_answer", "lookup_timing", "not_recorded_beyond_flow_horizon"];

  function methodsNode(kit, tk, methods) {
    var present = METHODS.filter(function (method) { return methods && methods[method] > 0; });
    if (present.length === 0) { return null; }
    return kit.h("span", { "class": "methods" }, present.map(function (method) {
      return kit.h("span", { key: method, "class": "badge method method-" + method, "data-method": method },
        [tk("method.", method), " ", kit.text(methods[method])]);
    }));
  }

  opnview.widgets.register({
    type: "sites_by_client",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, tk = input.tk, data = input.data || {};
      var showRate = input.parameters.show_attribution_rate !== false;
      var columns = [
        { key: "client", label: t("column.client") },
        { key: "sites", label: t("plugin.sites_by_client.widget.sites_by_client.sites") }
      ];
      if (showRate) { columns.push({ key: "rate", label: t("plugin.sites_by_client.widget.sites_by_client.named_share"), num: true }); }
      var rows = (data.rows || []).map(function (row) {
        var client = row.client || {};
        var chips = (row.sites || []).map(function (site) {
          var facts = [
            [t("column.flows"), kit.value(site.flows)],
            [t("column.bytes"), kit.value(site.bytes, "bytes")],
            [t("column.methods"), methodsNode(kit, tk, site.methods) || kit.none(t("figure.no_method"))],
            [t("column.operator"), site.operator || kit.none(t("figure.no_operator"))],
            [t("column.country"), site.country_code || kit.none(t("figure.no_country"))]
          ];
          /* A destination no name was inferred for is its address, with why it has no place where its
           * geolocation did not answer (docs/widget-api.md, "Destinations with no name"). */
          if (site.kind === "address") {
            return { key: "address:" + site.address, label: site.address,
              value: [kit.value(site.flows), site.lookup_state && site.lookup_state !== "resolved"
                ? kit.badge(tk("reason.", site.lookup_state), "gap") : null],
              subject: kit.subject("address", site.address, site.address, facts) };
          }
          return { key: site.name, label: site.name, value: [kit.value(site.flows), methodsNode(kit, tk, site.methods)],
            subject: kit.subject("site", site.name, site.name, facts) };
        });
        var remainder = row.remainder || { members: 0 };
        var below = row.below_min_flows || { members: 0 };
        var cell = [
          row.state === "named" ? null : kit.badge(t("reason.no_site_name"), "gap"),
          kit.chips(chips, remainder.members, remainder.members ? kit.value(remainder.count) : null),
          below.members ? h("span", { "class": "muted", "data-below-min-flows": String(below.members) },
            [" ", tp("column.sites_below_minimum", below.members), " ", kit.value(below.count)]) : null
        ];
        var attribution = row.attribution;
        var cells = [kit.clientLabel(client), cell];
        if (showRate) {
          cells.push(attribution && attribution.rate_state === "defined"
            ? h("span", { "data-attribution-rate": String(attribution.rate) }, input.format.percent(attribution.rate, 0))
            : kit.none(t("plugin.sites_by_client.widget.sites_by_client.rate_undefined")));
        }
        return { key: String(client.id), cells: cells, subject: kit.clientSubject(client, attribution ? [
          [t("plugin.sites_by_client.widget.sites_by_client.named_flows"), kit.value(attribution.named_flows)],
          [t("plugin.sites_by_client.widget.sites_by_client.eligible_flows"), kit.value(attribution.eligible_flows)]] : []) };
      });
      return kit.table(columns, rows, { empty: t("table.no_rows") });
    }
  });
})(this);
