/*
 * opnview front -- the shape kit: what every widget renderer draws with (specs/SPEC-step-7-frontend.md,
 * A2; docs/front-plugins.md, "Widget renderer").
 *
 * A RENDERER IS A PURE FUNCTION. It is given the widget's answer, its effective parameters, the card's
 * size in CSS pixels, the string lookup, the formatters and one kit made for this one drawing, and it
 * answers a view tree (front/core/view.js). It touches no document, reads no clock and sends nothing:
 * the core fetches, schedules and reconciles. The kit is how it draws without knowing any of that.
 *
 * WHAT THE KIT HOLDS, and the rules of docs/widget-catalogue.md, Part 1, it keeps for every renderer:
 *
 *   - figures: a figure node always carries its measure (data-measure); a figure that does not exist
 *     is a dash naming why, never a zero; two measures are never added (kit.add answers null);
 *   - truncation is never silent: every list, table, bar set and chip row takes a remainder and draws
 *     it with its own count;
 *   - charts are first-party SVG drawn at the card's own pixel size: linear between measured points
 *     by default, every sample drawn, every series element carrying data-sample-count, an area only
 *     to separate stacked series, a gap drawn as a labelled gap, an aggregation to fit the pixels
 *     stated;
 *   - inspecting is declarative: kit.pick(subject) marks a node as opening a detail about a subject,
 *     kit.tip(nodes) gives a node a tooltip; the core attaches both, and the subject's facts and
 *     pivots travel with it.
 *
 * NO COLOUR AND NO WORD IS WRITTEN HERE. A colour is a class of the stylesheet (s1..s6 strokes,
 * f1..f6 fills, sw-1..sw-6 swatches, and the status classes); a word comes from the catalogue through
 * the string lookup the kit is made with.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;

  /* The measures a volume can be given in, which a figure states wherever two could differ
   * (docs/widget-api.md, "Volumes and the observation-point limit"). */
  var VOLUME_MEASURES = ["logged", "netflow", "sampled", "interface_counter"];
  /* Every measure a figure of the widget API carries. */
  var MEASURES = VOLUME_MEASURES.concat(["refusals", "dns_lookups", "security_events", "clients", "addresses", "sampled_gauge",
    "dns", "security_event"]);

  /* The vocabularies of the shared words (placeReason, direction, directionState below). */
  var GEO_REASONS = ["miss", "pending", "no_row", "no_coordinates"];
  var SCOPES = ["north_south", "east_west", "this_firewall"];
  var DIRECTIONS = ["in", "out", "inbound", "outbound"];
  var DIRECTION_STATES = ["measured", "in_progress", "awaiting_export", "missing", "export_gap", "not_exported_when_refused",
    "forwarding_export_unsettled", "ingress_not_captured", "parent_copies_only", "interface_not_exported",
    "interface_not_captured", "capture_configuration_unknown", "ambiguous_records", "not_expected_when_refused"];

  /* How many series classes the stylesheet carries. */
  var SERIES_CLASSES = 6;

  function isFigure(value) {
    return value !== null && typeof value === "object" && typeof value.measure === "string";
  }

  /* The number a figure holds: a Count's value, a Volume's bytes, or a bare number. */
  function amount(value, field) {
    if (value === null || value === undefined) { return null; }
    if (typeof value === "number") { return value; }
    if (field && value[field] !== undefined) { return value[field]; }
    if (value.value !== undefined) { return value.value; }
    if (value.bytes !== undefined) { return value.bytes; }
    return null;
  }

  function niceCeil(value) {
    if (!(value > 0)) { return 1; }
    var magnitude = Math.pow(10, Math.floor(Math.log(value) / Math.LN10));
    var fraction = value / magnitude;
    var step = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 2.5 ? 2.5 : fraction <= 5 ? 5 : 10;
    return step * magnitude;
  }

  /* A BYTE AXIS IS NICE IN THE BASE ITS LABELS ARE PRINTED IN (the mockup's niceCeil1024): its top is a
   * step of the binary ladder whose quarter is itself round, so 0, 2, 4, 6, 8 GiB and never 4.66. */
  var BINARY_STEPS = [1, 2, 4, 5, 6, 8, 10, 12, 16, 20, 24, 32, 40, 48, 64, 80, 96, 128, 160, 192, 256, 320, 384, 512,
    640, 768, 1024];
  function niceCeil1024(value) {
    if (!(value > 0)) { return 1; }
    var divisor = 1;
    while (value / divisor >= 1024) { divisor *= 1024; }
    var fraction = value / divisor;
    for (var i = 0; i < BINARY_STEPS.length; i++) {
      if (fraction <= BINARY_STEPS[i]) { return BINARY_STEPS[i] * divisor; }
    }
    return 1024 * divisor;
  }

  /* The intervals a time axis is ticked at, in seconds: each tick on a round instant of the reader's
   * clock, as the mockup's axes read 13:00, 17:00, 21:00. */
  var TICK_STEPS = [60, 300, 600, 900, 1800, 3600, 7200, 10800, 14400, 21600, 43200, 86400, 172800, 604800];

  /* The base-ten logarithm, exact at a power of ten. */
  function log10(value) {
    var exact = Math.log(value) / Math.LN10;
    var whole = Math.round(exact);
    return Math.abs(exact - whole) < 1e-9 ? whole : exact;
  }

  function fixed(number) {
    return Math.round(number * 10) / 10;
  }

  /* A path through points, straight between them: only M and L. */
  function linearPath(points) {
    return points.map(function (point, index) {
      return (index === 0 ? "M" : "L") + fixed(point[0]) + " " + fixed(point[1]);
    }).join(" ");
  }

  /* A Catmull-Rom curve through every point: each piece ends exactly on the next measured point,
   * and its control points come from the neighbours, so no sample is moved and none is dropped. */
  function smoothPath(points) {
    if (points.length < 3) { return linearPath(points); }
    var parts = ["M" + fixed(points[0][0]) + " " + fixed(points[0][1])];
    for (var i = 0; i < points.length - 1; i++) {
      var p0 = points[i > 0 ? i - 1 : 0], p1 = points[i], p2 = points[i + 1];
      var p3 = points[i + 2 < points.length ? i + 2 : i + 1];
      parts.push("C" + fixed(p1[0] + (p2[0] - p0[0]) / 6) + " " + fixed(p1[1] + (p2[1] - p0[1]) / 6) + " " +
        fixed(p2[0] - (p3[0] - p1[0]) / 6) + " " + fixed(p2[1] - (p3[1] - p1[1]) / 6) + " " +
        fixed(p2[0]) + " " + fixed(p2[1]));
    }
    return parts.join(" ");
  }

  /* Makes the kit of one drawing. context: {t, tp, tk, td, format, size, prefix} -- prefix keys the
   * subjects and tooltips of this card, so two cards never share one. */
  function create(context) {
    var t = context.t, tp = context.tp, tk = context.tk, format = context.format;
    var subjects = {}, tips = {};
    var prefix = context.prefix || "w";
    var tipCount = 0;

    var kit = {
      h: h,
      MEASURES: MEASURES.slice(),
      VOLUME_MEASURES: VOLUME_MEASURES.slice(),
      size: context.size || { width: 640, height: 320 },

      /* -------------------------------------------------------------------------------------------
       * Figures.
       * ----------------------------------------------------------------------------------------- */

      amount: amount,
      isFigure: isFigure,

      /* Two figures added, or null when either is absent or their measures differ: a sum across
       * measures is never drawn. */
      add: function (a, b, field) {
        if (a === null || a === undefined || b === null || b === undefined) { return null; }
        if (isFigure(a) && isFigure(b) && a.measure !== b.measure) { return null; }
        var x = amount(a, field), y = amount(b, field);
        return x === null || y === null ? null : x + y;
      },

      /* The text of a figure in its unit: bytes, a count, a rate, a share or seconds. */
      text: function (value, unit) {
        if (value === null || value === undefined) { return null; }
        switch (unit) {
          case "bytes": return format.bytes(value);
          case "bits_per_second": case "bit_per_second": return format.rate(value);
          case "share": return format.percent(value);
          case "seconds": return format.duration(value);
          case "instant": return format.instant(value);
          default: return format.number(value, value % 1 === 0 ? 0 : 2);
        }
      },

      /* One figure, drawn: its text in its unit, the figure's measure on the node, or a dash naming
       * why there is none. figure is a Count, a Volume (field picks bytes or connections) or a
       * number. */
      value: function (figure, unit, options) {
        var settings = options || {};
        var number = amount(figure, settings.field);
        if (number === null) { return kit.none(settings.why); }
        var attrs = { "class": "fig" + (settings.className ? " " + settings.className : "") };
        if (isFigure(figure)) {
          attrs["data-measure"] = figure.measure;
          attrs["data-lower-bound"] = figure.lower_bound ? "true" : "false";
        } else if (settings.measure) {
          attrs["data-measure"] = settings.measure;
        } else {
          /* A bare number is a derived value -- a share, a delay, a count of members -- not a
           * measured figure, and says so. */
          attrs["class"] += " fig-derived";
        }
        return h("span", attrs, kit.text(number, unit));
      },

      /* No figure: a dash with the reason it has none -- a text of the catalogue -- in its title,
       * never a zero. */
      none: function (reason) {
        return h("span", { "class": "muted fig-none", "data-none": "true", title: reason || t("figure.none") }, t("figure.dash"));
      },

      /* The measure a figure is given in, as a chip a reader can tell apart from the others. */
      measure: function (measure) {
        if (!measure) { return null; }
        return h("span", { "class": "badge measure-chip measure-" + measure, "data-measure-chip": measure },
          tk("measure.", measure));
      },

      /* The measures of a set of figures, one chip each, in a stable order. */
      measures: function (figures) {
        var seen = {};
        (figures || []).forEach(function (figure) {
          if (isFigure(figure)) { seen[figure.measure] = true; }
        });
        return MEASURES.filter(function (measure) { return seen[measure]; }).map(kit.measure);
      },

      /* A lead figure: a large value and its label. */
      figure: function (key, valueNode, label, extra) {
        return h("div", { key: key, "class": "figure" }, [
          h("span", { "class": "figure-value" }, valueNode),
          h("span", { "class": "figure-label" }, label),
          extra || null
        ]);
      },

      figures: function (items) {
        return h("div", { "class": "figures figures-lead" }, items);
      },

      /* -------------------------------------------------------------------------------------------
       * Words of the shapes.
       * ----------------------------------------------------------------------------------------- */

      badge: function (text, kind, title) {
        return h("span", { "class": "badge" + (kind ? " badge-" + kind : ""), title: title || null }, text);
      },

      /* THE SHARED WORDS OF THE RENDERERS. Where an answer says where traffic went or why an address
       * could not be placed, every renderer says it in the core's words, so two widgets on one canvas
       * never name one thing two ways. Each answers null for a value outside its vocabulary, which the
       * renderer then names in its own words. */

      /* A scope or a geolocation reason: the four lookup states of an address, and the three places
       * traffic can be relative to the firewall. */
      placeReason: function (reason) {
        if (GEO_REASONS.indexOf(reason) >= 0) { return tk("reason.", reason); }
        if (SCOPES.indexOf(reason) >= 0) { return tk("column.scope.", reason); }
        return null;
      },
      /* A direction: in, out, inbound, outbound, or between interfaces. */
      direction: function (key) {
        if (DIRECTIONS.indexOf(key) >= 0) { return tk("column.direction.", key); }
        if (key === "inter_interface") { return tk("column.scope.", "east_west"); }
        return null;
      },
      /* The state of one direction of a NetFlow-measured connection (docs/widget-api.md, "Volumes"). */
      directionState: function (state) {
        return DIRECTION_STATES.indexOf(state) >= 0 ? tk("column.direction_state.", state) : null;
      },

      /* A named state of one series, one source or one row: never a zero, never an error. */
      stateBadge: function (condition) {
        var kind = condition === "unavailable" || condition === "state_unknown" ? "unavailable"
          : condition === "present_but_disabled" ? "off"
          : condition === "unresolved_reference" ? "ref"
          : condition === "reachable_no_rows" ? "ok" : "gap";
        return h("span", { "class": "badge badge-" + kind, "data-condition": condition }, tk("condition.", condition));
      },

      /* A coordinate written with two decimals at most, as every drawing of the kit writes one. */
      fixed: fixed,

      /* The formatter of a chart axis in a unit: a byte axis declares its binary base, so its ticks
       * fall on round binary figures. */
      axis: function (unit) {
        var formatter = function (value) { return kit.text(value, unit); };
        if (unit === "bytes") { formatter.axisBase = 1024; }
        return formatter;
      },

      legend: function (items) {
        return h("div", { "class": "legend" }, items.map(function (item, index) {
          return h("span", { key: item.key || String(index), "class": "legend-item" + (item.muted ? " muted" : "") }, [
            item.cls !== undefined ? h("span", { "class": "swatch sw-" + item.cls }) : null,
            item.label,
            item.extra || null
          ]);
        }));
      },

      /* A row a reader expands: the browser holds whether it is open, so a refresh -- which never
       * writes `open` -- leaves an expanded row expanded. */
      expand: function (key, summary, content) {
        return h("details", { key: key, "class": "expand", "data-expand": key }, [
          h("summary", { "class": "expand-summary" }, summary),
          h("div", { "class": "expand-body" }, content)
        ]);
      },

      /* Values side by side, each its own node, a separator mark between them: a value is never
       * glued to another into a text no catalogue holds. */
      join: function (items, separator) {
        var out = [];
        (items || []).forEach(function (item, index) {
          if (index > 0) { out.push(h("span", { key: "s" + index, "class": "sep", "aria-hidden": "true" }, separator || " · ")); }
          out.push(h("span", { key: "i" + index, "class": "joined-item" }, item));
        });
        return h("span", { "class": "joined" }, out);
      },

      /* A note at the foot of a drawing: what was cut, what was aggregated. */
      note: function (text, key) {
        return h("div", { key: key || null, "class": "clip-note" }, text);
      },

      /* The series class of the n-th series, cycling over the stylesheet's six. */
      seriesClass: function (index) {
        return String((index % SERIES_CLASSES) + 1);
      },

      /* -------------------------------------------------------------------------------------------
       * Subjects, details and tooltips.
       * ----------------------------------------------------------------------------------------- */

      /* Marks a node as opening a detail about a subject. subject: {kind, key, title, facts:
       * [[label, value node]], related: [subject], reference: a reference object the canvas can be
       * scoped to, and a widget added about it (the canvas picks which from the loaded manifests)}.
       * Answers the attributes to merge into the node. */
      pick: function (subject, extra) {
        var id = subject.kind + ":" + subject.key;
        subjects[id] = subject;
        var attrs = { "data-subject": id, role: "button", tabindex: "0",
          "aria-label": t("detail.open", { name: subject.title }) };
        Object.keys(extra || {}).forEach(function (name) { attrs[name] = extra[name]; });
        return attrs;
      },

      /* Gives a node a tooltip: nodes -- or a function answering them, called only when the tooltip
       * is shown -- are drawn when it is hovered or focused. Answers the attributes to merge into the
       * node. */
      tip: function (nodes, key) {
        tipCount += 1;
        var id = prefix + ":" + (key || String(tipCount));
        tips[id] = nodes;
        return { "data-tip": id };
      },

      /* Merges attribute objects, the later winning. */
      attrs: function () {
        var merged = {};
        for (var i = 0; i < arguments.length; i++) {
          var given = arguments[i] || {};
          Object.keys(given).forEach(function (name) { merged[name] = given[name]; });
        }
        return merged;
      },

      subjects: function () { return subjects; },
      tips: function () { return tips; },

      /* The facts of a detail: label and value, in order. */
      fact: function (label, value) { return [label, value]; },

      /* -------------------------------------------------------------------------------------------
       * References a detail's pivots carry. A reference names what the format can name; a subject
       * with no portable key has none, and its detail offers no scope pivot.
       * ----------------------------------------------------------------------------------------- */

      interfaceRef: function (iface) {
        if (!iface || !iface.identifier) { return null; }
        return { kind: "interface", by: "identifier", value: iface.identifier, label: iface.label || iface.identifier };
      },
      clientRef: function (client) {
        if (!client) { return null; }
        var label = kit.clientLabel(client);
        if (client.identity_kind === "mac" && client.mac) {
          return { kind: "client", by: "mac", value: client.mac, label: label };
        }
        if (client.identity_kind === "dhcp_client_id" && client.identity_key) {
          return { kind: "client", by: "dhcp_client_id", value: client.identity_key, label: label };
        }
        if (client.hostname) { return { kind: "client", by: "hostname", value: client.hostname, label: label }; }
        return null;
      },
      ownerRef: function (owner) {
        if (!owner || !owner.display_name) { return null; }
        return { kind: "owner", by: "display_name", value: owner.display_name, label: owner.display_name };
      },

      /* What a client is called: its host name, its MAC, its last address -- data, in that order. */
      clientLabel: function (client) {
        if (!client) { return ""; }
        return client.hostname || client.mac || client.last_address || client.identity_key || String(client.id);
      },
      /* What an interface is called: the label the server chose (its user label, else its
       * description), else its identifier. */
      interfaceLabel: function (iface) {
        if (!iface) { return ""; }
        return iface.label || iface.user_label || iface.description || iface.identifier || iface.device || String(iface.id);
      },

      /* A client as a subject. */
      clientSubject: function (client, facts, more) {
        var reference = kit.clientRef(client);
        var subject = {
          kind: "client", key: String(client.id), title: kit.clientLabel(client),
          facts: [
            kit.fact(t("detail.fact.address"), client.last_address || kit.none()),
            kit.fact(t("detail.fact.mac"), client.mac || kit.none()),
            kit.fact(t("detail.fact.identity"), client.identity_kind ? tk("people.identity.", client.identity_kind) : kit.none())
          ].concat(facts || []),
          related: [], reference: reference
        };
        Object.keys(more || {}).forEach(function (name) { subject[name] = more[name]; });
        return subject;
      },

      /* An interface as a subject. */
      interfaceSubject: function (iface, facts, more) {
        var reference = kit.interfaceRef(iface);
        var subject = {
          kind: "interface", key: String(iface.id), title: kit.interfaceLabel(iface),
          facts: [
            kit.fact(t("detail.fact.identifier"), iface.identifier || kit.none()),
            kit.fact(t("detail.fact.device"), iface.device || kit.none()),
            iface.vlan_tag !== null && iface.vlan_tag !== undefined
              ? kit.fact(t("detail.fact.vlan"), format.number(iface.vlan_tag)) : null
          ].filter(Boolean).concat(facts || []),
          related: [], reference: reference
        };
        Object.keys(more || {}).forEach(function (name) { subject[name] = more[name]; });
        return subject;
      },

      /* An owner as a subject. */
      ownerSubject: function (owner, facts, more) {
        var reference = kit.ownerRef(owner);
        var subject = {
          kind: "owner", key: String(owner.id), title: owner.display_name,
          facts: facts || [], related: [], reference: reference
        };
        Object.keys(more || {}).forEach(function (name) { subject[name] = more[name]; });
        return subject;
      },

      /* A plain subject: a site, an operator, a country, a rule, a cell, a mark. */
      subject: function (kind, key, title, facts, more) {
        var subject = { kind: kind, key: String(key), title: title, facts: facts || [], related: [],
          reference: null };
        Object.keys(more || {}).forEach(function (name) { subject[name] = more[name]; });
        return subject;
      },

      /* -------------------------------------------------------------------------------------------
       * Lists that never truncate in silence.
       * ----------------------------------------------------------------------------------------- */

      /* A remainder: "N more", with its own figure where it has one. */
      remainder: function (members, valueNode, key) {
        if (!members) { return null; }
        return h("div", { key: key || "remainder", "class": "remainder-row", "data-remainder": String(members) }, [
          h("span", { "class": "remainder-label" }, tp("shape.remainder", members)),
          valueNode ? h("span", { "class": "remainder-value" }, valueNode) : null
        ]);
      },

      /* A table. columns: [{key, label, num, className}]; rows: [{key, cells: [node], className,
       * subject}]; options: {remainder: {members, cells}, total: {label, cells}, caption, empty}. */
      table: function (columns, rows, options) {
        var settings = options || {};
        var head = h("thead", {}, h("tr", {}, columns.map(function (column) {
          return h("th", { key: column.key, "class": (column.num ? "num" : "") + (column.className ? " " + column.className : ""),
            scope: "col" }, column.label);
        })));
        var bodyRows = rows.map(function (row) {
          var attrs = kit.attrs({ key: "row-" + row.key, "class": row.className || null, "data-row": row.key }, row.attrs || {});
          return h("tr", attrs, row.cells.map(function (cell, index) {
            var column = columns[index] || {};
            var cellAttrs = { key: column.key || String(index), "class": column.num ? "num" : (column.cellClass || null) };
            if (index === 0 && row.subject) { cellAttrs = kit.attrs(cellAttrs, kit.pick(row.subject), { "class": "pickable" }); }
            return h("td", cellAttrs, cell);
          }));
        });
        if (rows.length === 0 && settings.empty) {
          bodyRows.push(h("tr", { key: "empty", "class": "is-empty" },
            h("td", { colspan: String(columns.length) }, settings.empty)));
        }
        if (settings.remainder && settings.remainder.members) {
          bodyRows.push(h("tr", { key: "remainder", "class": "is-remainder", "data-remainder": String(settings.remainder.members),
            "data-value": settings.remainder.value === undefined ? null : String(settings.remainder.value) },
            [h("td", { key: "label" }, settings.remainder.label || tp("shape.remainder", settings.remainder.members))]
              .concat((settings.remainder.cells || []).map(function (cell, index) {
                var column = columns[index + 1] || {};
                return h("td", { key: "c" + index, "class": column.num ? "num" : null }, cell);
              }))));
        }
        if (settings.total) {
          bodyRows.push(h("tr", { key: "total", "class": "is-total",
            "data-total": settings.total.value === undefined ? null : String(settings.total.value) },
            [h("td", { key: "label" }, settings.total.label)].concat((settings.total.cells || []).map(function (cell, index) {
              var column = columns[index + 1] || {};
              return h("td", { key: "c" + index, "class": column.num ? "num" : null }, cell);
            }))));
        }
        return h("div", { "class": "table-scroll" }, h("table", { "class": "data" + (settings.compact ? " is-compact" : "") }, [
          settings.caption ? h("caption", {}, settings.caption) : null,
          head,
          h("tbody", {}, bodyRows)
        ]));
      },

      /* Ranked bars. rows: [{key, label, sub, value (number), valueNode, parts: [{value, cls}],
       * subject}]; options: {max, remainder: {members, valueNode}, total: {label, valueNode}}.
       * A silent member keeps its row with an empty bar. */
      bars: function (rows, options) {
        var settings = options || {};
        var max = settings.max || rows.reduce(function (most, row) { return Math.max(most, row.value || 0); }, 0) || 1;
        var out = rows.map(function (row) {
          var parts = row.parts || [{ value: row.value || 0, cls: "1" }];
          var labelAttrs = { "class": "bar-label" };
          if (row.subject) { labelAttrs = kit.attrs(labelAttrs, kit.pick(row.subject), { "class": "bar-label pickable" }); }
          return h("div", { key: "bar-" + row.key, "class": "bar-block", "data-bar": row.key,
            "data-value": String(row.value === null || row.value === undefined ? "" : row.value) }, [
            h("div", { "class": "bar-row" }, [
              h("span", labelAttrs, row.label),
              kit.track(parts, max),
              h("span", { "class": "bar-value" }, row.valueNode)
            ]),
            row.sub ? h("div", { "class": "bar-sub" }, row.sub) : null
          ]);
        });
        if (settings.remainder && settings.remainder.members) {
          out.push(h("div", { key: "remainder", "class": "bar-row is-remainder",
            "data-remainder": String(settings.remainder.members), "data-value": String(settings.remainder.value || 0) }, [
            h("span", { "class": "bar-label" }, tp("shape.remainder", settings.remainder.members)),
            h("span", { "class": "bar-track-blank" }),
            h("span", { "class": "bar-value" }, settings.remainder.valueNode)
          ]));
        }
        if (settings.total) {
          out.push(h("div", { key: "total", "class": "bar-row is-total", "data-total": String(settings.total.value) }, [
            h("span", { "class": "bar-label" }, settings.total.label),
            h("span", { "class": "bar-track-blank" }),
            h("span", { "class": "bar-value" }, settings.total.valueNode)
          ]));
        }
        return h("div", { "class": "bars" }, out);
      },

      /* A bar's track: its parts laid end to end, each its share of max. Drawn as a scaled drawing
       * so a length is an attribute, never a style. */
      track: function (parts, max) {
        var at = 0;
        return h("svg", { "class": "bar-track", viewBox: "0 0 1000 10", preserveAspectRatio: "none",
          "aria-hidden": "true" }, parts.map(function (part, index) {
          var length = Math.max(0, Math.min(1000 - at, 1000 * (part.value || 0) / (max || 1)));
          var rect = h("rect", { key: "p" + index, "class": "bar-fill f" + (part.cls || "1"), x: fixed(at), y: "0",
            width: fixed(length), height: "10" });
          at += length;
          return rect;
        }));
      },

      /* Chips, each its own member's state, then a remainder chip. chips: [{key, label, value, title,
       * className, subject}]. */
      chips: function (chips, remainderMembers, remainderValue) {
        var out = chips.map(function (chip) {
          var attrs = { key: "chip-" + chip.key, "class": "chip" + (chip.className ? " " + chip.className : ""),
            title: chip.title || null };
          if (chip.subject) { attrs = kit.attrs(attrs, kit.pick(chip.subject)); }
          return h("span", attrs, [chip.label, chip.value ? h("span", { "class": "chip-value" }, chip.value) : null]);
        });
        if (remainderMembers) {
          out.push(h("span", { key: "more", "class": "chip chip-more", "data-remainder": String(remainderMembers) }, [
            tp("shape.remainder", remainderMembers),
            remainderValue ? h("span", { "class": "chip-value" }, remainderValue) : null
          ]));
        }
        return h("span", { "class": "chips" }, out);
      },

      /* A feed: entries newest first, each its own kind. entries: [{key, time, kind, what, by,
       * value, subject, className}]. sources: per contributing source, its degraded state as a row
       * of its own. */
      feed: function (columns, entries, options) {
        var settings = options || {};
        var rows = (settings.sources || []).map(function (source) {
          return h("li", { key: "source-" + source.key, "class": "feed-source", "data-source": source.key }, [
            h("span", { "class": "engine" }, source.label),
            h("span", { "class": "what" }, source.node)
          ]);
        });
        entries.forEach(function (entry) {
          var attrs = { key: "entry-" + entry.key, "class": entry.className || null };
          if (entry.subject) { attrs = kit.attrs(attrs, kit.pick(entry.subject)); }
          rows.push(h("li", attrs, [
            h("span", { "class": "time" }, entry.time),
            h("span", { "class": "engine" }, entry.kind),
            h("span", { "class": "what" }, entry.what),
            h("span", { "class": "num" }, entry.by)
          ]));
        });
        return h("div", { "class": "feed-wrap" }, [
          h("div", { "class": "feed-head" }, columns.map(function (column, index) {
            return h("span", { key: "h" + index, "class": column.className || null }, column.label);
          })),
          h("ul", { "class": "feed" }, rows),
          settings.remainder ? kit.remainder(settings.remainder, null, "feed-remainder") : null
        ]);
      },

      /* -------------------------------------------------------------------------------------------
       * Charts.
       * ----------------------------------------------------------------------------------------- */

      /* A line through samples, for a table cell or a card: one mark per sample, nothing smoothed.
       * values: numbers or null (a gap). */
      spark: function (values, cls, options) {
        var settings = options || {};
        var width = settings.width || 120, height = settings.height || 22, inset = 2;
        var numbers = values.filter(function (value) { return value !== null && value !== undefined; });
        if (numbers.length === 0) {
          return h("span", { "class": "spark-placeholder", title: t("chart.no_sample") });
        }
        var min = Math.min.apply(null, numbers), max = Math.max.apply(null, numbers), span = max - min || 1;
        var step = values.length > 1 ? (width - inset * 2) / (values.length - 1) : 0;
        var segments = [], current = [];
        values.forEach(function (value, index) {
          if (value === null || value === undefined) {
            if (current.length) { segments.push(current); current = []; }
            return;
          }
          current.push([inset + index * step, height - inset - (height - inset * 2) * (value - min) / span]);
        });
        if (current.length) { segments.push(current); }
        var children = [];
        segments.forEach(function (points, index) {
          if (points.length > 1 && settings.interpolation !== "points") {
            children.push(h("path", { key: "l" + index, "class": "series-line s" + cls,
              d: settings.interpolation === "smooth" ? smoothPath(points) : linearPath(points),
              "data-sample-count": String(points.length) }));
          }
          children.push(h("g", { key: "d" + index, "class": "sample-dots", "data-sample-count": String(points.length) },
            points.map(function (point, at) {
              return h("circle", { key: String(at), "class": "sample-dot f" + cls, cx: fixed(point[0]), cy: fixed(point[1]),
                r: "1.2" });
            })));
        });
        return h("svg", { "class": "spark", viewBox: "0 0 " + width + " " + height, width: String(width),
          height: String(height), role: "img", "aria-label": t("chart.spark") }, children);
      },

      /* A time-series chart drawn at the card's own size.
       *
       * spec: {
       *   from, to                       the window, epoch seconds;
       *   series: [{key, label, cls, axis ("left"|"right"), render ("line"|"area"|"bars"|"points"),
       *             points: [{from, to, value (number|null), gap (bool)}], measure}];
       *   stacked                        stack the series of an axis, an area separating each;
       *   interpolation                  "linear" (default) | "smooth" | "points";
       *   tooltip                        "single" (each sample its own) | "all_series" (each instant
       *                                  lists the whole stack);
       *   format: {left, right}          the axis formatters;
       *   scale: {left, right}           "linear" (default) | "log": a logarithmic axis, ticked at the
       *                                  powers of ten its values span; nothing stacks on it, and a
       *                                  value at or below zero, which it cannot place, is not drawn
       *                                  and is counted in a note under the chart;
       *   counts                         the values are whole counts: the axis steps by whole
       *                                  numbers;
       *   width, height                  the drawing's size in CSS pixels.
       * }
       * Answers a node: the drawing, its stated aggregation and its labelled gaps. */
      chart: function (spec) {
        var width = Math.max(240, Math.round(spec.width || kit.size.width || 640));
        var height = Math.max(120, Math.round(spec.height || 180));
        var interpolation = spec.interpolation || "linear";
        var formats = spec.format || {};
        var leftFormat = formats.left || function (value) { return format.number(value); };
        var rightFormat = formats.right || leftFormat;
        var series = (spec.series || []).filter(function (one) { return one && one.points; });
        var hasRight = series.some(function (one) { return one.axis === "right"; });
        var padL = 56, padR = hasRight ? 56 : 12, padT = 8, padB = 22;
        var innerW = width - padL - padR, innerH = height - padT - padB;
        var span = Math.max(1, (spec.to || 1) - (spec.from || 0));
        var scales = spec.scale || {};
        var isLog = function (axis) { return scales[axis || "left"] === "log"; };
        /* The values a log axis cannot place: at or below zero. Never drawn, never moved to a made-up
         * floor, and counted under the chart. */
        var notPlaced = 0;

        /* AGGREGATION TO FIT THE PIXELS, STATED. One point per pixel at most: past that, consecutive
         * buckets are folded into one point by their maximum -- a spike survives it -- and the note
         * under the chart says how many buckets each point holds. */
        var bucketCount = series.reduce(function (most, one) { return Math.max(most, one.points.length); }, 0);
        var fold = bucketCount > innerW ? Math.ceil(bucketCount / innerW) : 1;
        function folded(points) {
          if (fold === 1) { return points; }
          var out = [];
          for (var i = 0; i < points.length; i += fold) {
            var group = points.slice(i, i + fold);
            var values = group.map(function (p) { return p.value; }).filter(function (v) { return v !== null && v !== undefined; });
            out.push({ from: group[0].from, to: group[group.length - 1].to,
              value: values.length ? Math.max.apply(null, values) : null,
              gap: group.some(function (p) { return p.gap; }) });
          }
          return out;
        }
        var drawn = series.map(function (one) {
          var copy = {};
          Object.keys(one).forEach(function (name) { copy[name] = one[name]; });
          copy.points = folded(one.points);
          return copy;
        });

        /* Stacking: each axis's stackable series ride on the ones before them. */
        var stackAll = !!spec.stacked;
        var stacksAny = false;
        var running = { left: {}, right: {} };
        drawn.forEach(function (one) {
          var logAxis = isLog(one.axis);
          var stacks = !logAxis && (stackAll || one.render === "area");
          one.lower = [];
          one.upper = [];
          one.points.forEach(function (point, index) {
            var base = stacks ? (running[one.axis || "left"][index] || 0) : 0;
            var value = point.value === null || point.value === undefined ? null : point.value;
            if (logAxis && value !== null && !(value > 0)) {
              notPlaced += 1;
              value = null;
            }
            one.lower.push(stacks ? base : 0);
            one.upper.push(value === null ? null : base + value);
            if (stacks && value !== null) { running[one.axis || "left"][index] = base + value; }
          });
          one.stacked = stacks;
          stacksAny = stacksAny || stacks;
        });
        /* Each axis's domain: from zero to a nice ceiling, or on a log axis the decades its positive
         * values span, whole powers of ten at both ends. */
        function domain(axis) {
          var most = 0, least = null;
          drawn.forEach(function (one) {
            if ((one.axis || "left") !== axis) { return; }
            one.upper.forEach(function (value) {
              if (value === null) { return; }
              if (value > most) { most = value; }
              if (value > 0 && (least === null || value < least)) { least = value; }
            });
          });
          if (!isLog(axis)) {
            /* Whole counts step by whole numbers: the ceiling is a multiple of the four steps. */
            if (spec.counts) { return { log: false, top: Math.max(4, Math.ceil(niceCeil(most) / 4) * 4) }; }
            var formatter = axis === "right" ? rightFormat : leftFormat;
            return { log: false, top: formatter && formatter.axisBase === 1024 ? niceCeil1024(most) : niceCeil(most) };
          }
          var low = least === null ? 0 : Math.floor(log10(least));
          var high = least === null ? 1 : Math.ceil(log10(most));
          if (high <= low) { high = low + 1; }
          return { log: true, low: low, high: high };
        }
        var domains = { left: domain("left"), right: domain("right") };
        /* Where a value sits up an axis, as a fraction of the plot's height. */
        function fraction(value, axis) {
          var d = domains[axis || "left"];
          return d.log ? (log10(value) - d.low) / (d.high - d.low) : value / d.top;
        }
        /* The ticks of an axis: five even steps, or one per power of ten, at most seven. */
        function ticksOf(axis) {
          var d = domains[axis];
          if (!d.log) {
            return [0, 1, 2, 3, 4].map(function (step) { return { at: step / 4, value: d.top * step / 4 }; });
          }
          var stride = Math.max(1, Math.ceil((d.high - d.low) / 6));
          var out = [];
          for (var k = d.low; k <= d.high; k += stride) { out.push({ at: (k - d.low) / (d.high - d.low), value: Math.pow(10, k) }); }
          return out;
        }
        var xAt = function (time) { return padL + innerW * Math.max(0, Math.min(1, (time - spec.from) / span)); };
        var yAt = function (value, axis) { return padT + innerH - innerH * fraction(value, axis); };
        var baseY = padT + innerH;
        var mid = function (point) { return (point.from + point.to) / 2; };

        var children = [];
        /* A tick whose label reads as the one below it says nothing: a small range formatted to whole
         * units would read 0, 1, 1, 2, 2. Only the first of equal labels is drawn. */
        function labelled(ticks, formatter) {
          var last = null;
          return ticks.map(function (tick) {
            var text = formatter(tick.value);
            var shown = typeof text === "string" ? text : JSON.stringify(text, function (key, value) {
              return typeof value === "function" ? undefined : value;
            });
            var repeated = shown === last;
            last = shown;
            return { at: tick.at, text: repeated ? null : text };
          });
        }
        labelled(ticksOf("left"), leftFormat).forEach(function (tick, step) {
          var y = padT + innerH - innerH * tick.at;
          children.push(h("line", { key: "grid-" + step, "class": "grid-line", x1: padL, y1: fixed(y), x2: width - padR, y2: fixed(y) }));
          if (tick.text !== null) {
            children.push(h("text", { key: "tl-" + step, "class": "tick", x: padL - 4, y: fixed(y + 3), "text-anchor": "end" },
              tick.text));
          }
        });
        if (hasRight) {
          labelled(ticksOf("right"), rightFormat).forEach(function (tick, step) {
            var y = padT + innerH - innerH * tick.at;
            if (tick.text === null) { return; }
            children.push(h("text", { key: "tr-" + step, "class": "tick", x: width - padR + 4, y: fixed(y + 3),
              "text-anchor": "start" }, tick.text));
          });
        }
        children.push(h("line", { key: "axis", "class": "axis-line", x1: padL, y1: padT + innerH, x2: width - padR, y2: padT + innerH }));
        /* The time axis is ticked on round instants of the reader's clock, at the smallest interval
         * that leaves room for every label. */
        var tickCount = Math.max(2, Math.min(7, Math.floor(innerW / 80)));
        var interval = TICK_STEPS[TICK_STEPS.length - 1];
        for (var s = 0; s < TICK_STEPS.length; s++) {
          if (span / TICK_STEPS[s] <= tickCount) { interval = TICK_STEPS[s]; break; }
        }
        var offset = -new Date((spec.from || 0) * 1000).getTimezoneOffset() * 60;
        var first = Math.ceil(((spec.from || 0) + offset) / interval) * interval - offset;
        for (var tick = first, index = 0; tick <= spec.to; tick += interval, index++) {
          children.push(h("text", { key: "tx-" + index, "class": "tick", x: fixed(xAt(tick)), y: height - 6,
            "text-anchor": "middle" }, format.tick(tick, span)));
        }

        /* A GAP IS DRAWN AS A LABELLED GAP: the buckets nobody collected, as a hatched band across
         * the plot, named in its title -- never a line at zero, never a straight line across. */
        var gapRuns = [];
        var reference = drawn[0] ? drawn[0].points : [];
        var run = null;
        var byFrom = drawn.map(function (one) {
          var index = {};
          one.points.forEach(function (p) { index[p.from] = p; });
          return index;
        });
        reference.forEach(function (point) {
          var isGap = byFrom.every(function (index) {
            var same = index[point.from];
            return !same || same.gap;
          });
          if (isGap) {
            if (!run) { run = { from: point.from, to: point.to }; } else { run.to = point.to; }
          } else if (run) { gapRuns.push(run); run = null; }
        });
        if (run) { gapRuns.push(run); }
        gapRuns.forEach(function (gap, index) {
          children.push(h("rect", { key: "gap-" + index, "class": "gap-band", "data-gap": "true",
            x: fixed(xAt(gap.from)), y: padT, width: fixed(Math.max(1, xAt(gap.to) - xAt(gap.from))), height: innerH },
            h("title", {}, t("chart.gap", { from: format.instant(gap.from), to: format.instant(gap.to) }))));
        });

        drawn.forEach(function (one, seriesIndex) {
          var cls = one.cls || kit.seriesClass(seriesIndex);
          var axis = one.axis || "left";
          var segments = [], current = [];
          one.points.forEach(function (point, index) {
            if (one.upper[index] === null) {
              if (current.length) { segments.push(current); current = []; }
              return;
            }
            current.push({ x: xAt(mid(point)), y: yAt(one.upper[index], axis),
              low: isLog(axis) ? baseY : yAt(one.lower[index], axis),
              point: point, value: point.value });
          });
          if (current.length) { segments.push(current); }
          var group = [];
          segments.forEach(function (segment, segmentIndex) {
            var points = segment.map(function (p) { return [p.x, p.y]; });
            if (one.stacked) {
              /* An area separates a stacked series from the one under it; it is the band between
               * its lower and upper edges, drawn straight between measured points. */
              var lower = segment.map(function (p) { return [p.x, p.low]; }).reverse();
              group.push(h("path", { key: "area-" + segmentIndex, "class": "area f" + cls,
                d: linearPath(points) + " " + linearPath(lower).replace(/^M/, "L") + " Z",
                "data-sample-count": String(segment.length) }));
            }
            if (one.render === "bars") {
              /* A bar is as wide as its own bucket, never a share of the plot: two buckets of an
               * hour each are two narrow bars, not two halves of the window. */
              group.push(h("g", { key: "bars-" + segmentIndex, "class": "series-bars", "data-sample-count": String(segment.length) },
                segment.map(function (p, at) {
                  var left = xAt(p.point.from), barWidth = Math.max(1, xAt(p.point.to) - left - 1);
                  return h("rect", { key: String(at), "class": "f" + cls, x: fixed(left + 0.5), y: fixed(p.y),
                    width: fixed(barWidth), height: fixed(Math.max(0, p.low - p.y)) });
                })));
            } else if (interpolation !== "points" && one.render !== "points" && segment.length > 1) {
              group.push(h("path", { key: "line-" + segmentIndex, "class": "series-line s" + cls,
                d: interpolation === "smooth" ? smoothPath(points) : linearPath(points),
                "data-sample-count": String(segment.length) }));
            }
            var radius = Math.max(0.9, Math.min(2.2, innerW / Math.max(1, one.points.length) / 4));
            group.push(h("g", { key: "dots-" + segmentIndex, "class": "sample-dots", "data-sample-count": String(segment.length) },
              segment.map(function (p, at) {
                var attrs = { key: String(at), "class": "sample-dot f" + cls, cx: fixed(p.x), cy: fixed(p.y), r: fixed(radius) };
                if (spec.tooltip !== "all_series") {
                  attrs = kit.attrs(attrs, kit.tip(function () {
                    return [
                      h("div", { "class": "tt-head" }, format.instant(p.point.from)),
                      h("div", { "class": "tt-row" }, [h("span", {}, one.label), h("span", {},
                        (axis === "right" ? rightFormat : leftFormat)(p.value))])
                    ];
                  }));
                }
                return h("circle", attrs);
              })));
          });
          children.push(h("g", { key: "series-" + one.key, "class": "series", "data-series": one.key,
            "data-measure": one.measure || null }, group));
        });

        /* An all_series tooltip: each instant a column whose tooltip lists the whole stack. */
        if (spec.tooltip === "all_series" && drawn[0]) {
          var columns = drawn[0].points.map(function (point, index) {
            var rows = function () { return drawn.map(function (one) {
              var match = one.points[index];
              var value = match ? match.value : null;
              return h("div", { key: one.key, "class": "tt-row" }, [
                h("span", {}, [h("span", { "class": "swatch sw-" + (one.cls || "1") }), one.label]),
                h("span", {}, value === null || value === undefined ? t("figure.dash")
                  : ((one.axis || "left") === "right" ? rightFormat : leftFormat)(value))
              ]);
            }); };
            var x0 = xAt(point.from), x1 = xAt(point.to);
            return h("rect", kit.attrs({ key: String(index), "class": "hit-column", x: fixed(x0), y: padT,
              width: fixed(Math.max(1, x1 - x0)), height: innerH },
              kit.tip(function () { return [h("div", { "class": "tt-head" }, format.instant(point.from))].concat(rows()); },
                "instant-" + index)));
          });
          children.push(h("g", { key: "hits", "class": "hit-columns" }, columns));
        }

        var notes = [];
        if (fold > 1) {
          notes.push(kit.note(t("chart.aggregated", { count: format.number(fold) }), "aggregated"));
        }
        if (notPlaced > 0) {
          notes.push(kit.note(tp("chart.log_not_placed", notPlaced), "log-not-placed"));
        }
        return h("div", { "class": "chart-wrap" }, [
          h("svg", { key: "svg", "class": "chart-svg", viewBox: "0 0 " + width + " " + height, width: String(width),
            height: String(height), role: "img", "aria-label": spec.label || t("chart.label"),
            "data-scale-left": isLog("left") ? "log" : "linear", "data-scale-right": isLog("right") ? "log" : "linear",
            "data-stacked": stacksAny ? "true" : "false" }, children),
          notes
        ]);
      },

      /* -------------------------------------------------------------------------------------------
       * A ring: parts of a whole.
       * ----------------------------------------------------------------------------------------- */

      /* slices: [{key, label, value, cls, subject}]; options: {total, centreValue, centreLabel,
       * label}. A ring closes on its own whole; a total of zero draws an empty ring. */
      donut: function (slices, options) {
        var settings = options || {};
        var R = 54, r = 33, cx = 62, cy = 62, start = -Math.PI / 2;
        var total = settings.total !== undefined ? settings.total
          : slices.reduce(function (sum, slice) { return sum + (slice.value || 0); }, 0);
        var paths = [];
        if (!(total > 0)) {
          paths.push(h("circle", { key: "empty", "class": "donut-empty", cx: "62", cy: "62", r: String((R + r) / 2) }));
        }
        slices.forEach(function (slice) {
          var fraction = total > 0 ? (slice.value || 0) / total : 0;
          if (fraction <= 0) { return; }
          var end = start + fraction * Math.PI * 2;
          if (fraction > 0.9999) { end = start + Math.PI * 1.9999; }
          var large = end - start > Math.PI ? 1 : 0;
          var d = "M" + fixed(cx + R * Math.cos(start)) + " " + fixed(cy + R * Math.sin(start)) +
            " A" + R + " " + R + " 0 " + large + " 1 " + fixed(cx + R * Math.cos(end)) + " " + fixed(cy + R * Math.sin(end)) +
            " L" + fixed(cx + r * Math.cos(end)) + " " + fixed(cy + r * Math.sin(end)) +
            " A" + r + " " + r + " 0 " + large + " 0 " + fixed(cx + r * Math.cos(start)) + " " + fixed(cy + r * Math.sin(start)) + " Z";
          var attrs = { key: "slice-" + slice.key, "class": "donut-slice f" + (slice.cls || "1"), d: d,
            "data-slice": slice.key, "data-value": String(slice.value) };
          if (slice.subject) { attrs = kit.attrs(attrs, kit.pick(slice.subject)); }
          paths.push(h("path", attrs));
          start = end;
        });
        return h("div", { "class": "donut" }, [
          h("svg", { viewBox: "0 0 124 124", width: "124", height: "124", role: "group",
            "aria-label": settings.label || t("chart.ring") }, paths),
          h("div", { "class": "donut-centre" }, [
            h("span", { "class": "donut-centre-value" }, settings.centreValue || ""),
            h("span", { "class": "donut-centre-label" }, settings.centreLabel || "")
          ])
        ]);
      },

      /* -------------------------------------------------------------------------------------------
       * A map: point marks on the embedded basemap.
       * ----------------------------------------------------------------------------------------- */

      /* points: [{key, latitude, longitude, size, cls, subject, title}]; options: {cluster, label}.
       * No choropleth and no arcs: the land is background, the marks are the data. */
      map: function (points, options) {
        var settings = options || {};
        var basemap = opnview.basemap || { width: 720, height: 360, countries: [] };
        var W = basemap.width, H = basemap.height;
        var project = function (lat, lon) { return [(lon + 180) * W / 360, (90 - lat) * H / 180]; };
        var marks = points.filter(function (point) {
          return typeof point.latitude === "number" && typeof point.longitude === "number";
        });
        if (settings.cluster) {
          /* Marks closer than one cell merge into one, carrying the merged count inside it. */
          var cells = {}, order = [];
          marks.forEach(function (point) {
            var at = project(point.latitude, point.longitude);
            var cell = Math.floor(at[0] / 24) + ":" + Math.floor(at[1] / 24);
            if (!cells[cell]) {
              cells[cell] = { key: "cluster-" + cell, x: 0, y: 0, size: 0, members: [], cls: point.cls };
              order.push(cell);
            }
            var c = cells[cell];
            c.members.push(point);
            c.x += at[0];
            c.y += at[1];
            c.size += point.size || 0;
          });
          marks = order.map(function (cell) {
            var c = cells[cell];
            return { key: c.key, x: c.x / c.members.length, y: c.y / c.members.length, size: c.size,
              cls: c.cls, members: c.members,
              subject: c.members.length === 1 ? c.members[0].subject : null };
          });
        } else {
          marks = marks.map(function (point) {
            var at = project(point.latitude, point.longitude);
            return { key: point.key, x: at[0], y: at[1], size: point.size || 0, cls: point.cls, members: [point],
              subject: point.subject };
          });
        }
        var largest = marks.reduce(function (most, mark) { return Math.max(most, mark.size); }, 0) || 1;
        var land = basemap.countries.map(function (path, index) {
          return h("path", { key: "c" + index, "class": "map-country", d: path });
        });
        /* The graticule of the mockup: every thirty degrees, under the land. */
        var graticule = [];
        for (var lon = -150; lon <= 150; lon += 30) {
          graticule.push(h("line", { key: "lon" + lon, "class": "map-graticule", x1: fixed((lon + 180) * W / 360), y1: 0,
            x2: fixed((lon + 180) * W / 360), y2: H }));
        }
        for (var lat = -60; lat <= 60; lat += 30) {
          graticule.push(h("line", { key: "lat" + lat, "class": "map-graticule", x1: 0, y1: fixed((90 - lat) * H / 180),
            x2: W, y2: fixed((90 - lat) * H / 180) }));
        }
        var drawnMarks = marks.map(function (mark) {
          var radius = 2.5 + 9 * Math.sqrt(mark.size / largest);
          var attrs = { key: mark.key, "class": "map-mark map-mark-" + (mark.cls || "allowed"),
            cx: fixed(mark.x), cy: fixed(mark.y), r: fixed(radius), "data-size": String(mark.size),
            "data-members": String(mark.members.length) };
          if (mark.subject) {
            attrs = kit.attrs(attrs, kit.pick(mark.subject));
          } else if (mark.members.length > 1) {
            attrs = kit.attrs(attrs, kit.pick(kit.subject("cluster", mark.key, tp("map.cluster", mark.members.length),
              [], { related: mark.members.map(function (member) { return member.subject; }).filter(Boolean) })));
          }
          /* A MARK IS PLACED, NOT SCALED (the mockup's view on the basemap): it sits at its point in
           * a group the canvas translates with the view, drawn at the origin, so zooming separates
           * marks instead of magnifying them. */
          attrs.cx = "0";
          attrs.cy = "0";
          var children = [h("circle", attrs)];
          if (mark.members.length > 1) {
            children.push(h("text", { key: "n", "class": "map-count", x: "0", y: "3",
              "text-anchor": "middle" }, format.number(mark.members.length)));
          }
          return h("g", { key: mark.key, "class": "map-mark-group map-mark-at", "data-x": fixed(mark.x), "data-y": fixed(mark.y),
            transform: "translate(" + fixed(mark.x) + " " + fixed(mark.y) + ")" }, children);
        });
        /* The view -- a scale and an offset -- is the canvas's, kept per card across refreshes and
         * written onto the drawing after each render (front/views/canvas.js, "The map's view"): the
         * wheel zooms about the pointer, a drag pans, and the stacked pair and the reset in the corner
         * do the same from a click or the keyboard. */
        return h("div", { "class": "map-zoomable" }, [
          h("svg", { key: "svg", "class": "map-svg", viewBox: "0 0 " + W + " " + H, role: "group", tabindex: "0",
            "data-map-svg": prefix, "aria-label": settings.label || t("map.label") }, [
            h("g", { key: "pan", "class": "map-pan", "aria-hidden": "true", transform: "translate(0 0) scale(1)" }, [
              h("g", { key: "graticule", "class": "map-graticules" }, graticule),
              h("g", { key: "land", "class": "map-land" }, land)
            ]),
            h("g", { key: "marks", "class": "map-marks" }, drawnMarks)
          ]),
          h("div", { key: "controls", "class": "map-controls" }, [
            h("button", { key: "in", type: "button", "data-map-zoom": "in", "data-map-for": prefix,
              "aria-label": t("map.zoom_in"), title: t("map.zoom_in") }, t("map.zoom_in_face")),
            h("button", { key: "out", type: "button", "data-map-zoom": "out", "data-map-for": prefix,
              "aria-label": t("map.zoom_out"), title: t("map.zoom_out") }, t("map.zoom_out_face")),
            h("button", { key: "reset", type: "button", "data-map-zoom": "reset", "data-map-for": prefix,
              "aria-label": t("map.zoom_reset"), title: t("map.zoom_reset") },
              h("svg", { viewBox: "0 0 12 12", width: "11", height: "11", "aria-hidden": "true", focusable: "false" },
                h("path", { d: "M1.5 4V1.5H4M8 1.5h2.5V4M10.5 8v2.5H8M4 10.5H1.5V8", fill: "none", stroke: "currentColor",
                  "stroke-width": "1.4" })))
          ])
        ]);
      },

      /* -------------------------------------------------------------------------------------------
       * A matrix: a figure at row x column.
       * ----------------------------------------------------------------------------------------- */

      /* rows: [{key, label}]; columns: [{key, label}]; cell(rowKey, columnKey) answers {node,
       * subject, className} or null for a pair the answer did not produce (absent, never 0). A row
       * with no pair keeps its row. */
      matrix: function (rows, columns, cell, options) {
        var settings = options || {};
        return h("div", { "class": "matrix-scroll" }, h("table", { "class": "matrix" }, [
          h("thead", {}, h("tr", {}, [h("th", { key: "corner", "class": "corner" }, settings.corner || "")]
            .concat(columns.map(function (column) {
              return h("th", { key: column.key, "class": "colhead", scope: "col" }, column.label);
            })))),
          h("tbody", {}, rows.map(function (row) {
            return h("tr", { key: row.key, "data-row": row.key }, [h("th", { key: "head", "class": "rowhead", scope: "row" }, row.label)]
              .concat(columns.map(function (column) {
                var found = cell(row.key, column.key);
                if (!found) {
                  return h("td", { key: column.key, "class": "cell empty-cell", "data-absent": "true" }, t("figure.dash"));
                }
                var attrs = { key: column.key, "class": "cell" + (found.className ? " " + found.className : ""),
                  "data-cell": row.key + "|" + column.key };
                if (found.subject) { attrs = kit.attrs(attrs, kit.pick(found.subject)); }
                return h("td", attrs, found.node);
              })));
          }))
        ]));
      },

      /* -------------------------------------------------------------------------------------------
       * A Sankey: flows between two sides.
       * ----------------------------------------------------------------------------------------- */

      /* spec: {ribbons: [{left, right, value, blockedShare}], label(key, side), subject(key, side),
       * unit, field, other (number|null), total (figure|null)}. Every end is labelled with its name;
       * the Other band carries its own figure; what was drawn, the Other and the total are stated
       * under the drawing. */
      sankey: function (spec) {
        var lefts = [], rights = [], leftTotal = {}, rightTotal = {};
        var label = spec.label || function (key) { return key; };
        spec.ribbons.forEach(function (ribbon) {
          if (leftTotal[ribbon.left] === undefined) { lefts.push(ribbon.left); leftTotal[ribbon.left] = 0; }
          if (rightTotal[ribbon.right] === undefined) { rights.push(ribbon.right); rightTotal[ribbon.right] = 0; }
          leftTotal[ribbon.left] += ribbon.value;
          rightTotal[ribbon.right] += ribbon.value;
        });
        var width = Math.max(320, Math.round(kit.size.width || 640));
        var gutter = Math.min(180, Math.round(width * 0.28));
        var nodeW = 10, leftX = gutter, rightX = width - gutter - nodeW;
        var rows = Math.max(lefts.length, rights.length, 1);
        var height = Math.max(160, Math.min(560, 28 * rows + 20));
        var sum = function (keys, totals) { return keys.reduce(function (s, k) { return s + totals[k]; }, 0); };
        var scale = (height - 20 - 6 * rows) / Math.max(1, sum(lefts, leftTotal), sum(rights, rightTotal));
        var leftY = {}, rightY = {}, y = 10;
        lefts.forEach(function (k) { leftY[k] = y; y += leftTotal[k] * scale + 6; });
        y = 10;
        rights.forEach(function (k) { rightY[k] = y; y += rightTotal[k] * scale + 6; });
        var cursorL = {}, cursorR = {};
        lefts.forEach(function (k) { cursorL[k] = leftY[k]; });
        rights.forEach(function (k) { cursorR[k] = rightY[k]; });
        var children = spec.ribbons.map(function (ribbon, index) {
          var thick = ribbon.value * scale;
          var y0 = cursorL[ribbon.left], y1 = cursorR[ribbon.right];
          cursorL[ribbon.left] += thick;
          cursorR[ribbon.right] += thick;
          var x0 = leftX + nodeW, mx = (x0 + rightX) / 2;
          var d = "M" + x0 + " " + fixed(y0) + " C" + fixed(mx) + " " + fixed(y0) + " " + fixed(mx) + " " + fixed(y1) + " " +
            rightX + " " + fixed(y1) + " L" + rightX + " " + fixed(y1 + thick) + " C" + fixed(mx) + " " + fixed(y1 + thick) + " " +
            fixed(mx) + " " + fixed(y0 + thick) + " " + x0 + " " + fixed(y0 + thick) + " Z";
          var rows = [h("div", { key: "head", "class": "tt-head" }, label(ribbon.left, "left") + " → " + label(ribbon.right, "right")),
            h("div", { key: "value", "class": "tt-row" }, kit.text(ribbon.value, spec.unit))];
          if (ribbon.blockedShare !== undefined && ribbon.blockedShare !== null) {
            rows.push(h("div", { key: "blocked", "class": "tt-row" }, t("sankey.blocked_share", { share: format.percent(ribbon.blockedShare) })));
          }
          /* A ribbon takes the series colour of the node it leaves, so the flows of one source read
           * as one hue across the drawing; picking it opens the pair, with both ends to go on to. */
          var ends = spec.subject ? [spec.subject(ribbon.left, "left"), spec.subject(ribbon.right, "right")].filter(Boolean) : [];
          var pair = kit.subject("flow", ribbon.left + "|" + ribbon.right, label(ribbon.left, "left") + " → " + label(ribbon.right, "right"),
            [[t("detail.fact.value"), kit.text(ribbon.value, spec.unit)]].concat(ribbon.blockedShare !== undefined && ribbon.blockedShare !== null
              ? [[t("column.blocked"), format.percent(ribbon.blockedShare)]] : []), { related: ends });
          return h("path", kit.attrs({ key: "r" + index, "class": "sankey-ribbon f" + kit.seriesClass(lefts.indexOf(ribbon.left)) +
            (ribbon.blockedShare > 0 ? " has-blocked" : "") +
            (ribbon.other ? " sankey-ribbon-other" : ""), d: d, "data-left": ribbon.left, "data-right": ribbon.right,
            "data-value": String(ribbon.value) }, kit.tip(rows, "ribbon-" + index), kit.pick(pair)));
        });
        function side(keys, totals, positions, x, anchor, name) {
          keys.forEach(function (key) {
            var subject = spec.subject ? spec.subject(key, name) : null;
            subject = subject || kit.subject("node", name + ":" + key, label(key, name),
              [[t("detail.fact.value"), kit.text(totals[key], spec.unit)]]);
            children.push(h("rect", kit.attrs({ key: name + "-node-" + key, "class": "sankey-node", x: x, y: fixed(positions[key]),
              width: nodeW, height: fixed(Math.max(1, totals[key] * scale)), "data-node": key, "data-side": name,
              "data-value": String(totals[key]) }, kit.pick(subject))));
            children.push(h("text", { key: name + "-label-" + key, x: anchor === "end" ? x - 5 : x + nodeW + 5,
              y: fixed(positions[key] + totals[key] * scale / 2 + 3), "text-anchor": anchor }, label(key, name)));
          });
        }
        side(lefts, leftTotal, leftY, leftX, "end", "left");
        side(rights, rightTotal, rightY, rightX, "start", "right");
        var summary = [{ key: "drawn", cells: [t("sankey.drawn"), kit.text(sum(lefts, leftTotal), spec.unit)] }];
        if (spec.other !== null && spec.other !== undefined) {
          summary.push({ key: "other", cells: [t("sankey.other"), kit.text(spec.other, spec.unit)] });
        }
        return h("div", { "class": "shape-sankey" }, [
          h("svg", { key: "svg", "class": "sankey-svg", viewBox: "0 0 " + width + " " + height, width: String(width),
            height: String(height), role: "group", "aria-label": t("sankey.label") }, children),
          kit.table([{ key: "what", label: t("sankey.column.what") }, { key: "value", label: t("sankey.column.value"), num: true }],
            summary, { total: spec.total ? { label: t("shape.total"), cells: [kit.value(spec.total, spec.unit, { field: spec.field })] } : null })
        ]);
      },

      /* -------------------------------------------------------------------------------------------
       * Text width, for a drawing that places labels: an approximation in the system font, the same
       * in the browser and in the tests.
       * ----------------------------------------------------------------------------------------- */
      textWidth: function (text, pixels) {
        return String(text).length * (pixels || 11) * 0.56;
      }
    };
    return kit;
  }

  opnview.kit = { create: create, niceCeil: niceCeil, linearPath: linearPath, smoothPath: smoothPath };
})(this);
