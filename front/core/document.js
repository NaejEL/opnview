/*
 * The document module: the structural validation of a dashboard document
 * (docs/dashboard-format.md, format version 1, as amended by decision D7 of
 * specs/SPEC-step-7-frontend.md: the optional canvas `period` and `scope`).
 *
 * IT MIRRORS internal/dashboard EXACTLY. The Go validator is the authoritative
 * one (decision D13): a save is refused by the server whatever this module
 * says. This one exists so the editor can name a structural defect, with its
 * line, before anything is sent. The same codes, the same indices, the same
 * field and the same path, found in the same order: the conformance fixtures
 * of testdata/dashboards/conformance are run through both, and a test fails
 * when they disagree on any of them.
 *
 * STRUCTURAL VALIDITY ONLY. An unknown widget type, a reference matching
 * nothing, an unknown parameter key or a value outside a vocabulary is never a
 * refusal here: the format makes those fail-open.
 *
 * A classic script, run unchanged in the browser and in the Go test harness:
 * no module syntax, no browser-only API. It carries no user-visible string;
 * what it answers are closed codes the interface translates.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var CURRENT_FORMAT_VERSION = 1;
  var MINIMUM_FORMAT_VERSION = 1;
  var MAX_DOCUMENT_BYTES = 1048576;
  /* The deepest a container may sit below a widget's `parameters` or a
     canvas's `scope`: a parameter value, an object of a series list, a
     reference object it carries. */
  var MAX_CONTAINER_DEPTH = 3;
  var SLUG = /^[a-z0-9][a-z0-9_-]*$/;
  var PLACEMENT_KEYS = ["x", "y", "w", "h"];

  var CONDITIONS = {
    malformedBody: "malformed_body",
    formatVersionMissing: "format_version_missing",
    formatVersionTooNew: "format_version_too_new",
    formatVersionTooOld: "format_version_too_old",
    canvasesMissing: "canvases_missing",
    canvasesEmpty: "canvases_empty",
    canvasIdMissing: "canvas_id_missing",
    canvasIdMalformed: "canvas_id_malformed",
    canvasTitleMissing: "canvas_title_missing",
    widgetIdMissing: "widget_id_missing",
    widgetTypeAndShapeMissing: "widget_type_and_shape_missing",
    placementMissing: "placement_missing",
    placementNotInteger: "placement_not_integer",
    duplicateId: "duplicate_id",
    nestingTooDeep: "nesting_too_deep",
    documentTooLarge: "document_too_large"
  };

  function isObject(value) {
    return value !== null && typeof value === "object" && !Array.isArray(value);
  }

  function has(object, key) {
    return Object.prototype.hasOwnProperty.call(object, key);
  }

  /* The last member named in a path: "canvases[0].widgets[1].placement.x"
     is "x", "parameters.series[2]" is "series". */
  function lastMember(path) {
    var cut = path.lastIndexOf(".");
    if (cut >= 0) { path = path.slice(cut + 1); }
    cut = path.indexOf("[");
    if (cut >= 0) { path = path.slice(0, cut); }
    return path;
  }

  function refusal(condition, canvas, widget, path) {
    return {
      condition: condition,
      canvas_index: canvas >= 0 ? canvas : null,
      widget_index: widget >= 0 ? widget : null,
      field: lastMember(path),
      path: path
    };
  }

  /* A JSON number holding a whole value; 3.0 is whole, Infinity is not. */
  function wholeNumber(value) {
    return typeof value === "number" && isFinite(value) && Math.floor(value) === value;
  }

  function isNonEmptyString(value) {
    return typeof value === "string" && value !== "";
  }

  /* The length of a text in UTF-8 bytes, which is what the server bounds. */
  function utf8Length(text) {
    var bytes = 0, index, code;
    for (index = 0; index < text.length; index += 1) {
      code = text.charCodeAt(index);
      if (code < 0x80) {
        bytes += 1;
      } else if (code < 0x800) {
        bytes += 2;
      } else if (code >= 0xd800 && code <= 0xdbff && index + 1 < text.length &&
          text.charCodeAt(index + 1) >= 0xdc00 && text.charCodeAt(index + 1) <= 0xdfff) {
        bytes += 4;
        index += 1;
      } else {
        bytes += 3;
      }
    }
    return bytes;
  }

  /* The path of the first container nested deeper than MAX_CONTAINER_DEPTH,
     members visited in sorted order and elements in order, or "". */
  function tooDeep(value, path, depth) {
    var keys, index, deep;
    if (Array.isArray(value)) {
      if (depth > MAX_CONTAINER_DEPTH) { return path; }
      for (index = 0; index < value.length; index += 1) {
        deep = tooDeep(value[index], path + "[" + index + "]", depth + 1);
        if (deep !== "") { return deep; }
      }
    } else if (isObject(value)) {
      if (depth > MAX_CONTAINER_DEPTH) { return path; }
      keys = Object.keys(value).sort();
      for (index = 0; index < keys.length; index += 1) {
        deep = tooDeep(value[keys[index]], path + "." + keys[index], depth + 1);
        if (deep !== "") { return deep; }
      }
    }
    return "";
  }

  function validateVersion(document) {
    var version = document.format_version, condition, refused;
    if (!wholeNumber(version)) {
      return refusal(CONDITIONS.formatVersionMissing, -1, -1, "format_version");
    }
    if (version > CURRENT_FORMAT_VERSION) {
      condition = CONDITIONS.formatVersionTooNew;
    } else if (version < MINIMUM_FORMAT_VERSION) {
      condition = CONDITIONS.formatVersionTooOld;
    } else {
      return null;
    }
    refused = refusal(condition, -1, -1, "format_version");
    refused.file_version = version;
    refused.current = CURRENT_FORMAT_VERSION;
    refused.minimum = MINIMUM_FORMAT_VERSION;
    return refused;
  }

  function validateWidget(widget, canvas, index, path, ids) {
    var key, value, deep, i;
    if (!isObject(widget) || !isNonEmptyString(widget.id)) {
      return refusal(CONDITIONS.widgetIdMissing, canvas, index, path + ".id");
    }
    if (has(ids, widget.id)) {
      return refusal(CONDITIONS.duplicateId, canvas, index, path + ".id");
    }
    ids[widget.id] = true;
    if (!isNonEmptyString(widget.type) && !isNonEmptyString(widget.shape)) {
      return refusal(CONDITIONS.widgetTypeAndShapeMissing, canvas, index, path + ".type");
    }
    if (!isObject(widget.placement)) {
      return refusal(CONDITIONS.placementMissing, canvas, index, path + ".placement");
    }
    for (i = 0; i < PLACEMENT_KEYS.length; i += 1) {
      key = PLACEMENT_KEYS[i];
      value = widget.placement[key];
      if (!has(widget.placement, key) || value === null) {
        return refusal(CONDITIONS.placementMissing, canvas, index, path + ".placement." + key);
      }
      if (!wholeNumber(value) || value < 0) {
        return refusal(CONDITIONS.placementNotInteger, canvas, index, path + ".placement." + key);
      }
    }
    if (has(widget, "parameters") && widget.parameters !== null) {
      if (!isObject(widget.parameters)) {
        return refusal(CONDITIONS.malformedBody, canvas, index, path + ".parameters");
      }
      deep = tooDeep(widget.parameters, path + ".parameters", 0);
      if (deep !== "") {
        return refusal(CONDITIONS.nestingTooDeep, canvas, index, deep);
      }
    }
    return null;
  }

  function validateCanvas(canvas, index, seen) {
    var path = "canvases[" + index + "]", deep, ids, i, refused;
    if (!isObject(canvas) || !isNonEmptyString(canvas.id)) {
      return refusal(CONDITIONS.canvasIdMissing, index, -1, path + ".id");
    }
    if (!SLUG.test(canvas.id)) {
      return refusal(CONDITIONS.canvasIdMalformed, index, -1, path + ".id");
    }
    if (has(seen, canvas.id)) {
      return refusal(CONDITIONS.duplicateId, index, -1, path + ".id");
    }
    seen[canvas.id] = true;
    if (!isNonEmptyString(canvas.title)) {
      return refusal(CONDITIONS.canvasTitleMissing, index, -1, path + ".title");
    }
    if (has(canvas, "scope") && canvas.scope !== null) {
      if (!isObject(canvas.scope)) {
        return refusal(CONDITIONS.malformedBody, index, -1, path + ".scope");
      }
      deep = tooDeep(canvas.scope, path + ".scope", 0);
      if (deep !== "") {
        return refusal(CONDITIONS.nestingTooDeep, index, -1, deep);
      }
    }
    if (!has(canvas, "widgets") || canvas.widgets === null) {
      /* An absent widget list is an empty canvas, which the format allows. */
      return null;
    }
    if (!Array.isArray(canvas.widgets)) {
      return refusal(CONDITIONS.malformedBody, index, -1, path + ".widgets");
    }
    ids = {};
    for (i = 0; i < canvas.widgets.length; i += 1) {
      refused = validateWidget(canvas.widgets[i], index, i, path + ".widgets[" + i + "]", ids);
      if (refused) { return refused; }
    }
    return null;
  }

  /* Validates a parsed document: the first structural defect in document
     order, or null. */
  function validate(document) {
    var refused, seen, i;
    if (!isObject(document)) {
      return refusal(CONDITIONS.malformedBody, -1, -1, "");
    }
    refused = validateVersion(document);
    if (refused) { return refused; }
    if (!Array.isArray(document.canvases)) {
      return refusal(CONDITIONS.canvasesMissing, -1, -1, "canvases");
    }
    if (document.canvases.length === 0) {
      return refusal(CONDITIONS.canvasesEmpty, -1, -1, "canvases");
    }
    seen = {};
    for (i = 0; i < document.canvases.length; i += 1) {
      refused = validateCanvas(document.canvases[i], i, seen);
      if (refused) { return refused; }
    }
    return null;
  }

  /* Validates a document's JSON text, as the server receives it. */
  function validateText(text) {
    var parsed;
    if (utf8Length(text) > MAX_DOCUMENT_BYTES) {
      return refusal(CONDITIONS.documentTooLarge, -1, -1, "");
    }
    try {
      parsed = JSON.parse(text);
    } catch (error) {
      return refusal(CONDITIONS.malformedBody, -1, -1, "");
    }
    return validate(parsed);
  }

  opnview.document = {
    CURRENT_FORMAT_VERSION: CURRENT_FORMAT_VERSION,
    MINIMUM_FORMAT_VERSION: MINIMUM_FORMAT_VERSION,
    MAX_DOCUMENT_BYTES: MAX_DOCUMENT_BYTES,
    CONDITIONS: CONDITIONS,
    validate: validate,
    validateText: validateText
  };
})(this);
