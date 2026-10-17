/*
 * opnview front -- the dock: the one editing surface beside the canvas (F7-34, F7-35, F7-37).
 *
 * ONE PANEL, THREE TABS, ONE APPLY. The step-3 mockup's single panel: Document (the whole dashboard),
 * This widget (one widget's own lines) and Form (the widget's parameters as controls), a YAML/JSON
 * radio for the two code tabs, one Apply that commits whichever tab is showing, one Revert, and a
 * width the reader drags or moves with the arrow keys. Home Assistant's weight, and no more: no file
 * tree, no minimap, no command palette, no diff viewer (docs/ui-references.md).
 *
 * THE PREVIEW IS THE CARD ON THE CANVAS. While the form holds values the document does not, the card
 * draws them and says "unapplied"; the document is untouched until Apply.
 *
 * THE CODE PANE IS TWO LAYERS IN REGISTER: the textarea that holds the caret and the typing, with its
 * own text drawn in the pane's ground, and over it a highlighted copy that takes no pointer. A
 * reference that did not resolve, a type this build does not serve and a key it keeps without
 * understanding are marked on their own lines, each with a notice naming its kind, its `by`, its
 * value and the label the exporting installation gave it; a refused Apply marks its line. Every mark
 * is anchored on the text of its line and found again after each keystroke, so it follows the text.
 *
 * A REFRESH NEVER TOUCHES THE DOCK while it holds unapplied values (data-dirty, front/core/view.js).
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk, td = opnview.strings.td;

  var TABS = ["document", "widget", "form"];
  var MIN_WIDTH = 352, STEP = 32;

  /* The series vocabulary of custom_chart (docs/widget-api.md, "Series of custom_chart"): each
   * source, the key its figure is named under, and the figures it offers. */
  var SERIES_SOURCES = {
    traffic: { key: "measure", values: ["bytes", "connections", "allowed", "blocked", "netflow_bytes", "netflow_packets"] },
    firewall_metric: { key: "metric", values: ["uptime", "cpu", "memory", "swap", "disk", "temperature", "gateway_latency",
      "gateway_loss"] },
    interface_throughput: { key: "measure", values: ["bits_per_second", "bytes", "packets", "errors"] },
    dns: { key: "measure", values: ["lookups", "blocked"] },
    security_event: { key: "measure", values: ["events", "blocked"] }
  };
  var SERIES_AXES = ["left", "right"];
  var SERIES_RENDERS = ["line", "area", "bars", "points"];
  var SERIES_AGGREGATIONS = ["mean", "min", "max", "sum", "last"];
  var SERIES_REFERENCES = ["interface", "client", "rule", "provider", "country", "operator", "site"];

  function clone(value) { return value === undefined ? undefined : JSON.parse(JSON.stringify(value)); }
  function same(a, b) { return JSON.stringify(a) === JSON.stringify(b); }

  /* ---------------------------------------------------------------------------------------------
   * Highlighting.
   * ------------------------------------------------------------------------------------------- */

  function token(kind, text, index) { return h("span", { key: "t" + index, "class": "tok-" + kind }, text); }

  function scalarKind(text) {
    var trimmed = text.trim();
    if (/^-?\d+(\.\d+)?([eE][-+]?\d+)?$/.test(trimmed)) { return "num"; }
    if (/^(true|false|null|~)$/.test(trimmed)) { return "bool"; }
    if (/^[\[\]{},]*$/.test(trimmed)) { return "punct"; }
    return "str";
  }

  function highlightYaml(line) {
    var out = [];
    var match = /^(\s*)(- )?(.*)$/.exec(line);
    var index = 0;
    if (match[1]) { out.push(match[1]); }
    if (match[2]) { out.push(token("punct", "- ", index++)); }
    var rest = match[3];
    var comment = /(^|\s)#.*$/.exec(rest);
    var tail = "";
    if (comment && rest.charAt(0) !== "\"" && rest.charAt(0) !== "'") {
      tail = rest.slice(comment.index);
      rest = rest.slice(0, comment.index);
    }
    var key = /^("(?:[^"\\]|\\.)*"|'[^']*'|[^\s:'"#][^:]*?):(\s|$)(.*)$/.exec(rest);
    if (key && rest.charAt(0) !== "{" && rest.charAt(0) !== "[") {
      out.push(token("key", key[1], index++));
      out.push(token("punct", ":", index++));
      if (key[2]) { out.push(key[2]); }
      if (key[3]) { out.push(token(scalarKind(key[3]), key[3], index++)); }
    } else if (rest) {
      out.push(token(scalarKind(rest), rest, index++));
    }
    if (tail) { out.push(token("punct", tail, index++)); }
    return out;
  }

  function highlightJson(line) {
    var out = [];
    var pattern = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?)|(true|false|null)|([{}\[\],:])|(\s+)|([^\s"{}\[\],:]+)/g;
    var found, index = 0;
    while ((found = pattern.exec(line)) !== null) {
      if (found[1] !== undefined) {
        out.push(token(found[2] ? "key" : "str", found[1], index++));
        if (found[2]) { out.push(token("punct", found[2], index++)); }
      } else if (found[3] !== undefined) { out.push(token("num", found[3], index++)); }
      else if (found[4] !== undefined) { out.push(token("bool", found[4], index++)); }
      else if (found[5] !== undefined) { out.push(token("punct", found[5], index++)); }
      else if (found[6] !== undefined) { out.push(found[6]); }
      else { out.push(token("str", found[7], index++)); }
    }
    return out;
  }

  /* ---------------------------------------------------------------------------------------------
   * Marks: what the pane says about a line, found again by the line's text after every keystroke.
   * ------------------------------------------------------------------------------------------- */

  /* The notice of one issue: kind, by, value and the exporting label, in words. */
  function noticeOf(issue) {
    var label = issue.label === null || issue.label === undefined ? "" :
      typeof issue.label === "string" ? issue.label : JSON.stringify(issue.label);
    var value = typeof issue.value === "string" ? issue.value : JSON.stringify(issue.value);
    return tk("dock.notice.", issue.condition === "unresolved_reference" || issue.condition === "unknown_widget_type" ||
      issue.condition === "unknown_parameter_key" || issue.condition === "shape_not_served" ? issue.condition : "invalid_parameter",
    { kind: String(issue.kind), by: String(issue.by), value: value === undefined ? "" : value, label: label });
  }

  /* Anchors the marks on the lines of a freshly written text. */
  function anchor(lines, issues, subject, prefix) {
    var anchors = { issues: [], subject: null, error: null };
    issues.forEach(function (issue) {
      var path = issue.path;
      if (prefix) {
        if (path.indexOf(prefix + ".") !== 0) { return; }
        path = path.slice(prefix.length + 1);
      }
      var at = opnview.document.lineOf(lines, path);
      if (at < 0) { return; }
      anchors.issues.push({ text: lines[at].text, notice: noticeOf(issue), condition: issue.condition });
    });
    if (subject) {
      var first = -1, count = 0;
      lines.forEach(function (line, index) {
        if (line.path === subject || line.path.indexOf(subject + ".") === 0 || line.path.indexOf(subject + "[") === 0) {
          if (first < 0) { first = index; }
          count = index - first + 1;
        }
      });
      if (first >= 0) { anchors.subject = { text: lines[first].text, count: count }; }
    }
    return anchors;
  }

  function marksFor(anchors, raw) {
    var marks = {}, used = {};
    var trimEnd = function (text) { return text.replace(/\s+$/, ""); };
    if (anchors.subject) {
      for (var i = 0; i < raw.length; i++) {
        if (trimEnd(raw[i]) === trimEnd(anchors.subject.text)) {
          for (var j = i; j < Math.min(raw.length, i + anchors.subject.count); j++) { marks[j] = { cls: ["is-subject"] }; }
          break;
        }
      }
    }
    anchors.issues.forEach(function (issue) {
      for (var k = 0; k < raw.length; k++) {
        if (used[k] || trimEnd(raw[k]) !== trimEnd(issue.text)) { continue; }
        used[k] = true;
        var mark = marks[k] || (marks[k] = { cls: [] });
        mark.cls.push(issue.condition === "unknown_parameter_key" ? "is-kept" : "is-unresolved");
        mark.notice = issue.notice;
        break;
      }
    });
    if (anchors.error && anchors.error.line !== null && anchors.error.line >= 0 && anchors.error.line < raw.length) {
      var marked = marks[anchors.error.line] || (marks[anchors.error.line] = { cls: [] });
      marked.cls.push("is-error");
      marked.notice = anchors.error.notice;
    }
    return marks;
  }

  function ghostLines(text, syntax, anchors) {
    var raw = text.split("\n");
    var marks = marksFor(anchors, raw);
    return raw.map(function (line, index) {
      var mark = marks[index];
      return h("span", { key: "l" + index, "class": "code-line" + (mark ? " " + mark.cls.join(" ") : ""),
        "data-line": String(index + 1) }, [
        line === "" ? " " : (syntax === "json" ? highlightJson(line) : highlightYaml(line)),
        mark && mark.notice ? h("span", { key: "notice", "class": "code-notice" }, mark.notice) : null
      ]);
    });
  }

  /* ---------------------------------------------------------------------------------------------
   * The dock's state, for one canvas view.
   * ------------------------------------------------------------------------------------------- */

  function dockState(context) {
    var state = context.state;
    return state.dockPane || (state.dockPane = { mounted: null, text: "", dirty: false, error: null, anchors: null,
      confirmComments: false, pending: null, invalid: {} });
  }

  /* What the code tabs show, freshly written from the model. */
  function written(model, tab, syntax) {
    if (tab === "widget") {
      return model.widget ? opnview.document.serialise(model.widget, syntax) : { text: "", lines: [] };
    }
    return opnview.document.serialise(model.document, syntax);
  }

  function widgetPath(model) {
    return "canvases[" + model.canvasIndex + "].widgets[" + model.widgetIndex + "]";
  }

  /* Mounts a tab: the text written from the model, and its marks anchored. */
  function mount(context, model, key) {
    var pane = dockState(context);
    var dock = opnview.editing.dock();
    var syntax = opnview.editing.syntax();
    pane.mounted = key;
    pane.dirty = false;
    pane.error = null;
    pane.confirmComments = false;
    pane.invalid = {};
    if (dock.tab === "form") {
      pane.text = "";
      pane.anchors = null;
      return;
    }
    var out = written(model, dock.tab, syntax);
    pane.text = out.text;
    pane.anchors = anchor(out.lines, model.issues || [], dock.tab === "document" && model.widget ? widgetPath(model) : null,
      dock.tab === "widget" && model.widget ? widgetPath(model) : null);
  }

  /* ---------------------------------------------------------------------------------------------
   * The form.
   * ------------------------------------------------------------------------------------------- */

  /* A parameter's label: the widget's own, else the core's, else the key itself (data). */
  function parameterLabel(row, key) {
    var own = row && row.title_key ? row.title_key.replace(/\.title$/, ".parameter." + key) : null;
    if (own && opnview.strings.has(own)) { return td(own); }
    if (opnview.strings.has("parameter." + key)) { return tk("parameter.", key); }
    return key;
  }

  /* A value's label: the widget's own, else the core's, else the value itself (data). */
  function valueLabel(row, key, value) {
    var own = row && row.title_key ? row.title_key.replace(/\.title$/, ".value." + key + "." + value) : null;
    if (own && opnview.strings.has(own)) { return td(own); }
    if (key === "period" && opnview.strings.has("period." + value)) { return td("period." + value); }
    if (opnview.strings.has("parameter.value." + value)) { return tk("parameter.value.", String(value)); }
    return String(value);
  }

  /* The reference a candidate makes: by the first accepted key the candidate carries. */
  function referenceFor(kind, list, candidate) {
    var by = (list.by || []).filter(function (key) {
      return candidate.keys && candidate.keys[key] !== null && candidate.keys[key] !== undefined;
    })[0];
    return by ? { kind: kind, by: by, value: candidate.keys[by], label: candidate.label } : null;
  }

  function matches(reference, candidate) {
    return !!reference && typeof reference === "object" && !!candidate.keys && reference.by in candidate.keys &&
      candidate.keys[reference.by] === reference.value;
  }

  /* One reference dropdown: the candidates this installation has, the current value shown as the
   * selection -- marked not found when it matches none -- and never a free-text field. */
  function referenceSelect(context, model, options) {
    var kind = options.kind;
    var list = model.references(kind);
    var current = options.value;
    var candidates = list && list.candidates ? list.candidates : [];
    var found = -1;
    candidates.forEach(function (candidate, index) { if (found < 0 && matches(current, candidate)) { found = index; } });
    var unresolved = !!current && found < 0;
    var choices = [h("option", { key: "none", value: "", selected: !current }, options.noneLabel)];
    if (unresolved) {
      choices.push(h("option", { key: "current", value: "current", selected: true },
        t("form.reference.not_found", { name: String(current.label || current.value) })));
    }
    candidates.forEach(function (candidate, index) {
      choices.push(h("option", { key: "c" + index, value: String(index), selected: index === found,
        disabled: candidate.referenceable === false }, candidate.label));
    });
    var selected = unresolved ? "current" : found >= 0 ? String(found) : "";
    return h("select", { id: options.id, "class": "form-select" + (unresolved ? " is-unresolved" : ""),
      "data-form-reference": kind, "aria-invalid": unresolved ? "true" : null, value: selected,
      on: { change: function (event) {
        var value = event.target.value;
        if (value === "current") { return; }
        if (value === "") { options.onChange(null); return; }
        var candidate = candidates[Number(value)];
        var reference = candidate ? referenceFor(kind, list, candidate) : null;
        if (reference) { options.onChange(reference); }
      } } }, choices);
  }

  /* The pending parameters of the form, created from the widget's on first change. */
  function pendingOf(context, model) {
    var pane = dockState(context);
    if (!pane.pending || pane.pending.id !== model.widget.id) {
      pane.pending = { id: model.widget.id, parameters: clone(model.widget.parameters || {}) || {} };
    }
    return pane.pending.parameters;
  }

  function setPending(context, model, key, value) {
    var parameters = pendingOf(context, model);
    if (value === undefined) { delete parameters[key]; } else { parameters[key] = value; }
    var pane = dockState(context);
    var differs = !same(parameters, model.widget.parameters || {});
    if (!differs) { pane.pending = null; }
    model.onPending(differs ? { id: model.widget.id, parameters: clone(parameters) } : null);
  }

  function fieldId(model, key, suffix) {
    return "form-" + String(model.widget.id).replace(/[^a-z0-9_-]/gi, "_") + "-" + key + (suffix || "");
  }

  function clearButton(context, model, key, present) {
    if (!present) { return null; }
    return h("button", { key: "clear", type: "button", "class": "btn btn-small form-clear", "data-form-clear": key,
      on: { click: function () { setPending(context, model, key, undefined); } } }, t("form.default"));
  }

  function selectField(id, options, selected, onChange, extra) {
    return h("select", { id: id, "class": "form-select", value: selected,
      "data-form-key": extra && extra.key, on: { change: function (event) { onChange(event.target.value); } } },
      options.map(function (option) {
        return h("option", { key: "o-" + option[0], value: option[0], selected: option[0] === selected }, option[1]);
      }));
  }

  function field(context, model, parameter, parameters) {
    var row = model.row;
    var key = parameter.key;
    var id = fieldId(model, key);
    var present = Object.prototype.hasOwnProperty.call(parameters, key);
    var value = parameters[key];
    var pane = dockState(context);
    var control;
    var classes = "form-field";
    var change = function (next) { setPending(context, model, key, next); };
    switch (parameter.kind) {
      case "enum": {
        var unset = key === "period"
          ? t("form.follow_canvas", { period: tk("period.", opnview.canvasModel.canvasPeriod(model.canvas)) })
          : t("form.unset");
        var choices = [["", unset]].concat((parameter.values || []).map(function (v) { return [String(v), valueLabel(row, key, v)]; }));
        if (present && (parameter.values || []).indexOf(value) < 0) {
          choices.push(["current", t("form.not_offered", { value: typeof value === "string" ? value : JSON.stringify(value) })]);
        }
        control = selectField(id, choices, present ? ((parameter.values || []).indexOf(value) >= 0 ? String(value) : "current") : "",
          function (next) { if (next !== "current") { change(next === "" ? undefined : next); } }, { key: key });
        break;
      }
      case "boolean":
        control = selectField(id, [["", t("form.unset")], ["true", t("form.yes")], ["false", t("form.no")]],
          present && typeof value === "boolean" ? String(value) : "",
          function (next) { change(next === "" ? undefined : next === "true"); }, { key: key });
        break;
      case "integer":
      case "number": {
        var invalid = pane.invalid[key];
        control = h("input", { id: id, type: "number", "class": "form-input", "data-form-key": key,
          min: parameter.min, max: parameter.max, step: parameter.kind === "integer" ? "1" : "any",
          placeholder: t("form.unset"), "aria-invalid": invalid ? "true" : null,
          value: present && value !== null ? String(value) : "",
          on: { input: function (event) {
            var text = String(event.target.value).trim();
            if (text === "") { delete pane.invalid[key]; change(undefined); return; }
            var number = Number(text);
            var bad = !isFinite(number) || (parameter.kind === "integer" && Math.floor(number) !== number) ||
              (parameter.min !== undefined && parameter.min !== null && number < parameter.min) ||
              (parameter.max !== undefined && parameter.max !== null && number > parameter.max);
            if (bad) { pane.invalid[key] = true; context.rerender(); return; }
            delete pane.invalid[key];
            change(number);
          } } });
        if (invalid) {
          control = [control, h("span", { key: "error", "class": "field-error", role: "status" },
            parameter.min !== undefined && parameter.min !== null && parameter.max !== undefined && parameter.max !== null
              ? t("form.out_of_range", { min: opnview.format.number(parameter.min), max: opnview.format.number(parameter.max) })
              : parameter.min !== undefined && parameter.min !== null
                ? t("form.at_least", { min: opnview.format.number(parameter.min) }) : t("form.not_a_number"))];
        }
        break;
      }
      case "enum_set": {
        var chosen = present && Array.isArray(value) ? value : (Array.isArray(parameter["default"]) ? parameter["default"] : []);
        control = h("div", { "class": "form-checks", role: "group", "aria-labelledby": id + "-label" },
          (parameter.values || []).map(function (option, index) {
            var boxId = fieldId(model, key, "-" + index);
            return h("label", { key: "c-" + option, "class": "form-check", "for": boxId }, [
              h("input", { id: boxId, type: "checkbox", "data-form-key": key, "data-form-value": String(option),
                checked: chosen.indexOf(option) >= 0, on: { change: function (event) {
                  var next = chosen.filter(function (item) { return item !== option; });
                  if (event.target.checked) { next = (parameter.values || []).filter(function (item) {
                    return item === option || chosen.indexOf(item) >= 0; }); }
                  change(next);
                } } }),
              h("span", null, valueLabel(row, key, option))
            ]);
          }));
        break;
      }
      case "string_list":
        control = h("input", { id: id, type: "text", "class": "form-input", "data-form-key": key,
          placeholder: t("form.list_placeholder"),
          value: present && Array.isArray(value) ? value.join(", ") : "",
          on: { change: function (event) {
            var items = String(event.target.value).split(",").map(function (item) { return item.trim(); })
              .filter(function (item) { return item !== ""; });
            change(items.length ? items : undefined);
          } } });
        break;
      case "reference":
        control = referenceSelect(context, model, { id: id, kind: parameter.reference, value: present ? value : null,
          noneLabel: t("form.reference.none"), onChange: function (reference) { change(reference || undefined); } });
        if (present && value && model.references(parameter.reference) &&
            !(model.references(parameter.reference).candidates || []).some(function (c) { return matches(value, c); })) {
          classes += " is-unresolved";
        }
        break;
      case "reference_list": {
        var list = present && Array.isArray(value) ? value : [];
        var unresolvedAny = false;
        var known = model.references(parameter.reference);
        var entries = list.map(function (reference, index) {
          if (known && !(known.candidates || []).some(function (c) { return matches(reference, c); })) { unresolvedAny = true; }
          return h("div", { key: "e" + index, "class": "form-list-entry" }, [
            referenceSelect(context, model, { id: index === 0 ? id : fieldId(model, key, "-" + index), kind: parameter.reference,
              value: reference, noneLabel: t("form.reference.remove"), onChange: function (next) {
                var copy = list.slice();
                if (next) { copy[index] = next; } else { copy.splice(index, 1); }
                change(copy.length ? copy : undefined);
              } })
          ]);
        });
        entries.push(h("div", { key: "add", "class": "form-list-entry" },
          referenceSelect(context, model, { id: list.length === 0 ? id : fieldId(model, key, "-add"), kind: parameter.reference,
            value: null, noneLabel: t("form.reference.add"), onChange: function (next) {
              if (next) { change(list.concat([next])); }
            } })));
        if (unresolvedAny) { classes += " is-unresolved"; }
        control = h("div", { "class": "form-list" }, entries);
        break;
      }
      case "series":
        control = seriesEditor(context, model, key, present && Array.isArray(value) ? value : [], change);
        break;
      default:
        // An object -- custom_chart's axes, the health thresholds -- is edited in the code tabs.
        control = h("button", { type: "button", "class": "btn btn-small", "data-form-code": key,
          on: { click: function () { model.switchTab("widget"); } } }, t("form.edit_in_code"));
    }
    return h("div", { key: "f-" + key, "class": classes, "data-form-field": key }, [
      h("label", { key: "label", id: id + "-label", "for": id }, [
        h("span", { key: "name" }, parameterLabel(row, key)),
        classes.indexOf("is-unresolved") >= 0 ? h("span", { key: "badge", "class": "badge badge-ref" }, t("form.not_found")) : null,
        clearButton(context, model, key, present)
      ]),
      control
    ]);
  }

  /* The ordered list editor of custom_chart's series. */
  function seriesEditor(context, model, key, list, change) {
    var datasets = model.datasets();
    var sources = Object.keys(SERIES_SOURCES).map(function (name) { return [name, tk("series.source.", name)]; });
    (datasets || []).forEach(function (dataset) {
      if (SERIES_SOURCES[dataset.name] || !dataset.time_column) { return; }
      sources.push([dataset.name, dataset.label && opnview.strings.has(dataset.label) ? td(dataset.label) : dataset.name]);
    });
    var update = function (index, mutate) {
      var copy = clone(list);
      mutate(copy[index], copy);
      change(copy);
    };
    var rows = list.map(function (series, index) {
      series = series && typeof series === "object" ? series : {};
      var base = fieldId(model, key, "-" + index);
      var source = SERIES_SOURCES[series.source];
      var dataset = null;
      (datasets || []).forEach(function (candidate) { if (candidate.name === series.source) { dataset = candidate; } });
      var figureKey = source ? source.key : "measure";
      var figures = source ? source.values.map(function (v) { return [v, valueLabel(null, "", v)]; })
        : dataset ? (dataset.columns || []).filter(function (column) { return column.role === "measure"; })
          .map(function (column) { return [column.name, column.name]; }) : [];
      var scopeKind = SERIES_REFERENCES.filter(function (kind) { return series[kind]; })[0] || "";
      var select = function (suffix, label, options, selected, onChange) {
        return h("div", { key: suffix, "class": "series-control" }, [
          h("label", { "for": base + "-" + suffix }, label),
          selectField(base + "-" + suffix, options, selected, onChange)
        ]);
      };
      return h("li", { key: "s" + index, "class": "series-entry", "data-series-index": String(index) }, [
        h("div", { key: "head", "class": "series-head" }, [
          h("label", { "for": base + "-label" }, t("series.label")),
          h("input", { id: base + "-label", type: "text", "class": "form-input", value: typeof series.label === "string" ? series.label : "",
            on: { change: function (event) { update(index, function (entry) {
              if (event.target.value) { entry.label = event.target.value; } else { delete entry.label; } }); } } }),
          h("button", { key: "up", type: "button", "class": "btn btn-small", disabled: index === 0,
            "aria-label": t("series.move_up", { index: String(index + 1) }),
            on: { click: function () { update(index, function (entry, all) { all.splice(index, 1); all.splice(index - 1, 0, entry); }); } } },
          t("edit.face.up")),
          h("button", { key: "down", type: "button", "class": "btn btn-small", disabled: index === list.length - 1,
            "aria-label": t("series.move_down", { index: String(index + 1) }),
            on: { click: function () { update(index, function (entry, all) { all.splice(index, 1); all.splice(index + 1, 0, entry); }); } } },
          t("edit.face.down")),
          h("button", { key: "remove", type: "button", "class": "btn btn-small btn-destructive",
            "aria-label": t("series.remove", { index: String(index + 1) }),
            on: { click: function () { update(index, function (entry, all) { all.splice(index, 1); }); } } }, t("series.remove_face"))
        ]),
        h("div", { key: "controls", "class": "series-controls" }, [
          select("source", t("series.source"), [["", t("form.unset")]].concat(sources), series.source || "", function (next) {
            update(index, function (entry) {
              if (next) { entry.source = next; } else { delete entry.source; }
              delete entry.metric;
              delete entry.measure;
            });
          }),
          select("figure", t("series.figure"), [["", t("form.unset")]].concat(figures),
            String(series[figureKey] || ""), function (next) {
              update(index, function (entry) { if (next) { entry[figureKey] = next; } else { delete entry[figureKey]; } });
            }),
          select("axis", t("series.axis"), [["", t("form.unset")]].concat(SERIES_AXES.map(function (v) { return [v, tk("series.axis.", v)]; })),
            series.axis || "", function (next) { update(index, function (entry) { if (next) { entry.axis = next; } else { delete entry.axis; } }); }),
          select("render", t("series.render"), [["", t("form.unset")]].concat(SERIES_RENDERS.map(function (v) { return [v, tk("series.render.", v)]; })),
            series.render || "", function (next) { update(index, function (entry) { if (next) { entry.render = next; } else { delete entry.render; } }); }),
          select("aggregation", t("series.aggregation"), [["", t("form.unset")]].concat(SERIES_AGGREGATIONS.map(function (v) {
            return [v, tk("series.aggregation.", v)]; })), series.aggregation || "", function (next) {
            update(index, function (entry) { if (next) { entry.aggregation = next; } else { delete entry.aggregation; } });
          }),
          select("scope", t("series.scope"), [["", t("form.reference.none")]].concat(SERIES_REFERENCES.map(function (kind) {
            return [kind, tk("series.reference.", kind)]; })), scopeKind, function (next) {
            update(index, function (entry) { SERIES_REFERENCES.forEach(function (kind) { if (kind !== next) { delete entry[kind]; } }); });
            if (next) { model.references(next); }
          }),
          scopeKind ? h("div", { key: "reference", "class": "series-control" }, [
            h("label", { "for": base + "-reference" }, tk("series.reference.", scopeKind)),
            referenceSelect(context, model, { id: base + "-reference", kind: scopeKind, value: series[scopeKind],
              noneLabel: t("form.reference.none"), onChange: function (reference) {
                update(index, function (entry) { if (reference) { entry[scopeKind] = reference; } else { delete entry[scopeKind]; } });
              } })
          ]) : null
        ])
      ]);
    });
    return h("div", { "class": "series-editor" }, [
      h("ol", { key: "list", "class": "series-list" }, rows),
      h("button", { key: "add", type: "button", "class": "btn btn-small", "data-series-add": "true",
        on: { click: function () { change(list.concat([{ source: "traffic", measure: "bytes", axis: "left", render: "line" }])); } } },
      t("series.add"))
    ]);
  }

  function formBody(context, model) {
    if (!model.widget) { return h("p", { "class": "state-text" }, t("dock.no_widget")); }
    if (!model.row) {
      return h("div", { "class": "state state-unresolved" }, h("span", { "class": "state-badge" },
        tk("condition.", model.widget.type ? "unknown_widget_type" : "shape_not_served")));
    }
    var pane = dockState(context);
    var parameters = pane.pending && pane.pending.id === model.widget.id ? pane.pending.parameters : (model.widget.parameters || {});
    var declared = {};
    var fields = (model.row.parameters || []).map(function (parameter) {
      declared[parameter.key] = true;
      return field(context, model, parameter, parameters);
    });
    var kept = Object.keys(parameters).filter(function (key) { return !declared[key]; });
    return h("div", { "class": "dock-form" }, [
      fields,
      kept.length ? h("p", { key: "kept", "class": "form-footnote", "data-form-kept": kept.join(",") }, [
        h("span", { key: "badge", "class": "badge badge-kept" }, t("card.kept")),
        " ",
        h("span", { key: "keys", "class": "mono" }, kept.join(", "))
      ]) : null
    ]);
  }

  /* ---------------------------------------------------------------------------------------------
   * Apply and Revert.
   * ------------------------------------------------------------------------------------------- */

  /* A refusal in words: what is wrong, then where -- the line, the canvas index, the widget index
   * and the field, each named only when the refusal carries it. */
  function refusalNotice(error) {
    var given = function (value) { return value !== null && value !== undefined && value !== ""; };
    var parts = [t("document.not_applied") + " — " + tk("document.refusal.", error.condition, {
      file_version: given(error.file_version) ? String(error.file_version) : "",
      current: given(error.current) ? String(error.current) : "",
      minimum: given(error.minimum) ? String(error.minimum) : "" })];
    if (given(error.line)) { parts.push(t("document.at.line", { line: String(error.line) })); }
    if (given(error.canvas_index)) { parts.push(t("document.at.canvas", { canvas: String(error.canvas_index) })); }
    if (given(error.widget_index)) { parts.push(t("document.at.widget", { widget: String(error.widget_index) })); }
    if (given(error.field)) { parts.push(t("document.at.field", { field: String(error.field) })); }
    return parts.join(" · ");
  }

  function refuse(context, error) {
    var pane = dockState(context);
    pane.error = error;
    if (pane.anchors) {
      pane.anchors.error = { line: error.line ? error.line - 1 : null, notice: refusalNotice(error) };
    }
    context.rerender();
  }

  function applied(context, model, outcome) {
    var pane = dockState(context);
    if (!outcome.ok) {
      refuse(context, { condition: outcome.condition || "read_failed", line: null });
      return;
    }
    pane.pending = null;
    pane.mounted = null;
    pane.dirty = false;
    pane.error = null;
    pane.confirmComments = false;
    model.onPending(null);
    model.onApplied();
  }

  function apply(context, model) {
    var pane = dockState(context);
    var dock = opnview.editing.dock();
    var editing = opnview.editing;
    if (dock.tab === "form") {
      if (!model.widget || !pane.pending || Object.keys(pane.invalid).length) {
        if (Object.keys(pane.invalid).length) { refuse(context, { condition: "form_invalid", line: null }); }
        return;
      }
      var parameters = clone(pane.pending.parameters);
      var widgetId = model.widget.id, canvasId = model.canvas.id;
      editing.edit(model.api, model.dashboard, function (document) {
        var canvas = opnview.canvasModel.canvasIn(document, canvasId);
        var widget = canvas && (canvas.widgets || []).filter(function (w) { return w && w.id === widgetId; })[0];
        if (!widget) { return false; }
        if (Object.keys(parameters).length) { widget.parameters = parameters; } else { delete widget.parameters; }
        return true;
      }, "form").then(function (outcome) { applied(context, model, outcome); });
      return;
    }
    var syntax = opnview.editing.syntax();
    if (syntax === "yaml" && opnview.document.hasComments(pane.text) && !pane.confirmComments) {
      pane.confirmComments = true;
      context.rerender();
      return;
    }
    var rowFor = function (type) { return opnview.widgets.row(type); };
    if (dock.tab === "document") {
      var loaded = opnview.document.load(pane.text, rowFor);
      if (loaded.error) { refuse(context, loaded.error); return; }
      editing.replace(model.api, model.dashboard, loaded.document, "apply").then(function (outcome) {
        applied(context, model, outcome);
      });
      return;
    }
    // This widget: its own text, checked inside a copy of the whole document.
    var parsed = opnview.document.parse(pane.text, syntax);
    if (parsed.error) { refuse(context, parsed.error); return; }
    var copy = clone(model.document);
    var canvasCopy = copy.canvases[model.canvasIndex];
    canvasCopy.widgets[model.widgetIndex] = parsed.document;
    var prefix = widgetPath(model);
    var lineIn = function (path) {
      var relative = path.indexOf(prefix + ".") === 0 ? path.slice(prefix.length + 1) : path === prefix ? "" : null;
      return relative !== null && parsed.paths && parsed.paths[relative] !== undefined ? parsed.paths[relative] + 1 : null;
    };
    var refused = opnview.document.validate(copy);
    if (refused) { refused.line = lineIn(refused.path); refuse(context, refused); return; }
    var coerced = opnview.document.coerce(copy, rowFor);
    if (coerced.refusal) { coerced.refusal.line = lineIn(coerced.refusal.path); refuse(context, coerced.refusal); return; }
    editing.replace(model.api, model.dashboard, coerced.document, "apply").then(function (outcome) {
      if (outcome.ok && parsed.document && parsed.document.id !== model.widget.id) { opnview.editing.dock().widget = parsed.document.id; }
      applied(context, model, outcome);
    });
  }

  function revert(context, model) {
    var pane = dockState(context);
    pane.pending = null;
    pane.mounted = null;
    pane.dirty = false;
    pane.error = null;
    model.onPending(null);
    context.rerender();
  }

  /* ---------------------------------------------------------------------------------------------
   * The panel.
   * ------------------------------------------------------------------------------------------- */

  function codeBody(context, model) {
    var pane = dockState(context);
    var syntax = opnview.editing.syntax();
    var anchors = pane.anchors || { issues: [], subject: null, error: null };
    var repaint = function (input) {
      var ghost = root.document && root.document.getElementById("dock-ghost");
      if (ghost) {
        opnview.view.render(ghost, ghostLines(pane.text, syntax, anchors));
        ghost.scrollTop = input.scrollTop;
      }
    };
    var notices = anchors.issues;
    return [
      h("div", { key: "editor", "class": "code-editor" }, [
        h("textarea", { key: "input", id: "dock-code", "class": "code-input", spellcheck: "false", autocomplete: "off",
          "aria-label": t("dock.code.label"), value: pane.text, on: {
            input: function (event) {
              var wasDirty = pane.dirty;
              pane.text = event.target.value;
              pane.dirty = true;
              pane.confirmComments = false;
              if (wasDirty) { repaint(event.target); } else { context.rerender(); }
            },
            scroll: function (event) {
              var ghost = root.document && root.document.getElementById("dock-ghost");
              if (ghost) { ghost.scrollTop = event.target.scrollTop; }
            }
          } }),
        h("pre", { key: "ghost", id: "dock-ghost", "class": "code", "aria-hidden": "true" }, ghostLines(pane.text, syntax, anchors))
      ]),
      notices.length ? h("ul", { key: "notices", "class": "code-notice-list" }, notices.map(function (issue, index) {
        return h("li", { key: "n" + index }, [issue.notice, h("span", { key: "line", "class": "code-notice-line" }, issue.text.trim())]);
      })) : null
    ];
  }

  function grip(context) {
    var drag = null;
    var setWidth = function (width) {
      var max = Math.max(MIN_WIDTH, (root.innerWidth || 1440) - 320);
      var next = Math.max(MIN_WIDTH, Math.min(max, Math.round(width)));
      opnview.editing.setDockWidth(next);
      applyWidth();
      context.rerender();
    };
    var current = function () {
      var aside = root.document && root.document.getElementById("doc-dock");
      return aside && aside.getBoundingClientRect ? aside.getBoundingClientRect().width : (opnview.editing.dockWidth() || 544);
    };
    return h("button", { key: "grip", type: "button", id: "dock-grip", "class": "dock-grip",
      "aria-label": t("dock.grip"), on: {
        pointerdown: function (event) {
          if (event.preventDefault) { event.preventDefault(); }
          drag = { x: event.clientX, width: current() };
          var move = function (moved) { if (drag) { setWidth(drag.width + (drag.x - moved.clientX)); } };
          var up = function () {
            drag = null;
            root.document.removeEventListener("pointermove", move);
            root.document.removeEventListener("pointerup", up);
          };
          root.document.addEventListener("pointermove", move);
          root.document.addEventListener("pointerup", up);
        },
        keydown: function (event) {
          if (event.key === "ArrowLeft") { if (event.preventDefault) { event.preventDefault(); } setWidth(current() + STEP); }
          if (event.key === "ArrowRight") { if (event.preventDefault) { event.preventDefault(); } setWidth(current() - STEP); }
        }
      } });
  }

  /* The dock's width is a length on the document element, the one property written. */
  function applyWidth() {
    var element = root.document && root.document.documentElement;
    var width = opnview.editing.dockWidth();
    if (element && element.style && element.style.setProperty && width) {
      element.style.setProperty("--dock-width", width + "px");
    }
  }

  /* The dock. model: {api, dashboard, document, canvas, canvasIndex, widget, widgetIndex, row,
   * issues, references(kind), datasets(), onPending(pending), onApplied(), switchTab(tab), close()}. */
  function render(context, model) {
    var dock = opnview.editing.dock();
    if (!dock.open || !model.document) { return null; }
    var pane = dockState(context);
    var syntax = opnview.editing.syntax();
    var tab = dock.tab;
    if (tab !== "document" && !model.widget) { tab = dock.tab = "document"; }
    var key = tab + "|" + (model.widget ? model.widget.id : "") + "|" + syntax + "|" + model.dashboard + "|" +
      (opnview.editing.revision(model.dashboard) || "") + "|" + (model.dragging ? "drag" : "");
    if (!pane.dirty && pane.mounted !== key && !(pane.pending && tab === "form")) { mount(context, model, key); }
    applyWidth();
    var switchTo = function (next) {
      pane.mounted = null;
      pane.pending = null;
      model.onPending(null);
      dock.tab = next;
      context.rerender();
    };
    var setSyntax = function (next) {
      opnview.editing.setSyntax(next);
      pane.mounted = null;
      context.rerender();
    };
    var dirty = pane.dirty || !!pane.pending;
    var title = model.widget ? model.widgetTitle : null;
    var body = tab === "form" ? formBody(context, model) : codeBody(context, model);
    return h("aside", { key: "dock", id: "doc-dock", "class": "doc-dock", "aria-label": t("dock.title"),
      "data-dirty": dirty ? "true" : null }, [
      grip(context),
      h("div", { key: "head", "class": "panel-head" }, [
        h("h2", { "class": "panel-title" }, t("dock.title")),
        h("button", { type: "button", id: "dock-close", "class": "btn btn-small", on: { click: function () { model.close(); } } },
          t("action.close"))
      ]),
      h("div", { key: "tabs", "class": "panel-tabs" }, [
        h("div", { key: "list", "class": "panel-tabs", role: "tablist", "aria-label": t("dock.tabs") }, TABS.map(function (name) {
          return h("button", { key: name, type: "button", role: "tab", id: "dock-tab-" + name, "class": "btn btn-small",
            "aria-selected": name === tab ? "true" : "false", disabled: name !== "document" && !model.widget,
            on: { click: function () { if (name !== tab) { switchTo(name); } } } }, tk("dock.tab.", name));
        })),
        tab !== "form" ? h("span", { key: "syntax", "class": "panel-subtabs", role: "radiogroup", "aria-label": t("dock.syntax") }, [
          h("span", { key: "label", "class": "panel-subtabs-label" }, t("dock.syntax")),
          opnview.editing.SYNTAXES.map(function (name) {
            return h("button", { key: name, type: "button", role: "radio", id: "syntax-" + name, "class": "btn btn-small",
              "aria-checked": name === syntax ? "true" : "false", on: { click: function () { if (name !== syntax) { setSyntax(name); } } } },
            tk("dock.syntax.", name));
          })
        ]) : null
      ]),
      tab !== "document" && model.widget ? h("p", { key: "subject", "class": "dock-subject" }, [
        h("span", { key: "name" }, title),
        h("span", { key: "id", "class": "dock-subject-id" }, String(model.widget.id) + " · " + String(model.widget.type || model.widget.shape || ""))
      ]) : null,
      h("div", { key: "body", "class": "doc-dock-body" + (tab === "form" ? "" : " is-code") }, body),
      pane.confirmComments ? h("p", { key: "comments", "class": "code-warning", role: "alert" }, t("dock.comments_lost")) : null,
      h("div", { key: "actions", "class": "dock-actions" }, [
        h("button", { type: "button", id: "dock-apply", "class": "btn btn-primary", disabled: !dirty,
          on: { click: function () { apply(context, model); } } },
          pane.confirmComments ? t("dock.apply_losing_comments") : t("dock.apply")),
        h("button", { type: "button", id: "dock-revert", "class": "btn btn-small", disabled: !dirty,
          on: { click: function () { revert(context, model); } } }, t("dock.revert")),
        dirty ? h("span", { key: "hint", "class": "code-hint" }, t("dock.unapplied")) : null
      ]),
      pane.error ? h("p", { key: "error", id: "dock-error", "class": "code-error", role: "alert" }, refusalNotice(pane.error)) : null
    ]);
  }

  opnview.dock = {
    render: render,
    refusalText: refusalNotice,
    /* What the tests reach. */
    SERIES_SOURCES: SERIES_SOURCES,
    SERIES_AXES: SERIES_AXES,
    SERIES_RENDERS: SERIES_RENDERS,
    SERIES_AGGREGATIONS: SERIES_AGGREGATIONS,
    SERIES_REFERENCES: SERIES_REFERENCES,
    TABS: TABS,
    pending: function (context) { var pane = dockState(context); return pane.pending; },
    forget: function (context) { context.state.dockPane = null; }
  };
})(this);
