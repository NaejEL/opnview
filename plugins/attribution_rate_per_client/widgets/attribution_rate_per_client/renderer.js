/*
 * The renderer of `attribution_rate_per_client` (docs/widget-catalogue.md, "Attribution rate per
 * client"; docs/widget-api.md): how much of each client's traffic can be named at all. One row per
 * client: the share of its eligible flows a site name was inferred for, the named and the eligible
 * flows, the methods each name was inferred by, and the mean delay between a lookup and its flow. A
 * rate with no eligible flow is a dash, never a zero; the clients past the limit are counted.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var METHODS = ["resolver_cache_answer", "lookup_timing", "not_recorded_beyond_flow_horizon"];

  opnview.widgets.register({
    type: "attribution_rate_per_client",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, format = input.format;
      var data = input.data || {};

      function methodsCell(methods) {
        var present = METHODS.filter(function (method) { return methods && methods[method] > 0; });
        if (present.length === 0) { return kit.none(t("figure.no_method")); }
        return h("span", { "class": "methods" }, present.map(function (method) {
          return h("span", { key: method, "class": "badge method method-" + method, "data-method": method },
            [tk("method.", method), " ", kit.text(methods[method])]);
        }));
      }
      function rateCell(row) {
        if (row.rate_state !== "defined" || row.rate === null || row.rate === undefined) {
          return kit.none(t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.no_eligible_flow"));
        }
        return h("span", { "class": "fig fig-derived", "data-rate": String(row.rate) }, format.percent(row.rate));
      }
      var rows = (data.rows || []).map(function (row) {
        var client = row.client;
        var delay = row.mean_delay_seconds === null || row.mean_delay_seconds === undefined ? kit.none()
          : kit.text(row.mean_delay_seconds, "seconds");
        return {
          key: String(client.id),
          subject: kit.clientSubject(client, [
            [t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.rate"), rateCell(row)],
            [t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.named"), kit.value(row.named_flows)],
            [t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.eligible"), kit.value(row.eligible_flows)],
            [t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.eligible_bytes"),
              row.eligible_bytes ? kit.value(row.eligible_bytes, "bytes") : kit.none()],
            [t("column.methods"), methodsCell(row.methods)],
            [t("column.mean_delay"), delay]
          ]),
          cells: [
            h("span", {}, [kit.clientLabel(client), client.unstable_identity
              ? kit.badge(t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.unstable")) : null]),
            rateCell(row),
            kit.value(row.named_flows),
            kit.value(row.eligible_flows),
            methodsCell(row.methods),
            delay
          ]
        };
      });
      var columns = [
        { key: "client", label: t("column.client") },
        { key: "rate", label: t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.rate"), num: true },
        { key: "named", label: t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.named"), num: true },
        { key: "eligible", label: t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.column.eligible"), num: true },
        { key: "methods", label: t("column.methods") },
        { key: "delay", label: t("column.mean_delay"), num: true }
      ];
      var remainder = data.remainder || { members: 0 };
      var notes = [];
      if (data.resolver_covered === false) {
        notes.push(kit.note(t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.resolver_not_covered"), "resolver"));
      }
      if (data.cache_covered === false) {
        notes.push(kit.note(t("plugin.attribution_rate_per_client.widget.attribution_rate_per_client.cache_not_covered"), "cache"));
      }
      return h("div", { "class": "attribution-rate" }, [
        kit.table(columns, rows, {
          remainder: remainder.members ? { members: remainder.members, cells: ["", "", kit.value(remainder.count), "", ""] } : null,
          empty: t("table.no_rows")
        }),
        notes
      ]);
    }
  });
})(this);
