// custom_chart: the time_series shape with no binding -- the reader names the series
// (docs/widget-catalogue.md, "Custom chart"). One read per series, every series on the same
// UTC-aligned buckets so a telemetry series and a traffic series share a time axis. Each series
// carries its own availability, coverage, gaps and unresolved references, so one missing source or
// one unresolved reference degrades its own line and no other.
//
// A series' source is one of the five of docs/widget-api.md, "Series of custom_chart", or any
// dataset of the data catalogue with a time column (P8), which the core reads onto the same buckets
// (host.series.fillDataset).

// SERIES_PROVIDERS is the provider kinds a series of each of the five sources reads.
var SERIES_PROVIDERS = {
  traffic: ["firewall_log"],
  firewall_metric: ["measurement_sample"],
  interface_throughput: ["measurement_sample"],
  dns: ["dns_lookup"],
  security_event: ["security_event"]
};

// text reads a string member of a series entry, "" when it holds none.
function text(fields, key) {
  return typeof fields[key] === "string" ? fields[key] : "";
}

// clipped is the part of the window from the earliest record a source holds to now, or null when it
// holds none or that part is empty.
function clipped(host, earliest) {
  if (earliest === null) {
    return null;
  }
  var window = host.request.window;
  var covered = { from: Math.max(window.from, earliest), to: Math.min(window.to, host.request.now) };
  return covered.to <= covered.from ? null : covered;
}

// setCovered records a series' covered interval and its coverage on its window report.
function setCovered(host, report, covered) {
  var coverage = host.envelope.coverage;
  report.covered = covered;
  if (covered === null) {
    report.coverage = coverage.not_covered;
  } else if (covered.from <= report.requested.from && covered.to >= report.requested.to) {
    report.coverage = coverage.covered;
  } else {
    report.coverage = coverage.partially_covered;
  }
}

// bucketStart is the start of the bucket an interval of the window belongs to.
function bucketStart(host, interval, width) {
  return interval.from - host.buckets.floorMod(interval.from, width);
}

// measuredFigure reads a traffic measure naming a declared measure's figure -- <measure>_bytes or
// <measure>_packets, which the core offers for every measure a plugin declares -- as that measure and
// the figure it draws; null for the logged figures.
function measuredFigure(host, name) {
  var cut = name.lastIndexOf("_");
  if (cut <= 0) {
    return null;
  }
  var measure = name.slice(0, cut);
  var figure = name.slice(cut + 1);
  if ((figure !== "bytes" && figure !== "packets") || host.figures.declared.indexOf(measure) < 0) {
    return null;
  }
  return { measure: measure, figure: figure };
}

// fillTraffic reads a traffic series: the figures of the window per bucket, every traffic direction
// summed, in the measure the answer is given in -- or a declared measure for its <measure>_bytes and
// <measure>_packets, which carry no figure when that measure cannot answer the window.
function fillTraffic(host, line, entry, index, width) {
  var measured = measuredFigure(host, entry.measure);
  if (measured !== null && host.series.measureState(measured.measure) !== null) {
    line.measure = measured.measure;
    line.unit = entry.measure;
    line.lower_bound = true;
    host.buckets.intervals(host.request.window, width).forEach(function (interval) {
      line.buckets.push({ from: interval.from, to: interval.to, gap: true, value: null });
    });
    return false;
  }
  var reference = function (key) {
    return host.refs.entry(index, key);
  };
  var idOf = function (key) {
    var resolved = reference(key);
    return resolved === null ? null : resolved.id;
  };
  var textOf = function (key) {
    var resolved = reference(key);
    return resolved === null ? null : resolved.text;
  };
  var scopedInterface = idOf("interface");
  var traffic = host.series.traffic({
    interfaces: scopedInterface === null ? null : [scopedInterface],
    client_id: idOf("client"),
    rule_id: idOf("rule"),
    asn: idOf("operator"),
    country: textOf("country"),
    site: textOf("site"),
    measure: measured === null ? null : measured.measure
  }, width);
  setCovered(host, line.window, traffic.covered);
  line.measure = measured !== null ? measured.measure : host.figures.measure;
  line.unit = entry.measure;
  line.lower_bound = true;
  var figure = measured !== null ? measured.figure :
    { bytes: "bytes", connections: "connections", allowed: "allowed", blocked: "blocked" }[entry.measure];
  if (figure === undefined) {
    figure = "";
  }
  var total = host.figures.zero();
  var rows = false;
  host.buckets.intervals(host.request.window, width).forEach(function (interval) {
    var directions = traffic.buckets[bucketStart(host, interval, width)];
    var sum = host.figures.zero();
    var present = false;
    if (directions !== undefined) {
      Object.keys(directions).forEach(function (direction) {
        sum = host.figures.add(sum, directions[direction]);
        present = true;
      });
    }
    var bucket = { from: interval.from, to: interval.to, gap: host.buckets.isGap(interval, traffic.covered, line.gaps),
      value: null };
    if (present || !bucket.gap) {
      bucket.value = host.figures.value(figure, sum);
      bucket.volume = host.figures.volume(sum);
      total = host.figures.add(total, sum);
      rows = rows || present;
    }
    line.buckets.push(bucket);
  });
  line.total = host.figures.value(figure, total);
  return rows;
}

