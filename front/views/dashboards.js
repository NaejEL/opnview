/*
 * opnview front -- importing, exporting and managing dashboards (F7-33, F7-38, F7-40).
 *
 * IMPORT. A file from the picker or a paste, JSON or YAML detected from the content
 * (docs/dashboard-format.md, "JSON and YAML"). A structurally broken file is refused whole, naming
 * the canvas index, the widget index, the field and the line, and nothing is stored. A whole one is
 * stored as a new dashboard of origin `imported`, and the reader lands on its first canvas with the
 * import report: what the file names that this installation does not have, never rewritten.
 *
 * EXPORT. One dashboard per file, in YAML or JSON, chosen at export and preset from the editor's
 * syntax radio, written from memory and handed to the browser with no request.
 *
 * MANAGING. Create, rename, reorder and delete a dashboard, each undoable: a deletion offers its
 * undo, which restores the dashboard from the deletion's own answer.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk;

  function editing() { return opnview.editing; }

  /* Reads every dashboard's document, for export and renaming. */
  function load(context) {
    return Promise.all([context.reloadDashboards(), opnview.widgets.load(context.api)]).then(function (loaded) {
      var dashboards = loaded[0] || [];
      context.state.loaded = true;
      return Promise.all(dashboards.map(function (dashboard) {
        return context.api.get("/api/dashboards/" + encodeURIComponent(dashboard.id)).then(function (result) {
          if (result.status === 200 && result.body) { editing().adopt(dashboard.id, result); }
        });
      }));
    });
  }

  function refusalText(error) { return opnview.dock.refusalText(error); }

  /* -----------------------------------------------------------------------------------------------
   * Import.
   * --------------------------------------------------------------------------------------------- */

  function importText(context) {
    var state = context.state;
    var text = state.importText || "";
    state.refusal = null;
    var read = editing().read(text);
    if (read.error) {
      state.refusal = read.error;
      context.rerender();
      return Promise.resolve(null);
    }
    if (read.comments && !state.confirmComments) {
      state.confirmComments = true;
      context.rerender();
      return Promise.resolve(null);
    }
    state.confirmComments = false;
    state.importing = true;
    context.rerender();
    return editing().importDocument(context.api, read.document, state.fileName || null).then(function (outcome) {
      state.importing = false;
      if (!outcome.ok) {
        state.refusal = outcome.refusal && outcome.refusal.condition ? outcome.refusal : { condition: outcome.condition };
        context.rerender();
        return null;
      }
      state.importText = "";
      state.fileName = null;
      var document = editing().document(outcome.id);
      var first = document && document.canvases && document.canvases[0] ? document.canvases[0].id : "";
      return context.reloadDashboards().then(function () {
        return context.go("/canvas/" + encodeURIComponent(outcome.id) + "/" + encodeURIComponent(first));
      });
    });
  }

  function chooseFile(context, input) {
    var state = context.state;
    var file = input && input.files && input.files[0];
    if (!file || !root.FileReader) { return; }
    var reader = new root.FileReader();
    reader.onload = function () {
      state.importText = String(reader.result || "");
      state.fileName = file.name;
      state.refusal = null;
      state.confirmComments = false;
      context.rerender();
    };
    reader.readAsText(file);
  }

  function importView(context) {
    var state = context.state;
    return h("section", { key: "import", "class": "panel-card", id: "import-panel" }, [
      h("div", { key: "head", "class": "panel-card-head" }, h("h2", null, t("import.title"))),
      h("div", { key: "file", "class": "form-field" }, [
        h("label", { "for": "import-file" }, t("import.file")),
        h("input", { id: "import-file", type: "file", accept: ".json,.yaml,.yml,application/json,application/yaml,text/yaml",
          on: { change: function (event) { chooseFile(context, event.target); } } })
      ]),
      h("div", { key: "paste", "class": "form-field import-paste" }, [
        h("label", { "for": "import-text" }, t("import.paste")),
        h("textarea", { id: "import-text", "class": "code-input-plain mono", rows: "12", spellcheck: "false",
          value: state.importText || "", on: { input: function (event) {
            var wasEmpty = !(state.importText || "").trim();
            var hadNotice = !!state.refusal || !!state.confirmComments || !!state.fileName;
            state.importText = event.target.value;
            state.fileName = null;
            state.refusal = null;
            state.confirmComments = false;
            // The Import button follows whether there is anything to import; a notice about the
            // text before goes with it.
            if (hadNotice || wasEmpty !== !state.importText.trim()) { context.rerender(); }
          } } })
      ]),
      state.fileName ? h("p", { key: "name", "class": "hint mono" }, state.fileName) : null,
      state.confirmComments ? h("p", { key: "comments", "class": "code-warning", role: "alert" }, t("dock.comments_lost")) : null,
      h("div", { key: "actions", "class": "dock-actions" }, [
        h("button", { type: "button", id: "import-submit", "class": "btn btn-primary",
          disabled: !(state.importText || "").trim() || !!state.importing, on: { click: function () { importText(context); } } },
        state.confirmComments ? t("import.submit_losing_comments") : t("import.submit"))
      ]),
      state.refusal ? h("p", { key: "refusal", id: "import-refusal", "class": "code-error", role: "alert",
        "data-condition": state.refusal.condition }, refusalText(state.refusal)) : null
    ]);
  }

  /* -----------------------------------------------------------------------------------------------
   * Export and management.
   * --------------------------------------------------------------------------------------------- */

  function exportOne(context, dashboard) {
    var state = context.state;
    var document = editing().document(dashboard.id);
    if (!document) { return null; }
    var syntax = (state.exportSyntax || {})[dashboard.id] || editing().syntax();
    var file = editing().exported(document, syntax);
    state.lastExport = file;
    editing().download(file);
    context.rerender();
    return file;
  }

  function rename(context, dashboard, title) {
    var trimmed = String(title || "").trim();
    if (!trimmed) { return Promise.resolve(); }
    return editing().edit(context.api, dashboard.id, function (document) {
      if (document.title === trimmed) { return false; }
      document.title = trimmed;
      return true;
    }, "rename_dashboard").then(function (outcome) { return after(context, outcome); });
  }

  function move(context, index, by) {
    var dashboards = context.dashboards();
    var target = index + by;
    if (target < 0 || target >= dashboards.length) { return Promise.resolve(); }
    var before = dashboards.map(function (dashboard) { return dashboard.id; });
    var ids = before.slice();
    var moved = ids.splice(index, 1)[0];
    ids.splice(target, 0, moved);
    return editing().order(context.api, ids, before).then(function (outcome) { return after(context, outcome); });
  }

  function remove(context, dashboard) {
    return editing().remove(context.api, dashboard.id, "delete_dashboard").then(function (outcome) {
      if (outcome.ok) {
        editing().setNotice({ kind: "dashboard_removed", params: { name: String(dashboard.title || dashboard.id) }, undo: true });
      }
      return after(context, outcome);
    });
  }

  function create(context) {
    var document = { format_version: 1, title: t("dashboard.new_title"),
      canvases: [{ id: "canvas-1", title: t("canvas.new_title"), widgets: [] }] };
    return editing().create(context.api, document, "created", null, "create_dashboard").then(function (outcome) {
      if (!outcome.ok) { return after(context, outcome); }
      return context.reloadDashboards().then(function () {
        return context.go("/canvas/" + encodeURIComponent(outcome.id) + "/canvas-1");
      });
    });
  }

  function after(context, outcome) {
    context.state.failure = outcome && !outcome.ok ? outcome.condition || "read_failed" : null;
    return context.reloadDashboards().then(function () { context.rerender(); });
  }

  function undo(context) {
    if (!editing().canUndo()) { return Promise.resolve(); }
    return editing().undo(context.api).then(function (outcome) {
      context.state.failure = outcome.ok ? null : outcome.condition || "read_failed";
      return load(context);
    }).then(function () { context.rerender(); });
  }

  function listView(context) {
    var state = context.state;
    var dashboards = context.dashboards();
    var rows = dashboards.map(function (dashboard, index) {
      var syntax = (state.exportSyntax || {})[dashboard.id] || editing().syntax();
      var titleId = "dashboard-title-" + dashboard.id;
      var syntaxId = "dashboard-syntax-" + dashboard.id;
      return h("tr", { key: "d" + dashboard.id, "data-dashboard": String(dashboard.id) }, [
        h("td", { key: "title" }, [
          h("label", { key: "label", "class": "visually-hidden", "for": titleId }, t("dashboards.title_of")),
          h("input", { key: "input", id: titleId, type: "text", "class": "form-input", value: dashboard.title || "",
            on: { change: function (event) { rename(context, dashboard, event.target.value); } } })
        ]),
        h("td", { key: "canvases" }, (dashboard.canvases || []).map(function (canvas, ci) {
          var path = "/canvas/" + encodeURIComponent(dashboard.id) + "/" + encodeURIComponent(canvas.id);
          return [ci ? ", " : null, h("a", { key: "c" + ci, href: path, "data-route": path }, canvas.title || canvas.id)];
        })),
        h("td", { key: "origin" }, [h("span", { key: "o", "class": "badge" }, tk("dashboards.origin.", dashboard.origin)),
          dashboard.source_name ? [" ", h("span", { key: "s", "class": "mono" }, dashboard.source_name)] : null]),
        h("td", { key: "order" }, [
          h("button", { key: "up", type: "button", "class": "btn btn-small", "data-move": "up", disabled: index === 0,
            "aria-label": t("dashboards.move_up", { name: String(dashboard.title || dashboard.id) }),
            on: { click: function () { move(context, index, -1); } } }, t("edit.face.up")),
          h("button", { key: "down", type: "button", "class": "btn btn-small", "data-move": "down", disabled: index === dashboards.length - 1,
            "aria-label": t("dashboards.move_down", { name: String(dashboard.title || dashboard.id) }),
            on: { click: function () { move(context, index, 1); } } }, t("edit.face.down"))
        ]),
        h("td", { key: "export" }, [
          h("label", { key: "label", "class": "visually-hidden", "for": syntaxId }, t("dashboards.syntax")),
          h("select", { key: "syntax", id: syntaxId, "class": "btn", value: syntax, on: { change: function (event) {
            state.exportSyntax = state.exportSyntax || {};
            state.exportSyntax[dashboard.id] = event.target.value;
            context.rerender();
          } } }, editing().SYNTAXES.map(function (name) {
            return h("option", { key: name, value: name, selected: name === syntax }, tk("dock.syntax.", name));
          })),
          h("button", { key: "go", type: "button", "class": "btn btn-small", "data-export": String(dashboard.id),
            on: { click: function () { exportOne(context, dashboard); } } }, t("dashboards.export"))
        ]),
        h("td", { key: "delete" }, h("button", { type: "button", "class": "btn btn-small btn-destructive",
          "data-delete": String(dashboard.id), on: { click: function () { remove(context, dashboard); } } },
        t("dashboards.delete")))
      ]);
    });
    return h("section", { key: "list", "class": "panel-card", id: "dashboards-panel" }, [
      h("div", { key: "head", "class": "panel-card-head" }, [
        h("h2", { key: "title" }, t("dashboards.list")),
        h("button", { key: "new", type: "button", "class": "btn btn-small", id: "dashboard-new",
          on: { click: function () { create(context); } } }, t("dashboards.new")),
        h("button", { key: "undo", type: "button", "class": "btn btn-small", id: "dashboards-undo",
          disabled: !editing().canUndo(), on: { click: function () { undo(context); } } }, t("action.undo"))
      ]),
      opnview.editChrome.notice(editing().notice(), function () { undo(context); },
        function () { editing().setNotice(null); context.rerender(); }),
      state.failure ? h("p", { key: "failure", "class": "field-error", role: "status" }, tk("error.", state.failure)) : null,
      dashboards.length === 0
        ? h("div", { key: "none", "class": "state state-gap" }, h("span", { "class": "state-badge" }, t("canvas.state.no_dashboard")))
        : h("div", { key: "table", "class": "table-scroll" }, h("table", { "class": "data" }, [
          h("thead", { key: "head" }, h("tr", null, [
            h("th", { key: "t" }, t("dashboards.column.title")), h("th", { key: "c" }, t("dashboards.column.canvases")),
            h("th", { key: "o" }, t("dashboards.column.origin")), h("th", { key: "p" }, t("dashboards.column.order")),
            h("th", { key: "e" }, t("dashboards.column.export")), h("th", { key: "d" }, t("dashboards.column.delete"))
          ])),
          h("tbody", { key: "body" }, rows)
        ])),
      state.lastExport ? h("p", { key: "exported", "class": "hint", role: "status" },
        t("dashboards.exported", { name: state.lastExport.name })) : null
    ]);
  }

  function keydown(context, event) {
    var target = event.target;
    var editable = target && target.tagName && /^(INPUT|TEXTAREA|SELECT)$/i.test(String(target.tagName));
    if ((event.ctrlKey || event.metaKey) && !event.shiftKey && String(event.key).toLowerCase() === "z" && !editable) {
      if (event.preventDefault) { event.preventDefault(); }
      undo(context);
      return true;
    }
    return false;
  }

  opnview.views.register({
    id: "import",
    path: "/dashboards/import",
    rail: "dashboard",
    order: 10,
    title: function () { return t("import.title"); },
    load: function (context) { return opnview.widgets.load(context.api); },
    render: function (context) { return importView(context); },
    keydown: keydown
  });

  opnview.views.register({
    id: "dashboards",
    path: "/dashboards",
    rail: "dashboard",
    order: 20,
    title: function () { return t("dashboards.title"); },
    railTitle: function () { return t("rail.export"); },
    load: load,
    render: function (context) {
      if (!context.state.loaded) { return h("p", { "class": "loading", role: "status" }, t("state.loading")); }
      return listView(context);
    },
    keydown: keydown
  });

  /* What the tests reach. */
  opnview.dashboardsView = { exportOne: exportOne };
})(this);
