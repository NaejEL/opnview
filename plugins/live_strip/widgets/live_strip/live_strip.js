// live_strip: the rolling strip (coverage row 8 of docs/widget-api.md, "Live figures") -- a trailing
// series at the live cadence of the sampled throughput by traffic direction, and of the logged
// decisions per engine, counted from the records' own timestamps.
//
// TWO MEASURES, NEVER SUMMED. The throughput is sampled (`sampled`, traffic/top's two-second
// captures); the decisions are logged (`logged` for the filter log, `dns_lookups` for the
// resolvers). A throughput bucket with no sample is a gap, never a rate of 0; a decision bucket past
// its kind's last healthy pass, or meeting a gap of its kind, is a gap, never a zero.
//
// It reads stored samples and records only, renews the live-interest lease, and never calls the
// firewall.

// The window a strip covers, in seconds: the mockup's two minutes by default, an hour at most -- fine
// samples are kept an hour by default -- and never more than MAX_BUCKETS buckets.
var DEFAULT_WINDOW_SECONDS = 120;
var MAX_WINDOW_SECONDS = 3600;
var MAX_BUCKETS = 1000;

// The throughput series, by traffic direction, then the local addresses whose sending no sample
// splits between outbound and inter_interface.
var THROUGHPUT_KEYS = ["outbound", "inbound", "inter_interface", "outbound_unsplit"];

// The decision series: the filter log's allowed connections, its refusals by engine kind
// (docs/data-model.md, the blocked_decision entity), and the resolvers' refusals of every list.
var FIREWALL_REFUSALS = ["firewall_rule", "firewall_no_rule", "firewall_reason_not_recorded"];
var DECISIONS = [
  { key: "allowed", measure: "logged", kind: "firewall_log", earliest: "earliest_flow" },
  { key: "firewall_rule", measure: "logged", kind: "firewall_log", earliest: "earliest_flow" },
  { key: "firewall_no_rule", measure: "logged", kind: "firewall_log", earliest: "earliest_flow" },
  { key: "firewall_reason_not_recorded", measure: "logged", kind: "firewall_log", earliest: "earliest_flow" },
  { key: "dns_refusal", measure: "dns_lookups", kind: "dns_lookup", earliest: "earliest_lookup" }
];

// The gap opnview records for its own absence, which every series meets.
var GAP_KIND_OPNVIEW = "opnview";

// The outcomes of a healthy pass.
var HEALTHY = { healthy: true, healthy_empty: true };

// windowBounds are the window a request at a cadence may ask for: two cadences at least, an hour and
// MAX_BUCKETS buckets at most.
function windowBounds(cadence) {
  var minimum = 2 * cadence;
  var maximum = Math.min(MAX_WINDOW_SECONDS, cadence * MAX_BUCKETS);
  if (maximum < minimum) {
    maximum = minimum;
  }
  return { minimum: minimum, maximum: maximum };
}

// windowOf is the window in force: the one asked for, or two minutes; never outside the bounds the
// cadence allows. A window asked for outside them is drawn at the nearest bound, and the answer says
// so with the state window_out_of_range.
function windowOf(host, cadence) {
  var asked = host.params.effective().window_seconds;
  var bounds = windowBounds(cadence);
  var seconds = asked === null || asked === undefined ? DEFAULT_WINDOW_SECONDS : asked;
  var clamped = Math.min(Math.max(seconds, bounds.minimum), bounds.maximum);
  if (asked !== null && asked !== undefined && clamped !== asked) {
    host.envelope.state("window_out_of_range");
  }
  return clamped;
}

// throughput averages each direction's rate over the samples of each bucket, fine and baseline.
function throughput(host, window, buckets, width) {
  var series = host.rates.series(window);
  var byBucket = {};
  series.instants.forEach(function (instant) {
    var start = instant.at - host.buckets.floorMod(instant.at, width);
    (byBucket[start] = byBucket[start] || []).push(instant);
  });
  return THROUGHPUT_KEYS.map(function (key) {
    var entry = { key: key, measure: "sampled", unit: "bit_per_second", span_seconds: series.span_seconds,
      cadence_seconds: width, lower_bound: true, buckets: [] };
    buckets.forEach(function (bucket) {
      var inside = byBucket[bucket.from] || [];
      var cell = { from: bucket.from, to: bucket.to, gap: false, value: null, samples: inside.length, sampled_at: null };
      if (inside.length === 0) {
        cell.gap = true;
        entry.buckets.push(cell);
        return;
      }
      var sum = 0;
      var latest = 0;
      inside.forEach(function (instant) {
        // A sample taken with no reading in this direction measured none: the instant was sampled, so
        // its rate there is a measured 0, not a gap.
        var rate = key === "outbound_unsplit" ? instant.outbound_unsplit : instant.by_direction[key];
        if (rate) {
          sum += rate.in_bits_per_second + rate.out_bits_per_second;
        } else {
          sum += 0;
        }
        if (instant.at > latest) {
          latest = instant.at;
        }
      });
      cell.value = sum / inside.length;
      cell.sampled_at = latest;
      entry.buckets.push(cell);
    });
    return entry;
  });
}

