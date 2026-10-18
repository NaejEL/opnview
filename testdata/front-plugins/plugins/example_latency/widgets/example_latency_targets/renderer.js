/*
 * The renderer a plugin widget carries in its own directory: the slowest sample per probe target, as
 * ranked bars. `show_samples`, a rendering parameter, adds the number of samples under each bar.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var PREFIX = "plugin.example_latency.widget.example_latency_targets.";

  opnview.widgets.register({
    type: "example_latency_targets",
    render: function (input) {
      var kit = input.kit, tp = input.tp;
      var showSamples = input.parameters.show_samples !== false;
      var targets = (input.data && input.data.targets) || [];
      return kit.bars(targets.map(function (row) {
        return {
          key: row.target,
          label: row.target,
          value: row.worst_ms,
          valueNode: kit.value(row.worst_ms / 1000, "seconds"),
          sub: showSamples ? tp(PREFIX + "samples", row.samples) : null
        };
      }));
    }
  });
})(this);
