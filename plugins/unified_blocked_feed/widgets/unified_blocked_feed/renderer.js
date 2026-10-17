/*
 * The renderer of `unified_blocked_feed` (docs/widget-catalogue.md, "Unified blocked feed"): what was
 * blocked, and what blocked it. Entries rather than cells, newest first: the instant, the engine that
 * decided, what was refused and on which interface, and what decided it -- a rule, a list, a
 * signature, or a rule or list that is no longer known, said in the row rather than left blank. Each
 * contributing source is a row of its own with its condition and its count, never collapsed, so an
 * empty feed is never read as "nothing happened". Grouped by engine, the groups and their counts lead.
 * The entries the limit dropped are counted.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "unified_blocked_feed",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {};
      var requested = (input.answer && input.answer.window && input.answer.window.requested) || {};
      var span = requested.to && requested.from ? requested.to - requested.from : 86400;
      function decidedBy(decided) {
        if (!decided) { return kit.none(); }
        switch (decided.kind) {
          case "rule": return decided.rule ? (decided.rule.description || decided.rule.pf_label || String(decided.reference)) : String(decided.reference);
          case "rule_not_found": return t("plugin.unified_blocked_feed.widget.unified_blocked_feed.rule_not_found", { reference: String(decided.reference) });
          case "list_not_recorded": return t("plugin.unified_blocked_feed.widget.unified_blocked_feed.list_not_recorded");
          default: return decided.reference === null || decided.reference === undefined ? kit.none() : String(decided.reference);
        }
      }
      var entries = (data.entries || []).map(function (entry) {
        var target = entry.target_is_this_firewall ? t("plugin.unified_blocked_feed.widget.unified_blocked_feed.this_firewall") : String(entry.target);
        var facts = [
          [t("feed.column.time"), input.format.instant(entry.occurred_at)],
          [t("plugin.unified_blocked_feed.widget.unified_blocked_feed.engine"), tk("plugin.unified_blocked_feed.widget.unified_blocked_feed.engine.", entry.engine_kind)],
          [t("plugin.unified_blocked_feed.widget.unified_blocked_feed.decided_by"), decidedBy(entry.decided_by)],
          [t("plugin.unified_blocked_feed.widget.unified_blocked_feed.target"), target],
          [t("column.interface"), entry.interface ? kit.interfaceLabel(entry.interface) : kit.none()]
        ];
        return {
          key: entry.source_table + ":" + entry.source_id,
          time: input.format.tick(entry.occurred_at, span),
          kind: tk("plugin.unified_blocked_feed.widget.unified_blocked_feed.engine.", entry.engine_kind),
          what: [h("span", { "class": entry.target_kind === "domain" ? "site-name" : "mono" }, target),
            entry.interface ? h("span", { "class": "muted" }, [" · ", kit.interfaceLabel(entry.interface)]) : null],
          by: decidedBy(entry.decided_by),
          subject: kit.subject("blocked", entry.source_table + ":" + entry.source_id, target, facts,
            { related: entry.interface ? [kit.interfaceSubject(entry.interface)] : [] })
        };
      });
      var sources = (data.sources || []).map(function (source) {
        return { key: source.provider_kind, label: tk("collection.kind.", source.provider_kind),
          node: [kit.stateBadge(source.condition), " ", kit.value(source.count)] };
      });
      var groups = data.group_by && data.group_by !== "none" && data.groups ? data.groups : null;
      return h("div", { "class": "unified-blocked-feed" }, [
        groups ? kit.chips(groups.map(function (group) {
          return { key: String(group.key), label: data.group_by === "engine" ? tk("plugin.unified_blocked_feed.widget.unified_blocked_feed.engine.", group.key) : String(group.key),
            value: kit.value(group.count) };
        })) : null,
        kit.feed([{ label: t("feed.column.time") }, { label: t("plugin.unified_blocked_feed.widget.unified_blocked_feed.engine"), className: "engine" },
          { label: t("feed.column.what") }, { label: t("plugin.unified_blocked_feed.widget.unified_blocked_feed.decided_by"), className: "num" }], entries,
          { sources: sources, remainder: data.remainder ? data.remainder.members : 0 }),
        data.total ? h("div", { key: "total", "class": "remainder-row is-total", "data-total": String(kit.amount(data.total)) }, [
          h("span", {}, t("shape.total")), kit.value(data.total)]) : null
      ]);
    }
  });
})(this);
