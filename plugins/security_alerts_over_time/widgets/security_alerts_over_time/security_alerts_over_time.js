// security_alerts_over_time: the security events of the window in buckets, one series per severity
// present -- unknown among them -- each naming the providers that contributed it
// (docs/widget-catalogue.md, "Security alerts over time"). Several providers of the kind may be
// active at once, so the provider is part of every series.
//
// THE SEVERITY IS NEVER GUESSED. An event's severity is its own when the provider ships one, else the
// rule-info cache's answer; with neither it is counted in the unknown series, which says how many of
// its events had each reason: the rule never fetched, unknown to the provider, or known with no
// severity. A detection whose severity could not be looked up is still a detection.
//
// THE BUCKET (decision D6 of specs/SPEC-step-6-alerts.md): auto is the width decision 8 gives the
// period; 1m, 15m, 1h and 24h ask for one. A width that would cut the window into more than
// maxBuckets buckets is served at the finest width that does not, and the answer states the width
// it used beside the one asked for.

// severityOrder is opnview's severity vocabulary, most severe first (docs/data-model.md, "Severity:
// one ordered vocabulary"), and unknown after it: the order the series are served in.
var severityOrder = ["critical", "high", "medium", "low", "informational", "unknown"];

// widths are the bucket widths a reader may ask for, finest first, in seconds.
var widths = { "1m": 60, "15m": 900, "1h": 3600, "24h": 86400 };
var widthOrder = ["1m", "15m", "1h", "24h"];

// maxBuckets is the most buckets a series is cut into (decision D6).
var maxBuckets = 1000;

// bucketCount is how many buckets of a width a window is cut into: the UTC-aligned buckets that
// meet it.
function bucketCount(host, window, width) {
  var start = window.from - host.buckets.floorMod(window.from, width);
  return Math.ceil((window.to - start) / width);
}

// chooseWidth is the width the series are cut at, and whether it is coarser than the one asked.
function chooseWidth(host, window, asked) {
  var width = asked === "auto" || widths[asked] === undefined ? host.buckets.width() : widths[asked];
  if (bucketCount(host, window, width) <= maxBuckets) {
    return { width: width, coarsened: false };
  }
  for (var i = 0; i < widthOrder.length; i++) {
    var candidate = widths[widthOrder[i]];
    if (candidate > width && bucketCount(host, window, candidate) <= maxBuckets) {
      return { width: candidate, coarsened: true };
    }
  }
  return { width: widths[widthOrder[widthOrder.length - 1]], coarsened: true };
}

// severityOf is the series an event group counts in, and the reason when it is unknown.
function severityOf(row) {
  if (row.event_severity !== null) {
    return { severity: row.event_severity, reason: null };
  }
  if (row.lookup_state === null) {
    return { severity: "unknown", reason: "not_fetched" };
  }
  if (row.lookup_state !== "found") {
    return { severity: "unknown", reason: "rule_not_found" };
  }
  if (row.rule_severity === null) {
    return { severity: "unknown", reason: "not_reported_by_provider" };
  }
  return { severity: row.rule_severity, reason: null };
}

// eventCount is a count of security events, a lower bound like every count of logged material.
function eventCount(value) {
  return { measure: "security_events", lower_bound: true, value: value };
}

exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var earliest = host.envelope.earliest("widget_earliest_security_event");
    host.envelope.coverFrom(earliest);
    var covered = null;
    if (earliest !== null) {
      covered = { from: Math.max(window.from, earliest), to: Math.min(window.to, host.request.now) };
      if (covered.to <= covered.from) {
        covered = null;
      }
    }
    var asked = host.params.text("bucket");
    var chosen = chooseWidth(host, window, asked);
    var width = chosen.width;
    var wanted = {};
    (host.params.set("severities") || []).forEach(function (severity) { wanted[severity] = true; });

    // Per severity: its events per bucket start, per provider, and per reason when unknown.
    var bySeverity = {};
    var total = 0;
    host.query.rows("widget_security_alerts_buckets", {
      from: window.from,
      to: window.to,
      width: width,
      provider_ids: host.refs.ids("providers"),
      client_ids: host.refs.ids("clients"),
      interface_ids: host.refs.ids("interfaces")
    }).forEach(function (row) {
      var resolved = severityOf(row);
      if (!wanted[resolved.severity]) {
        return;
      }
      var series = bySeverity[resolved.severity];
      if (series === undefined) {
        series = { buckets: {}, providers: {}, reasons: {}, total: 0 };
        bySeverity[resolved.severity] = series;
      }
      series.buckets[row.bucket_start] = (series.buckets[row.bucket_start] || 0) + row.events;
      var provider = series.providers[row.provider_id];
      if (provider === undefined) {
        provider = { kind: row.provider_kind, provider_key: row.provider_key, display_name: row.display_name, events: 0 };
        series.providers[row.provider_id] = provider;
      }
      provider.events += row.events;
      if (resolved.reason !== null) {
        series.reasons[resolved.reason] = (series.reasons[resolved.reason] || 0) + row.events;
      }
      series.total += row.events;
      total += row.events;
    });

    var gaps = host.envelope.gaps();
    var intervals = host.buckets.intervals(window, width);
    var out = [];
    severityOrder.forEach(function (severity) {
      var series = bySeverity[severity];
      if (series === undefined) {
        return;
      }
      var buckets = intervals.map(function (interval) {
        var start = interval.from - host.buckets.floorMod(interval.from, width);
        var present = Object.prototype.hasOwnProperty.call(series.buckets, String(start));
        var gap = host.buckets.isGap(interval, covered, gaps);
        var value = null;
        if (present) {
          value = eventCount(series.buckets[start]);
        } else if (!gap) {
          value = eventCount(0);
        }
        return { from: interval.from, to: interval.to, gap: gap, value: value };
      });
      var providers = Object.keys(series.providers).map(function (id) { return series.providers[id]; });
      providers.sort(function (a, b) {
        var kinds = host.figures.compare(a.kind, b.kind);
        return kinds !== 0 ? kinds : host.figures.compare(a.provider_key, b.provider_key);
      });
      var entry = {
        key: severity,
        severity: severity,
        providers: providers.map(function (provider) {
          return { kind: provider.kind, provider_key: provider.provider_key, display_name: provider.display_name,
            events: eventCount(provider.events) };
        }),
        total: eventCount(series.total),
        buckets: buckets
      };
      if (severity === "unknown") {
        var reasons = {};
        Object.keys(series.reasons).sort(host.figures.compare).forEach(function (reason) {
          reasons[reason] = eventCount(series.reasons[reason]);
        });
        entry.reasons = reasons;
      }
      out.push(entry);
    });

    return {
      data: {
        bucket_seconds: width,
        bucket_requested: asked,
        bucket_coarsened: chosen.coarsened,
        series: out,
        total: eventCount(total)
      },
      has_rows: total > 0
    };
  }
};
