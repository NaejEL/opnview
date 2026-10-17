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
 * EDITING (Lot 3) goes through front/core/editing.js: the document of the dashboard on screen is
 * the one the session holds, every edit is one undoable save, and edit mode, the picker, the canvas
 * management, the import report (front/views/edit.js) and the dock (front/views/dock.js) are drawn
 * here around the cards. A gesture in progress -- a drag, a form's pending values -- is drawn from
 * this view's memory and written once, when it ends or on Apply.
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
      state.dashboardId = state.chosen ? state.chosen.dashboard.id : null;
      state.missing = !!wanted.dashboard && !chosen;
      state.loaded = true;
      state.answers = state.answers || {};
      state.sizes = state.sizes || {};
      state.drawings = state.drawings || {};
      if (!state.chosen) { return null; }
      return context.api.get("/api/dashboards/" + encodeURIComponent(state.chosen.dashboard.id)).then(function (result) {
        if (result.status !== 200 || !result.body) {
          state.unreadable = true;
          return null;
        }
        opnview.editing.adopt(state.dashboardId, result);
        start(context);
        loadReferences(context);
        return null;
      });
    });
  }

  /* The candidates of the three scope kinds, for the canvas bar's dropdowns. */
  function loadReferences(context) {
    REFERENCE_KINDS.forEach(function (kind) { referencesOf(context, kind); });
  }

  /* The candidates of one reference kind, asked once a view: the canvas bar's scope and the form's
   * reference dropdowns. Null until they arrive. */
  function referencesOf(context, kind) {
    var state = context.state;
    state.references = state.references || {};
    state.referencesAsked = state.referencesAsked || {};
    if (!state.referencesAsked[kind]) {
      state.referencesAsked[kind] = true;
      context.api.get("/api/references/" + encodeURIComponent(kind)).then(function (result) {
        state.references[kind] = result.status === 200 && result.body ? result.body : { candidates: [], by: [] };
        context.rerender();
      });
    }
    return state.references[kind] || null;
  }

  /* The data catalogue's datasets, for the series editor's sources; asked once a view. */
  function datasetsOf(context) {
    var state = context.state;
    if (!state.datasetsAsked) {
      state.datasetsAsked = true;
      context.api.get("/api/datasets").then(function (result) {
        state.datasets = result.status === 200 && result.body ? result.body.datasets || [] : [];
        context.rerender();
      });
    }
    return state.datasets || null;
  }

  /* ---------------------------------------------------------------------------------------------
   * The widgets of the canvas on screen, their requests and their refresh.
   * ------------------------------------------------------------------------------------------- */

  /* The document of the dashboard on screen, as the session holds it, with a drag in progress
   * drawn where the pointer has it. */
  function currentDocument(state) {
    if (!state.dashboardId) { return null; }
    var document = opnview.editing.document(state.dashboardId);
    if (!document || !state.drag || !state.drag.placement) { return document; }
    var copy = JSON.parse(JSON.stringify(document));
    var canvas = model.canvasIn(copy, state.chosen.canvas.id);
    (canvas && canvas.widgets || []).forEach(function (widget) {
      if (widget && widget.id === state.drag.id) { widget.placement = state.drag.placement; }
    });
    return copy;
  }

  function currentCanvas(state) {
    if (!state.chosen) { return null; }
    var document = currentDocument(state);
    return document ? model.canvasIn(document, state.chosen.canvas.id) : null;
  }

  function canvasIndexOf(document, id) {
    var canvases = document && Array.isArray(document.canvases) ? document.canvases : [];
    for (var i = 0; i < canvases.length; i++) { if (canvases[i] && canvases[i].id === id) { return i; } }
    return -1;
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
    // The form's pending values are drawn on the card they preview, never written until Apply.
    var asked = state.pending && state.pending.id === id
      ? { id: widget.id, type: widget.type, placement: widget.placement, parameters: state.pending.parameters } : widget;
    var built = model.request(row, asked, currentCanvas(state));
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

  /* An edit of the canvas on screen: mutate(canvas, document) changes a copy, which is saved as one
   * undoable edit. A mutation answering false changes nothing. */
  function edit(context, mutate, label) {
    var state = context.state;
    if (!state.dashboardId || !opnview.editing.document(state.dashboardId)) { return Promise.resolve({ ok: false }); }
    var canvasId = state.chosen.canvas.id;
    return opnview.editing.edit(context.api, state.dashboardId, function (document) {
      var canvas = model.canvasIn(document, canvasId);
      if (!canvas) { return false; }
      return mutate(canvas, document);
    }, label).then(function (outcome) {
      settled(context, outcome);
      return outcome;
    });
  }

  /* After a save: the widgets asked again, the import report collected again, the canvas drawn. */
  function settled(context, outcome) {
    var state = context.state;
    state.editFailed = outcome.ok ? null : (outcome.condition || "read_failed");
    if (outcome.ok && !outcome.unchanged) {
      start(context);
      if (opnview.editing.report(state.dashboardId)) {
        opnview.editing.recollect(context.api, state.dashboardId).then(function () { context.rerender(); });
      }
      context.reloadDashboards();
    }
    context.rerender();
  }

  /* Undoes the session's last edit, whichever dashboard it touched. */
  function undo(context) {
    var state = context.state;
    if (!opnview.editing.canUndo()) { return Promise.resolve(); }
    return opnview.editing.undo(context.api).then(function (outcome) {
      state.editFailed = outcome.ok ? null : (outcome.condition || "read_failed");
      if (!outcome.ok) { context.rerender(); return; }
      var entry = outcome.entry;
      if (entry.kind !== "document" || entry.dashboard !== state.dashboardId) {
        // The undo touched the dashboards themselves, or another one: the canvas is read again.
        context.reloadDashboards().then(function () {
          context.go(root.location ? root.location.pathname : "/", { replace: true });
        });
        return;
      }
      if (!currentCanvas(state)) {
        context.go("/canvas/" + encodeURIComponent(state.dashboardId) + "/" +
          encodeURIComponent(firstCanvasId(state)), { replace: true });
        return;
      }
      settled(context, { ok: true });
    });
  }

  function firstCanvasId(state) {
    var document = opnview.editing.document(state.dashboardId);
    return document && document.canvases && document.canvases[0] ? document.canvases[0].id : "";
  }

  function setPeriod(context, period) {
    return edit(context, function (canvas) {
      canvas.period = period;
    }, "period");
  }

  function setScope(context, kind, references) {
    var key = kind + "s";
    return edit(context, function (canvas) {
      var scope = canvas.scope && typeof canvas.scope === "object" ? canvas.scope : {};
      if (references && references.length) { scope[key] = references; } else { delete scope[key]; }
      if (Object.keys(scope).length) { canvas.scope = scope; } else { delete canvas.scope; }
    }, "scope");
  }

  function addWidgetAbout(context, subject) {
    var about = subject.about;
    return edit(context, function (canvas) {
      if (!Array.isArray(canvas.widgets)) { canvas.widgets = []; }
      canvas.widgets.push({ id: model.freshWidgetId(canvas, about.type), type: about.type,
        placement: { x: 0, y: model.bottomRow(canvas), w: 6, h: 8 }, parameters: JSON.parse(JSON.stringify(about.parameters)) });
    }, "add");
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
        disabled: !opnview.editing.canUndo(), on: { click: function () { undo(context); } } }, t("action.undo")));
      if (state.editFailed) {
        controls.push(h("span", { key: "failed", "class": "field-error", role: "status" }, tk("error.", state.editFailed)));
      }
    }
    var document = currentDocument(state);
    return h("div", { key: "bar", "class": "canvas-bar" }, [
      tabs(context),
      controls.length ? h("div", { key: "controls", "class": "canvas-controls" }, controls) : null,
      canvas && opnview.editing.editMode() ? opnview.editChrome.manage({ canvas: canvas, count: document.canvases.length,
        index: canvasIndexOf(document, canvas.id), onRename: function (title) { renameCanvas(context, title); },
        onMove: function (by) { moveCanvas(context, by); }, onAdd: function () { addCanvas(context); },
        onRemove: function () { removeCanvas(context); } }) : null,
      opnview.editChrome.notice(opnview.editing.notice(), function () { undo(context); },
        function () { opnview.editing.setNotice(null); context.rerender(); })
    ]);
  }

  /* -----------------------------------------------------------------------------------------------
   * Editing the layout and the canvases.
   * --------------------------------------------------------------------------------------------- */

  function setPlacement(context, id, placement, label) {
    return edit(context, function (canvas) {
      var widget = (canvas.widgets || []).filter(function (w) { return w && w.id === id; })[0];
      if (!widget) { return false; }
      widget.placement = { x: placement.x, y: placement.y, w: placement.w, h: placement.h };
      return true;
    }, label || "placement");
  }

  function authoredColumns(canvas) {
    return canvas && canvas.grid && Number(canvas.grid.columns) > 0 ? Number(canvas.grid.columns) : 12;
  }

  function step(context, widget, action) {
    var columns = authoredColumns(currentCanvas(context.state));
    var placement = opnview.grid.placementOf(widget, columns);
    return setPlacement(context, widget.id, opnview.editChrome.stepped(placement, action, columns), action);
  }

  function removeWidget(context, widget, name) {
    return edit(context, function (canvas) {
      var before = (canvas.widgets || []).length;
      canvas.widgets = (canvas.widgets || []).filter(function (w) { return !(w && w.id === widget.id); });
      return canvas.widgets.length !== before;
    }, "remove").then(function (outcome) {
      if (outcome && outcome.ok && !outcome.unchanged) {
        opnview.editing.setNotice({ kind: "widget_removed", params: { name: name }, undo: true });
        if (opnview.editing.dock().widget === widget.id) { opnview.editing.dock().widget = null; }
        context.rerender();
      }
    });
  }

  function addWidget(context, row) {
    var state = context.state;
    var added = null;
    state.picker = false;
    return edit(context, function (canvas) {
      if (!Array.isArray(canvas.widgets)) { canvas.widgets = []; }
      added = opnview.editChrome.newWidget(canvas, row);
      canvas.widgets.push(added);
      return true;
    }, "add").then(function (outcome) {
      if (outcome && outcome.ok && added) {
        opnview.editing.dock().widget = added.id;
        context.rerender();
      }
    });
  }

  /* A canvas id not yet taken in the document. */
  function freshCanvasId(document) {
    var taken = {};
    (document.canvases || []).forEach(function (canvas) { if (canvas) { taken[canvas.id] = true; } });
    for (var n = (document.canvases || []).length + 1; ; n++) {
      if (!taken["canvas-" + n]) { return "canvas-" + n; }
    }
  }

  function renameCanvas(context, title) {
    var trimmed = String(title || "").trim();
    if (!trimmed) { return Promise.resolve(); }
    return edit(context, function (canvas) {
      if (canvas.title === trimmed) { return false; }
      canvas.title = trimmed;
      return true;
    }, "rename_canvas");
  }

  function moveCanvas(context, by) {
    var id = context.state.chosen.canvas.id;
    return edit(context, function (canvas, document) {
      var index = canvasIndexOf(document, id);
      var target = index + by;
      if (index < 0 || target < 0 || target >= document.canvases.length) { return false; }
      document.canvases.splice(index, 1);
      document.canvases.splice(target, 0, canvas);
      return true;
    }, "move_canvas");
  }

  function addCanvas(context) {
    var state = context.state;
    var created = null;
    return edit(context, function (canvas, document) {
      created = { id: freshCanvasId(document), title: t("canvas.new_title"), widgets: [] };
      document.canvases.push(created);
      return true;
    }, "add_canvas").then(function (outcome) {
      if (outcome && outcome.ok && created) {
        context.go("/canvas/" + encodeURIComponent(state.dashboardId) + "/" + encodeURIComponent(created.id));
      }
    });
  }

  function removeCanvas(context) {
    var state = context.state;
    var id = state.chosen.canvas.id;
    var title = state.chosen.canvas.title;
    return edit(context, function (canvas, document) {
      if (document.canvases.length < 2) { return false; }
      document.canvases = document.canvases.filter(function (other) { return !(other && other.id === id); });
      return true;
    }, "remove_canvas").then(function (outcome) {
      if (outcome && outcome.ok && !outcome.unchanged) {
        opnview.editing.setNotice({ kind: "canvas_removed", params: { name: String(title || id) }, undo: true });
        context.go("/canvas/" + encodeURIComponent(state.dashboardId) + "/" + encodeURIComponent(firstCanvasId(state)),
          { replace: true });
      }
    });
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
    var editing = opnview.editing.editMode();
    var dock = opnview.editing.dock();
    var selected = dock.open && dock.widget === widget.id;
    var pending = state.pending && state.pending.id === widget.id;
    var classes = "card" + (state.detail && state.detail.widget === widget.id ? " has-detail" : "") +
      (selected ? " is-selected" : "") + (pending ? " is-pending" : "") +
      (state.drag && state.drag.id === widget.id ? " is-dragging" : "");
    return h("section", { key: "card-" + widget.id, "class": classes,
      "data-widget-card": widget.id, "data-type": widget.type || widget.shape || "",
      "data-cell": cell ? [cell.column, cell.span, cell.row, cell.rows].join(",") : "" }, [
      editing ? opnview.editChrome.bar(widget, title, function (action) { step(context, widget, action); },
        function () { removeWidget(context, widget, title); }) : null,
      h("header", { key: "head", "class": "card-head" }, [
        h("h2", { "class": "card-title" }, title),
        chips(record, widget, row),
        pending ? h("span", { key: "pending", "class": "card-pending", "data-chip": "unapplied" }, t("card.unapplied")) : null,
        h("button", { key: "gear", type: "button", "class": "card-gear", "data-configure": widget.id,
          "aria-label": t("card.configure", { name: title }), title: t("card.configure", { name: title }),
          on: { click: function () { configure(context, widget.id, "form"); } } }, t("card.configure_mark"))
      ]),
      h("div", { key: "body", "class": "card-body", "data-widget-body": widget.id }, body(context, widget, row, record, size)),
      editing ? opnview.editChrome.grips(widget) : null
    ]);
  }

  /* The configure control: the dock, opened on this widget. */
  function configure(context, id, tab) {
    var dock = opnview.editing.dock();
    if (dock.widget !== id) { clearPending(context); }
    opnview.editing.openDock(tab || dock.tab || "form", id);
    context.rerender();
  }

  /* The form's pending values, drawn on their card and asked for with them. */
  function setPending(context, pending) {
    var state = context.state;
    var before = state.pending;
    state.pending = pending;
    var id = (pending && pending.id) || (before && before.id);
    if (id && JSON.stringify(before) !== JSON.stringify(pending)) {
      state.answers[id] = null;
      fetchWidget(context, id);
    }
    context.rerender();
  }

  function clearPending(context) {
    if (context.state.pending) { setPending(context, null); }
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
    var canScope = !!reference && REFERENCE_KINDS.indexOf(reference.kind) >= 0 && !!opnview.editing.document(state.dashboardId);
    var canAdd = !!subject.about && !!opnview.widgets.row(subject.about.type) && !!opnview.editing.document(state.dashboardId);
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
      pointerdown: function (event) { beginDrag(context, event); },
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
    var slot = document.getElementById("add-widget-slot");
    if (slot && slot.style) {
      var slotCell = (slot.getAttribute("data-cell") || "").split(",").map(Number);
      slot.style.gridColumn = "1 / -1";
      slot.style.gridRow = slotCell[2] + " / span " + slotCell[3];
    }
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
    var editing = opnview.editing.editMode();
    var lowest = cells.reduce(function (row, cell) { return Math.max(row, cell.row + cell.rows); }, 1);
    return h("div", { key: "grid", id: "canvas-grid", "class": "grid cols-" + state.columns + (editing ? " is-editing" : ""),
      on: handlers }, [
      cells.map(function (cell) { return card(context, widgets[cell.index], cell); }),
      editing ? h("button", { key: "slot", type: "button", "class": "slot", id: "add-widget-slot",
        "data-cell": [1, state.columns, lowest, 2].join(","), on: { click: function () { openPicker(context); } } },
      t("edit.add_here")) : null
    ]);
  }

  function openPicker(context) {
    context.state.picker = true;
    context.rerender();
  }

  /* -----------------------------------------------------------------------------------------------
   * Dragging a card: the authored placement follows the pointer by whole columns and rows, drawn as
   * it goes, and is written once, when the pointer is released.
   * --------------------------------------------------------------------------------------------- */

  function beginDrag(context, event) {
    var state = context.state;
    if (!opnview.editing.editMode() || (event.button !== undefined && event.button > 0)) { return; }
    var target = event.target;
    var gripMode = target && target.getAttribute ? target.getAttribute("data-grip") : null;
    // A control or a mark keeps its own meaning: only the card itself is grabbed to move it.
    for (var node = target; !gripMode && node && node.getAttribute && !node.getAttribute("data-widget-card"); node = node.parentNode) {
      if (/^(BUTTON|A|INPUT|SELECT|TEXTAREA)$/i.test(String(node.tagName)) || node.getAttribute("data-subject")) { return; }
    }
    var cardNode = target;
    while (cardNode && cardNode.getAttribute && !cardNode.getAttribute("data-widget-card")) { cardNode = cardNode.parentNode; }
    if (!cardNode || !cardNode.getAttribute) { return; }
    var widget = findWidget(state, cardNode.getAttribute("data-widget-card"));
    var grid = root.document.getElementById("canvas-grid");
    if (!widget || !grid) { return; }
    var columns = authoredColumns(currentCanvas(state));
    if (event.preventDefault) { event.preventDefault(); }
    state.drag = { id: widget.id, mode: gripMode || "move", x0: event.clientX, y0: event.clientY,
      start: opnview.grid.placementOf(widget, columns), placement: null, columns: columns,
      column: (grid.clientWidth + GAP) / columns, row: ROW_HEIGHT + GAP };
    if (!state.dragListening) {
      state.dragListening = true;
      root.document.addEventListener("pointermove", function (moved) { dragMove(context, moved); });
      root.document.addEventListener("pointerup", function () { endDrag(context); });
      root.document.addEventListener("pointercancel", function () { endDrag(context); });
    }
  }

  function dragMove(context, event) {
    var drag = context.state.drag;
    if (!drag) { return; }
    var dx = Math.round((event.clientX - drag.x0) / drag.column);
    var dy = Math.round((event.clientY - drag.y0) / drag.row);
    var next = opnview.editChrome.dragged(drag.start, drag.mode, dx, dy, drag.columns);
    var held = drag.placement || drag.start;
    if (next.x === held.x && next.y === held.y && next.w === held.w && next.h === held.h) { return; }
    drag.placement = next;
    context.rerender();
  }

  function endDrag(context) {
    var state = context.state;
    var drag = state.drag;
    if (!drag) { return; }
    state.drag = null;
    if (drag.placement && JSON.stringify(drag.placement) !== JSON.stringify(drag.start)) {
      setPlacement(context, drag.id, drag.placement, drag.mode === "move" ? "move" : "resize");
    } else {
      context.rerender();
    }
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
          canvas.description ? h("p", { "class": "state-text" }, canvas.description) : null,
          h("button", { key: "invite", type: "button", "class": "btn btn-primary", id: "canvas-invite",
            on: { click: function () { openPicker(context); } } }, t("canvas.invite"))
        ])
      : grid(context, canvas);
    var report = opnview.editing.report(state.dashboardId);
    return [head,
      report ? opnview.editChrome.report({ report: report, titleOf: function (type) {
        var row = type ? opnview.widgets.row(type) : null;
        return row ? opnview.editChrome.titleOf(row) : String(type || "");
      }, onRepair: function (row) { repair(context, row); },
      onDismiss: function () { opnview.editing.dismissReport(state.dashboardId); context.rerender(); } }) : null,
      content, detailLayer(context),
      state.picker ? opnview.editChrome.picker(function (row) { addWidget(context, row); },
        function () { state.picker = false; context.rerender(); }) : null,
      h("div", { key: "tip", id: "canvas-tip", "class": "tip-layer", "data-reconcile": "manual" })];
  }

  /* A report row's repair: the dock, on the widget it concerns -- the form for a reference, the
   * document for what only the code can change. */
  function repair(context, row) {
    var state = context.state;
    var document = opnview.editing.document(state.dashboardId);
    var canvas = document && document.canvases ? document.canvases[row.canvas_index] : null;
    if (!canvas) { return; }
    var tab = row.condition === "unresolved_reference" && row.widget_id !== null ? "form" : "document";
    opnview.editing.openDock(tab, row.widget_id);
    if (canvas.id !== state.chosen.canvas.id) {
      context.go("/canvas/" + encodeURIComponent(state.dashboardId) + "/" + encodeURIComponent(canvas.id));
      return;
    }
    context.rerender();
  }

  /* The issues the code view marks on their lines: the import report's, and what the answers of the
   * canvas on screen report. Paths are the document's. */
  function issues(context) {
    var state = context.state;
    var out = [];
    var seen = {};
    var report = opnview.editing.report(state.dashboardId);
    (report ? report.rows : []).forEach(function (row) { seen[row.path] = true; out.push(row); });
    var document = opnview.editing.document(state.dashboardId);
    var ci = canvasIndexOf(document, state.chosen ? state.chosen.canvas.id : null);
    if (ci < 0) { return out; }
    (document.canvases[ci].widgets || []).forEach(function (widget, wi) {
      if (!widget) { return; }
      var at = "canvases[" + ci + "].widgets[" + wi + "]";
      var record = state.answers[widget.id];
      var found = [];
      if (widget.type && !opnview.widgets.row(widget.type)) {
        found.push({ condition: "unknown_widget_type", path: at + ".type", kind: "widget_type", by: "type", value: widget.type,
          label: widget.title || null });
      } else if (!widget.type && widget.shape) {
        found.push({ condition: "shape_not_served", path: at + ".shape", kind: "shape", by: "shape", value: widget.shape,
          label: widget.title || null });
      } else if (record && record.answer && !(state.pending && state.pending.id === widget.id)) {
        opnview.editing.issuesOf(record.answer).forEach(function (issue) {
          issue.path = at + ".parameters." + issue.path;
          found.push(issue);
        });
      }
      found.forEach(function (issue) { if (!seen[issue.path]) { seen[issue.path] = true; out.push(issue); } });
    });
    return out;
  }

  /* The dock, beside the canvas: what it edits, and what it is told back. */
  function aside(context) {
    var state = context.state;
    if (!state.chosen || !opnview.editing.dock().open) { return null; }
    var document = currentDocument(state);
    if (!document) { return null; }
    var ci = canvasIndexOf(document, state.chosen.canvas.id);
    var canvas = ci >= 0 ? document.canvases[ci] : null;
    var dock = opnview.editing.dock();
    var widget = null, wi = -1;
    (canvas && canvas.widgets || []).forEach(function (candidate, index) {
      if (candidate && candidate.id === dock.widget) { widget = candidate; wi = index; }
    });
    if (!widget && canvas && canvas.widgets && canvas.widgets.length && dock.tab !== "document") {
      widget = canvas.widgets[0];
      wi = 0;
      dock.widget = widget.id;
    }
    var row = widget && widget.type ? opnview.widgets.row(widget.type) : null;
    return opnview.dock.render(context, {
      api: context.api, dashboard: state.dashboardId, document: document, canvas: canvas, canvasIndex: ci,
      widget: widget, widgetIndex: wi, row: row, widgetTitle: widget ? titleOf(widget, row) : null,
      issues: issues(context), dragging: !!state.drag,
      references: function (kind) { return referencesOf(context, kind); },
      datasets: function () { return datasetsOf(context); },
      onPending: function (pending) { setPending(context, pending); },
      onApplied: function () { settled(context, { ok: true }); },
      switchTab: function (tab) { opnview.editing.openDock(tab); context.rerender(); },
      close: function () { clearPending(context); opnview.editing.closeDock(); context.rerender(); }
    });
  }

  opnview.views.register({
    id: "canvas",
    paths: ["/", "/canvas/{dashboard}/{canvas}"],
    path: "/",
    title: function () { return t("canvas.title"); },
    heading: false,
    load: load,
    render: render,
    aside: aside,
    /* Edit mode is entered from the header, on the canvas. */
    headerControls: function (context) {
      var on = opnview.editing.editMode();
      return [h("button", { key: "edit-toggle", type: "button", id: "edit-toggle", "class": "btn",
        "aria-pressed": on ? "true" : "false", on: { click: function () {
          opnview.editing.setEditMode(!on);
          context.rerender();
        } } }, t("edit.toggle"))];
    },
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
      context.state.drag = null;
    },
    visible: function (context) {
      if (context.state.scheduler) { context.state.scheduler.shown(); }
    },
    resized: function (context) { context.rerender(); },
    keydown: function (context, event) {
      if (event.key === "Escape" && context.state.picker) { context.state.picker = false; context.rerender(); return true; }
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
  opnview.canvasView = { panel: panel, PANEL_CONDITIONS: PANEL_CONDITIONS.slice(), undo: undo };
})(this);
