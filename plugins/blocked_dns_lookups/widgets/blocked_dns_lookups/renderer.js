/*
 * The renderer of `blocked_dns_lookups` (docs/widget-catalogue.md, "Blocked DNS lookups";
 * docs/widget-api.md): which domains the resolver refused, and which list did the refusing. One row
 * per domain and list: the list, its purpose, the refused lookups, the clients that asked -- and the
 * asking addresses no client matched, counted rather than dropped -- and when it was last refused.
 * The domains past the limit and those under min_count are rows of their own; the total closes it.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "blocked_dns_lookups",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, tp = input.tp, format = input.format;
      var data = input.data || {};

      /* A purpose: one of the blocklist purposes, or the two states a list can be in without one. */
      function purposeOf(purpose) {
        if (purpose === "unassigned") { return t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.purpose.unassigned"); }
        if (purpose === "not_recorded") { return t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.purpose.not_recorded"); }
        return tk("blocklists.purpose.", purpose);
      }
      function clientsOf(row) {
        var clients = (row.clients || []).filter(function (client) { return client && typeof client === "object"; });
        return clients;
      }
      var rows = (data.rows || []).map(function (row) {
        var clients = clientsOf(row);
        var clientCount = (row.clients || []).length;
        var unmatched = (row.unmatched_client_addresses || []).length;
        var listSubject = row.list ? kit.subject("blocklist", row.list, row.list,
          [[t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.purpose"), purposeOf(row.purpose)]]) : null;
        var related = (listSubject ? [listSubject] : []).concat(clients.map(function (client) { return kit.clientSubject(client); }));
        var facts = [
          [t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.list"), row.list ? row.list
            : kit.none(t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.no_list"))],
          [t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.purpose"), purposeOf(row.purpose)],
          [t("column.refused_lookups"), kit.value(row.lookups)],
          [t("column.clients"), kit.text(clientCount)],
          [t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.unmatched"), unmatched
            ? h("span", { "class": "mono" }, kit.join(row.unmatched_client_addresses, ", ")) : kit.text(0)],
          [t("column.last_seen"), row.last_seen_at ? format.instant(row.last_seen_at) : kit.none()]
        ];
        return {
          key: row.domain + "|" + row.list,
          subject: kit.subject("domain", row.domain + "|" + row.list, row.domain, facts, { related: related }),
          cells: [
            h("span", { "class": "mono" }, row.domain),
            row.list ? row.list : kit.none(t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.no_list")),
            purposeOf(row.purpose),
            kit.value(row.lookups),
            h("span", {}, [kit.text(clientCount), unmatched ? h("span", { "class": "ident" },
              tp("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.unmatched_count", unmatched)) : null]),
            row.last_seen_at ? format.instant(row.last_seen_at) : kit.none()
          ]
        };
      });
      var columns = [
        { key: "domain", label: t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.domain") },
        { key: "list", label: t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.list") },
        { key: "purpose", label: t("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.column.purpose") },
        { key: "lookups", label: t("column.refused_lookups"), num: true },
        { key: "clients", label: t("column.clients"), num: true },
        { key: "last", label: t("column.last_seen") }
      ];
      var below = data.below_min_count || { members: 0 };
      if (below.members) {
        rows.push({ key: "below-min-count", className: "is-remainder", cells: [
          tp("plugin.blocked_dns_lookups.widget.blocked_dns_lookups.below_min_count", below.members), "", "",
          kit.value(below.count), "", ""] });
      }
      var remainder = data.remainder || { members: 0 };
      return h("div", { "class": "blocked-dns-lookups" }, [
        kit.table(columns, rows, {
          remainder: remainder.members ? { members: remainder.members, cells: ["", "", kit.value(remainder.count), "", ""] } : null,
          total: data.total ? { label: t("shape.total"), cells: ["", "", kit.value(data.total), "", ""] } : null,
          empty: t("table.no_rows")
        })
      ]);
    }
  });
})(this);
