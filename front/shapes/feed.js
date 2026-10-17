/*
 * opnview front -- the `feed` shape: events, newest first (docs/widget-catalogue.md, Part 1, "Feed").
 * Draws {entries, remainder}: each entry its own row -- its instant, then the fields it carries -- and
 * the entries the limit dropped, counted.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    shape: "feed",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, data = input.data || {};
      var entries = (data.entries || []).map(function (entry, index) {
        var fields = Object.keys(entry).filter(function (name) { return name !== "at"; }).sort();
        return { key: String(index), time: input.format.instant(entry.at), kind: fields[0] ? String(entry[fields[0]]) : "",
          what: h("span", {}, fields.slice(1).map(function (name) {
            return h("span", { key: name, "class": "feed-field" }, [h("span", { "class": "muted" }, name), " ", String(entry[name])]);
          })), by: "" };
      });
      return kit.feed([{ label: t("feed.column.time") }, { label: t("feed.column.kind"), className: "engine" },
        { label: t("feed.column.what") }, { label: "", className: "num" }], entries,
        { remainder: data.remainder ? data.remainder.members : 0 });
    }
  });
})(this);
