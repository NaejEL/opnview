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
  /* The deepest the two parsers read a text, in containers from its root: a
     valid document is a dozen deep, and an unknown member it keeps may be
     deeper, but a text nested past this is refused by name and line rather
     than read until the interpreter runs out of stack. */
  var MAX_PARSE_DEPTH = 256;
  /* The byte-order mark an editor may write at the head of a UTF-8 file. It
     is not part of the document, and is dropped before it is read. */
  var BYTE_ORDER_MARK = "\uFEFF";
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
    /* encodeURIComponent writes each UTF-8 byte it escapes as three characters and leaves the
     * others as one: the bytes are its length less two per escape. It is native, where the loop
     * below is one interpreted step per character of a text that may be a mebibyte long. It
     * refuses a lone surrogate, which the loop counts as three bytes, as the server does. */
    try {
      var encoded = encodeURIComponent(text);
      return encoded.length - 2 * (encoded.split("%").length - 1);
    } catch (error) {
      return utf8Counted(text);
    }
  }

  function utf8Counted(text) {
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


  /* =====================================================================
   * READING AND WRITING THE TEXT: JSON, a restricted YAML, the migration
   * ladder and the coercion of parameters (F7-36 of
   * specs/SPEC-step-7-frontend.md; docs/dashboard-format.md, "JSON and
   * YAML" and "Versioning and compatibility").
   *
   * ONE MODEL, TWO SYNTAXES. A document is parsed from either syntax into
   * the same object, members in their written order, and every member and
   * element is mapped to the line it starts on, so the editor can mark a
   * refusal, an unresolved reference or a kept key where it is written.
   *
   * THE YAML IS A SUBSET, AND WHAT IS OUTSIDE IT IS REFUSED BY NAME AND
   * LINE, never half-read: anchors, aliases, tags, multiple documents,
   * non-string keys, block scalars, tabs in indentation and duplicate keys.
   * It accepts every construct of the format's worked example -- block maps
   * and sequences, "- key: value" items, one-line flow maps and sequences,
   * comments, plain and quoted scalars -- and every construct its own
   * serialiser writes. Comments are not in the model: they are reported, so
   * the editor can warn before a save rewrites the text.
   *
   * What these functions answer are closed codes and line numbers; the
   * interface translates them.
   * =================================================================== */

  var PARSE_CONDITIONS = {
    documentEmpty: "document_empty",
    jsonSyntax: "json_syntax",
    yamlSyntax: "yaml_syntax",
    yamlIndentation: "yaml_indentation",
    yamlTab: "yaml_tab",
    yamlAnchor: "yaml_anchor",
    yamlAlias: "yaml_alias",
    yamlTag: "yaml_tag",
    yamlMultipleDocuments: "yaml_multiple_documents",
    yamlNonStringKey: "yaml_non_string_key",
    yamlDuplicateKey: "yaml_duplicate_key",
    yamlUnterminatedString: "yaml_unterminated_string",
    documentTooDeep: "document_too_deep",
    documentUnreadable: "document_unreadable",
    valueNotCoercible: "value_not_coercible"
  };

  /* A parse failure, thrown inside the parsers and caught by parse(). */
  function ParseFailure(condition, line) {
    this.condition = condition;
    this.line = line === undefined ? null : line;
  }

  function fail(condition, line) {
    throw new ParseFailure(condition, line);
  }

  /* Sets a member without ever reaching the prototype: a key named
     "__proto__" is a member like any other. */
  function setMember(object, key, value) {
    if (key === "__proto__") {
      Object.defineProperty(object, key, { value: value, enumerable: true, writable: true, configurable: true });
    } else {
      object[key] = value;
    }
  }

  function childPath(path, key) {
    return path === "" ? key : path + "." + key;
  }

  /* A text without the byte-order mark an editor may have written first. */
  function withoutMark(text) {
    var written = String(text);
    return written.charAt(0) === BYTE_ORDER_MARK ? written.slice(1) : written;
  }

  function detect(text) {
    var match = /^\s*(\S)/.exec(withoutMark(text));
    return match && match[1] === "{" ? "json" : "yaml";
  }

  /* ---------------------------------------------------------------------
   * JSON, with the line each member starts on.
   * ------------------------------------------------------------------- */

  function parseJSON(text, paths) {
    var position = 0, line = 1, length = text.length;

    function whitespace() {
      var c;
      while (position < length) {
        c = text.charAt(position);
        if (c === "\n") { line += 1; } else if (c !== " " && c !== "\t" && c !== "\r") { return; }
        position += 1;
      }
    }

    function stringToken() {
      var start = position, c;
      position += 1;
      while (position < length) {
        c = text.charAt(position);
        if (c === "\\") { position += 2; continue; }
        if (c === "\"") {
          position += 1;
          try { return JSON.parse(text.slice(start, position)); } catch (error) { fail(PARSE_CONDITIONS.jsonSyntax, line); }
        }
        if (c === "\n") { fail(PARSE_CONDITIONS.jsonSyntax, line); }
        position += 1;
      }
      return fail(PARSE_CONDITIONS.jsonSyntax, line);
    }

    function value(path, depth) {
      var c, result, key, index, start, token;
      whitespace();
      if (position >= length) { fail(PARSE_CONDITIONS.jsonSyntax, line); }
      c = text.charAt(position);
      if ((c === "{" || c === "[") && depth >= MAX_PARSE_DEPTH) { fail(PARSE_CONDITIONS.documentTooDeep, line); }
      if (c === "{") {
        position += 1;
        result = {};
        whitespace();
        if (text.charAt(position) === "}") { position += 1; return result; }
        for (;;) {
          whitespace();
          if (text.charAt(position) !== "\"") { fail(PARSE_CONDITIONS.jsonSyntax, line); }
          start = line;
          key = stringToken();
          whitespace();
          if (text.charAt(position) !== ":") { fail(PARSE_CONDITIONS.jsonSyntax, line); }
          position += 1;
          paths[childPath(path, key)] = start - 1;
          setMember(result, key, value(childPath(path, key), depth + 1));
          whitespace();
          c = text.charAt(position);
          position += 1;
          if (c === "}") { return result; }
          if (c !== ",") { fail(PARSE_CONDITIONS.jsonSyntax, line); }
        }
      }
      if (c === "[") {
        position += 1;
        result = [];
        whitespace();
        if (text.charAt(position) === "]") { position += 1; return result; }
        for (index = 0; ; index += 1) {
          whitespace();
          paths[path + "[" + index + "]"] = line - 1;
          result.push(value(path + "[" + index + "]", depth + 1));
          whitespace();
          c = text.charAt(position);
          position += 1;
          if (c === "]") { return result; }
          if (c !== ",") { fail(PARSE_CONDITIONS.jsonSyntax, line); }
        }
      }
      if (c === "\"") { return stringToken(); }
      start = position;
      while (position < length && /[-+0-9.eEa-z]/.test(text.charAt(position))) { position += 1; }
      token = text.slice(start, position);
      if (token === "true") { return true; }
      if (token === "false") { return false; }
      if (token === "null") { return null; }
      if (/^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$/.test(token)) { return Number(token); }
      return fail(PARSE_CONDITIONS.jsonSyntax, line);
    }

    var document;
    whitespace();
    if (position >= length) { fail(PARSE_CONDITIONS.documentEmpty, null); }
    document = value("", 0);
    whitespace();
    if (position < length) { fail(PARSE_CONDITIONS.jsonSyntax, line); }
    return document;
  }

  /* ---------------------------------------------------------------------
   * The YAML subset.
   * ------------------------------------------------------------------- */

  var YAML_INT = /^[-+]?[0-9]+$/;
  var YAML_FLOAT = /^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$/;

  /* What a plain scalar means: the core schema's null, booleans and
     numbers, and otherwise the text itself. */
  function resolvePlain(text) {
    if (text === "~" || text === "null" || text === "Null" || text === "NULL") { return null; }
    if (text === "true" || text === "True" || text === "TRUE") { return true; }
    if (text === "false" || text === "False" || text === "FALSE") { return false; }
    if (YAML_INT.test(text) || YAML_FLOAT.test(text)) { return Number(text); }
    return text;
  }

  /* Whether a character can open a token: a quote opens a quoted scalar
     only there, never inside a plain one. */
  function tokenStart(text, index) {
    return index === 0 || /[\s:\-\[{,]/.test(text.charAt(index - 1));
  }

  /* A line without its comment: {text, comment}. A "#" is a comment only
     outside quotes and at the start or after whitespace. */
  function stripComment(text) {
    var index, c, quote = null;
    for (index = 0; index < text.length; index += 1) {
      c = text.charAt(index);
      if (quote === "\"") {
        if (c === "\\") { index += 1; } else if (c === "\"") { quote = null; }
      } else if (quote === "'") {
        if (c === "'") { quote = null; }
      } else if ((c === "\"" || c === "'") && tokenStart(text, index)) {
        quote = c;
      } else if (c === "#" && (index === 0 || /\s/.test(text.charAt(index - 1)))) {
        return { text: text.slice(0, index), comment: true };
      }
    }
    return { text: text, comment: false };
  }

  /* The index just past the quoted scalar opening at index, or -1. */
  function quoteEnd(text, index) {
    var quote = text.charAt(index), i;
    for (i = index + 1; i < text.length; i += 1) {
      if (quote === "\"" && text.charAt(i) === "\\") { i += 1; continue; }
      if (text.charAt(i) === quote) {
        if (quote === "'" && text.charAt(i + 1) === "'") { i += 1; continue; }
        return i + 1;
      }
    }
    return -1;
  }

  function decodeQuoted(token, line) {
    if (token.charAt(0) === "'") {
      return token.slice(1, -1).replace(/''/g, "'");
    }
    try { return JSON.parse(token); } catch (error) { return fail(PARSE_CONDITIONS.yamlSyntax, line); }
  }

  /* The index just past the bracket closing the one at index, or -1. */
  function bracketEnd(text, index) {
    var depth = 0, i, c, end;
    for (i = index; i < text.length; i += 1) {
      c = text.charAt(i);
      if ((c === "\"" || c === "'") && tokenStart(text, i)) {
        end = quoteEnd(text, i);
        if (end < 0) { return -1; }
        i = end - 1;
      } else if (c === "[" || c === "{") {
        depth += 1;
      } else if (c === "]" || c === "}") {
        depth -= 1;
        if (depth === 0) { return i + 1; }
      }
    }
    return -1;
  }

  /* What an indicator at the start of a token refuses. */
  function refuseIndicator(c, line) {
    if (c === "&") { fail(PARSE_CONDITIONS.yamlAnchor, line); }
    if (c === "*") { fail(PARSE_CONDITIONS.yamlAlias, line); }
    if (c === "!") { fail(PARSE_CONDITIONS.yamlTag, line); }
    if (c === "|" || c === ">" || c === "%" || c === "@" || c === "`") { fail(PARSE_CONDITIONS.yamlSyntax, line); }
  }

  /* A mapping entry's key and the rest of its line, or null when the line
     is not a mapping entry. */
  function splitKey(text, line) {
    var c = text.charAt(0), end, after, cut, key, resolved;
    if (c === "\"" || c === "'") {
      end = quoteEnd(text, 0);
      if (end < 0) { fail(PARSE_CONDITIONS.yamlUnterminatedString, line); }
      after = text.slice(end).replace(/^ +/, "");
      if (after.charAt(0) === ":" && (after.length === 1 || after.charAt(1) === " ")) {
        return { key: decodeQuoted(text.slice(0, end), line), rest: after.slice(1) };
      }
      return null;
    }
    if (c === "[" || c === "{") {
      end = bracketEnd(text, 0);
      if (end > 0) {
        after = text.slice(end).replace(/^ +/, "");
        if (after.charAt(0) === ":" && (after.length === 1 || after.charAt(1) === " ")) {
          fail(PARSE_CONDITIONS.yamlNonStringKey, line);
        }
      }
      return null;
    }
    if (c === "?" && (text.length === 1 || text.charAt(1) === " ")) { fail(PARSE_CONDITIONS.yamlNonStringKey, line); }
    cut = text.indexOf(": ");
    if (text.charAt(text.length - 1) === ":" && (cut < 0 || cut > text.length - 1)) { cut = text.length - 1; }
    if (cut < 0) { return null; }
    refuseIndicator(c, line);
    key = text.slice(0, cut).replace(/\s+$/, "");
    if (key === "") { fail(PARSE_CONDITIONS.yamlNonStringKey, line); }
    resolved = resolvePlain(key);
    if (typeof resolved !== "string") { fail(PARSE_CONDITIONS.yamlNonStringKey, line); }
    return { key: key, rest: text.slice(cut + 1) };
  }

  /* One value written on a line: a quoted or plain scalar, or a flow
     collection on that one line. */
  function inlineValue(text, line, path, paths, depth) {
    var position = 0;

    function spaces() {
      while (text.charAt(position) === " ") { position += 1; }
    }

    function quoted() {
      var end = quoteEnd(text, position), token;
      if (end < 0) { fail(PARSE_CONDITIONS.yamlUnterminatedString, line); }
      token = text.slice(position, end);
      position = end;
      return decodeQuoted(token, line);
    }

    function plain(stops) {
      var start = position, c, token;
      while (position < text.length) {
        c = text.charAt(position);
        if (stops.indexOf(c) >= 0) { break; }
        if (c === ":" && (position + 1 === text.length || /[ ,\]}]/.test(text.charAt(position + 1))) &&
            stops.indexOf(":") >= 0) { break; }
        position += 1;
      }
      token = text.slice(start, position).replace(/\s+$/, "");
      if (token === "") { fail(PARSE_CONDITIONS.yamlSyntax, line); }
      return token;
    }

    function flowKey() {
      var c = text.charAt(position), key;
      if (c === "\"" || c === "'") { return quoted(); }
      if (c === "[" || c === "{" || c === "?") { fail(PARSE_CONDITIONS.yamlNonStringKey, line); }
      refuseIndicator(c, line);
      key = plain(":,]}");
      if (typeof resolvePlain(key) !== "string") { fail(PARSE_CONDITIONS.yamlNonStringKey, line); }
      return key;
    }

    function flow(at, inFlow, level) {
      var c, result, key, index, token, seen;
      spaces();
      c = text.charAt(position);
      if (position >= text.length) { fail(PARSE_CONDITIONS.yamlSyntax, line); }
      refuseIndicator(c, line);
      if ((c === "{" || c === "[") && level >= MAX_PARSE_DEPTH) { fail(PARSE_CONDITIONS.documentTooDeep, line); }
      if (c === "{") {
        position += 1;
        result = {};
        seen = {};
        spaces();
        if (text.charAt(position) === "}") { position += 1; return result; }
        for (;;) {
          spaces();
          key = flowKey();
          spaces();
          if (text.charAt(position) !== ":") { fail(PARSE_CONDITIONS.yamlSyntax, line); }
          position += 1;
          if (Object.prototype.hasOwnProperty.call(seen, key)) { fail(PARSE_CONDITIONS.yamlDuplicateKey, line); }
          seen[key] = true;
          paths[childPath(at, key)] = line - 1;
          setMember(result, key, flow(childPath(at, key), true, level + 1));
          spaces();
          c = text.charAt(position);
          position += 1;
          if (c === "}") { return result; }
          if (c !== ",") { fail(PARSE_CONDITIONS.yamlSyntax, line); }
        }
      }
      if (c === "[") {
        position += 1;
        result = [];
        spaces();
        if (text.charAt(position) === "]") { position += 1; return result; }
        for (index = 0; ; index += 1) {
          paths[at + "[" + index + "]"] = line - 1;
          result.push(flow(at + "[" + index + "]", true, level + 1));
          spaces();
          c = text.charAt(position);
          position += 1;
          if (c === "]") { return result; }
          if (c !== ",") { fail(PARSE_CONDITIONS.yamlSyntax, line); }
        }
      }
      if (c === "\"" || c === "'") { return quoted(); }
      if (!inFlow) {
        token = text.slice(position).replace(/\s+$/, "");
        position = text.length;
        if (token.indexOf(": ") >= 0 || token.charAt(token.length - 1) === ":" ||
            (token.charAt(0) === "-" && (token.length === 1 || token.charAt(1) === " ")) ||
            (token.charAt(0) === "?" && (token.length === 1 || token.charAt(1) === " "))) {
          fail(PARSE_CONDITIONS.yamlSyntax, line);
        }
        return resolvePlain(token);
      }
      return resolvePlain(plain(",]}"));
    }

    var result = flow(path, false, depth || 0);
    spaces();
    if (position < text.length) { fail(PARSE_CONDITIONS.yamlSyntax, line); }
    return result;
  }

  function isSequenceItem(text) {
    return text === "-" || text.slice(0, 2) === "- ";
  }

  function parseYAML(text, paths, report) {
    var raw = String(text).split("\n"), lines = [], index, body, stripped, lead, content = false, marker = false;
    for (index = 0; index < raw.length; index += 1) {
      stripped = stripComment(raw[index].replace(/\r$/, ""));
      if (stripped.comment) { report.comments = true; }
      body = stripped.text.replace(/\s+$/, "");
      if (body.replace(/^\s+/, "") === "") { continue; }
      lead = /^[ \t]*/.exec(body)[0];
      if (lead.indexOf("\t") >= 0) { fail(PARSE_CONDITIONS.yamlTab, index + 1); }
      body = body.slice(lead.length);
      if (body === "---" || body.slice(0, 4) === "--- " || body === "...") {
        if (body === "---" && !content && !marker && lead === "") { marker = true; continue; }
        fail(PARSE_CONDITIONS.yamlMultipleDocuments, index + 1);
      }
      if (body.charAt(0) === "%" && !content) { fail(PARSE_CONDITIONS.yamlSyntax, index + 1); }
      content = true;
      lines.push({ n: index + 1, indent: lead.length, text: body });
    }
    if (lines.length === 0) { fail(PARSE_CONDITIONS.documentEmpty, null); }
    var position = 0;

    /* How many block containers below the root the line being read sits in. */
    var depth = 0;

    function block(indent, path) {
      var line = lines[position], result;
      if (depth + 1 >= MAX_PARSE_DEPTH) { fail(PARSE_CONDITIONS.documentTooDeep, line.n); }
      depth += 1;
      result = isSequenceItem(line.text) ? sequence(indent, path) : mapping(indent, path);
      depth -= 1;
      return result;
    }

    function entryValue(rest, indent, path, line) {
      var next;
      rest = rest.replace(/^ +/, "");
      if (rest !== "") { return inlineValue(rest, line, path, paths, depth + 1); }
      if (position < lines.length) {
        next = lines[position];
        if (next.indent > indent) { return block(next.indent, path); }
        if (next.indent === indent && isSequenceItem(next.text)) {
          if (depth + 1 >= MAX_PARSE_DEPTH) { fail(PARSE_CONDITIONS.documentTooDeep, next.n); }
          depth += 1;
          next = sequence(indent, path);
          depth -= 1;
          return next;
        }
      }
      return null;
    }

    function mapping(indent, path) {
      var result = {}, seen = {}, line, entry, at;
      while (position < lines.length) {
        line = lines[position];
        if (line.indent < indent) { break; }
        if (line.indent > indent) { fail(PARSE_CONDITIONS.yamlIndentation, line.n); }
        if (isSequenceItem(line.text)) { fail(PARSE_CONDITIONS.yamlSyntax, line.n); }
        entry = splitKey(line.text, line.n);
        if (!entry) { fail(PARSE_CONDITIONS.yamlSyntax, line.n); }
        if (Object.prototype.hasOwnProperty.call(seen, entry.key)) { fail(PARSE_CONDITIONS.yamlDuplicateKey, line.n); }
        seen[entry.key] = true;
        at = childPath(path, entry.key);
        paths[at] = line.n - 1;
        position += 1;
        setMember(result, entry.key, entryValue(entry.rest, indent, at, line.n));
      }
      return result;
    }

    function sequence(indent, path) {
      var result = [], line, rest, trimmed, at, column;
      while (position < lines.length) {
        line = lines[position];
        if (line.indent < indent) { break; }
        if (line.indent > indent) { fail(PARSE_CONDITIONS.yamlIndentation, line.n); }
        if (!isSequenceItem(line.text)) { break; }
        at = path + "[" + result.length + "]";
        paths[at] = line.n - 1;
        rest = line.text === "-" ? "" : line.text.slice(2);
        trimmed = rest.replace(/^ +/, "");
        if (trimmed === "") {
          position += 1;
          result.push(position < lines.length && lines[position].indent > indent ? block(lines[position].indent, at) : null);
        } else if (isSequenceItem(trimmed) || splitKey(trimmed, line.n)) {
          column = indent + 2 + (rest.length - trimmed.length);
          lines[position] = { n: line.n, indent: column, text: trimmed };
          result.push(block(column, at));
        } else {
          position += 1;
          result.push(inlineValue(trimmed, line.n, at, paths, depth + 1));
        }
      }
      return result;
    }

    var first = lines[0], document;
    if (isSequenceItem(first.text)) {
      document = sequence(first.indent, "");
    } else if (splitKey(first.text, first.n)) {
      document = mapping(first.indent, "");
    } else {
      if (lines.length > 1) { fail(PARSE_CONDITIONS.yamlSyntax, lines[1].n); }
      position = 1;
      document = inlineValue(first.text, first.n, "", paths);
    }
    if (position < lines.length) { fail(PARSE_CONDITIONS.yamlIndentation, lines[position].n); }
    return document;
  }

  /* Parses a text in one syntax, detected when not given. */
  function parse(text, syntax) {
    var paths = {}, report = { comments: false }, document, written = withoutMark(text);
    var chosen = syntax || detect(written);
    try {
      document = chosen === "json" ? parseJSON(written, paths) : parseYAML(written, paths, report);
    } catch (error) {
      if (error instanceof ParseFailure) { return { error: { condition: error.condition, line: error.line } }; }
      /* The depth bound above is what refuses a deep text; an interpreter whose stack is shorter
         than it is still answered by name, never by a thrown error. */
      if (error instanceof RangeError) { return { error: { condition: PARSE_CONDITIONS.documentTooDeep, line: null } }; }
      throw error;
    }
    return { document: document, syntax: chosen, comments: report.comments, paths: paths };
  }

  function hasComments(text) {
    var raw, index;
    if (detect(text) === "json") { return false; }
    raw = withoutMark(text).split("\n");
    for (index = 0; index < raw.length; index += 1) {
      if (stripComment(raw[index]).comment) { return true; }
    }
    return false;
  }

  /* ---------------------------------------------------------------------
   * Serialisers: the text and, per line, the path it writes.
   * ------------------------------------------------------------------- */

  function indentation(level) {
    return new Array(level + 1).join("  ");
  }

  function isContainer(value) {
    return value !== null && typeof value === "object";
  }

  function jsonLines(value, path, level, prefix, comma, out) {
    var keys, index, end = comma ? "," : "";
    if (!isContainer(value)) {
      out.push({ text: indentation(level) + prefix + JSON.stringify(value) + end, path: path });
      return;
    }
    if (Array.isArray(value)) {
      if (value.length === 0) { out.push({ text: indentation(level) + prefix + "[]" + end, path: path }); return; }
      out.push({ text: indentation(level) + prefix + "[", path: path });
      for (index = 0; index < value.length; index += 1) {
        jsonLines(value[index], path + "[" + index + "]", level + 1, "", index < value.length - 1, out);
      }
      out.push({ text: indentation(level) + "]" + end, path: path });
      return;
    }
    keys = Object.keys(value);
    if (keys.length === 0) { out.push({ text: indentation(level) + prefix + "{}" + end, path: path }); return; }
    out.push({ text: indentation(level) + prefix + "{", path: path });
    for (index = 0; index < keys.length; index += 1) {
      jsonLines(value[keys[index]], childPath(path, keys[index]), level + 1, JSON.stringify(keys[index]) + ": ",
        index < keys.length - 1, out);
    }
    out.push({ text: indentation(level) + "}" + end, path: path });
  }

  /* Whether a string can be written plain and read back as itself. */
  function plainSafe(text) {
    return typeof text === "string" && text !== "" && text === text.replace(/^\s+|\s+$/g, "") &&
      !/[\u0000-\u001f\u007f\u2028\u2029\ufeff]/.test(text) &&
      "-?:,[]{}#&*!|>'\"%@`".indexOf(text.charAt(0)) < 0 &&
      text.indexOf(": ") < 0 && text.indexOf(" #") < 0 && text.charAt(text.length - 1) !== ":" &&
      resolvePlain(text) === text;
  }

  function yamlScalar(value) {
    if (typeof value === "string") { return plainSafe(value) ? value : JSON.stringify(value); }
    return JSON.stringify(value);
  }

  function yamlEntries(object, path, level, out) {
    Object.keys(object).forEach(function (key) {
      var value = object[key], at = childPath(path, key), head = indentation(level) + yamlScalar(key) + ":";
      if (!isContainer(value)) {
        out.push({ text: head + " " + yamlScalar(value), path: at });
      } else if (Array.isArray(value)) {
        if (value.length === 0) { out.push({ text: head + " []", path: at }); return; }
        out.push({ text: head, path: at });
        yamlItems(value, at, level + 1, out);
      } else {
        if (Object.keys(value).length === 0) { out.push({ text: head + " {}", path: at }); return; }
        out.push({ text: head, path: at });
        yamlEntries(value, at, level + 1, out);
      }
    });
  }

  function yamlItems(array, path, level, out) {
    array.forEach(function (value, index) {
      var at = path + "[" + index + "]", head = indentation(level) + "-", nested;
      if (!isContainer(value)) {
        out.push({ text: head + " " + yamlScalar(value), path: at });
      } else if (Array.isArray(value)) {
        if (value.length === 0) { out.push({ text: head + " []", path: at }); return; }
        out.push({ text: head, path: at });
        yamlItems(value, at, level + 1, out);
      } else if (Object.keys(value).length === 0) {
        out.push({ text: head + " {}", path: at });
      } else {
        nested = [];
        yamlEntries(value, at, level + 1, nested);
        nested[0].text = head + " " + nested[0].text.slice((level + 1) * 2);
        nested.forEach(function (line) { out.push(line); });
      }
    });
  }

  function serialise(document, syntax) {
    var lines = [];
    if (syntax === "json") {
      jsonLines(document, "", 0, "", false, lines);
    } else if (Array.isArray(document)) {
      if (document.length === 0) { lines.push({ text: "[]", path: "" }); } else { yamlItems(document, "", 0, lines); }
    } else if (isContainer(document)) {
      if (Object.keys(document).length === 0) { lines.push({ text: "{}", path: "" }); } else { yamlEntries(document, "", 0, lines); }
    } else {
      lines.push({ text: yamlScalar(document), path: "" });
    }
    var text = lines.map(function (line) { return line.text; }).join("\n");
    return { text: syntax === "json" ? text : text + "\n", lines: lines };
  }

  function lineOf(lines, path) {
    var index;
    for (index = 0; index < lines.length; index += 1) {
      if (lines[index].path === path) { return index; }
    }
    for (index = 0; index < lines.length; index += 1) {
      if (lines[index].path.indexOf(path + ".") === 0 || lines[index].path.indexOf(path + "[") === 0) { return index; }
    }
    return -1;
  }

  /* The line (1-based) a path starts on, or the nearest enclosing path's:
     a refusal naming a member that is absent is marked on its owner. */
  function lineForPath(paths, path) {
    var at = path, cut;
    while (at !== "") {
      if (Object.prototype.hasOwnProperty.call(paths, at)) { return paths[at] + 1; }
      cut = Math.max(at.lastIndexOf("."), at.lastIndexOf("["));
      at = cut < 0 ? "" : at.slice(0, cut);
    }
    return null;
  }

  /* ---------------------------------------------------------------------
   * The migration ladder: one-way, in memory, one step per version. Empty
   * while the current version is the first.
   * ------------------------------------------------------------------- */

  var MIGRATIONS = {};

  function copyOf(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function migrate(document) {
    var refused, migrated, version, from;
    if (!isObject(document)) { return { refusal: refusal(CONDITIONS.malformedBody, -1, -1, "") }; }
    refused = validateVersion(document);
    if (refused) { return { refusal: refused }; }
    migrated = copyOf(document);
    from = migrated.format_version;
    for (version = from; version < CURRENT_FORMAT_VERSION; version += 1) {
      migrated = MIGRATIONS[version](migrated);
      migrated.format_version = version + 1;
    }
    return { document: migrated, from: from, to: CURRENT_FORMAT_VERSION };
  }

  /* ---------------------------------------------------------------------
   * Coercion of scalars to the type the manifest declares
   * (docs/dashboard-format.md, the note under the worked example).
   * ------------------------------------------------------------------- */

  var NOT_COERCIBLE = {};

  function coerceScalar(value, kind) {
    if (value === null || value === undefined) { return value; }
    if (kind === "integer") {
      if (wholeNumber(value)) { return value; }
      if (typeof value === "string" && /^[-+]?[0-9]+$/.test(value)) { return Number(value); }
      return NOT_COERCIBLE;
    }
    if (kind === "number") {
      if (typeof value === "number" && isFinite(value)) { return value; }
      if (typeof value === "string" && YAML_FLOAT.test(value)) { return Number(value); }
      return NOT_COERCIBLE;
    }
    if (kind === "boolean") {
      if (typeof value === "boolean") { return value; }
      if (value === "true") { return true; }
      if (value === "false") { return false; }
      return NOT_COERCIBLE;
    }
    if (kind === "enum" || kind === "text") {
      if (typeof value === "string") { return value; }
      if (typeof value === "number" || typeof value === "boolean") { return String(value); }
      return NOT_COERCIBLE;
    }
    return value;
  }

  function coerceValue(value, kind, path, fail) {
    var index, element, coerced;
    if (kind === "enum_set" || kind === "string_list") {
      if (value === null || value === undefined) { return value; }
      if (!Array.isArray(value)) { return fail(path, value); }
      for (index = 0; index < value.length; index += 1) {
        element = coerceScalar(value[index], "enum");
        if (element === NOT_COERCIBLE) { return fail(path + "[" + index + "]", value[index]); }
        value[index] = element;
      }
      return value;
    }
    if (kind === "integer" || kind === "number" || kind === "boolean" || kind === "enum" || kind === "text") {
      coerced = coerceScalar(value, kind);
      return coerced === NOT_COERCIBLE ? fail(path, value) : coerced;
    }
    return value;
  }

  function coerce(document, rowFor) {
    var copy = copyOf(document), found = null;
    function failAt(canvas, widget) {
      return function (path, value) {
        if (!found) {
          found = refusal(PARSE_CONDITIONS.valueNotCoercible, canvas, widget, path);
          found.value = value === undefined ? null : value;
        }
        return value;
      };
    }
    (Array.isArray(copy.canvases) ? copy.canvases : []).forEach(function (canvas, canvasIndex) {
      if (!isObject(canvas)) { return; }
      var base = "canvases[" + canvasIndex + "]";
      if (isObject(canvas.grid)) {
        ["columns", "row_height"].forEach(function (key) {
          if (has(canvas.grid, key)) {
            canvas.grid[key] = coerceValue(canvas.grid[key], "integer", base + ".grid." + key, failAt(canvasIndex, -1));
          }
        });
      }
      (Array.isArray(canvas.widgets) ? canvas.widgets : []).forEach(function (widget, widgetIndex) {
        var row = isObject(widget) && typeof widget.type === "string" && rowFor ? rowFor(widget.type) : null;
        if (!row || !isObject(widget.parameters)) { return; }
        (row.parameters || []).forEach(function (parameter) {
          if (!has(widget.parameters, parameter.key)) { return; }
          widget.parameters[parameter.key] = coerceValue(widget.parameters[parameter.key], parameter.kind,
            base + ".widgets[" + widgetIndex + "].parameters." + parameter.key, failAt(canvasIndex, widgetIndex));
        });
      });
    });
    return { document: copy, refusal: found };
  }

  /* ---------------------------------------------------------------------
   * One call for an import and for Apply.
   * ------------------------------------------------------------------- */

  function failure(refused, line) {
    var error = {
      condition: refused.condition, line: line,
      canvas_index: has(refused, "canvas_index") ? refused.canvas_index : null,
      widget_index: has(refused, "widget_index") ? refused.widget_index : null,
      field: has(refused, "field") ? refused.field : null,
      path: has(refused, "path") ? refused.path : null
    };
    ["file_version", "current", "minimum", "value"].forEach(function (key) {
      if (has(refused, key)) { error[key] = refused[key]; }
    });
    return { error: error };
  }

  function load(text, rowFor) {
    var parsed, refused, migrated, coerced;
    if (utf8Length(withoutMark(text)) > MAX_DOCUMENT_BYTES) {
      return failure(refusal(CONDITIONS.documentTooLarge, -1, -1, ""), null);
    }
    parsed = parse(text);
    if (parsed.error) { return failure({ condition: parsed.error.condition }, parsed.error.line); }
    refused = validate(parsed.document);
    if (refused) { return failure(refused, lineForPath(parsed.paths, refused.path)); }
    migrated = migrate(parsed.document);
    if (migrated.refusal) { return failure(migrated.refusal, lineForPath(parsed.paths, migrated.refusal.path)); }
    coerced = coerce(migrated.document, rowFor);
    if (coerced.refusal) { return failure(coerced.refusal, lineForPath(parsed.paths, coerced.refusal.path)); }
    return { document: coerced.document, syntax: parsed.syntax, comments: parsed.comments, paths: parsed.paths };
  }

  opnview.document = {
    CURRENT_FORMAT_VERSION: CURRENT_FORMAT_VERSION,
    MINIMUM_FORMAT_VERSION: MINIMUM_FORMAT_VERSION,
    MAX_DOCUMENT_BYTES: MAX_DOCUMENT_BYTES,
    MAX_PARSE_DEPTH: MAX_PARSE_DEPTH,
    CONDITIONS: CONDITIONS,
    PARSE_CONDITIONS: Object.keys(PARSE_CONDITIONS).map(function (name) { return PARSE_CONDITIONS[name]; }),
    validate: validate,
    validateText: validateText,
    detect: detect,
    parse: parse,
    serialise: serialise,
    lineOf: lineOf,
    hasComments: hasComments,
    migrate: migrate,
    coerce: coerce,
    load: load
  };
})(this);
