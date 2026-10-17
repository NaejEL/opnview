/*
 * opnview front -- what a canvas asks the widget API for, and how its document is edited
 * (F7-19, F7-26; decisions D7 and D8 of specs/SPEC-step-7-frontend.md).
 *
 * A CANVAS CARRIES A PERIOD AND A SCOPE (docs/dashboard-format.md, as amended by D7): `period`, one of
 * the period vocabulary, 24h by default, and `scope`, up to three lists of references -- `interfaces`,
 * `clients`, `owners`. A widget inherits both unless it carries its own value.
 *
 * A SCOPE APPLIES ONLY WHERE THE MANIFEST DECLARES A PARAMETER OF THAT KIND (D8). The request carries
 * the inherited value exactly under the key the manifest declares for that reference kind, and never
 * anywhere else; a widget that declares none says "not scoped" for that kind. Nothing is expanded or
 * inferred: an owner is never turned into client references.
 *
 * AN EDIT IS EXPLICIT AND UNDOABLE. The period, the scope, a pivot that scopes the canvas or adds a
 * widget: each is a whole new document saved at the next revision, the previous text kept on an undo
 * stack for the session. Opening or closing a detail is never an edit.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The period vocabulary of the widget API (docs/widget-api.md, "Windows"). */
  var PERIODS = ["1h", "24h", "7d", "30d"];
  var DEFAULT_PERIOD = "24h";
  /* The scope kinds a canvas carries, each under its list's key. */
  var SCOPE_KINDS = [["interface", "interfaces"], ["client", "clients"], ["owner", "owners"]];

  function clone(value) { return value === undefined ? undefined : JSON.parse(JSON.stringify(value)); }

  function given(value) {
    return value !== null && value !== undefined && !(Array.isArray(value) && value.length === 0);
  }

  function canvasPeriod(canvas) {
    return canvas && PERIODS.indexOf(canvas.period) >= 0 ? canvas.period : DEFAULT_PERIOD;
  }

  function canvasScope(canvas) {
    var scope = canvas && canvas.scope && typeof canvas.scope === "object" ? canvas.scope : {};
    var out = {};
    SCOPE_KINDS.forEach(function (pair) {
      out[pair[0]] = Array.isArray(scope[pair[1]]) ? scope[pair[1]] : [];
    });
    return out;
  }

  /* The parameters one widget is requested with, and what its header says about them.
   * row: the widget's registry row (its manifest's parameters); widget: the document's widget;
   * canvas: the document's canvas. */
  function request(row, widget, canvas) {
    var parameters = clone(widget && widget.parameters && typeof widget.parameters === "object" ? widget.parameters : {}) || {};
    var declared = (row && row.parameters) || [];
    var notes = { ownRange: false, ownScope: [], notScoped: [], period: null };
    var periodParameter = declared.filter(function (p) { return p.key === "period"; })[0];
    if (periodParameter) {
      if (given(parameters.period)) {
        notes.ownRange = true;
        notes.period = parameters.period;
      } else {
        var inherited = canvasPeriod(canvas);
        if (!periodParameter.values || periodParameter.values.indexOf(inherited) >= 0) {
          parameters.period = inherited;
          notes.period = inherited;
        }
      }
    }
    var scope = canvasScope(canvas);
    SCOPE_KINDS.forEach(function (pair) {
      var kind = pair[0], references = scope[kind];
      if (references.length === 0) { return; }
      var accepting = declared.filter(function (p) {
        return p.reference === kind && (p.kind === "reference_list" || p.kind === "reference");
      });
      var applied = false, own = false;
      accepting.forEach(function (p) {
        if (given(parameters[p.key])) { own = true; return; }
        if (p.kind === "reference_list") {
          parameters[p.key] = clone(references);
          applied = true;
        } else if (references.length === 1) {
          parameters[p.key] = clone(references[0]);
          applied = true;
        }
      });
      if (own) { notes.ownScope.push(kind); } else if (!applied) { notes.notScoped.push(kind); }
    });
    return { parameters: parameters, notes: notes };
  }

  /* The query string of one widget request. */
  function query(parameters) {
    return "?parameters=" + encodeURIComponent(JSON.stringify(parameters || {}));
  }

  /* The live cadence an answer reports: the shortest cadence_seconds of a sampled figure it carries,
   * or null when it carries none. */
  function liveCadence(answer) {
    var shortest = null;
    (function walk(value, depth) {
      if (!value || typeof value !== "object" || depth > 8) { return; }
      if (Array.isArray(value)) { value.forEach(function (item) { walk(item, depth + 1); }); return; }
      if (value.measure === "sampled" && typeof value.cadence_seconds === "number" && value.cadence_seconds > 0) {
        shortest = shortest === null ? value.cadence_seconds : Math.min(shortest, value.cadence_seconds);
      }
      Object.keys(value).forEach(function (key) { walk(value[key], depth + 1); });
    })(answer && answer.data, 0);
    return shortest;
  }

  /* -----------------------------------------------------------------------------------------------
   * Editing a dashboard's document.
   * --------------------------------------------------------------------------------------------- */

  /* An editor of one stored dashboard. options: {api, id, revision, document (text or object),
   * onChange(document, revision)}. */
  function editor(options) {
    var state = {
      id: options.id,
      revision: options.revision,
      text: typeof options.document === "string" ? options.document : JSON.stringify(options.document),
      undo: [],
      saving: null
    };

    function current() { return JSON.parse(state.text); }

    function save(text) {
      return options.api.post("/api/dashboards/" + encodeURIComponent(state.id),
        { revision: state.revision, document: JSON.parse(text) }).then(function (result) {
        if (result.status === 200 && result.body) {
          state.revision = result.body.revision;
          state.text = text;
          if (options.onChange) { options.onChange(JSON.parse(text), state.revision); }
          return { ok: true };
        }
        return { ok: false, status: result.status, condition: result.body ? result.body.condition : null,
          revision: result.body ? result.body.revision : null };
      });
    }

    return {
      document: current,
      revision: function () { return state.revision; },
      canUndo: function () { return state.undo.length > 0; },
      /* Applies an edit: mutate(document) changes a copy, which is saved at the next revision, and
       * the text before it goes on the undo stack. */
      edit: function (mutate) {
        var before = state.text;
        var next = current();
        mutate(next);
        var text = JSON.stringify(next);
        if (text === before) { return Promise.resolve({ ok: true, unchanged: true }); }
        return save(text).then(function (outcome) {
          if (outcome.ok) { state.undo.push(before); }
          return outcome;
        });
      },
      /* Puts back the document as it was before the last edit, as a save of its own. */
      undo: function () {
        if (state.undo.length === 0) { return Promise.resolve({ ok: false }); }
        var previous = state.undo.pop();
        return save(previous).then(function (outcome) {
          if (!outcome.ok) { state.undo.push(previous); }
          return outcome;
        });
      },
      /* Adopts a document saved elsewhere: the undo stack no longer describes it. */
      adopt: function (document, revision) {
        var text = typeof document === "string" ? document : JSON.stringify(document);
        if (text !== state.text || revision !== state.revision) { state.undo = []; }
        state.text = text;
        state.revision = revision;
      }
    };
  }

  /* The canvas of a document by its id. */
  function canvasIn(document, id) {
    var canvases = document && Array.isArray(document.canvases) ? document.canvases : [];
    for (var i = 0; i < canvases.length; i++) {
      if (canvases[i] && canvases[i].id === id) { return canvases[i]; }
    }
    return null;
  }

  /* A widget id not yet taken on a canvas, from a type. */
  function freshWidgetId(canvas, type) {
    var taken = {};
    (canvas.widgets || []).forEach(function (widget) { taken[widget.id] = true; });
    var base = String(type).replace(/_/g, "-");
    for (var n = 1; ; n++) {
      var id = n === 1 ? base : base + "-" + n;
      if (!taken[id]) { return id; }
    }
  }

  /* The first row free below every card of a canvas. */
  function bottomRow(canvas) {
    return (canvas.widgets || []).reduce(function (lowest, widget) {
      var p = widget.placement || {};
      return Math.max(lowest, (Number(p.y) || 0) + (Number(p.h) || 0));
    }, 0);
  }

  opnview.canvasModel = {
    PERIODS: PERIODS.slice(),
    DEFAULT_PERIOD: DEFAULT_PERIOD,
    SCOPE_KINDS: SCOPE_KINDS.map(function (pair) { return pair.slice(); }),
    canvasPeriod: canvasPeriod,
    canvasScope: canvasScope,
    request: request,
    query: query,
    liveCadence: liveCadence,
    editor: editor,
    canvasIn: canvasIn,
    freshWidgetId: freshWidgetId,
    bottomRow: bottomRow
  };
})(this);
