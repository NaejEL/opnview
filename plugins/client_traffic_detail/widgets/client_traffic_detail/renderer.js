/*
 * The renderer of `client_traffic_detail` (docs/widget-catalogue.md, "Client traffic detail";
 * docs/widget-api.md): where one machine went, and what was allowed. A head naming the client, its
 * current rate as a live figure -- a dash when no current sample holds one, never a zero -- and its
 * leases; then one row per logged connection, newest first, in the columns the `columns` parameter
 * chooses and in its order. Every attributed record carries the method its site name was inferred by
 * and the delay between the lookup and the flow (F7-25). A NetFlow record carries its measured bytes
 * per direction and its coverage. The records past the limit are counted.
 *
 * A pure function of its input (docs/front-plugins.md, "Widget renderer").
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* Every column the widget can draw, in the order the catalogue lists them. */
  var ALL_COLUMNS = ["observed_at", "destination", "port", "protocol", "action", "scope", "bytes", "country", "operator",
    "site_name", "method", "correlation_delay", "interface"];

  opnview.widgets.register({
    type: "client_traffic_detail",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t, tk = input.tk, format = input.format;
      var data = input.data || {};
      var chosen = Array.isArray(input.parameters.columns) ? input.parameters.columns : ALL_COLUMNS;
      var columnKeys = chosen.filter(function (key) { return ALL_COLUMNS.indexOf(key) >= 0; });
      var client = data.client;

      function labelOf(key) {
        switch (key) {
          case "observed_at": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.observed_at");
          case "destination": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.destination");
          case "port": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.port");
          case "protocol": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.protocol");
          case "action": return t("interfaces.column.action");
          case "scope": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.scope");
          case "bytes": return t("column.bytes");
          case "country": return t("column.country");
          case "operator": return t("column.operator");
          case "site_name": return t("column.site");
          case "method": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.method");
          case "correlation_delay": return t("plugin.client_traffic_detail.widget.client_traffic_detail.column.correlation_delay");
          default: return t("column.interface");
        }
      }
      function actionOf(action) {
        if (action === "pass") { return t("plugin.client_traffic_detail.widget.client_traffic_detail.action.pass"); }
        if (action === "block") {
          return kit.badge(t("plugin.client_traffic_detail.widget.client_traffic_detail.action.block"), "blocked");
        }
        return action ? String(action) : kit.none();
      }
      function scopeOf(scope) {
        if (scope === "north_south") { return t("plugin.client_traffic_detail.widget.client_traffic_detail.scope.north_south"); }
        if (scope === "east_west") { return t("plugin.client_traffic_detail.widget.client_traffic_detail.scope.east_west"); }
        if (scope === "this_firewall") { return t("plugin.client_traffic_detail.widget.client_traffic_detail.scope.this_firewall"); }
        return scope ? String(scope) : kit.none();
      }
      /* A live figure: its rate in, and out where it was measured; a dash where no sample holds one. */
      function rateOf(rate) {
        if (!rate || rate.state === "no_current_sample" || rate.in_bits_per_second === null || rate.in_bits_per_second === undefined) {
          return kit.none(t("plugin.client_traffic_detail.widget.client_traffic_detail.no_current_sample"));
        }
        var parts = [h("span", { key: "in", "class": "fig", "data-measure": rate.measure },
          t("plugin.client_traffic_detail.widget.client_traffic_detail.rate_in", { rate: format.rate(rate.in_bits_per_second) }))];
        parts.push(" ");
        parts.push(rate.out_measured && rate.out_bits_per_second !== null
          ? h("span", { key: "out", "class": "fig", "data-measure": rate.measure },
            t("plugin.client_traffic_detail.widget.client_traffic_detail.rate_out", { rate: format.rate(rate.out_bits_per_second) }))
          : kit.none(t("plugin.client_traffic_detail.widget.client_traffic_detail.out_not_measured")));
        if (rate.state === "stale_sample") {
          parts.push(kit.badge(t("plugin.client_traffic_detail.widget.client_traffic_detail.stale_sample"), "unavailable"));
        }
        return h("span", { "class": "live-rate", "data-rate-state": rate.state }, parts);
      }
      var peerRates = {};
      (data.current_rate_by_peer || []).forEach(function (entry) { peerRates[entry.peer] = entry.rate; });

      /* The bytes of a record: its figure, and for a NetFlow record each direction's own bytes and
       * whether NetFlow measured it. */
      function bytesOf(record) {
        var figure = kit.value(record.bytes, "bytes");
        if (!record.forward && !record.reverse) { return figure; }
        var direction = function (part, key) {
          if (!part) { return null; }
          var values = { bytes: part.bytes === null || part.bytes === undefined ? t("figure.dash") : format.bytes(part.bytes),
            state: part.state ? String(part.state) : t("figure.dash") };
          return h("span", { key: key, "class": "ident", "data-direction-state": part.state || "" },
            key === "forward" ? t("plugin.client_traffic_detail.widget.client_traffic_detail.forward", values)
              : t("plugin.client_traffic_detail.widget.client_traffic_detail.reverse", values));
        };
        return h("span", {}, [figure, direction(record.forward, "forward"), direction(record.reverse, "reverse")]);
      }
      function cellOf(key, record) {
        var geo = record.geo || {};
        switch (key) {
          case "observed_at": return format.instant(record.observed_at);
          case "destination":
            return h("span", { "class": "mono" }, [record.destination, peerRates[record.destination]
              ? h("span", { "class": "ident" }, rateOf(peerRates[record.destination])) : null]);
          case "port": return record.port === null || record.port === undefined ? kit.none() : String(record.port);
          case "protocol": return record.protocol ? String(record.protocol) : kit.none();
          case "action": return actionOf(record.action);
          case "scope": return scopeOf(record.scope);
          case "bytes": return bytesOf(record);
          case "country": return geo.country_code || kit.none(t("figure.no_country"));
          case "operator": return geo.operator || kit.none(t("figure.no_operator"));
          case "site_name": return record.site_name || kit.none(t("plugin.client_traffic_detail.widget.client_traffic_detail.no_site_name"));
          case "method":
            return record.method ? h("span", { "class": "badge method method-" + record.method, "data-method": record.method },
              tk("method.", record.method)) : kit.none(t("figure.no_method"));
          case "correlation_delay":
            return record.correlation_delay_seconds === null || record.correlation_delay_seconds === undefined
              ? kit.none(t("figure.no_method"))
              : h("span", { "class": "fig fig-derived", "data-correlation-delay": String(record.correlation_delay_seconds) },
                kit.text(record.correlation_delay_seconds, "seconds"));
          default: return record.interface_description || record.interface_device || kit.none();
        }
      }
      function recordSubject(record) {
        var geo = record.geo || {};
        var related = [];
        if (geo.asn) {
          related.push(kit.subject("operator", geo.asn, geo.operator || String(geo.asn), [[t("column.asn"), kit.text(geo.asn)]],
            { reference: { kind: "operator", by: "asn", value: geo.asn, label: geo.operator || String(geo.asn) } }));
        }
        if (geo.country_code) {
          related.push(kit.subject("country", geo.country_code, geo.country_name || geo.country_code, [],
            { reference: { kind: "country", by: "iso_3166_1_alpha_2", value: geo.country_code, label: geo.country_name || geo.country_code } }));
        }
        if (client) { related.push(kit.clientSubject(client)); }
        return kit.subject("record", record.flow_id, record.destination, ALL_COLUMNS.filter(function (key) {
          return key !== "destination";
        }).map(function (key) { return [labelOf(key), cellOf(key, record)]; }), { related: related });
      }
      var rows = (data.records || []).map(function (record) {
        return { key: String(record.flow_id), className: record.action === "block" ? "is-blocked" : null,
          subject: recordSubject(record),
          cells: columnKeys.map(function (key) { return cellOf(key, record); }) };
      });
      var head = client ? h("div", { "class": "client-head" }, [
        kit.figures([kit.figure("rate", rateOf(data.current_rate),
          h("span", kit.attrs({ "class": "pickable" }, kit.pick(kit.clientSubject(client))), [
            kit.clientLabel(client), client.last_address ? [" · ", client.last_address] : null
          ]))]),
        (data.leases || []).length ? (kit.chips((data.leases || []).map(function (lease, index) {
          return { key: String(index), label: lease.address, value: lease.backend || lease.provider_key,
            title: lease.expires_at ? t("plugin.client_traffic_detail.widget.client_traffic_detail.lease_expires",
              { at: format.instant(lease.expires_at) }) : null };
        }))) : null
      ]) : null;
      var remainder = data.remainder || { members: 0 };
      var columns = columnKeys.map(function (key) {
        return { key: key, label: labelOf(key), num: key === "bytes" || key === "correlation_delay" || key === "port" };
      });
      return h("div", { "class": "client-traffic-detail", "data-columns": columnKeys.join(" ") }, [
        head,
        columns.length ? kit.table(columns, rows, {
          remainder: remainder.members ? { members: remainder.members, cells: columnKeys.slice(1).map(function (key) {
            return key === "bytes" ? kit.value(remainder.count, "bytes") : "";
          }) } : null,
          total: data.total ? { label: input.tp("plugin.client_traffic_detail.widget.client_traffic_detail.total", data.total.members || 0),
            cells: columnKeys.slice(1).map(function (key) { return key === "bytes" ? kit.value(data.total.count, "bytes") : ""; }) } : null,
          empty: t("table.no_rows")
        }) : kit.note(input.tp("plugin.client_traffic_detail.widget.client_traffic_detail.no_column", rows.length), "no-column")
      ]);
    }
  });
})(this);
