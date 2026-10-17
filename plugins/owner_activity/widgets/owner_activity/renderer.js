/*
 * The renderer of `owner_activity` (docs/widget-catalogue.md, "Per-person activity"): what one person's
 * machines did, taken together. One card per person -- and the machines nobody claimed as a card like
 * any other -- with the person's volume, the count of machines, the blocked connections, the current
 * rate (a sampled figure, a dash when no current sample exists) and, with show_timeline, the period's
 * history one mark per bucket. Each machine is a chip carrying its own state: measured, with its
 * figure, or no data in the period. The machines past clients_per_card are the remainder chip.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "owner_activity",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tp = input.tp, data = input.data || {};
      var field = data.measure === "allowed" ? "allowed_bytes" : data.measure === "blocked" ? "blocked_bytes" : "bytes";
      var showTimeline = input.parameters.show_timeline !== false;

      function rate(figure) {
        if (!figure || figure.state === "no_current_sample") { return kit.none(t("reason.no_current_sample")); }
        return h("span", { "class": "rate" }, [
          kit.value({ measure: figure.measure, lower_bound: figure.lower_bound, value: figure.in_bits_per_second }, "bits_per_second"),
          " / ",
          figure.out_measured ? kit.value({ measure: figure.measure, lower_bound: figure.lower_bound, value: figure.out_bits_per_second },
            "bits_per_second") : kit.none(t("plugin.owner_activity.widget.owner_activity.out_not_measured")),
          figure.state === "stale_sample" ? kit.badge(t("reason.stale_sample"), "gap") : null
        ]);
      }

      function card(group, unassigned) {
        var volume = group.volume || {};
        var name = unassigned ? t("plugin.owner_activity.widget.owner_activity.unassigned") : group.owner.display_name;
        var facts = [
          [t("column.bytes"), kit.value(volume, "bytes", { field: "bytes" })],
          [t("column.connections"), kit.value(volume, null, { field: "connections" })],
          [t("column.blocked"), kit.value(volume, null, { field: "blocked_connections" })],
          [t("plugin.owner_activity.widget.owner_activity.now"), rate(group.current_rate)]
        ];
        var subject = unassigned ? kit.subject("owner", "unassigned", name, facts) : kit.ownerSubject(group.owner, facts);
        var chips = (group.chips || []).map(function (chip) {
          var client = chip.client || {};
          var silent = chip.state !== "measured";
          /* The mockup's chip: the machine's name, its address and MAC in small type, its figure. */
          var ident = [client.last_address, client.mac].filter(function (part) { return part && part !== kit.clientLabel(client); });
          return {
            key: String(client.id), className: silent ? "chip-silent" : null,
            label: [kit.clientLabel(client), ident.length ? [" ", h("span", { key: "id", "class": "chip-id mono" }, kit.join(ident))] : null],
            value: silent ? t("plugin.owner_activity.widget.owner_activity.no_data_in_period") : kit.text(chip.volume ? chip.volume[field] : null, "bytes"),
            subject: kit.clientSubject(client, [[t("column.bytes"), kit.value(chip.volume, "bytes", { field: "bytes" })],
              [t("plugin.owner_activity.widget.owner_activity.now"), rate(chip.current_rate)]])
          };
        });
        var remainder = group.chip_remainder || { members: 0 };
        var blocked = volume.blocked_connections || 0;
        return h("div", kit.attrs({ key: group.key, "class": "owner" + (unassigned ? " owner-unassigned" : ""), "data-owner": group.key },
          {}), [
          h("div", { "class": "owner-head" }, [
            h("span", kit.attrs({ "class": "owner-name" }, kit.pick(subject)), name),
            kit.badge(tp("plugin.owner_activity.widget.owner_activity.machines", group.machines || 0)),
            unassigned ? kit.badge(t("plugin.owner_activity.widget.owner_activity.no_owner_assigned")) : null,
            blocked > 0 ? kit.badge(tp("plugin.owner_activity.widget.owner_activity.blocked", blocked), "blocked") : null,
            h("span", { "class": "owner-rate" }, kit.value(volume, "bytes", { field: field }))
          ]),
          /* The rate now, where a sample exists, beside the period: the mockup's head figure. */
          group.current_rate && group.current_rate.state !== "no_current_sample"
            ? h("div", { "class": "owner-line" }, [h("span", { "class": "line-label" }, t("plugin.owner_activity.widget.owner_activity.now")), rate(group.current_rate)]) : null,
          h("div", { "class": "owner-line" }, [h("span", { "class": "line-label" }, t("plugin.owner_activity.widget.owner_activity.machines_label")),
            kit.chips(chips, remainder.members, remainder.members ? kit.text(remainder.volume ? remainder.volume[field] : null, "bytes") : null)]),
          showTimeline && group.timeline ? h("div", { "class": "owner-line" }, [h("span", { "class": "line-label" }, t("plugin.owner_activity.widget.owner_activity.timeline")),
            kit.spark(group.timeline.map(function (bucket) { return bucket.gap && !bucket.volume ? null : (bucket.volume ? bucket.volume[field] : bucket.value); }),
              "1", { width: 160 })]) : null
        ]);
      }

      var cards = (data.cards || []).map(function (group) { return card(group, false); });
      if (data.unassigned) { cards.push(card(data.unassigned, true)); }
      return h("div", { "class": "owners" }, [
        cards,
        data.totals_exclude_unassigned ? kit.note(t("plugin.owner_activity.widget.owner_activity.totals_exclude_unassigned"), "exclude") : null,
        data.total ? h("div", { key: "total", "class": "remainder-row is-total" }, [h("span", {}, t("shape.total")),
          kit.value(data.total, "bytes", { field: field })]) : null
      ]);
    }
  });
})(this);