// completeUntil is the instant a kind's records are complete up to: the oldest last healthy pass of its
// active providers, never later than now; null when an active provider has none.
function completeUntil(providers, kind, now) {
  var until = null;
  for (var i = 0; i < providers.length; i++) {
    var provider = providers[i];
    if (provider.kind !== kind || !provider.active) {
      continue;
    }
    if (provider.outcome === null || provider.checked_at === null || !HEALTHY[provider.outcome]) {
      return null;
    }
    if (until === null || provider.checked_at < until) {
      until = provider.checked_at;
    }
  }
  if (until !== null && until > now) {
    until = now;
  }
  return until;
}

// decisions counts the decisions of each series per bucket.
function decisions(host, window, buckets, width, now, gaps) {
  var counts = {};
  var add = function (series, start, count) {
    var held = counts[series] = counts[series] || {};
    held[start] = (held[start] || 0) + count;
  };
  var bounds = { width: width, from: window.from, to: window.to };
  host.query.rows("widget_live_strip_allowed", bounds).forEach(function (row) {
    add("allowed", row.bucket_start, row.decisions);
  });
  host.query.rows("widget_live_strip_refusals", bounds).forEach(function (row) {
    if (row.engine_kind.indexOf("dns_") === 0) {
      add("dns_refusal", row.bucket_start, row.decisions);
    } else if (FIREWALL_REFUSALS.indexOf(row.engine_kind) >= 0) {
      add(row.engine_kind, row.bucket_start, row.decisions);
    }
  });
  var earliest = host.query.row("widget_live_strip_earliest", {});
  var providers = host.envelope.availability();
  return DECISIONS.map(function (definition) {
    var series = { key: definition.key, measure: definition.measure, provider_kind: definition.kind,
      complete_until: null, lower_bound: true, buckets: [] };
    var until = completeUntil(providers, definition.kind, now);
    var covered = null;
    if (until !== null) {
      series.complete_until = until;
      var first = earliest[definition.earliest];
      if (first !== null && first < until) {
        covered = { from: first, to: until };
      }
    }
    var kindGaps = gaps.filter(function (gap) {
      return gap.provider_kind === definition.kind || gap.provider_kind === GAP_KIND_OPNVIEW;
    });
    var held = counts[definition.key] || {};
    buckets.forEach(function (bucket) {
      var counted = Object.prototype.hasOwnProperty.call(held, bucket.from);
      var cell = { from: bucket.from, to: bucket.to, gap: false, value: null };
      // Records in a bucket the collection did not cover whole are shown, and the bucket still says it
      // is incomplete; with none, the bucket is a gap with no value, never a zero.
      if (host.buckets.isGap(bucket, covered, kindGaps)) {
        cell.gap = true;
        if (counted) {
          cell.value = held[bucket.from];
        }
      } else {
        cell.value = counted ? held[bucket.from] : 0;
      }
      series.buckets.push(cell);
    });
    return series;
  });
}

// stored says whether anything stored falls in the strip: a sampled bucket or a counted decision.
function stored(data) {
  var sampled = data.throughput.some(function (series) {
    return series.buckets.some(function (bucket) { return bucket.samples > 0; });
  });
  return sampled || data.decisions.some(function (series) {
    return series.buckets.some(function (bucket) { return bucket.value !== null && bucket.value > 0; });
  });
}

exports.widget = {
  serve: function (host) {
    var now = host.request.now;
    var width = host.live.cadence();
    var seconds = windowOf(host, width);
    var liveInterestUntil = host.live.renew();
    var to = now - host.buckets.floorMod(now, width) + width;
    var from = now - seconds;
    from -= host.buckets.floorMod(from, width);
    var window = { from: from, to: to };
    var buckets = host.buckets.intervals(window, width);
    var gaps = host.envelope.readGaps(["firewall_log", "dns_lookup"], window);
    var data = {
      cadence_seconds: width,
      window: window,
      live_interest_until: liveInterestUntil,
      throughput: throughput(host, window, buckets, width),
      decisions: decisions(host, window, buckets, width, now, gaps),
      gaps: gaps
    };
    // The strip always draws its buckets, a bucket with nothing in it being a gap or a measured zero;
    // it holds rows when a sample or a decision falls in its window, and a degraded source with none
    // answers no data at all, never a strip of zeros.
    return { data: data, has_rows: stored(data) };
  }
};
