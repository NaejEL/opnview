/*
 * The renderer of `unresolved_joins` (docs/widget-catalogue.md, "Unresolved joins";
 * docs/widget-api.md): how much of what is on the canvas could not be joined to a name. One tile per
 * counter: what did not resolve, of how many, each figure in its own measure and never a share
 * invented where the whole is zero. With `show_records`, the records behind a counter are listed under
 * its tile.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  opnview.widgets.register({
    type: "unresolved_joins",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {}, format = input.format;
      var counters = data.counters || [];
      var tiles = counters.map(function (counter) {
        var name = tk("plugin.unresolved_joins.widget.unresolved_joins.counter.", counter.counter);
        var unresolved = kit.amount(counter.unresolved), of = kit.amount(counter.of);
        var share = unresolved !== null && of !== null && of > 0 &&
          (!kit.isFigure(counter.unresolved) || !kit.isFigure(counter.of) || counter.unresolved.measure === counter.of.measure)
          ? format.percent(unresolved / of) : null;
        var records = counter.records || [];
        return h("div", kit.attrs({ key: counter.counter, "class": "tile", "data-counter": counter.counter },
          kit.pick(kit.subject("counter", counter.counter, name, [
            [t("plugin.unresolved_joins.widget.unresolved_joins.fact.unresolved"), kit.value(counter.unresolved)],
            [t("plugin.unresolved_joins.widget.unresolved_joins.fact.of"), kit.value(counter.of)],
            [t("column.share"), share === null ? kit.none() : share]
          ]))), [
          h("span", { "class": "tile-label" }, name),
          h("span", { "class": "tile-value" }, kit.value(counter.unresolved)),
          h("span", { "class": "tile-sub" }, [t("plugin.unresolved_joins.widget.unresolved_joins.of"), " ", kit.value(counter.of),
            share === null ? null : [" · ", share]]),
          records.length ? kit.expand("records-" + counter.counter,
            input.tp("plugin.unresolved_joins.widget.unresolved_joins.records", records.length),
            h("ul", { "class": "counter-list" }, records.map(function (record, index) {
              var fields = Object.keys(record).filter(function (key) { return key !== "observed_at"; }).sort();
              return h("li", { key: String(index) }, [
                h("span", {}, format.instant(record.observed_at)),
                h("span", { "class": "mono" }, kit.join(fields.map(function (key) {
                  return record[key] === null || record[key] === undefined ? "" : String(record[key]);
                }).filter(Boolean), " · "))
              ]);
            }))) : null
        ]);
      });
      return h("div", { "class": "unresolved-joins" }, [
        h("div", { key: "tiles", "class": "tiles" }, tiles),
        h("div", { key: "measures", "class": "legend" }, kit.measures(counters.map(function (c) { return c.of; })))
      ]);
    }
  });
})(this);
