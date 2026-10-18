/*
 * The renderer of `blocked_by_firewall_rule` (docs/widget-catalogue.md, "Blocked by firewall rule";
 * docs/widget-api.md): which rules are actually firing, and against whom. One row per rule: its
 * description -- or, for a rule that no longer exists, its label, as a first-class row saying so --
 * the interfaces it fired on, the clients it refused, its blocked connections, when it last fired
 * and its timeline across the window, every bucket a mark. The rules past the limit and the
 * automatic rules left out are rows of their own; the total closes the table.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "blocked_by_firewall_rule",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, format = input.format;
      var data = input.data || {};

      function ruleName(row) {
        if (row.rule) { return row.rule.description || row.rule.pf_label || row.rid; }
        return row.rid;
      }
      var rows = (data.rows || []).map(function (row) {
        var interfaces = (row.fired_on || []).map(function (fired) {
          return kit.subject("interface", fired.interface_id, fired.description || fired.device,
            [[t("detail.fact.device"), fired.device]]);
        });
        var badges = [];
        if (row.state === "rule_not_found") {
          badges.push(kit.badge(t("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.rule_not_found"), "gap"));
        }
        if (row.rule && row.rule.automatic) {
          badges.push(kit.badge(t("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.automatic")));
        }
        var timeline = (row.timeline || []).map(function (bucket) {
          return bucket.value === null || bucket.value === undefined ? null : kit.amount(bucket.value);
        });
        var facts = [
          [t("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.column.label"), h("span", { "class": "mono" }, row.rid)],
          [t("column.blocked"), kit.value(row.blocked_connections)],
          [t("column.clients"), kit.text((row.clients || []).length)],
          [t("column.interface"), interfaces.length ? kit.join(interfaces.map(function (subject) { return subject.title; }), ", ") : kit.none()],
          [t("column.last_seen"), row.last_seen_at ? format.instant(row.last_seen_at) : kit.none()]
        ];
        return {
          key: row.rid,
          className: row.state === "rule_not_found" ? "is-flagged" : null,
          subject: kit.subject("rule", row.rid, ruleName(row), facts, { related: interfaces,
            reference: row.rule && row.rule.pf_label ? { kind: "rule", by: "pf_label", value: row.rule.pf_label, label: ruleName(row) } : null }),
          cells: [
            h("span", {}, [ruleName(row), badges]),
            interfaces.length ? kit.join(interfaces.map(function (subject) { return subject.title; }), ", ") : kit.none(),
            kit.text((row.clients || []).length),
            kit.value(row.blocked_connections),
            row.last_seen_at ? format.instant(row.last_seen_at) : kit.none(),
            kit.spark(timeline, "5", { width: 96, height: 18 })
          ]
        };
      });
      var columns = [
        { key: "rule", label: t("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.column.rule") },
        { key: "interfaces", label: t("column.interface") },
        { key: "clients", label: t("column.clients"), num: true },
        { key: "blocked", label: t("column.blocked"), num: true },
        { key: "last", label: t("column.last_seen") },
        { key: "timeline", label: t("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.column.timeline") }
      ];
      var excluded = data.excluded_automatic || { members: 0 };
      if (excluded.members) {
        rows.push({ key: "excluded-automatic", className: "is-remainder", cells: [
          tp("plugin.blocked_by_firewall_rule.widget.blocked_by_firewall_rule.excluded_automatic", excluded.members), "", "",
          kit.value(excluded.count), "", ""] });
      }
      var remainder = data.remainder || { members: 0 };
      return h("div", { "class": "blocked-by-rule" }, [
        kit.table(columns, rows, {
          remainder: remainder.members ? { members: remainder.members, cells: ["", "", kit.value(remainder.count), "", ""] } : null,
          total: data.total ? { label: t("shape.total"), cells: ["", "", kit.value(data.total), "", ""] } : null,
          empty: t("table.no_rows")
        })
      ]);
    }
  });
})(this);
