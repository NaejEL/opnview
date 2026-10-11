// traffic_over_time_by_scope: one series per selected scope -- outbound, inbound and
// inter-interface, from the traffic direction every flow carries -- in buckets, with the totals
// brought forward (docs/widget-catalogue.md, "Traffic over time by scope"). It is a multi-series
// response, so each series carries its own availability, coverage and gaps.

exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var width = host.buckets.width();
    var traffic = host.series.traffic({ interfaces: host.refs.ids("interfaces") }, width);
    host.envelope.cover(traffic.covered);
    host.envelope.hourFreshness(window.from, window.to);
    var measure = host.params.text("measure");
    var conditions = host.envelope.conditions;
    var availability = host.envelope.availability();
    var report = host.series.window();
    var gaps = host.envelope.gaps();
    var condition = host.envelope.kindCondition("firewall_log");
    var intervals = host.buckets.intervals(window, width);
    var series = [];
    var hasRows = false;
    (host.params.set("scopes") || []).forEach(function (scope) {
      // The members in the order a series has always been written in; label, aggregation and the
      // entry's parameters are a custom chart's and left out here.
      var line = {
        key: scope,
        condition: condition,
        availability: availability,
        window: report,
        gaps: gaps,
        unresolved_references: [],
        measure: host.figures.measure,
        lower_bound: true,
        unit: measure === "" ? undefined : measure,
        total: null,
        total_volume: undefined,
        buckets: null
      };
      var total = host.figures.zero();
      intervals.forEach(function (bucket) {
        var start = bucket.from - host.buckets.floorMod(bucket.from, width);
        var directions = traffic.buckets[start];
        var present = directions !== undefined && directions[scope] !== undefined;
        var sums = present ? directions[scope] : host.figures.zero();
        var gap = host.buckets.isGap(bucket, traffic.covered, gaps);
        var entry = { from: bucket.from, to: bucket.to, gap: gap, value: null };
        if (present || !gap) {
          entry.value = host.figures.value(measure, sums);
          entry.volume = host.figures.volume(sums);
          total = host.figures.add(total, sums);
          if (present) {
            hasRows = true;
          }
        }
        if (line.buckets === null) {
          line.buckets = [];
        }
        line.buckets.push(entry);
      });
      line.total = host.figures.value(measure, total);
      line.total_volume = host.figures.volume(total);
      var connections = total.allowed_connections + total.blocked_connections + total.unknown_connections;
      if (line.condition === conditions.reachable && total.bytes === 0 && connections === 0) {
        line.condition = conditions.reachable_no_rows;
      }
      series.push(line);
    });
    return { data: { bucket_seconds: width, series: series }, has_rows: hasRows };
  }
};
