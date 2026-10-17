/*
 * The renderer of `aggregate_freshness` (docs/widget-catalogue.md, "Aggregate freshness";
 * docs/widget-api.md): whether the pre-computed numbers are current. One row per aggregate period:
 * its slots, the slots that are stale, when it was last computed, what it covers and, when asked,
 * the bytes it holds. `compact` keeps the period, the stale slots and the last computation only.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "aggregate_freshness",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, format = input.format;
      var data = input.data || {};
      var compact = input.parameters.compact === true;
      var periods = data.periods || [];
      var showBytes = periods.some(function (period) { return period.bytes !== undefined && period.bytes !== null; });
      var columns = [
        { key: "period", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.period") },
        { key: "state", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.state") },
        { key: "stale", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.stale_slots"), num: true },
        { key: "computed", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.last_computed") }
      ];
      if (!compact) {
        columns.push({ key: "slots", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.slots"), num: true });
        columns.push({ key: "covers", label: t("plugin.aggregate_freshness.widget.aggregate_freshness.column.covers") });
        if (showBytes) { columns.push({ key: "bytes", label: t("column.bytes"), num: true }); }
      }
      var rows = periods.map(function (period) {
        var state = period.stale
          ? kit.badge(t("plugin.aggregate_freshness.widget.aggregate_freshness.stale"), "unavailable")
          : kit.badge(t("plugin.aggregate_freshness.widget.aggregate_freshness.fresh"), "ok");
        var computed = period.last_computed_at ? format.instant(period.last_computed_at)
          : kit.none(t("plugin.aggregate_freshness.widget.aggregate_freshness.never_computed"));
        var covers = period.earliest_period_start_at && period.latest_period_end_at
          ? t("plugin.aggregate_freshness.widget.aggregate_freshness.between",
            { from: format.instant(period.earliest_period_start_at), to: format.instant(period.latest_period_end_at) })
          : kit.none();
        var facts = [
          [t("plugin.aggregate_freshness.widget.aggregate_freshness.column.slots"), kit.text(period.slots)],
          [t("plugin.aggregate_freshness.widget.aggregate_freshness.column.stale_slots"), kit.text(period.stale_slots)],
          [t("plugin.aggregate_freshness.widget.aggregate_freshness.column.last_computed"), computed],
          [t("plugin.aggregate_freshness.widget.aggregate_freshness.column.covers"), covers],
          [t("plugin.aggregate_freshness.widget.aggregate_freshness.read_from"),
            period.read_from ? format.instant(period.read_from) : kit.none()]
        ];
        if (period.bytes) { facts.push([t("column.bytes"), kit.value(period.bytes, "bytes")]); }
        var cells = [tk("period.short.", period.period), state, kit.text(period.stale_slots), computed];
        if (!compact) {
          cells.push(kit.text(period.slots));
          cells.push(covers);
          if (showBytes) { cells.push(period.bytes ? kit.value(period.bytes, "bytes") : kit.none()); }
        }
        return { key: period.period, className: period.stale ? "is-flagged" : null, cells: cells,
          subject: kit.subject("aggregate_period", period.period, tk("period.", period.period), facts) };
      });
      return h("div", { "class": "aggregate-freshness", "data-compact": compact ? "true" : "false" }, [
        kit.table(columns, rows, { compact: compact, empty: t("table.no_rows") })
      ]);
    }
  });
})(this);
