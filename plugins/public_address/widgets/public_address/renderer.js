/*
 * The renderer of `public_address` (docs/widget-catalogue.md, "Public address"; docs/widget-api.md):
 * what address this installation presents, on which upstream interface and gateway, and since when.
 * One block per upstream interface: its current addresses, each with its family, whether it is
 * public or which special-purpose block holds it, and when it was first and last seen; its gateways;
 * its earlier addresses when the history is asked for, the ones past the limit counted.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "public_address",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, format = input.format;
      var data = input.data || {};

      function familyOf(family) {
        if (family === "ipv4") { return t("plugin.public_address.widget.public_address.family.ipv4"); }
        if (family === "ipv6") { return t("plugin.public_address.widget.public_address.family.ipv6"); }
        return String(family);
      }
      function reach(address) {
        if (address.public) { return kit.badge(t("plugin.public_address.widget.public_address.public"), "ok"); }
        if (address.special_purpose) {
          return kit.badge(address.special_purpose.name, "gap", t("plugin.public_address.widget.public_address.special_purpose_source", { block: address.special_purpose.block,
            reference: address.special_purpose.reference }));
        }
        return kit.badge(t("plugin.public_address.widget.public_address.not_public"), "gap");
      }
      function changed(address) {
        if (address.changed_at) { return format.instant(address.changed_at); }
        if (address.change_state === "not_recorded_only_one_address_seen") {
          return kit.none(t("plugin.public_address.widget.public_address.only_one_address_seen"));
        }
        return kit.none();
      }
      function addressSubject(address, iface) {
        var facts = [
          [t("plugin.public_address.widget.public_address.column.family"), familyOf(address.family)],
          [t("plugin.public_address.widget.public_address.column.reach"), reach(address)],
          [t("column.first_seen"), address.first_seen_at ? format.instant(address.first_seen_at) : kit.none()],
          [t("column.last_seen"), address.last_seen_at ? format.instant(address.last_seen_at) : kit.none()]
        ];
        if (address.prefix_length !== null && address.prefix_length !== undefined) {
          facts.push([t("plugin.public_address.widget.public_address.column.prefix"), kit.text(address.prefix_length)]);
        }
        return kit.subject("address", address.address, address.address, facts,
          { related: iface ? [kit.interfaceSubject(iface)] : [] });
      }
      var columns = [
        { key: "address", label: t("plugin.public_address.widget.public_address.column.address") },
        { key: "family", label: t("plugin.public_address.widget.public_address.column.family") },
        { key: "reach", label: t("plugin.public_address.widget.public_address.column.reach") },
        { key: "since", label: t("plugin.public_address.widget.public_address.column.changed") },
        { key: "last", label: t("column.last_seen") }
      ];
      function rowsOf(addresses, iface, prefix, withChange) {
        return (addresses || []).map(function (address, index) {
          return { key: prefix + index, subject: addressSubject(address, iface), cells: [
            h("span", { "class": "mono" }, address.prefix_length !== null && address.prefix_length !== undefined
              ? [address.address, "/", String(address.prefix_length)] : address.address),
            familyOf(address.family),
            reach(address),
            withChange ? changed(address) : (address.first_seen_at ? format.instant(address.first_seen_at) : kit.none()),
            address.last_seen_at ? format.instant(address.last_seen_at) : kit.none()
          ] };
        });
      }
      /* THE MOCKUP'S TILES: one per question -- the IPv4 address, the IPv6 address, the interface that
       * carries them, the gateway, the last change -- each a label, a figure and a line under it; the
       * earlier addresses as a table under the tiles when the history is asked for. */
      function tile(key, label, value, sub, subject, mono) {
        var attrs = { key: key, "class": "tile", "data-tile": key };
        return h("div", subject ? kit.attrs(attrs, kit.pick(subject)) : attrs, [
          h("span", { "class": "tile-label" }, label),
          h("span", { "class": "tile-value" + (value === null ? " tile-value-empty" : "") + (mono ? " mono" : "") },
            value === null ? kit.none() : value),
          h("span", { "class": "tile-sub" }, sub)
        ]);
      }
      function reachWords(address) {
        if (address.public) { return t("plugin.public_address.widget.public_address.public"); }
        if (address.special_purpose) { return address.special_purpose.name; }
        return t("plugin.public_address.widget.public_address.not_public");
      }
      function written(address) {
        return address.prefix_length !== null && address.prefix_length !== undefined
          ? [address.address, "/", String(address.prefix_length)] : address.address;
      }
      var several = (data.upstreams || []).length > 1;
      var blocks = (data.upstreams || []).map(function (upstream, index) {
        var iface = upstream.interface;
        var label = iface ? kit.interfaceLabel(iface) : String(index);
        var history = upstream.history || [];
        var historyRemainder = upstream.history_remainder ? upstream.history_remainder.members : 0;
        var current = [], seen = {};
        (upstream.current || []).forEach(function (address) {
          if (!seen[address.address]) { seen[address.address] = true; current.push(address); }
        });
        function family(name) {
          var found = current.filter(function (address) { return address.family === name; })[0];
          return tile(name, familyOf(name), found ? written(found) : null,
            found ? reachWords(found) : t("plugin.public_address.widget.public_address.no_address"), found ? addressSubject(found, iface) : null, true);
        }
        var gateway = (upstream.gateways || [])[0] || null;
        var changedAt = current.reduce(function (latest, address) {
          return address.changed_at && address.changed_at > latest ? address.changed_at : latest;
        }, 0);
        var tiles = [
          family("ipv4"),
          family("ipv6"),
          tile("carried-by", t("plugin.public_address.widget.public_address.carried_by"), label,
            upstream.behind_upstream_nat ? t("plugin.public_address.widget.public_address.behind_nat") : (iface && iface.device ? iface.device : ""),
            iface ? kit.interfaceSubject(iface) : null, false),
          tile("gateway", t("plugin.public_address.widget.public_address.gateway"), gateway ? gateway.address : null,
            gateway ? familyOf(gateway.family) : t("plugin.public_address.widget.public_address.no_address"), gateway ? addressSubject(gateway, iface) : null, true),
          tile("changed", t("plugin.public_address.widget.public_address.column.changed"), changedAt ? format.instant(changedAt) : null,
            history.length || historyRemainder ? input.tp("plugin.public_address.widget.public_address.earlier_count", history.length + historyRemainder)
              : t("plugin.public_address.widget.public_address.only_one_address_seen"), null, false)
        ];
        return h("div", { key: "upstream-" + (iface ? iface.id : index), "class": "public-upstream",
          "data-upstream": iface ? String(iface.id) : String(index) }, [
          several ? h("div", { key: "head", "class": "owner-head" }, h("span", { "class": "owner-name" }, label)) : null,
          h("div", { key: "tiles", "class": "tiles" }, tiles),
          history.length || historyRemainder ? kit.table(columns, rowsOf(history, iface, "history-", false),
            { caption: t("plugin.public_address.widget.public_address.history"),
              remainder: historyRemainder ? { members: historyRemainder, cells: ["", "", "", ""] } : null }) : null
        ]);
      });
      if (blocks.length === 0) {
        blocks.push(h("p", { key: "none", "class": "table-foot" }, t("plugin.public_address.widget.public_address.no_upstream")));
      }
      return h("div", { "class": "public-address" }, blocks);
    }
  });
})(this);
