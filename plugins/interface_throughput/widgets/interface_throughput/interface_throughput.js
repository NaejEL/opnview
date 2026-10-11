// interface_throughput: per interface, bandwidth in and out over time, and the packet and error
// counters, from the interface's OWN counters (docs/widget-catalogue.md, "Interface throughput").
// They include traffic no logging rule matched, which the filter log never recorded, so their
// measure is interface_counter and never logged; traffic between two clients behind one interface
// still never reaches them. Interfaces are named by the user's label or the discovered description,
// never by the device alone.

exports.widget = {
  serve: function (host) {
    host.envelope.coverFrom(host.envelope.earliest("widget_earliest_sample"));
    var selected = host.refs.ids("interfaces");
    var measure = host.params.text("measure");
    var wanted = host.params.text("direction");
    var series = [];
    var hasRows = false;
    host.installation.interfaces().forEach(function (info) {
      if (selected !== null && selected.indexOf(info.id) < 0) {
        return;
      }
      ["in", "out"].forEach(function (direction) {
        if (wanted !== "both" && wanted !== direction) {
          return;
        }
        var read = host.samples.throughput(info.device, measure, direction);
        // One interface's one direction, at the resolution its samples have; lower_bound is true,
        // since intra-interface traffic never reaches the counters either, and every counter reset
        // in the window is counted, each a hole and never a rate.
        var line = {
          key: host.figures.interfaceKey(info.id) + ":" + direction,
          interface: info,
          direction: direction,
          measure: host.figures.measures.interface_counter,
          unit: measure,
          lower_bound: true,
          points: [],
          resets: read.resets,
          current: null
        };
        if (read.points.length > 0) {
          hasRows = true;
          line.points = read.points;
          line.current = read.points[read.points.length - 1];
        }
        series.push(line);
      });
    });
    return { data: { series: series }, has_rows: hasRows };
  }
};
