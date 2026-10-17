/*
 * The renderer of `top_sites` (docs/widget-catalogue.md, "Top sites"; docs/widget-api.md): which sites
 * this network is using. A table of the named sites, ranked as the answer ranks them, each row with
 * its flows, its bytes, its clients and -- per record, as F7-25 requires -- the attribution methods
 * its name was inferred by. The rows the limit dropped and the rows under min_flows are their own
 * rows with their own counts; the total closes the table.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The attribution methods a site's flows can be named by, in the order they are shown. */
  var METHODS = ["resolver_cache_answer", "lookup_timing", "not_recorded_beyond_flow_horizon"];

  /* The methods of one row: each method that named a flow, with its count. */
  function methodsCell(kit, t, tk, methods) {
    var present = METHODS.filter(function (method) { return methods && methods[method] > 0; });
    if (present.length === 0) { return kit.none(t("figure.no_method")); }
    return kit.h("span", { "class": "methods" }, present.map(function (method) {
      return kit.h("span", { key: method, "class": "badge method method-" + method, "data-method": method },
        [tk("method.", method), " ", kit.text(methods[method])]);
    }));
  }

  opnview.widgets.register({
    type: "top_sites",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {};
      var rows = (data.rows || []).map(function (row) {
        var facts = [
          [t("column.flows"), kit.value(row.flows)],
          [t("column.bytes"), kit.value(row.bytes, "bytes")],
          [t("column.clients"), kit.text(row.clients)],
          [t("column.methods"), methodsCell(kit, t, tk, row.methods)],
          [t("column.mean_delay"), row.mean_delay_seconds === null || row.mean_delay_seconds === undefined ? kit.none()
            : kit.text(row.mean_delay_seconds, "seconds")],
          [t("column.operator"), row.operator || kit.none(t("figure.no_operator"))],
          [t("column.country"), row.country_code || kit.none(t("figure.no_country"))]
        ];
        /* A destination no name was inferred for is ranked with the sites, and drawn as its address,
         * with why it has no place where its geolocation did not answer (docs/widget-api.md,
         * "Destinations with no name"). */
        if (row.kind === "address") {
          return {
            key: "address:" + row.address,
            subject: kit.subject("address", row.address, row.address, facts.filter(function (fact) {
              return fact[0] !== t("column.methods") && fact[0] !== t("column.mean_delay");
            })),
            cells: [
              h("span", { "class": "site-name mono" }, [row.address, row.lookup_state && row.lookup_state !== "resolved"
                ? [" ", kit.badge(tk("reason.", row.lookup_state), "gap")] : null]),
              kit.value(row.flows),
              kit.value(row.bytes, "bytes"),
              kit.text(row.clients),
              kit.none(t("figure.no_method"))
            ]
          };
        }
        return {
          key: row.name,
          subject: kit.subject("site", row.name, row.name, facts,
            { reference: null, related: row.asn ? [kit.subject("operator", row.asn, row.operator || String(row.asn), [
              [t("column.asn"), kit.text(row.asn)]])] : [] }),
          cells: [
            h("span", { "class": "site-name" }, row.name),
            kit.value(row.flows),
            kit.value(row.bytes, "bytes"),
            kit.text(row.clients),
            methodsCell(kit, t, tk, row.methods)
          ]
        };
      });
      var columns = [
        { key: "site", label: t("column.site") },
        { key: "flows", label: t("column.flows"), num: true },
        { key: "bytes", label: t("column.bytes"), num: true },
        { key: "clients", label: t("column.clients"), num: true },
        { key: "methods", label: t("column.methods") }
      ];
      var remainder = data.remainder || { members: 0 };
      var below = data.below_min_flows || { members: 0 };
      var total = data.total || null;
      var extraRows = [];
      if (below.members) {
        extraRows.push({ key: "below-min-flows", className: "is-remainder", cells: [
          input.tp("column.sites_below_minimum", below.members), kit.value(below.count), kit.none(), kit.none(), ""] });
      }
      var attribution = data.attribution;
      return h("div", { "class": "top-sites" }, [
        kit.table(columns, rows.concat(extraRows), {
          remainder: remainder.members ? { members: remainder.members, cells: [kit.value(remainder.count), kit.none(), kit.none(), ""] } : null,
          total: total ? { label: input.tp("plugin.top_sites.widget.top_sites.total", total.members), cells: [kit.value(total.count), "", "", ""] } : null,
          empty: t("table.no_rows")
        }),
        attribution && attribution.rate_state === "defined" ? h("p", { "class": "table-foot", "data-attribution-rate": String(attribution.rate) },
          t("plugin.top_sites.widget.top_sites.named_share", { share: input.format.percent(attribution.rate), named: kit.text(kit.amount(attribution.named_flows)),
            eligible: kit.text(kit.amount(attribution.eligible_flows)) })) : null
      ]);
    }
  });
})(this);