// fillMetric reads a firewall_metric series: the samples of the metric's first measure, of every
// subject that reported it, aggregated per bucket.
function fillMetric(host, line, entry, width) {
  setCovered(host, line.window, clipped(host, host.envelope.earliest("widget_earliest_sample")));
  var measure = host.samples.metrics[entry.metric].measures[0];
  var points = [];
  host.samples.subjects(entry.metric).forEach(function (item) {
    if (item.measure !== measure) {
      return;
    }
    line.unit = item.unit;
    points = points.concat(host.samples.points(item));
  });
  line.measure = "sampled_gauge";
  line.buckets = host.samples.aggregate(host.request.window, width, points, entry.aggregation);
  return points.length > 0;
}

// fillThroughput reads an interface_throughput series: the counters of every interface, or the one
// the entry names, in and out, aggregated per bucket.
function fillThroughput(host, line, entry, index, width) {
  setCovered(host, line.window, clipped(host, host.envelope.earliest("widget_earliest_sample")));
  var scoped = host.refs.entry(index, "interface");
  var points = [];
  host.installation.interfaces().forEach(function (info) {
    if (scoped !== null && scoped.id !== info.id) {
      return;
    }
    ["in", "out"].forEach(function (direction) {
      points = points.concat(host.samples.throughput(info.device, entry.measure, direction).points);
    });
  });
  line.measure = host.figures.measures.interface_counter;
  line.unit = entry.measure;
  line.lower_bound = true;
  line.buckets = host.samples.aggregate(host.request.window, width, points, entry.aggregation);
  return points.length > 0;
}

// fillCounts reads a dns or security_event series: the records of the window per bucket, all of
// them or the blocked ones.
function fillCounts(host, line, entry, index, width) {
  var dns = entry.source === "dns";
  var covered = clipped(host, host.envelope.earliest(dns ? "widget_earliest_lookup" : "widget_earliest_security_event"));
  setCovered(host, line.window, covered);
  var window = host.request.window;
  var client = host.refs.entry(index, "client");
  var scopedInterface = host.refs.entry(index, "interface");
  var provider = host.refs.entry(index, "provider");
  var parameters = {
    from: window.from,
    to: window.to,
    width: width,
    client_id: client === null ? null : client.id,
    interface_ids: scopedInterface === null ? null : host.refs.jsonIds([scopedInterface.id])
  };
  var rows = [];
  if (dns) {
    // A DNS series scoped to a resolver reads that resolver's lookups.
    parameters.resolver = provider === null ? null : provider.text;
    rows = host.query.rows("widget_dns_buckets", parameters).map(function (row) {
      return { start: row.bucket_start, all: row.lookups, blocked: row.blocked };
    });
  } else {
    parameters.provider_id = provider === null ? null : provider.id;
    rows = host.query.rows("widget_security_buckets", parameters).map(function (row) {
      return { start: row.bucket_start, all: row.events, blocked: row.blocked };
    });
  }
  var counts = {};
  rows.forEach(function (row) {
    counts[row.start] = row;
  });
  line.measure = entry.source;
  line.unit = entry.measure;
  line.lower_bound = true;
  var found = false;
  var total = 0;
  host.buckets.intervals(window, width).forEach(function (interval) {
    var count = counts[bucketStart(host, interval, width)];
    var present = count !== undefined;
    var bucket = { from: interval.from, to: interval.to, gap: host.buckets.isGap(interval, covered, line.gaps),
      value: null };
    if (present || !bucket.gap) {
      var value = 0;
      if (present) {
        value = entry.measure === "blocked" ? count.blocked : count.all;
      }
      bucket.value = value;
      total += value;
      found = found || present;
    }
    line.buckets.push(bucket);
  });
  line.total = total;
  return found;
}

// fillDataset reads a series from a dataset of the data catalogue, through the core's catalogue
// read, onto the same buckets.
function fillDataset(host, line, index, width) {
  var read = host.series.fillDataset(index, line.window, line.gaps, width);
  if (read === null) {
    return false;
  }
  line.window = read.window;
  line.measure = read.measure;
  line.unit = read.unit;
  line.buckets = read.buckets;
  line.total = read.total;
  return read.rows;
}

// fill reads one series into its buckets and records its coverage.
function fill(host, line, entry, index, width) {
  switch (entry.source) {
    case "traffic":
      return fillTraffic(host, line, entry, index, width);
    case "firewall_metric":
      return fillMetric(host, line, entry, width);
    case "interface_throughput":
      return fillThroughput(host, line, entry, index, width);
    case "dns":
    case "security_event":
      return fillCounts(host, line, entry, index, width);
  }
  return fillDataset(host, line, index, width);
}

