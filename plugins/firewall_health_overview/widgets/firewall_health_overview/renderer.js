/*
 * The renderer of `firewall_health_overview` (docs/widget-catalogue.md, "Firewall health overview";
 * docs/widget-api.md): is the firewall itself healthy right now. One tile per metric and per subject
 * -- a sensor, a gateway, a filesystem, as many as the installation answers -- each its latest reading,
 * its recent history at the resolution it was sampled, and, only when the reader set one, its
 * threshold state: no threshold is ever assumed.
 *
 * A READING THAT DOES NOT EXIST IS ABSENT, NEVER ZERO. A tile whose metric no sample answers keeps its
 * label and draws a dash with the reason in its title: a zero CPU and an unknown CPU are different
 * facts. Firewall health and telemetry are open gaps of the model (G9, G10): an absent tile stays
 * marked absent.
 *
 * `layout` (`tiles` | `rows`) chooses a grid of tiles or one row per reading.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The threshold states a reading can be in, each its badge. */
  var THRESHOLD_BADGE = { ok: "ok", warning: "unavailable", critical: "blocked" };

  opnview.widgets.register({
    type: "firewall_health_overview",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, data = input.data || {}, format = input.format;
      var parameters = input.parameters || {};
      var layout = parameters.layout === "rows" ? "rows" : "tiles";
      var showLastSampled = parameters.show_last_sampled !== false;

      function reading(tile) {
        if (tile.state !== "measured" || tile.value === null || tile.value === undefined) {
          return kit.none(t("plugin.firewall_health_overview.widget.firewall_health_overview.absent"));
        }
        var text;
        if (tile.unit === "ratio") { text = format.percent(tile.value); }
        else if (tile.unit === "seconds") { text = format.duration(tile.value); }
        else if (tile.unit === "bytes") { text = format.bytes(tile.value); }
        else { text = kit.text(tile.value) + (tile.unit ? " " + tile.unit : ""); }
        return h("span", { "class": "fig" + (tile.measure ? "" : " fig-derived"), "data-measure": tile.measure || null }, text);
      }

      var tiles = (data.tiles || []).map(function (tile, index) {
        var name = tk("plugin.firewall_health_overview.widget.firewall_health_overview.metric.", tile.metric);
        var label = tile.subject_key && tile.subject_key !== tile.metric
          ? t("plugin.firewall_health_overview.widget.firewall_health_overview.metric_subject", { metric: name, subject: tile.subject_key }) : name;
        var series = (tile.series || []).map(function (sample) { return sample.value; });
        var absent = tile.state !== "measured";
        var facts = [
          [t("plugin.firewall_health_overview.widget.firewall_health_overview.fact.reading"), reading(tile)],
          [t("plugin.firewall_health_overview.widget.firewall_health_overview.fact.samples"), kit.text(series.length)]
        ];
        if (tile.last_sampled_at) {
          facts.push([t("plugin.firewall_health_overview.widget.firewall_health_overview.fact.sampled_at"), format.instant(tile.last_sampled_at)]);
        }
        return h("div", kit.attrs({ key: tile.metric + ":" + (tile.subject_key || "") + ":" + index,
          "class": "tile" + (absent ? " is-absent" : ""), "data-metric": tile.metric, "data-state": tile.state },
          kit.pick(kit.subject("metric", tile.metric + ":" + (tile.subject_key || index), label, facts))), [
          h("span", { "class": "tile-label" }, label),
          h("span", { "class": "tile-value" + (absent ? " tile-value-empty" : "") }, reading(tile)),
          absent ? kit.badge(t("plugin.firewall_health_overview.widget.firewall_health_overview.absent"), "gap") : null,
          tile.threshold_state && THRESHOLD_BADGE[tile.threshold_state]
            ? kit.badge(tk("plugin.firewall_health_overview.widget.firewall_health_overview.threshold.", tile.threshold_state),
              THRESHOLD_BADGE[tile.threshold_state]) : null,
          series.length ? kit.spark(series, kit.seriesClass(index), { width: layout === "rows" ? 160 : 120, height: 22 }) : null,
          showLastSampled && tile.last_sampled_at
            ? h("span", { "class": "tile-sub" }, t("plugin.firewall_health_overview.widget.firewall_health_overview.sampled_at",
              { at: format.instant(tile.last_sampled_at) })) : null
        ]);
      });
      return h("div", { "class": "tiles" + (layout === "rows" ? " is-rows" : ""), "data-layout": layout }, tiles);
    }
  });
})(this);
