/*
 * opnview front -- the canvas's editing chrome: edit mode, the picker, canvas management and the
 * import report (F7-30, F7-31, F7-33, F7-38).
 *
 * EDIT MODE IS HOMARR'S MECHANICS, NONE OF ITS ROUNDING (docs/ui-references.md). A card is dragged to
 * move it and dragged at its right edge, bottom edge or corner to resize it; every gesture writes the
 * widget's own `placement` -- the authored twelve-column one, never the derived layout -- and buttons
 * named by the card's heading do the same from the keyboard. An explicit "add a widget here" button
 * opens the picker.
 *
 * THE PICKER EXPLAINS. Every type GET /api/widgets serves, with its title, its question and its shape:
 * a chooser may explain, where a card may not. A new widget starts at its manifest's defaults.
 *
 * THE IMPORT REPORT is a card of its own above the grid, grouped by canvas then widget, each row
 * linking to the repair. It is not part of the document and closing it changes nothing.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk, td = opnview.strings.td;

  /* The smallest card the buttons and the grips make, in columns and rows. */
  var MIN_W = 2, MIN_H = 3;
  /* Where a new widget lands, and its size. */
  var NEW_W = 6, NEW_H = 8;

  var MOVES = [["move", "left"], ["move", "right"], ["move", "up"], ["move", "down"],
    ["size", "narrower"], ["size", "wider"], ["size", "shorter"], ["size", "taller"]];

  /* A placement moved or resized by one step, within the authored columns. */
  function stepped(placement, action, columns) {
    var p = { x: placement.x, y: placement.y, w: placement.w, h: placement.h };
    switch (action) {
      case "left": if (p.x > 0) { p.x -= 1; } break;
      case "right": if (p.x + p.w < columns) { p.x += 1; } break;
      case "up": if (p.y > 0) { p.y -= 1; } break;
      case "down": p.y += 1; break;
      case "narrower": if (p.w > MIN_W) { p.w -= 1; } break;
      case "wider": if (p.x + p.w < columns) { p.w += 1; } break;
      case "shorter": if (p.h > MIN_H) { p.h -= 1; } break;
      case "taller": p.h += 1; break;
    }
    return p;
  }

  /* A placement dragged by dx columns and dy rows from where the gesture began. */
  function dragged(start, mode, dx, dy, columns) {
    var p = { x: start.x, y: start.y, w: start.w, h: start.h };
    if (mode === "move") {
      p.x = Math.max(0, Math.min(columns - start.w, start.x + dx));
      p.y = Math.max(0, start.y + dy);
      return p;
    }
    if (mode === "e" || mode === "se") { p.w = Math.max(MIN_W, Math.min(columns - start.x, start.w + dx)); }
    if (mode === "s" || mode === "se") { p.h = Math.max(MIN_H, start.h + dy); }
    return p;
  }

  /* The edit bar of one card: where it is, and a button per step, each named by the card's heading. */
  function bar(widget, name, onStep, onRemove) {
    var p = widget.placement || {};
    return h("div", { key: "edit-bar", "class": "edit-bar", "data-edit-bar": widget.id }, [
      h("span", { key: "where", "class": "grip mono" }, [String(p.x), ",", String(p.y), " ", String(p.w), "×", String(p.h)]),
      MOVES.map(function (pair) {
        var label = pair[0] === "move" ? tk("edit.move.", pair[1], { name: name }) : tk("edit.size.", pair[1], { name: name });
        return h("button", { key: pair[1], type: "button", "class": "btn btn-small", "data-edit": pair[1],
          "aria-label": label, title: label, on: { click: function () { onStep(pair[1]); } } }, tk("edit.face.", pair[1]));
      }),
      h("button", { key: "remove", type: "button", "class": "btn btn-small btn-destructive", "data-edit": "remove",
        "aria-label": t("edit.remove", { name: name }), title: t("edit.remove", { name: name }),
        on: { click: function () { onRemove(); } } }, t("edit.remove_face"))
    ]);
  }

  function grips(widget) {
    return [
      h("span", { key: "grip-e", "class": "grip-edge grip-e", "data-grip": "e", "data-widget": widget.id, "aria-hidden": "true" }),
      h("span", { key: "grip-s", "class": "grip-edge grip-s", "data-grip": "s", "data-widget": widget.id, "aria-hidden": "true" }),
      h("span", { key: "grip-se", "class": "grip-corner", "data-grip": "se", "data-widget": widget.id, "aria-hidden": "true" })
    ];
  }

  /* The parameters a new widget starts with: its manifest's defaults, and nothing else. */
  function defaults(row) {
    var parameters = {};
    (row.parameters || []).forEach(function (parameter) {
      if (parameter["default"] !== null && parameter["default"] !== undefined) {
        parameters[parameter.key] = JSON.parse(JSON.stringify(parameter["default"]));
      }
    });
    return parameters;
  }

  /* A widget of a type, at the bottom of a canvas. */
  function newWidget(canvas, row) {
    return { id: opnview.canvasModel.freshWidgetId(canvas, row.type), type: row.type,
      placement: { x: 0, y: opnview.canvasModel.bottomRow(canvas), w: NEW_W, h: NEW_H }, parameters: defaults(row) };
  }

  function titleOf(row) {
    return row.title_key && opnview.strings.has(row.title_key) ? td(row.title_key) : row.type;
  }

  /* The picker: every served type, its title, its question and its shape. */
  function picker(onPick, onClose) {
    var rows = opnview.widgets.rows().slice().sort(function (a, b) {
      var ta = titleOf(a), tb = titleOf(b);
      return ta < tb ? -1 : ta > tb ? 1 : 0;
    });
    return h("div", { key: "picker", "class": "picker-backdrop", on: { click: function (event) {
      if (event.target === event.currentTarget) { onClose(); } } } }, [
      h("div", { "class": "picker", role: "dialog", "aria-modal": "true", "aria-labelledby": "picker-title" }, [
        h("div", { key: "head", "class": "panel-head" }, [
          h("h2", { id: "picker-title", "class": "panel-title" }, t("picker.title")),
          h("button", { type: "button", id: "picker-close", "class": "btn btn-small", on: { click: onClose } }, t("action.close"))
        ]),
        h("div", { key: "list", "class": "picker-list" }, rows.map(function (row) {
          var question = row.question_key && opnview.strings.has(row.question_key) ? td(row.question_key) : null;
          return h("button", { key: row.type, type: "button", "class": "picker-item", "data-pick": row.type,
            on: { click: function () { onPick(row); } } }, [
            h("span", { key: "name", "class": "pi-name" }, titleOf(row)),
            question ? h("span", { key: "question", "class": "pi-question" }, question) : null,
            h("span", { key: "shape", "class": "pi-shape" }, [
              h("span", { key: "label", "class": "badge" }, tk("shape.name.", row.shape)),
              " ",
              h("span", { key: "type", "class": "pi-type" }, row.type)
            ])
          ]);
        }))
      ])
    ]);
  }

  /* The edit-mode controls of the canvas bar: add, rename, move and remove a canvas. */
  function manage(options) {
    var canvas = options.canvas;
    var count = options.count;
    var index = options.index;
    return h("div", { key: "manage", "class": "canvas-manage", role: "group", "aria-label": t("canvas.manage.label") }, [
      h("label", { key: "label", "for": "canvas-title-input" }, t("canvas.manage.title")),
      h("input", { key: "title", id: "canvas-title-input", type: "text", "class": "form-input", value: canvas.title,
        on: { keydown: function (event) {
          if (event.key === "Enter") { options.onRename(event.target.value); }
        }, change: function (event) { options.onRename(event.target.value); } } }),
      h("button", { key: "left", type: "button", "class": "btn btn-small", id: "canvas-move-left", disabled: index === 0,
        on: { click: function () { options.onMove(-1); } } }, t("canvas.manage.move_left")),
      h("button", { key: "right", type: "button", "class": "btn btn-small", id: "canvas-move-right", disabled: index >= count - 1,
        on: { click: function () { options.onMove(1); } } }, t("canvas.manage.move_right")),
      h("button", { key: "add", type: "button", "class": "btn btn-small", id: "canvas-add",
        on: { click: function () { options.onAdd(); } } }, t("canvas.manage.add")),
      count > 1 ? h("button", { key: "remove", type: "button", "class": "btn btn-small btn-destructive", id: "canvas-remove",
        on: { click: function () { options.onRemove(); } } }, t("canvas.manage.remove")) : null
    ]);
  }

  /* The import report: grouped by canvas, then by widget, each row linking to its repair. */
  function report(options) {
    var held = options.report;
    var groups = [];
    var byCanvas = {};
    held.rows.forEach(function (row) {
      var group = byCanvas[row.canvas_index];
      if (!group) {
        group = byCanvas[row.canvas_index] = { canvas_index: row.canvas_index, canvas_id: row.canvas_id,
          canvas_title: row.canvas_title, rows: [] };
        groups.push(group);
      }
      group.rows.push(row);
    });
    var body = [];
    groups.forEach(function (group) {
      body.push(h("tr", { key: "c" + group.canvas_index, "class": "group-row" }, [
        h("td", { colspan: "5" }, [t("report.canvas", { title: String(group.canvas_title || "") }), " ",
          h("span", { key: "id", "class": "ident mono" }, String(group.canvas_id))])
      ]));
      group.rows.forEach(function (row, index) {
        var reference = [row.kind, row.by, typeof row.value === "string" ? row.value : JSON.stringify(row.value)].join(" · ");
        var label = row.label === null || row.label === undefined ? "" : typeof row.label === "string" ? row.label : JSON.stringify(row.label);
        body.push(h("tr", { key: "r" + group.canvas_index + "-" + index, "data-report-path": row.path,
          "data-report-condition": row.condition }, [
          h("td", { key: "widget" }, row.widget_id === null ? t("report.canvas_scope") : [
            row.widget_title ? String(row.widget_title) : options.titleOf(row.widget_type),
            h("div", { key: "id", "class": "ident mono" }, String(row.widget_id))
          ]),
          h("td", { key: "condition" }, h("span", { "class": "badge " + (row.condition === "unknown_parameter_key" ? "badge-kept" : "badge-ref") },
            tk("report.condition.", row.condition))),
          h("td", { key: "reference", "class": "mono" }, reference),
          h("td", { key: "label" }, label),
          h("td", { key: "repair" }, h("button", { type: "button", "class": "link-button", "data-repair": row.path,
            on: { click: function () { options.onRepair(row); } } }, t("report.repair")))
        ]));
      });
    });
    return h("section", { key: "import-report", "class": "card import-report", "data-import-report": "true" }, [
      h("header", { key: "head", "class": "card-head" }, [
        h("h2", { "class": "card-title" }, t("report.title")),
        held.source ? h("span", { key: "source", "class": "card-chip card-meta" }, String(held.source)) : null,
        h("button", { key: "dismiss", type: "button", "class": "btn btn-small", id: "report-dismiss",
          on: { click: function () { options.onDismiss(); } } }, t("action.close"))
      ]),
      held.rows.length === 0
        ? h("p", { key: "none", "class": "state-text" }, t("report.none"))
        : h("div", { key: "body", "class": "table-scroll" }, h("table", { "class": "data" }, [
          h("thead", { key: "head" }, h("tr", null, [
            h("th", { key: "w" }, t("report.column.widget")), h("th", { key: "c" }, t("report.column.condition")),
            h("th", { key: "r" }, t("report.column.reference")), h("th", { key: "l" }, t("report.column.label")),
            h("th", { key: "a" }, t("report.column.repair"))
          ])),
          h("tbody", { key: "body" }, body)
        ]))
    ]);
  }

  /* A notice with its undo: what was just removed, and the way back. */
  function notice(held, onUndo, onClose) {
    if (!held) { return null; }
    return h("div", { key: "notice", "class": "edit-notice", role: "status" }, [
      h("span", { key: "text" }, tk("notice.", held.kind, held.params || {})),
      held.undo ? h("button", { key: "undo", type: "button", "class": "btn btn-small", id: "notice-undo",
        on: { click: onUndo } }, t("action.undo")) : null,
      h("button", { key: "close", type: "button", "class": "btn btn-small", on: { click: onClose } }, t("action.close"))
    ]);
  }

  opnview.editChrome = {
    MIN_W: MIN_W,
    MIN_H: MIN_H,
    MOVES: MOVES.map(function (pair) { return pair[1]; }),
    stepped: stepped,
    dragged: dragged,
    bar: bar,
    grips: grips,
    defaults: defaults,
    newWidget: newWidget,
    picker: picker,
    manage: manage,
    report: report,
    notice: notice,
    titleOf: titleOf
  };
})(this);
