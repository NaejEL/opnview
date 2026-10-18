/*
 * opnview front -- the `cards` shape: one card per member (docs/widget-catalogue.md, Part 1, "Cards").
 * Draws {cards: [{key, label, fields}], remainder}: each member its name and its fields as chips, the
 * members the limit dropped counted.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "cards",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      function shown(value) {
        if (value === null || value === undefined) { return t("figure.dash"); }
        return kit.isFigure(value) ? kit.text(kit.amount(value)) : String(value);
      }
      return h("div", { "class": "owners" }, (data.cards || []).map(function (card) {
        var fields = card.fields || {};
        var names = Object.keys(fields).sort();
        var label = card.label || String(card.key);
        return h("div", kit.attrs({ key: String(card.key), "class": "owner" },
          kit.pick(kit.subject("member", card.key, label, names.map(function (name) { return [name, shown(fields[name])]; })))), [
          h("div", { "class": "owner-head" }, h("span", { "class": "owner-name" }, label)),
          kit.chips(names.map(function (name) { return { key: name, label: name, value: shown(fields[name]) }; }))
        ]);
      }).concat([kit.remainder(data.remainder ? data.remainder.members : 0)]));
    }
  });
})(this);
