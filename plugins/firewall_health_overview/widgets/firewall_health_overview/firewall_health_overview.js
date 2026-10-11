// firewall_health_overview: one tile per reading -- uptime, processor, memory, swap, disk per
// discovered filesystem, temperature per discovered sensor, latency and loss per gateway -- each
// with its recent history and its last-sampled stamp (docs/widget-catalogue.md, "Firewall health
// overview"). The tiles repeat over the subjects discovered at runtime; a metric no subject reported
// is a tile whose state is absent, never a 0. No threshold is assumed: a tile is coloured only by
// thresholds the reader set.

// isObject reports whether a decoded parameter value is a JSON object.
function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// thresholdState compares a value with the reader's warning and critical thresholds for a metric,
// both optional; undefined, and the member left out, when the reader set none.
function thresholdState(thresholds, metric, value) {
  if (!isObject(thresholds) || !isObject(thresholds[metric])) {
    return undefined;
  }
  var limits = thresholds[metric];
  var state = "ok";
  if (typeof limits.warning === "number" && value >= limits.warning) {
    state = "warning";
  }
  if (typeof limits.critical === "number" && value >= limits.critical) {
    state = "critical";
  }
  return state;
}

exports.widget = {
  serve: function (host) {
    host.envelope.coverFrom(host.envelope.earliest("widget_earliest_sample"));
    var thresholds = host.params.effective().thresholds;
    var showLastSampled = host.params.flag("show_last_sampled");
    var from = host.request.window.from;
    var tiles = [];
    var hasRows = false;
    (host.params.set("metrics") || []).forEach(function (metric) {
      var subjects = host.samples.subjects(metric);
      if (subjects.length === 0) {
        tiles.push({
          metric: metric,
          subject_kind: host.samples.metrics[metric].kind,
          subject_key: "",
          measure: "",
          unit: "",
          state: "absent",
          value: null,
          series: []
        });
        return;
      }
      subjects.forEach(function (item) {
        hasRows = true;
        // One reading of one subject, its members in the order a tile has always been written in:
        // the state is measured, or absent where no sample of the window answers.
        var tile = {
          metric: metric,
          subject_kind: item.kind,
          subject_key: item.key,
          measure: item.measure,
          unit: item.unit,
          state: "absent",
          value: null,
          last_sampled_at: undefined,
          series: host.samples.points(item),
          threshold_state: undefined
        };
        var latest = host.query.row("widget_measurement_latest", {
          subject_kind: item.kind,
          subject_key: item.key,
          measure: item.measure,
          to: host.request.now
        });
        if (latest !== null && latest.sampled_at >= from) {
          tile.state = "measured";
          tile.value = latest.value;
          if (showLastSampled) {
            tile.last_sampled_at = latest.sampled_at;
          }
          tile.threshold_state = thresholdState(thresholds, metric, latest.value);
        }
        tiles.push(tile);
      });
    });
    return { data: { tiles: tiles }, has_rows: hasRows };
  }
};
