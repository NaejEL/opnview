/*
 * A front-only widget: resolver lookups per hour, read from the data catalogue's `dns` dataset.
 * Nothing on the server answers this type; the core performs the one read `requests` names and
 * hands its answer to `render` as `input.data`, a list of {path, status, body}.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var PREFIX = "widget.example_lookups_by_hour.";

  opnview.widgets.register({
    type: "example_lookups_by_hour",
    requests: function (input) {
      var query = {
        period: input.parameters.period || "24h",
        x: { column: "looked_up_at", bucket: 3600 },
        y: [{ column: "lookups", aggregation: "sum" }]
      };
      return ["/api/datasets/dns/query?parameters=" + encodeURIComponent(JSON.stringify(query))];
    },
    render: function (input) {
      var kit = input.kit, t = input.t, format = input.format;
      var read = (input.data || [])[0] || {};
      var series = read.body && read.body.data && read.body.data.series ? read.body.data.series : [];
      var buckets = series.length ? series[0].buckets : [];
      var rows = buckets.map(function (bucket) {
        return {
          key: String(bucket.from),
          cells: [
            format.instant(bucket.from),
            bucket.value === null || bucket.value === undefined ? kit.none() : kit.value(bucket.value, "count")
          ]
        };
      });
      return kit.table([
        { key: "hour", label: t(PREFIX + "column.hour") },
        { key: "lookups", label: t(PREFIX + "column.lookups"), num: true }
      ], rows, { empty: t("table.no_rows") });
    }
  });
})(this);
