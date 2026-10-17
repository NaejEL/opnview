/*
 * opnview front -- editing the dashboards, for the length of a session (F7-30 to F7-40).
 *
 * ONE DOCUMENT PER DASHBOARD, AS THE SERVER STORED IT. Each dashboard's document is kept as the
 * bytes GET /api/dashboards/{id} answered and the revision they carry. An edit changes a copy of the
 * document and saves it whole at the next revision (POST /api/dashboards/{id}); the bytes before it
 * go on the undo stack, so an undo sends them back unchanged and the server holds, byte for byte,
 * what it held before.
 *
 * ONE UNDO STACK FOR THE SESSION. Every edit a reader makes -- a move, a resize, an added or removed
 * widget, an Apply, a repair, a scope, a period, a canvas renamed, reordered, added or removed, a
 * dashboard created, imported, reordered or deleted -- is one entry, undone by the undo control or by
 * Ctrl/Cmd+Z, whatever view the reader is on. A deleted dashboard is put back by
 * POST /api/dashboards/restore with the answer its deletion gave.
 *
 * IMPORT NEVER BLOCKS ON A REFERENCE. A file that is structurally broken is refused whole, naming
 * the canvas, the widget, the field and the line (front/core/document.js); one that is whole is
 * stored as a new dashboard, and what it names that this installation does not have -- a reference
 * that matches nothing, a type this build does not serve, a key it does not know -- is collected
 * from the widget API's own envelopes into a report, grouped by canvas then widget. Nothing is
 * rewritten automatically.
 *
 * EXPORT IS FROM MEMORY. The stored document is written in YAML or JSON and handed to the browser as
 * a file, with no request: unknown keys and unresolved references go out as they came in.
 *
 * Nothing here carries a user-visible string; what it answers are codes the views translate.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var SYNTAX_KEY = "opnview.dock.syntax";
  var WIDTH_KEY = "opnview.dock.width";

  var state = {
    documents: {},
    undo: [],
    reports: {},
    mode: false,
    dock: { open: false, tab: "document", widget: null },
    notice: null,
    listeners: []
  };

  /* A new object holding the members of a, then those of b. */
  function merge(a, b) {
    var out = {};
    [a, b].forEach(function (from) { Object.keys(from || {}).forEach(function (key) { out[key] = from[key]; }); });
    return out;
  }

  function changed(what) {
    state.listeners.slice().forEach(function (listener) { listener(what); });
  }

  /* The bytes of the member "document" in a dashboard's answer: the server writes it last, as it
   * stored it (internal/web/surfaces/dashboards, writeWithDocument). */
  function storedText(answerText, document) {
    if (typeof answerText === "string") {
      var at = answerText.indexOf("\"document\":");
      if (at >= 0 && answerText.charAt(answerText.length - 1) === "}") {
        var bytes = answerText.slice(at + "\"document\":".length, -1);
        // Only what reads back as the very document answered is taken as its bytes.
        try {
          if (JSON.stringify(JSON.parse(bytes)) === JSON.stringify(document)) { return bytes; }
        } catch (e) { /* not the last member: written again below */ }
      }
    }
    return JSON.stringify(document);
  }

  function entry(id) { return state.documents[id] || null; }

  function parsed(record) {
    if (!record) { return null; }
    if (record.parsed === undefined) {
      try { record.parsed = JSON.parse(record.text); } catch (e) { record.parsed = null; }
    }
    return record.parsed;
  }

  /* Saves a document's text at the revision held. */
  function save(api, id, text) {
    var record = entry(id);
    if (!record) { return Promise.resolve({ ok: false, condition: "unknown_dashboard" }); }
    var body = "{\"revision\":" + JSON.stringify(record.revision) + ",\"document\":" + text + "}";
    return api.post("/api/dashboards/" + encodeURIComponent(id), api.raw(body)).then(function (result) {
      if (result.status === 200 && result.body) {
        state.documents[id] = { revision: result.body.revision, text: text };
        changed({ dashboard: id });
        return { ok: true };
      }
      var condition = result.body && result.body.condition ? result.body.condition : "read_failed";
      if (condition === "revision_conflict" && result.body && typeof result.body.revision === "number") {
        // Another tab saved meanwhile: what this one held no longer describes the stored document.
        return { ok: false, condition: condition, refusal: result.body };
      }
      return { ok: false, condition: condition, refusal: result.body };
    }, function () { return { ok: false, condition: "read_failed" }; });
  }

  function push(undoEntry) {
    state.undo.push(undoEntry);
    changed({ undo: true });
  }

  var editing = {
    SYNTAXES: ["yaml", "json"],

    /* What a view is told when a document, the undo stack or the dashboards change. */
    subscribe: function (listener) {
      state.listeners.push(listener);
      return function () {
        state.listeners = state.listeners.filter(function (other) { return other !== listener; });
      };
    },

    /* Holds a dashboard read from the server: result is the GET's {status, body, text}. A document
     * already held at the same revision is kept as it is. */
    adopt: function (id, result) {
      var body = result.body;
      var text = storedText(result.text, body.document);
      var held = entry(id);
      if (held && held.revision === body.revision && held.text === text) { return; }
      state.documents[id] = { revision: body.revision, text: text, origin: body.origin, source: body.source_name };
    },
    /* The document of a dashboard as held, parsed; null when none is held. */
    document: function (id) { return parsed(entry(id)); },
    text: function (id) { var record = entry(id); return record ? record.text : null; },
    revision: function (id) { var record = entry(id); return record ? record.revision : null; },
    forget: function (id) { delete state.documents[id]; },

    /* An edit: mutate(document) changes a copy, which is saved whole; the bytes before it are the
     * undo entry. A mutation that changes nothing saves nothing. */
    edit: function (api, id, mutate, label) {
      var record = entry(id);
      if (!record) { return Promise.resolve({ ok: false, condition: "unknown_dashboard" }); }
      var before = record.text;
      var next = JSON.parse(before);
      if (mutate(next) === false) { return Promise.resolve({ ok: true, unchanged: true }); }
      var text = JSON.stringify(next);
      if (text === JSON.stringify(JSON.parse(before))) { return Promise.resolve({ ok: true, unchanged: true }); }
      return save(api, id, text).then(function (outcome) {
        if (outcome.ok) { push({ kind: "document", dashboard: id, before: before, label: label || "edit" }); }
        return outcome;
      });
    },
    /* Replaces a document whole -- the code view's Apply -- as one undoable edit. */
    replace: function (api, id, document, label) {
      return editing.edit(api, id, function (copy) {
        Object.keys(copy).forEach(function (key) { delete copy[key]; });
        Object.keys(document).forEach(function (key) { copy[key] = JSON.parse(JSON.stringify(document[key])); });
      }, label || "apply");
    },

    canUndo: function () { return state.undo.length > 0; },
    /* The labels an edit is recorded under, each with its words in the catalogue (undo.what.*), so
     * the undo control can say what it will undo. */
    UNDO_LABELS: ["add", "add_canvas", "apply", "create", "create_dashboard", "delete", "delete_dashboard", "edit", "form", "import", "move", "move_canvas", "order", "period", "placement", "remove", "remove_canvas", "rename_canvas", "rename_dashboard", "resize", "scope"],
    /* What the undo control will undo, in words: its tooltip. */
    undoTitle: function () {
      var label = state.undo.length ? state.undo[state.undo.length - 1].label : null;
      if (!label) { return opnview.strings.t("action.undo"); }
      return opnview.strings.tk("undo.what.", editing.UNDO_LABELS.indexOf(label) >= 0 ? label : "edit");
    },
    /* The label of the edit the next undo reverts. */
    undoLabel: function () { return state.undo.length ? state.undo[state.undo.length - 1].label : null; },
    /* Reverts the last edit of the session. */
    undo: function (api) {
      var last = state.undo.pop();
      if (!last) { return Promise.resolve({ ok: false, condition: "nothing_to_undo" }); }
      var done;
      if (last.kind === "document") {
        done = save(api, last.dashboard, last.before);
      } else if (last.kind === "created") {
        done = api.post("/api/dashboards/" + encodeURIComponent(last.dashboard) + "/delete", {}).then(function (result) {
          if (result.status !== 200) { return { ok: false, condition: (result.body && result.body.condition) || "read_failed" }; }
          editing.forget(last.dashboard);
          delete state.reports[last.dashboard];
          return { ok: true };
        });
      } else if (last.kind === "deleted") {
        done = api.post("/api/dashboards/restore", api.raw(last.answer)).then(function (result) {
          if (result.status !== 200 || !result.body) {
            return { ok: false, condition: (result.body && result.body.condition) || "read_failed" };
          }
          state.documents[result.body.id] = { revision: result.body.revision,
            text: storedText(result.text, result.body.document) };
          return { ok: true };
        });
      } else if (last.kind === "order") {
        done = api.post("/api/dashboards/order", { ids: last.before }).then(function (result) {
          return result.status === 200 ? { ok: true } : { ok: false, condition: (result.body && result.body.condition) || "read_failed" };
        });
      } else {
        done = Promise.resolve({ ok: false, condition: "nothing_to_undo" });
      }
      return done.then(function (outcome) {
        if (!outcome.ok) { state.undo.push(last); }
        state.notice = null;
        changed({ undo: true, dashboards: last.kind !== "document", dashboard: last.dashboard });
        outcome.entry = last;
        return outcome;
      });
    },

    /* Creates a dashboard -- a new one, an import -- as one undoable edit. */
    create: function (api, document, origin, sourceName, label) {
      return api.post("/api/dashboards", { document: document, origin: origin || "created",
        source_name: sourceName || null }).then(function (result) {
        if (result.status !== 200 || !result.body) {
          return { ok: false, condition: (result.body && result.body.condition) || "read_failed", refusal: result.body };
        }
        var id = result.body.id;
        state.documents[id] = { revision: result.body.revision, text: storedText(result.text, result.body.document),
          origin: result.body.origin, source: result.body.source_name };
        push({ kind: "created", dashboard: id, label: label || "create" });
        changed({ dashboards: true, dashboard: id });
        return { ok: true, id: id, dashboard: result.body };
      }, function () { return { ok: false, condition: "read_failed" }; });
    },
    /* Deletes a dashboard; the undo restores it from the deletion's answer. */
    remove: function (api, id, label) {
      return api.post("/api/dashboards/" + encodeURIComponent(id) + "/delete", {}).then(function (result) {
        if (result.status !== 200 || !result.body) {
          return { ok: false, condition: (result.body && result.body.condition) || "read_failed" };
        }
        editing.forget(id);
        delete state.reports[id];
        push({ kind: "deleted", dashboard: id, answer: result.text, label: label || "delete" });
        changed({ dashboards: true, dashboard: id });
        return { ok: true };
      }, function () { return { ok: false, condition: "read_failed" }; });
    },
    /* Gives the dashboards a new order; before is the order held. */
    order: function (api, ids, before) {
      return api.post("/api/dashboards/order", { ids: ids }).then(function (result) {
        if (result.status !== 200) { return { ok: false, condition: (result.body && result.body.condition) || "read_failed" }; }
        push({ kind: "order", before: before, label: "order" });
        changed({ dashboards: true });
        return { ok: true };
      });
    },

    /* The one transient notice a view shows: a removal with its undo, a refusal. */
    notice: function () { return state.notice; },
    setNotice: function (notice) { state.notice = notice; },

    /* Edit mode, and the dock. */
    editMode: function () { return state.mode; },
    setEditMode: function (on) { state.mode = !!on; changed({ mode: true }); },
    dock: function () { return state.dock; },
    openDock: function (tab, widget) {
      state.dock.open = true;
      if (tab) { state.dock.tab = tab; }
      if (widget !== undefined) { state.dock.widget = widget; }
      changed({ dock: true });
    },
    closeDock: function () { state.dock.open = false; changed({ dock: true }); },
    syntax: function () {
      var stored = opnview.util.storage.get(SYNTAX_KEY);
      return stored === "json" ? "json" : "yaml";
    },
    setSyntax: function (syntax) { opnview.util.storage.set(SYNTAX_KEY, syntax === "json" ? "json" : "yaml"); },
    dockWidth: function () {
      var stored = Number(opnview.util.storage.get(WIDTH_KEY));
      return isFinite(stored) && stored >= 320 ? stored : null;
    },
    setDockWidth: function (width) { opnview.util.storage.set(WIDTH_KEY, String(Math.round(width))); },

    /* -------------------------------------------------------------------------------------------
     * Import, its report, and export.
     * ----------------------------------------------------------------------------------------- */

    /* Reads a file's text: a structural refusal ({error}), or the document to store. */
    read: function (text) {
      /* A file is never trusted input: whatever it makes the reader throw is a refusal of the file,
         named, and never an error escaping into the view that asked. */
      try {
        return opnview.document.load(text, function (type) { return opnview.widgets.row(type); });
      } catch (error) {
        return { error: { condition: "document_unreadable", line: null, canvas_index: null, widget_index: null,
          field: null, path: null } };
      }
    },

    /* Stores a read document as an imported dashboard and collects its report. */
    importDocument: function (api, document, sourceName) {
      return editing.create(api, document, "imported", sourceName, "import").then(function (created) {
        if (!created.ok) { return created; }
        return editing.collect(api, document).then(function (rows) {
          state.reports[created.id] = { source: sourceName || null, rows: rows };
          changed({ report: created.id });
          created.rows = rows;
          return created;
        });
      });
    },

    report: function (id) { return state.reports[id] || null; },
    dismissReport: function (id) { delete state.reports[id]; changed({ report: id }); },
    /* Collects the report of a dashboard again, after a repair: a row repaired is gone. */
    recollect: function (api, id) {
      var held = state.reports[id];
      var document = editing.document(id);
      if (!held || !document) { return Promise.resolve(null); }
      return editing.collect(api, document).then(function (rows) {
        held.rows = rows;
        changed({ report: id });
        return rows;
      });
    },

    /* What a document names that this installation does not have, by the widget API's own rules:
     * every widget asked with its own parameters, every canvas scope resolved. Rows: {canvas_index,
     * canvas_id, canvas_title, widget_index, widget_id, widget_title, widget_type, condition, reason,
     * path, kind, by, value, label}. */
    collect: function (api, document) {
      var rows = [];
      var asks = [];
      (document.canvases || []).forEach(function (canvas, ci) {
        var base = { canvas_index: ci, canvas_id: canvas.id, canvas_title: canvas.title };
        var scope = canvas.scope && typeof canvas.scope === "object" ? canvas.scope : {};
        var scoped = [];
        ["interfaces", "clients", "owners"].forEach(function (key) {
          (Array.isArray(scope[key]) ? scope[key] : []).forEach(function (reference, index) {
            scoped.push({ reference: reference, path: "canvases[" + ci + "].scope." + key + "[" + index + "]" });
          });
        });
        if (scoped.length) {
          asks.push(api.get("/api/references/resolve?references=" +
            encodeURIComponent(JSON.stringify(scoped.map(function (item) { return item.reference; })))).then(function (result) {
            var answered = result.status === 200 && result.body ? result.body.references || [] : [];
            answered.forEach(function (resolved) {
              if (resolved.state === "resolved") { return; }
              var item = scoped[resolved.index];
              if (!item) { return; }
              var reference = item.reference || {};
              rows.push(merge(base, { widget_index: null, widget_id: null, widget_title: null,
                condition: "unresolved_reference", reason: resolved.state, path: item.path, kind: reference.kind,
                by: reference.by, value: reference.value, label: reference.label }));
            });
          }));
        }
        (Array.isArray(canvas.widgets) ? canvas.widgets : []).forEach(function (widget, wi) {
          if (!widget || typeof widget !== "object") { return; }
          var at = "canvases[" + ci + "].widgets[" + wi + "]";
          var about = { canvas_index: ci, canvas_id: canvas.id, canvas_title: canvas.title, widget_index: wi,
            widget_id: widget.id, widget_title: widget.title || null, widget_type: widget.type || widget.shape };
          var add = function (row) { rows.push(merge(about, row)); };
          if (!widget.type) {
            add({ condition: "shape_not_served", path: at + ".shape", kind: "shape", by: "shape", value: widget.shape,
              label: widget.title || null });
            return;
          }
          if (!opnview.widgets.row(widget.type)) {
            add({ condition: "unknown_widget_type", path: at + ".type", kind: "widget_type", by: "type", value: widget.type,
              label: widget.title || null });
            return;
          }
          var parameters = widget.parameters && typeof widget.parameters === "object" ? widget.parameters : {};
          asks.push(api.get("/api/widget/" + encodeURIComponent(widget.type) +
            opnview.canvasModel.query(parameters)).then(function (result) {
            var answer = result.body;
            if (!answer || typeof answer !== "object") { return; }
            editing.issuesOf(answer).forEach(function (issue) {
              issue.path = at + ".parameters." + issue.path;
              add(issue);
            });
          }));
        });
      });
      return Promise.all(asks).then(function () {
        rows.sort(function (a, b) {
          return a.canvas_index - b.canvas_index ||
            (a.widget_index === null ? -1 : a.widget_index) - (b.widget_index === null ? -1 : b.widget_index) ||
            (a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
        });
        return rows;
      });
    },

    /* The issues one widget envelope reports, each with its path inside the widget's parameters:
     * unresolved references (a series' own included), keys kept and not understood, values that could
     * not be applied. */
    issuesOf: function (answer) {
      var issues = [];
      var seen = {};
      var addReference = function (reference) {
        if (!reference || seen["r:" + reference.path]) { return; }
        seen["r:" + reference.path] = true;
        issues.push({ condition: "unresolved_reference", reason: reference.reason || null, path: reference.path,
          kind: reference.kind, by: reference.by, value: reference.value, label: reference.label });
      };
      (answer.unresolved_references || []).forEach(addReference);
      var data = answer.data;
      if (data && Array.isArray(data.series)) {
        data.series.forEach(function (series) { (series && series.unresolved_references || []).forEach(addReference); });
      }
      var unknown = answer.unknown_parameters;
      if (unknown && typeof unknown === "object") {
        Object.keys(unknown).forEach(function (path) {
          issues.push({ condition: "unknown_parameter_key", reason: null, path: path, kind: "parameter", by: "key",
            value: path, label: unknown[path] });
        });
      }
      (answer.invalid_parameters || []).forEach(function (invalid) {
        issues.push({ condition: "invalid_parameter", reason: invalid.reason || null, path: invalid.path,
          kind: "parameter", by: "key", value: invalid.path, label: invalid.value });
      });
      return issues;
    },

    /* A dashboard written in a syntax, for export: {name, text, type}. */
    exported: function (document, syntax) {
      var written = opnview.document.serialise(document, syntax === "json" ? "json" : "yaml");
      var title = typeof document.title === "string" && document.title ? document.title : "dashboard";
      var slug = title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "dashboard";
      return { name: slug + (syntax === "json" ? ".json" : ".yaml"), text: /\n$/.test(written.text) ? written.text : written.text + "\n",
        type: syntax === "json" ? "application/json" : "application/yaml" };
    },
    /* Hands a file to the browser, from memory: no request is made. */
    download: function (file) {
      var document = root.document;
      if (!root.Blob || !root.URL || !root.URL.createObjectURL || !document || !document.createElement) { return false; }
      var url = root.URL.createObjectURL(new root.Blob([file.text], { type: file.type }));
      var link = document.createElement("a");
      link.setAttribute("href", url);
      link.setAttribute("download", file.name);
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      root.setTimeout(function () { root.URL.revokeObjectURL(url); }, 0);
      return true;
    },

    /* For the tests: the session's state, reset. */
    reset: function () {
      state.documents = {};
      state.undo = [];
      state.reports = {};
      state.mode = false;
      state.dock = { open: false, tab: "document", widget: null };
      state.notice = null;
    }
  };

  opnview.editing = editing;
})(this);
