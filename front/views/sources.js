/*
 * opnview front -- the Sources view (F7-12): every registered provider of every kind, the current
 * outages, and the collection gaps of the recent window, on GET /api/sources.
 *
 * WHEN A STATE BEGAN IS A BOUNDED INTERVAL, NEVER ONE INSTANT. opnview sees a provider only when it
 * probes or reads it, so a state is said to have begun after one check and by the next; with no
 * bounds the view names why there are none. The collector's diagnostic text is shown verbatim, as
 * data, and never read. A provider installed and not collecting links to the OPNsense page that
 * turns it on -- a change opnview never makes.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms, format = opnview.format;
  var t = opnview.strings.t, tk = opnview.strings.tk, td = opnview.strings.td;

  function load(context) {
    return context.api.get("/api/sources").then(function (result) {
      context.state.failed = result.status !== 200;
      if (result.status === 200) { context.state.answer = result.body; }
    });
  }

  function providerName(kind, key, displayName) {
    // A provider's name is composed from its registry row; the registry's own test holds every row
    // of a fresh schema to a catalogue entry. A display name the row carries is data.
    return displayName || td("provider." + kind + "." + key);
  }

  function availabilityBadge(code) {
    var tone = code === "healthy" ? "ok" : code === "not_collecting" ? "off" : code === "unavailable" ? "unavailable"
      : "gap";
    return forms.badge("availability", tk("state.availability.", code), tone);
  }

  function since(value) {
    if (!value) { return null; }
    if (value.after_at !== null && value.by_at !== null) {
      return t("sources.since.between", { after: format.instant(value.after_at), by: format.instant(value.by_at) });
    }
    return tk("sources.since.", value.state || "state_unknown");
  }

  function providersTable(answer) {
    var rows = answer.providers.map(function (provider) {
      return h("tr", { key: provider.kind + "/" + provider.provider_key,
        "class": provider.in_outage ? "is-flagged" : null }, [
        h("td", {}, tk("collection.kind.", provider.kind)),
        h("td", {}, [providerName(provider.kind, provider.provider_key, provider.display_name),
          h("span", { "class": "ident" }, provider.provider_key)]),
        h("td", { "class": "avail" }, [availabilityBadge(provider.availability),
          provider.outcome ? h("span", { "class": "ident" }, tk("outcome.", provider.outcome)) : null]),
        h("td", {}, provider.active ? t("state.yes") : t("state.no")),
        h("td", {}, provider.checked_at === null ? t("state.source.never_checked") : format.instant(provider.checked_at)),
        h("td", { "class": "diag" }, since(provider.since)),
        h("td", { "class": "mono" }, provider.probe),
        h("td", { "class": "diag" }, [
          provider.detail ? h("span", { "class": "mono detail" }, provider.detail)
            : h("span", { "class": "muted" }, t("state.source.no_detail")),
          (provider.opnsense_pages || []).map(function (page) {
            return h("a", { key: "page-" + page.page, href: page.url, rel: "noreferrer", "class": "opnsense-page" },
              td(page.label));
          })
        ])
      ]);
    });
    return h("div", { "class": "table-scroll" }, h("table", { "class": "data sources-table" }, [
      h("thead", {}, h("tr", {}, [
        h("th", {}, t("sources.column.kind")), h("th", {}, t("sources.column.provider")),
        h("th", {}, t("sources.column.availability")), h("th", {}, t("sources.column.active")),
        h("th", {}, t("sources.column.checked")), h("th", { "class": "diag" }, t("sources.column.since")),
        h("th", {}, t("sources.column.probe")), h("th", { "class": "diag" }, t("sources.column.detail"))
      ])),
      h("tbody", {}, rows)
    ]));
  }

  function outagesCard(answer) {
    var items = answer.outages.map(function (outage) {
      return h("li", { key: outage.kind + "/" + (outage.provider_key || "") }, [
        h("span", {}, tk("collection.kind.", outage.kind)),
        outage.provider_key ? h("span", { "class": "mono" }, outage.provider_key) : null,
        forms.badge("state", outage.provider_key ? t("state.outage.provider") : t("state.outage.kind"), "unavailable")
      ]);
    });
    return forms.card("outages", t("sources.heading.outages"), items.length === 0
      ? h("p", { "class": "muted" }, t("indicator.no_outage"))
      : h("ul", { "class": "outage-list" }, items));
  }

  function gapsCard(answer) {
    var rows = answer.recent_gaps.map(function (gap, index) {
      return h("tr", { key: "gap-" + index }, [
        h("td", {}, tk("collection.kind.", gap.kind)),
        h("td", { "class": "mono" }, gap.provider_key),
        h("td", { "class": "diag" }, tk("gap_reason.", gap.reason)),
        h("td", {}, format.instant(gap.start_at)),
        h("td", {}, format.instant(gap.end_at)),
        h("td", { "class": "num" }, format.duration(gap.missed_seconds)),
        h("td", { "class": "diag" }, gap.detail ? h("span", { "class": "mono detail" }, gap.detail) : null)
      ]);
    });
    return forms.card("gaps", t("sources.heading.gaps_window", { window: format.duration(answer.recent_gap_window_seconds) }),
      rows.length === 0 ? h("p", { "class": "muted" }, t("state.source.no_recent_gap"))
        : h("div", { "class": "table-scroll" }, h("table", { "class": "data" }, [
          h("thead", {}, h("tr", {}, [
            h("th", {}, t("sources.column.kind")), h("th", {}, t("sources.column.provider")),
            h("th", { "class": "diag" }, t("collection.label.gap_reason")), h("th", {}, t("sources.column.start")),
            h("th", {}, t("sources.column.end")), h("th", { "class": "num" }, t("collection.label.missed_seconds")),
            h("th", { "class": "diag" }, t("sources.column.detail"))
          ])),
          h("tbody", {}, rows)
        ])));
  }

  opnview.views.register({
    id: "sources",
    path: "/sources",
    rail: "installation",
    order: 40,
    title: function () { return t("sources.title"); },
    load: load,
    render: function (context) {
      var answer = context.state.answer;
      if (!answer) {
        return context.state.failed ? h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" },
          t("state.read_failed"))) : h("p", { "class": "loading", role: "status" }, t("state.loading"));
      }
      return h("div", { "class": "cards" }, [
        outagesCard(answer),
        forms.card("providers", t("sources.heading.providers"), providersTable(answer)),
        gapsCard(answer)
      ]);
    }
  });
})(this);
