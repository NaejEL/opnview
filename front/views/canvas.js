/*
 * opnview front -- the canvases (F7-17 to F7-28).
 *
 * TABS, NOT MENUS. One tab per canvas of every dashboard, in dashboard order then canvas order, one
 * click each, grouped under the dashboard titles in the tab bar itself when more than one dashboard
 * exists. The root path opens the first canvas; the address bar names the dashboard and the canvas,
 * so a reload returns to the same tab.
 *
 * THE CANVAS BAR holds the tabs, the canvas's range (`period`) and its scope -- interface, client,
 * person -- with dropdowns of the candidates GET /api/references/{kind} lists, and the undo of the
 * canvas's edits. Each writes the canvas's own fields in the document (front/core/canvasmodel.js).
 *
 * THE GRID is the mockup's: 12, 8 or 1 columns from the canvas container's width, every card floated
 * upward (front/core/grid.js); the derived layout is never written back.
 *
 * A CARD is a title and a shape. Its title is the instance's, else the catalogue's through strings,
 * with no explanation line. Its header carries chips -- own range, own scope, not scoped, stale, each
 * named state, kept and not understood, the measure of its volumes, cut off at this size -- and a
 * configure control named by the card's heading. Its body is its renderer's drawing, or a designed
 * panel naming why there is nothing to draw, which keeps the card's footprint and title and links to
 * the Sources view: never a zero, never a blank, never a red failure.
 *
 * A REFRESH NEVER REPLACES A CARD. Answers arrive, the cards are drawn again, and the reconciler
 * (front/core/view.js) patches what changed; an open detail, an expanded row, a tooltip, a focused
 * mark, a selection and the scroll position survive it.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk, tp = opnview.strings.tp, td = opnview.strings.td;
  var model = opnview.canvasModel;

  /* The conditions a card draws as a panel rather than as its shape. */
  var PANEL_CONDITIONS = ["not_yet_probed", "unavailable", "present_but_disabled", "state_unknown", "reachable_no_rows",
    "unresolved_reference", "missing_required_parameter", "withheld_by_aggregate_mode", "no_owner_exists",
    "unknown_widget_type", "script_failed", "read_failed", "renderer_failed", "malformed_parameters",
    "shape_not_served", "front_only_request_refused"];
  /* The panels' kinds, which choose their mark: a source that cannot answer, one switched off, a
   * reference this installation does not have, and a state of the model. */
  var PANEL_KIND = {
    not_yet_probed: "unavailable", unavailable: "unavailable", state_unknown: "unavailable",
    present_but_disabled: "disabled", reachable_no_rows: "silent", unresolved_reference: "unresolved",
    unknown_widget_type: "unresolved", shape_not_served: "unresolved"
  };
  var REFERENCE_KINDS = ["interface", "client", "owner"];

  /* The CSS pixels of the grid's row unit and gap, and of a card's chrome around its body. */
  var ROW_HEIGHT = 40, GAP = 16, CARD_PAD_X = 32, CARD_CHROME_Y = 44;

  function canvasPath(dashboard, canvas) {
    return "/canvas/" + encodeURIComponent(dashboard.id) + "/" + encodeURIComponent(canvas.id);
  }

  function hidden() {
    return !!(root.document && root.document.visibilityState === "hidden");
  }

  /* ---------------------------------------------------------------------------------------------
   * Loading.
   * ------------------------------------------------------------------------------------------- */

  function load(context) {
    var state = context.state;
    if (state.scheduler) { state.scheduler.stop(); }
    return Promise.all([context.reloadDashboards(), opnview.widgets.load(context.api)]).then(function (loaded) {
      var dashboards = loaded[0] || [];
      var wanted = context.params;
      var first = null, chosen = null;
      dashboards.forEach(function (dashboard) {
        (dashboard.canvases || []).forEach(function (canvas) {
          if (!first) { first = { dashboard: dashboard, canvas: canvas }; }
          if (String(dashboard.id) === wanted.dashboard && canvas.id === wanted.canvas) {
            chosen = { dashboard: dashboard, canvas: canvas };
          }
        });
      });
      state.chosen = chosen || (wanted.dashboard ? null : first);
      state.missing = !!wanted.dashboard && !chosen;
      state.loaded = true;
      state.answers = state.answers || {};
      state.sizes = state.sizes || {};
      state.drawings = state.drawings || {};
      if (!state.chosen) { return null; }
      return context.api.get("/api/dashboards/" + encodeURIComponent(state.chosen.dashboard.id)).then(function (result) {
        if (result.status !== 200 || !result.body) {
          state.document = null;
          return null;
        }
        state.document = result.body.document;
        state.editor = model.editor({ api: context.api, id: result.body.id, revision: result.body.revision,
          document: result.body.document, onChange: function (document) { state.document = document; } });
        start(context);
        loadReferences(context);
        return null;
      });
    });
  }

  /* The candidates of the three scope kinds, for the canvas bar's dropdowns. */
  function loadReferences(context) {
    var state = context.state;
    if (state.references) { return; }
    state.references = {};
    REFERENCE_KINDS.forEach(function (kind) {
      context.api.get("/api/references/" + kind).then(function (result) {
        state.references[kind] = result.status === 200 && result.body ? result.body : { candidates: [] };
        context.rerender();
      });
    });
  }

  /* ---------------------------------------------------------------------------------------------
   * The widgets of the canvas on screen, their requests and their refresh.
   * ------------------------------------------------------------------------------------------- */

  function currentCanvas(state) {
    if (!state.document || !state.chosen) { return null; }
    return model.canvasIn(state.document, state.chosen.canvas.id);
  }

  function widgetsOf(canvas) {
    return canvas && Array.isArray(canvas.widgets) ? canvas.widgets.filter(function (widget) {
      return widget && typeof widget === "object";
    }) : [];
  }

  function findWidget(state, id) {
    var found = null;
    widgetsOf(currentCanvas(state)).forEach(function (widget) { if (widget.id === id) { found = widget; } });
    return found;
  }

  /* Starts the canvas: every widget asked once, then on its schedule. */
  function start(context) {
    var state = context.state;
    if (state.scheduler) { state.scheduler.stop(); }
    state.scheduler = opnview.scheduler.create({
      hidden: hidden,
      fetch: function (id) { fetchWidget(context, id); }
    });
    state.generation = (state.generation || 0) + 1;
    widgetsOf(currentCanvas(state)).forEach(function (widget) { fetchWidget(context, widget.id); });
  }

  /* Asks for one widget's answer, through the widget API or, for a front-only widget, through the
   * reads its renderer names. */
  function fetchWidget(context, id) {
    var state = context.state;
    var generation = state.generation;
    var widget = findWidget(state, id);
    if (!widget) { return; }
    var row = widget.type ? opnview.widgets.row(widget.type) : null;
    if (!row) {
      state.answers[id] = { condition: widget.type ? "unknown_widget_type" : "shape_not_served" };
      schedule(context);
      return;
    }
    var built = model.request(row, widget, currentCanvas(state));
    var settle = function (record) {
      if (state.generation !== generation) { return; }
      record.notes = built.notes;
      record.parameters = built.parameters;
      state.answers[id] = record;
      if (record.answer) { state.scheduler.track(id, record.answer); }
      else { state.scheduler.track(id, null); }
      schedule(context);
    };
    if (row.backend === false) {
      fetchFrontOnly(context, row, built.parameters).then(settle);
      return;
    }
    context.api.get("/api/widget/" + encodeURIComponent(row.type) + model.query(built.parameters)).then(function (result) {
      if (result.status === 200 && result.body) {
        settle({ answer: result.body, condition: result.body.condition });
      } else {
        var condition = result.body && result.body.condition ? result.body.condition : "read_failed";
        settle({ condition: condition, failure: result.body });
      }
    }, function () { settle({ condition: "read_failed" }); });
  }

  function fetchFrontOnly(context, row, parameters) {
    var renderer = opnview.widgets.renderer(row.type, null);
    var paths;
    try { paths = renderer && renderer.requests ? renderer.requests({ parameters: parameters }) : []; }
    catch (e) { return Promise.resolve({ condition: "renderer_failed" }); }
    if (!Array.isArray(paths) || !paths.every(opnview.widgets.allowedRead)) {
      return Promise.resolve({ condition: "front_only_request_refused" });
    }
    return Promise.all(paths.map(function (path) {
      return context.api.get(path).then(function (result) { return { path: path, status: result.status, body: result.body }; });
    })).then(function (answers) {
      return { answer: { type: row.type, parameters: parameters, condition: "reachable", answers: answers, data: answers },
        condition: "reachable" };
    }, function () { return { condition: "read_failed" }; });
  }

  /* Answers arriving together are drawn once. */
  function schedule(context) {
    var state = context.state;
    if (state.drawPending) { return; }
    state.drawPending = true;
    root.setTimeout(function () {
      state.drawPending = false;
      context.refresh();
    }, 0);
  }

  /* ---------------------------------------------------------------------------------------------
   * Edits of the canvas: the range, the scope, and the two pivots that edit. Each is undoable.
   * ------------------------------------------------------------------------------------------- */

  function edit(context, mutate) {
    var state = context.state;
    if (!state.editor) { return Promise.resolve(); }
    var canvasId = state.chosen.canvas.id;
    return state.editor.edit(function (document) {
      var canvas = model.canvasIn(document, canvasId);
      if (canvas) { mutate(canvas, document); }
    }).then(function (outcome) {
      state.editFailed = outcome.ok ? null : (outcome.condition || "read_failed");
      if (outcome.ok && !outcome.unchanged) { start(context); }
      context.rerender();
    });
  }

  function undo(context) {
    var state = context.state;
    if (!state.editor || !state.editor.canUndo()) { return Promise.resolve(); }
    return state.editor.undo().then(function (outcome) {
      state.editFailed = outcome.ok ? null : (outcome.condition || "read_failed");
      if (outcome.ok) { start(context); }
      context.rerender();
    });
  }

  function setPeriod(context, period) {
    return edit(context, function (canvas) {
      canvas.period = period;
    });
  }

  function setScope(context, kind, references) {
    var key = kind + "s";
    return edit(context, function (canvas) {
      var scope = canvas.scope && typeof canvas.scope === "object" ? canvas.scope : {};
      if (references && references.length) { scope[key] = references; } else { delete scope[key]; }
      if (Object.keys(scope).length) { canvas.scope = scope; } else { delete canvas.scope; }
    });
  }

  function addWidgetAbout(context, subject) {
    var about = subject.about;
    return edit(context, function (canvas) {
      if (!Array.isArray(canvas.widgets)) { canvas.widgets = []; }
      canvas.widgets.push({ id: model.freshWidgetId(canvas, about.type), type: about.type,
        placement: { x: 0, y: model.bottomRow(canvas), w: 6, h: 8 }, parameters: JSON.parse(JSON.stringify(about.parameters)) });
    });
  }

  /* ---------------------------------------------------------------------------------------------
   * The canvas bar.
   * ------------------------------------------------------------------------------------------- */

  function tabs(context) {
    var dashboards = context.dashboards();
    var chosen = context.state.chosen;
    var grouped = dashboards.length > 1;
    return h("div", { key: "tabs", "class": "tabs", role: "tablist", "aria-label": t("canvas.tabs.label") },
      dashboards.map(function (dashboard) {
        var links = (dashboard.canvases || []).map(function (canvas) {
          var selected = !!chosen && chosen.dashboard.id === dashboard.id && chosen.canvas.id === canvas.id;
          var path = canvasPath(dashboard, canvas);
          return h("a", { key: "tab-" + dashboard.id + "-" + canvas.id, "class": "tab", role: "tab", href: path,
            "data-route": path, "aria-selected": selected ? "true" : "false" }, canvas.title || canvas.id);
        });
        if (!grouped) { return links; }
        return h("div", { key: "group-" + dashboard.id, "class": "tab-group", role: "group",
          "aria-label": dashboard.title || String(dashboard.id) }, [
          h("span", { "class": "tab-group-title" }, dashboard.title || String(dashboard.id)),
          links
        ]);
      }));
  }

  /* The reference a scope dropdown writes for a candidate: by the first key the candidate carries,
   * in the order of the kind's accepted keys. */
  function referenceFor(kind, list, candidate) {
    var by = (list.by || []).filter(function (key) {
      return candidate.keys && candidate.keys[key] !== null && candidate.keys[key] !== undefined;
    })[0];
    return by ? { kind: kind, by: by, value: candidate.keys[by], label: candidate.label } : null;
  }

  function matchesCandidate(reference, candidate) {
    return !!reference && !!candidate.keys && candidate.keys[reference.by] === reference.value;
  }

  function scopeControl(context, kind) {
    var state = context.state;
    var list = (state.references || {})[kind] || { candidates: [], by: [] };
    var canvas = currentCanvas(state);
    var current = model.canvasScope(canvas)[kind];
    var options = [h("option", { key: "all", value: "", selected: current.length === 0 }, tk("scope.all.", kind))];
    var selected = current.length === 0 ? "" : null;
    if (current.length > 1) {
      options.push(h("option", { key: "several", value: "several", selected: true },
        tp("scope.several", current.length)));
      selected = "several";
    }
    (list.candidates || []).forEach(function (candidate, index) {
      var isCurrent = current.length === 1 && matchesCandidate(current[0], candidate);
      if (isCurrent) { selected = String(index); }
      options.push(h("option", { key: "c" + index, value: String(index), selected: isCurrent,
        disabled: candidate.referenceable === false }, candidate.label));
    });
    if (selected === null && current.length === 1) {
      options.push(h("option", { key: "unresolved", value: "unresolved", selected: true },
        t("scope.not_found", { name: String(current[0].label || current[0].value) })));
      selected = "unresolved";
    }
    var id = "scope-" + kind;
    return h("div", { key: id, "class": "control-group scope-control", "data-scope-kind": kind }, [
      h("label", { "for": id }, tk("scope.kind.", kind)),
      h("select", { id: id, "class": "btn", value: selected || "", on: { change: function (event) {
        var value = event.target.value;
        if (value === "" ) { setScope(context, kind, []); return; }
        var candidate = (list.candidates || [])[Number(value)];
        var reference = candidate ? referenceFor(kind, list, candidate) : null;
        if (reference) { setScope(context, kind, [reference]); }
      } } }, options)
    ]);
  }

  function bar(context) {
    var state = context.state;
    var canvas = currentCanvas(state);
    var controls = [];
    if (canvas) {
      var period = model.canvasPeriod(canvas);
      controls.push(h("div", { key: "period", "class": "control-group period-control" }, [
        h("label", { "for": "period-select" }, t("canvas.range")),
        h("select", { id: "period-select", "class": "btn", value: period,
          on: { change: function (event) { setPeriod(context, event.target.value); } } },
          model.PERIODS.map(function (value) {
            return h("option", { key: value, value: value, selected: value === period }, tk("period.", value));
          }))
      ]));
      REFERENCE_KINDS.forEach(function (kind) { controls.push(scopeControl(context, kind)); });
      controls.push(h("button", { key: "undo", type: "button", "class": "btn", id: "canvas-undo",
        disabled: !(state.editor && state.editor.canUndo()), on: { click: function () { undo(context); } } }, t("action.undo")));
      if (state.editFailed) {
        controls.push(h("span", { key: "failed", "class": "field-error", role: "status" }, tk("error.", state.editFailed)));
      }
    }
    return h("div", { key: "bar", "class": "canvas-bar" }, [
      tabs(context),
      controls.length ? h("div", { key: "controls", "class": "canvas-controls" }, controls) : null
    ]);
  }

  /* ---------------------------------------------------------------------------------------------
   * Cards.
   * ------------------------------------------------------------------------------------------- */

  function titleOf(widget, row) {
    if (typeof widget.title === "string" && widget.title) { return widget.title; }
    if (row && row.title_key && opnview.strings.has(row.title_key)) { return td(row.title_key); }
    return widget.type || widget.shape || widget.id;
  }

  /* The size a card's body is drawn at: measured once laid out, estimated before. */
  function sizeOf(state, widget, cell, columns) {
    var measured = state.sizes[widget.id];
    if (measured) { return measured; }
    var width = state.canvasWidth || 1200;
    var columnWidth = (width - (columns - 1) * GAP) / columns;
    var span = cell ? cell.span : columns;
    var rows = cell ? cell.rows : 6;
    return { width: Math.max(160, Math.round(span * columnWidth + (span - 1) * GAP - CARD_PAD_X)),
      height: Math.max(80, Math.round(rows * ROW_HEIGHT + (rows - 1) * GAP - CARD_CHROME_Y)) };
  }

  /* A designed panel for a condition: the condition named in a word or two, what it concerns, and
   * a link to the Sources view. */
  function panel(condition, record) {
    var kind = PANEL_KIND[condition] || "gap";
    var details = [];
    var answer = record && record.answer;
    if (condition === "unresolved_reference" && answer) {
      (answer.unresolved_references || []).forEach(function (reference, index) {
        details.push(h("p", { key: "ref-" + index, "class": "state-detail mono" },
          t("state.unresolved_reference", { kind: String(reference.kind), by: String(reference.by),
            value: String(reference.value), label: reference.label === null || reference.label === undefined ? "" : String(reference.label) })));
      });
    }
    if (condition === "missing_required_parameter" && answer) {
      (answer.missing_parameters || []).forEach(function (key) {
        details.push(h("p", { key: "missing-" + key, "class": "state-detail mono" }, key));
      });
    }
    return h("div", { key: "panel", "class": "state state-" + kind, "data-condition": condition }, [
      h("span", { "class": "state-badge" }, tk("condition.", condition)),
      details,
      h("a", { "class": "link-button", href: "/sources", "data-route": "/sources" }, t("canvas.sources_link"))
    ]);
  }

  /* The chips of a card's header. */
  function chips(record, widget, row) {
    var out = [];
    var notes = record && record.notes;
    var answer = record && record.answer;
    if (notes && notes.ownRange && notes.period) {
      out.push(h("span", { key: "own-range", "class": "card-chip card-period", "data-chip": "own_range" },
        t("card.own_range", { period: tk("period.short.", notes.period) })));
    }
    if (notes) {
      notes.ownScope.forEach(function (kind) {
        out.push(h("span", { key: "own-" + kind, "class": "card-chip card-period", "data-chip": "own_scope" },
          tk("card.own_scope.", kind)));
      });
      notes.notScoped.forEach(function (kind) {
        out.push(h("span", { key: "not-" + kind, "class": "card-chip card-meta", "data-chip": "not_scoped" },
          tk("card.not_scoped.", kind)));
      });
    }
    if (answer) {
      if ((answer.freshness || []).some(function (entry) { return entry.stale; })) {
        out.push(h("span", { key: "stale", "class": "card-chip card-state", "data-chip": "stale" }, t("card.stale")));
      }
      (answer.states || []).forEach(function (name) {
        out.push(h("span", { key: "state-" + name, "class": "card-chip card-state", "data-chip": "state",
          "data-state": name }, tk("named_state.", name)));
      });
      if (answer.resolver_window_bounded === true) {
        out.push(h("span", { key: "resolver-window", "class": "card-chip card-state", "data-chip": "state",
          "data-state": "resolver_window_bounded" }, tk("named_state.", "resolver_window_bounded")));
      }
      if (answer.condition && PANEL_CONDITIONS.indexOf(answer.condition) >= 0 && answer.data !== null &&
          answer.data !== undefined && answer.condition !== "reachable_no_rows") {
        out.push(h("span", { key: "condition", "class": "card-chip card-state", "data-chip": "condition" },
          tk("condition.", answer.condition)));
      }
      var unknown = answer.unknown_parameters ? Object.keys(answer.unknown_parameters) : [];
      if (unknown.length) {
        out.push(h("span", { key: "kept", "class": "card-chip card-kept", "data-chip": "kept", title: unknown.join(", ") },
          t("card.kept")));
      }
      volumeMeasures(answer).forEach(function (measure) {
        out.push(h("span", { key: "measure-" + measure, "class": "card-chip measure-chip measure-" + measure,
          "data-chip": "measure", "data-measure-chip": measure }, tk("measure.", measure)));
      });
    }
    out.push(h("span", { key: "clip", "class": "clip-flag", "data-clip-flag": "true", hidden: true }, t("card.cut_off")));
    return out;
  }

  /* The volume measures an answer's figures are given in. */
  function volumeMeasures(answer) {
    var seen = {};
    (function walk(value, depth) {
      if (!value || typeof value !== "object" || depth > 7) { return; }
      if (Array.isArray(value)) { for (var i = 0; i < value.length && i < 50; i++) { walk(value[i], depth + 1); } return; }
      if (typeof value.measure === "string" && opnview.kit && ["logged", "netflow", "sampled", "interface_counter"]
        .indexOf(value.measure) >= 0) {
        seen[value.measure] = true;
      }
      Object.keys(value).forEach(function (key) { walk(value[key], depth + 1); });
    })(answer.data, 0);
    return ["logged", "netflow", "sampled", "interface_counter"].filter(function (measure) { return seen[measure]; });
  }

  /* The body of one card: its drawing, or the panel of its condition. */
  function body(context, widget, row, record, size) {
    var state = context.state;
    if (!record) {
      return h("p", { key: "loading", "class": "loading", role: "status" }, t("state.loading"));
    }
    var answer = record.answer;
    var condition = record.condition;
    var drawable = answer && answer.data !== null && answer.data !== undefined && condition !== "reachable_no_rows";
    if (!drawable) {
      state.drawings[widget.id] = null;
      return panel(condition || "read_failed", record);
    }
    /* A drawing is kept until its answer or its size changes: opening a detail or showing a state
     * draws nothing again. */
    var cached = state.drawings[widget.id];
    var drawing = cached && cached.answer === answer && cached.width === size.width && cached.height === size.height
      ? cached : null;
    if (!drawing) {
      drawing = opnview.widgets.draw({ answer: answer, parameters: answer.parameters || record.parameters, row: row,
        size: size, widget: { id: widget.id, title: widget.title }, prefix: widget.id });
      drawing.answer = answer;
      drawing.width = size.width;
      drawing.height = size.height;
    }
    state.drawings[widget.id] = drawing;
    if (drawing.failed) { return panel("renderer_failed", record); }
    return drawing.tree;
  }

  function card(context, widget, cell) {
    var state = context.state;
    var row = widget.type ? opnview.widgets.row(widget.type) : null;
    var record = state.answers[widget.id];
    var title = titleOf(widget, row);
    var size = sizeOf(state, widget, cell, state.columns);
    return h("section", { key: "card-" + widget.id, "class": "card" + (state.detail && state.detail.widget === widget.id ? " has-detail" : ""),
      "data-widget-card": widget.id, "data-type": widget.type || widget.shape || "",
      "data-cell": cell ? [cell.column, cell.span, cell.row, cell.rows].join(",") : "" }, [
      h("header", { key: "head", "class": "card-head" }, [
        h("h2", { "class": "card-title" }, title),
        chips(record, widget, row),
        h("button", { key: "gear", type: "button", "class": "card-gear", "data-configure": widget.id,
          "aria-label": t("card.configure", { name: title }), title: t("card.configure", { name: title }),
          on: { click: function (event) { openWidgetDetail(context, widget, row, record, event); } } }, t("card.configure_mark"))
      ]),
      h("div", { key: "body", "class": "card-body", "data-widget-body": widget.id }, body(context, widget, row, record, size))
    ]);
  }

  /* The configure control: the widget's own facts, as a detail -- inspecting what it asks for. */
  function openWidgetDetail(context, widget, row, record, event) {
    var parameters = record && record.parameters ? record.parameters : (widget.parameters || {});
    var facts = [[t("detail.fact.type"), widget.type || widget.shape || ""]];
    if (row) { facts.push([t("detail.fact.shape"), row.shape]); }
    Object.keys(parameters).sort().forEach(function (key) {
      var value = parameters[key];
      facts.push([key, value === null ? t("figure.dash") : typeof value === "object" ? JSON.stringify(value) : String(value)]);
    });
    openDetail(context, widget.id, { kind: "widget", key: widget.id, title: titleOf(widget, row), facts: facts,
      related: [], reference: null, about: null }, event);
  }

  /* ---------------------------------------------------------------------------------------------
   * The detail and the tooltip.
   * ------------------------------------------------------------------------------------------- */

  function anchorOf(event) {
    var target = event && (event.currentTarget && event.currentTarget.getBoundingClientRect ? event.currentTarget : event.target);
    if (target && target.getBoundingClientRect) {
      var box = target.getBoundingClientRect();
      return { x: box.left, y: box.bottom };
    }
    return null;
  }

  function openDetail(context, widgetId, subject, event) {
    context.state.detail = { widget: widgetId, id: subject.kind + ":" + subject.key, subject: subject, anchor: anchorOf(event) };
    context.rerender();
  }

  function closeDetail(context) {
    context.state.detail = null;
    context.rerender();
  }

  function detailLayer(context) {
    var state = context.state;
    var detail = state.detail;
    if (!detail) { return h("div", { key: "detail-layer", "class": "detail-layer", hidden: true }); }
    var drawing = state.drawings[detail.widget];
    var fresh = drawing && drawing.subjects ? drawing.subjects[detail.id] : null;
    var subject = fresh || detail.subject;
    if (fresh) { detail.subject = fresh; }
    var reference = subject.reference;
    var canScope = !!reference && REFERENCE_KINDS.indexOf(reference.kind) >= 0 && !!state.editor;
    var canAdd = !!subject.about && !!opnview.widgets.row(subject.about.type) && !!state.editor;
    return h("div", { key: "detail-layer", "class": "detail-layer" }, opnview.detail.render({
      subject: subject, stale: !fresh && subject.kind !== "widget", canScope: canScope, canAdd: canAdd,
      onClose: function () { closeDetail(context); },
      onOpen: function (other) { openDetail(context, detail.widget, other, null); },
      onScope: function (picked) { setScope(context, picked.reference.kind, [picked.reference]); },
      onAdd: function (picked) { addWidgetAbout(context, picked); }
    }));
  }

  /* The subject a picked node names, in the drawing of its card. */
  function subjectAt(state, element) {
    var node = element;
    while (node && node.getAttribute && !node.getAttribute("data-subject")) { node = node.parentNode; }
    if (!node || !node.getAttribute) { return null; }
    var cardNode = node;
    while (cardNode && cardNode.getAttribute && !cardNode.getAttribute("data-widget-card")) { cardNode = cardNode.parentNode; }
    if (!cardNode || !cardNode.getAttribute) { return null; }
    var widgetId = cardNode.getAttribute("data-widget-card");
    var drawing = state.drawings[widgetId];
    var subject = drawing && drawing.subjects ? drawing.subjects[node.getAttribute("data-subject")] : null;
    return subject ? { widget: widgetId, subject: subject, node: node } : null;
  }

  function tipAt(state, element) {
    var node = element;
    while (node && node.getAttribute && !node.getAttribute("data-tip")) { node = node.parentNode; }
    if (!node || !node.getAttribute) { return null; }
    var cardNode = node;
    while (cardNode && cardNode.getAttribute && !cardNode.getAttribute("data-widget-card")) { cardNode = cardNode.parentNode; }
    if (!cardNode || !cardNode.getAttribute) { return null; }
    var drawing = state.drawings[cardNode.getAttribute("data-widget-card")];
    var nodes = drawing && drawing.tips ? drawing.tips[node.getAttribute("data-tip")] : null;
    if (typeof nodes === "function") { nodes = nodes(); }
    return nodes ? { nodes: nodes, node: node } : null;
  }

  /* The tooltip is drawn in a region of its own, patched alone: a pointer moving over a chart does
   * not draw the canvas again. */
  function showTip(found) {
    var host = root.document && root.document.getElementById("canvas-tip");
    if (!host) { return; }
    opnview.view.render(host, found ? [h("div", { key: "tip", "class": "chart-tooltip", role: "tooltip" }, found.nodes)] : []);
    if (found && found.node.getBoundingClientRect && host.style && host.style.setProperty) {
      var box = found.node.getBoundingClientRect();
      host.style.setProperty("--tip-x", Math.round(box.left + box.width / 2) + "px");
      host.style.setProperty("--tip-y", Math.round(box.top) + "px");
    }
  }

  /* Delegated listeners of the canvas: a click or a key opens a detail, a pointer or a focus shows a
   * tooltip. Attached once to the canvas's root element. */
  function listeners(context) {
    return {
      click: function (event) {
        var found = subjectAt(context.state, event.target);
        if (found) { openDetail(context, found.widget, found.subject, { currentTarget: found.node }); }
      },
      keydown: function (event) {
        if ((event.key === "Enter" || event.key === " ") && event.target && event.target.getAttribute &&
            event.target.getAttribute("data-subject")) {
          var found = subjectAt(context.state, event.target);
          if (found) {
            if (event.preventDefault) { event.preventDefault(); }
            openDetail(context, found.widget, found.subject, { currentTarget: found.node });
          }
        }
      },
      mouseover: function (event) { showTip(tipAt(context.state, event.target)); },
      focusin: function (event) { showTip(tipAt(context.state, event.target)); },
      mouseleave: function () { showTip(null); }
    };
  }

  /* ---------------------------------------------------------------------------------------------
   * Laying the cards out, after they are drawn.
   * ------------------------------------------------------------------------------------------- */

  /* Places each card on the grid and measures its body: the one style the front writes is a grid
   * position, through the style object, never a colour. A card drawn at a size it does not have is
   * drawn again at the size it has. Answers whether a card must be drawn again. */
  function layOut(context) {
    var state = context.state;
    var document = root.document;
    var grid = document && document.getElementById ? document.getElementById("canvas-grid") : null;
    if (!grid) { return false; }
    var redraw = false;
    var cards = grid.querySelectorAll ? grid.querySelectorAll("[data-widget-card]") : [];
    for (var i = 0; i < cards.length; i++) {
      var element = cards[i];
      var cell = (element.getAttribute("data-cell") || "").split(",").map(Number);
      if (cell.length === 4 && element.style) {
        element.style.gridColumn = state.columns === 1 ? "1 / -1" : cell[0] + " / span " + cell[1];
        element.style.gridRow = cell[2] + " / span " + cell[3];
      }
      var bodyElement = element.querySelector ? element.querySelector("[data-widget-body]") : null;
      if (bodyElement && bodyElement.clientWidth > 0) {
        var id = element.getAttribute("data-widget-card");
        var size = { width: bodyElement.clientWidth, height: bodyElement.clientHeight };
        var known = state.sizes[id];
        if (!known || Math.abs(known.width - size.width) > 8 || Math.abs(known.height - size.height) > 8) {
          state.sizes[id] = size;
          redraw = true;
        }
        var flag = element.querySelector("[data-clip-flag]");
        if (flag) {
          flag.hidden = !(bodyElement.scrollHeight > bodyElement.clientHeight + 2 ||
            bodyElement.scrollWidth > bodyElement.clientWidth + 2);
        }
      }
    }
    var pop = document.getElementById("detail-pop");
    var anchor = state.detail && state.detail.anchor;
    if (pop && pop.style && pop.style.setProperty && anchor) {
      var width = root.innerWidth || 1200;
      pop.style.setProperty("--detail-x", Math.max(8, Math.min(anchor.x, width - 440)) + "px");
      pop.style.setProperty("--detail-y", Math.max(8, anchor.y + 6) + "px");
    }
    return redraw;
  }

  function measureCanvas(context) {
    var state = context.state;
    var document = root.document;
    var host = document && document.getElementById ? document.getElementById("canvas-grid") : null;
    var width = host && host.clientWidth ? host.clientWidth : 0;
    if (width > 0) { state.canvasWidth = width; }
    return opnview.grid.columnsFor(width || 0);
  }

  /* ---------------------------------------------------------------------------------------------
   * The view.
   * ------------------------------------------------------------------------------------------- */

  function grid(context, canvas) {
    var state = context.state;
    var widgets = widgetsOf(canvas);
    var authored = canvas && canvas.grid && Number(canvas.grid.columns) > 0 ? Number(canvas.grid.columns) : 12;
    var cells = opnview.grid.layout(widgets, state.columns, authored === 12 ? 12 : authored);
    var handlers = state.handlers || (state.handlers = listeners(context));
    return h("div", { key: "grid", id: "canvas-grid", "class": "grid cols-" + state.columns, on: handlers },
      cells.map(function (cell) { return card(context, widgets[cell.index], cell); }));
  }

  function render(context) {
    var state = context.state;
    if (!state.loaded) { return h("p", { "class": "loading", role: "status" }, t("state.loading")); }
    var head = bar(context);
    if (state.missing) {
      return [head, h("div", { key: "state", "class": "state state-unresolved" },
        h("span", { "class": "state-badge" }, t("canvas.state.not_found")))];
    }
    if (!state.chosen) {
      return [head, h("div", { key: "state", "class": "state state-gap" },
        h("span", { "class": "state-badge" }, t("canvas.state.no_dashboard")))];
    }
    var canvas = currentCanvas(state);
    if (!canvas) {
      return [head, h("p", { key: "loading", "class": "loading", role: "status" }, t("state.loading"))];
    }
    state.columns = measureCanvas(context);
    var widgets = widgetsOf(canvas);
    var content = widgets.length === 0
      ? h("div", { key: "empty", "class": "state state-gap canvas-empty" }, [
          h("span", { "class": "state-badge" }, t("canvas.state.empty")),
          canvas.description ? h("p", { "class": "state-text" }, canvas.description) : null
        ])
      : grid(context, canvas);
    return [head, content, detailLayer(context),
      h("div", { key: "tip", id: "canvas-tip", "class": "tip-layer", "data-reconcile": "manual" })];
  }

  opnview.views.register({
    id: "canvas",
    paths: ["/", "/canvas/{dashboard}/{canvas}"],
    path: "/",
    title: function () { return t("canvas.title"); },
    heading: false,
    load: load,
    render: render,
    /* After every drawing: the grid positions, the measured sizes, the cut-off flags. */
    rendered: function (context) {
      if (layOut(context) && !context.state.relaid) {
        context.state.relaid = true;
        context.refresh();
        context.state.relaid = false;
      }
    },
    /* Leaving the canvas stops its refresh. */
    leave: function (context) {
      if (context.state.scheduler) { context.state.scheduler.stop(); }
    },
    visible: function (context) {
      if (context.state.scheduler) { context.state.scheduler.shown(); }
    },
    resized: function (context) { context.rerender(); },
    keydown: function (context, event) {
      if (event.key === "Escape" && context.state.detail) { closeDetail(context); return true; }
      var target = event.target;
      var editable = target && target.tagName && /^(INPUT|TEXTAREA|SELECT)$/i.test(String(target.tagName));
      if ((event.ctrlKey || event.metaKey) && !event.shiftKey && String(event.key).toLowerCase() === "z" && !editable) {
        if (event.preventDefault) { event.preventDefault(); }
        undo(context);
        return true;
      }
      return false;
    }
  });

  /* What the tests reach. */
  opnview.canvasView = { panel: panel, PANEL_CONDITIONS: PANEL_CONDITIONS.slice() };
})(this);
