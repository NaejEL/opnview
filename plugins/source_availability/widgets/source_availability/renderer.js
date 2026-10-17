/*
 * The renderer of `source_availability` (docs/widget-catalogue.md, "Source availability";
 * docs/widget-api.md, "Condition codes"): which source is working, and what is therefore not seen.
 * One row per registered provider: its name, its condition, its last outcome, when it was checked
 * and the collector's own diagnostic text, shown verbatim as data. `compact` keeps the provider and
 * its condition only.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "source_availability",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, format = input.format;
      var data = input.data || {};
      var compact = input.parameters.compact === true;
      var providers = data.providers || [];
      var columns = [
        { key: "provider", label: t("sources.column.provider") },
        { key: "condition", label: t("plugin.source_availability.widget.source_availability.column.condition") }
      ];
      if (!compact) {
        columns.push({ key: "outcome", label: t("plugin.source_availability.widget.source_availability.column.outcome") });
        columns.push({ key: "checked", label: t("sources.column.checked") });
        columns.push({ key: "detail", label: t("sources.column.detail"),
          cellClass: "diag" });
      }
      function subjectOf(provider) {
        var siblings = providers.filter(function (other) {
          return other.kind === provider.kind && other.provider_key !== provider.provider_key;
        });
        return kit.subject("provider", provider.kind + "/" + provider.provider_key, provider.display_name, [
          [t("sources.column.kind"), h("span", { "class": "mono" }, provider.kind)],
          [t("plugin.source_availability.widget.source_availability.column.key"), h("span", { "class": "mono" }, provider.provider_key)],
          [t("sources.column.active"), provider.active
            ? t("plugin.source_availability.widget.source_availability.active") : t("plugin.source_availability.widget.source_availability.inactive")],
          [t("plugin.source_availability.widget.source_availability.column.condition"), kit.stateBadge(provider.condition)],
          [t("plugin.source_availability.widget.source_availability.column.outcome"), provider.outcome ? tk("outcome.", provider.outcome) : kit.none()],
          [t("sources.column.checked"),
            provider.checked_at ? format.instant(provider.checked_at) : kit.none(t("state.source.never_checked"))],
          [t("sources.column.detail"), provider.detail === null || provider.detail === undefined
            ? kit.none() : String(provider.detail)]
        ], { reference: { kind: "provider", by: "kind_and_key", value: provider.kind + "/" + provider.provider_key,
          label: provider.display_name },
          related: siblings.map(function (other) {
            return kit.subject("provider", other.kind + "/" + other.provider_key, other.display_name, [
              [t("plugin.source_availability.widget.source_availability.column.condition"), kit.stateBadge(other.condition)]]);
          }) });
      }
      var rows = providers.map(function (provider) {
        var name = h("span", {}, [
          provider.display_name,
          provider.active ? null : h("span", { "class": "badge badge-gap" }, t("plugin.source_availability.widget.source_availability.inactive")),
          compact ? null : h("span", { "class": "ident" }, [provider.kind, " · ", provider.provider_key])
        ]);
        var cells = [name, kit.stateBadge(provider.condition)];
        if (!compact) {
          cells.push(provider.outcome ? tk("outcome.", provider.outcome) : kit.none());
          cells.push(provider.checked_at ? format.instant(provider.checked_at)
            : kit.none(t("state.source.never_checked")));
          // The collector's diagnostic text is data, passed through verbatim and never read.
          cells.push(provider.detail === null || provider.detail === undefined ? kit.none()
            : h("span", { "class": "detail" }, String(provider.detail)));
        }
        return { key: provider.kind + "/" + provider.provider_key,
          className: provider.condition === "reachable" ? null : "is-refused", cells: cells, subject: subjectOf(provider) };
      });
      return h("div", { "class": "source-availability", "data-compact": compact ? "true" : "false" }, [
        kit.table(columns, rows, { compact: compact, empty: t("table.no_rows") })
      ]);
    }
  });
})(this);