// annotation reads one overlay: the buckets holding a security event, or a blocked connection, with
// their counts.
function annotation(host, kind, width) {
  var window = host.request.window;
  var overlay = { kind: kind, buckets: [] };
  if (kind === "security_events") {
    host.query.rows("widget_security_buckets", { from: window.from, to: window.to, width: width,
      provider_id: null, client_id: null, interface_ids: null }).forEach(function (row) {
      overlay.buckets.push({ from: row.bucket_start, to: row.bucket_start + width, gap: false, value: row.events });
    });
    return overlay;
  }
  var traffic = host.series.traffic({}, width);
  Object.keys(traffic.buckets).map(Number).sort(function (a, b) {
    return a - b;
  }).forEach(function (start) {
    var directions = traffic.buckets[start];
    var sum = host.figures.zero();
    Object.keys(directions).forEach(function (direction) {
      sum = host.figures.add(sum, directions[direction]);
    });
    if (sum.blocked_connections === 0) {
      return;
    }
    overlay.buckets.push({ from: start, to: start + width, gap: false, value: sum.blocked_connections });
  });
  return overlay;
}

// omitEmpty is a string member the series writes only when it holds something.
function omitEmpty(value) {
  return value === "" ? undefined : value;
}

exports.widget = {
  serve: function (host) {
    var conditions = host.envelope.conditions;
    var providers = host.series.providers();
    var width = host.buckets.width();
    var window = host.request.window;
    var report = host.series.window();
    var unresolved = host.series.unresolved();
    var effective = host.params.effective().series;
    var series = [];
    var hasRows = false;
    var kinds = [];
    (Array.isArray(effective) ? effective : []).forEach(function (fields, index) {
      var entry = {
        source: text(fields, "source"),
        metric: text(fields, "metric"),
        measure: text(fields, "measure"),
        label: text(fields, "label"),
        aggregation: text(fields, "aggregation")
      };
      var kindsOf = (SERIES_PROVIDERS[entry.source] || []).slice();
      var dataset = host.series.dataset(entry.source);
      if (dataset !== null) {
        // A dataset of the data catalogue: the provider that dates it.
        kindsOf = [dataset.freshness_kind];
      }
      if (entry.source === "traffic") {
        if (host.refs.entry(index, "country") !== null) {
          kindsOf.push("ip_geolocation");
        }
        if (host.refs.entry(index, "operator") !== null) {
          kindsOf.push("ip_geolocation");
        }
        if (host.refs.entry(index, "site") !== null) {
          kindsOf.push("dns_lookup");
        }
      }
      // The members in the order a series has always been written in; the entry's parameters are
      // echoed after defaults, as a map.
      var line = {
        key: "series:" + index,
        label: omitEmpty(entry.label),
        condition: "",
        availability: providers.filter(function (provider) {
          return kindsOf.indexOf(provider.kind) >= 0;
        }),
        window: null,
        gaps: [],
        unresolved_references: [],
        aggregation: omitEmpty(entry.aggregation),
        measure: "",
        lower_bound: false,
        unit: undefined,
        total: null,
        total_volume: undefined,
        buckets: [],
        parameters: Object.keys(fields).length === 0 ? undefined : host.json.map(fields)
      };
      kindsOf.forEach(function (kind) {
        if (kinds.indexOf(kind) < 0) {
          kinds.push(kind);
        }
      });
      var prefix = "series[" + index + "].";
      line.unresolved_references = unresolved.filter(function (reference) {
        return reference.path.length > prefix.length && reference.path.slice(0, prefix.length) === prefix;
      });
      line.window = { requested: { from: window.from, to: window.to }, covered: null,
        coverage: host.envelope.coverage.not_covered, flow_horizon_at: report.flow_horizon_at };
      if (host.refs.failed(index)) {
        line.condition = conditions.unresolved_reference;
        if (host.refs.withheld(index)) {
          // Scoped by a site under no_domains: no data, whatever the name, so the series cannot
          // tell which names were visited (A10, decision 6).
          line.condition = conditions.withheld_by_aggregate_mode;
        }
        line.buckets = null;
        series.push(line);
        return;
      }
      line.gaps = host.envelope.readGaps(kindsOf, window);
      var rows = fill(host, line, entry, index, width);
      line.unit = omitEmpty(line.unit === undefined ? "" : line.unit);
      line.condition = host.series.kindCondition(line.availability, kindsOf[0]);
      if (line.condition === conditions.reachable && !rows) {
        line.condition = conditions.reachable_no_rows;
      }
      if (line.condition !== conditions.reachable && line.condition !== conditions.reachable_no_rows && !rows) {
        line.buckets = null;
        line.total = null;
      }
      hasRows = hasRows || rows;
      series.push(line);
    });
    host.series.bind(kinds);
    if (series.length > 0) {
      host.envelope.cover(series[0].window.covered);
    }
    var annotations = (host.params.set("annotations") || []).map(function (kind) {
      return annotation(host, kind, width);
    });
    return {
      data: { bucket_seconds: width, series: series, annotations: annotations },
      has_rows: hasRows
    };
  }
};
