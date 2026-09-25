/* ====================================================================
 * FORM-AGAINST-CATALOGUE CHECK — run by hand, never part of `checks`.
 *
 * HOW TO RUN IT. From the repository root:
 *
 *     node docs/mockups/check-form-covers-catalogue.js
 *
 * It reads three files and nothing else. It is a Node script and not a
 * console paste because it has to read `docs/widget-catalogue.md`,
 * which the page cannot; the other two harnesses are console pastes
 * and say so in their own headers.
 *
 * WHY IT EXISTS. `check-inert-parameters.js` used to ask the page's own
 * `CATALOGUE[type].params` whether a parameter key was declared. That
 * is the list under test, so when the list was wrong the harness
 * inherited the error: `custom_chart.axes` is declared by
 * docs/widget-catalogue.md, was missing from the page's list, and was
 * therefore filed as a "kept unknown" and exempted from the count it
 * was the evidence for. This script is the check at source. Run it
 * whenever docs/widget-catalogue.md moves.
 *
 * WHAT IT ASSERTS, in three directions.
 *
 *   1. TRANSCRIPTION. Every key in `CATALOGUE_DOC` inside
 *      `check-inert-parameters.js` appears as a backticked token in
 *      that entry's **Parameters** paragraph in the catalogue. This is
 *      the direction that keeps the hand transcription honest.
 *
 *   2. OFFERED-UNDECLARED. Every key the page's `CATALOGUE[type].params`
 *      offers is either in the transcription or in the departures list
 *      this harness records. This is the direction that matters and
 *      the one that is reliable: a key the form offers and the document
 *      does not declare is either a departure or a defect.
 *
 *   3. DECLARED-NOT-OFFERED, reported and not failed. Keys the document
 *      declares that the form does not carry. It is a real fact about
 *      the page — `custom_chart` declares `series`, `axes` and
 *      `annotations` and the form offers none of the three — and it is
 *      information rather than a broken control,
 *      because nothing inert is on screen for it.
 *
 * WHAT IT CANNOT ASSERT. That a backticked token in the Parameters
 * paragraph IS a key: those paragraphs put keys and their permitted
 * VALUES in the same backticks, so the document→transcription
 * direction can only be checked as "the token occurs", never as "the
 * token is a key and the list is complete". That noise is why
 * direction 1 is a containment test and not an equality test.
 * ==================================================================== */

"use strict";

var fs = require("fs");
var path = require("path");

var ROOT = path.resolve(__dirname, "..", "..");
var CATALOGUE_MD = path.join(ROOT, "docs", "widget-catalogue.md");
var INERT_JS = path.join(ROOT, "docs", "mockups", "check-inert-parameters.js");
var PAGE_HTML = path.join(ROOT, "docs", "mockups", "canvas-mockup.html");

function read(file) { return fs.readFileSync(file, "utf8"); }

/* ---------------------------------------------------------------- *
 * The catalogue document: one record per "### " entry, carrying its
 * **Type** and the backticked tokens of its **Parameters** paragraph.
 * ---------------------------------------------------------------- */
function parseCatalogue(text) {
  var lines = text.split(/\r?\n/);
  var entries = [], current = null, mode = null, buffer = "";

  function flushParameters() {
    if (current && buffer !== "") {
      var tokens = buffer.match(/`[^`]+`/g) || [];
      tokens.forEach(function (t) {
        var token = t.slice(1, -1).trim();
        if (/^[a-z][a-z0-9_]*$/.test(token)) { current.tokens[token] = true; }
      });
    }
    buffer = "";
    mode = null;
  }

  lines.forEach(function (line) {
    if (/^### /.test(line)) {
      flushParameters();
      current = { heading: line.slice(4).trim(), type: null, tokens: {} };
      entries.push(current);
      return;
    }
    if (!current) { return; }
    if (/^\*\*Type\*\*/.test(line)) {
      var m = line.match(/`([a-z0-9_]+)`/);
      if (m) { current.type = m[1]; }
      return;
    }
    if (/^\*\*Parameters\*\*/.test(line)) { mode = "params"; buffer = line; return; }
    if (mode === "params") {
      if (line.trim() === "") { flushParameters(); return; }
      buffer += " " + line;
    }
  });
  flushParameters();

  var byType = {};
  entries.forEach(function (e) {
    if (e.type) { byType[e.type] = e; }
  });
  return { entries: entries, byType: byType };
}

/* ---------------------------------------------------------------- *
 * The transcription, read out of the browser harness so that there is
 * exactly one copy of it in the repository.
 * ---------------------------------------------------------------- */
function parseTranscription(text) {
  var start = text.indexOf("var CATALOGUE_DOC = {");
  if (start < 0) { throw new Error("CATALOGUE_DOC not found in check-inert-parameters.js"); }
  var open = text.indexOf("{", start);
  var depth = 0, i, end = -1;
  for (i = open; i < text.length; i++) {
    if (text[i] === "{") { depth++; }
    else if (text[i] === "}") { depth--; if (depth === 0) { end = i; break; } }
  }
  if (end < 0) { throw new Error("CATALOGUE_DOC block is not closed"); }
  /* A literal of string arrays only — evaluated rather than
   * re-parsed, because it is this repository's own file and its shape
   * is asserted immediately below. */
  var literal = text.slice(open, end + 1);
  if (/[^\s{}\[\]:,"'a-zA-Z0-9_]/.test(literal)) {
    throw new Error("CATALOGUE_DOC carries something other than identifiers and string arrays");
  }
  /* eslint-disable no-new-func */
  var value = (new Function("return (" + literal + ");"))();
  return value;
}

function parseDepartures(text) {
  var start = text.indexOf("var DECLARED_DEPARTURES = {");
  if (start < 0) { throw new Error("DECLARED_DEPARTURES not found in check-inert-parameters.js"); }
  var open = text.indexOf("{", start);
  var end = text.indexOf("};", open);
  var body = text.slice(open, end + 1);
  var out = {};
  (body.match(/"[a-z0-9_]+\.[a-z0-9_]+"/g) || []).forEach(function (q) {
    out[q.slice(1, -1)] = true;
  });
  return out;
}

/* ---------------------------------------------------------------- *
 * The page's own form list: CATALOGUE[type].params, read out of the
 * inline script by structure rather than by evaluating the page.
 * ---------------------------------------------------------------- */
function parsePageCatalogue(text) {
  var start = text.indexOf("var CATALOGUE = {");
  if (start < 0) { throw new Error("CATALOGUE not found in canvas-mockup.html"); }
  var open = text.indexOf("{", start);
  var depth = 0, i, end = -1;
  for (i = open; i < text.length; i++) {
    if (text[i] === "{") { depth++; }
    else if (text[i] === "}") { depth--; if (depth === 0) { end = i; break; } }
  }
  var block = text.slice(open, end + 1);

  /* Entries are "  <type>: {" at two spaces of indent inside the
   * literal; params are the `key: "…"` fields of the objects in that
   * entry's `params: [ … ]` array. */
  var out = {}, re = /\n  ([a-z][a-z0-9_]*): \{/g, m, marks = [];
  while ((m = re.exec(block)) !== null) { marks.push({ type: m[1], at: m.index }); }
  marks.forEach(function (mark, idx) {
    var slice = block.slice(mark.at, idx + 1 < marks.length ? marks[idx + 1].at : block.length);
    var pStart = slice.indexOf("params: [");
    if (pStart < 0) { out[mark.type] = []; return; }
    var pEnd = slice.indexOf("\n    ]", pStart);
    var params = slice.slice(pStart, pEnd < 0 ? slice.length : pEnd);
    var keys = [], km, kre = /\{ key: "([a-z0-9_]+)"/g;
    while ((km = kre.exec(params)) !== null) { keys.push(km[1]); }
    out[mark.type] = keys;
  });
  return out;
}

/* ---------------------------------------------------------------- */

var catalogue = parseCatalogue(read(CATALOGUE_MD));
var inertSource = read(INERT_JS);
var transcription = parseTranscription(inertSource);
var departures = parseDepartures(inertSource);
var pageParams = parsePageCatalogue(read(PAGE_HTML));

var failures = [];
var information = [];

/* 1. Transcription against the document. */
Object.keys(transcription).forEach(function (type) {
  var entry = catalogue.byType[type];
  if (!entry) {
    failures.push("transcription: type `" + type + "` has no entry in docs/widget-catalogue.md");
    return;
  }
  transcription[type].forEach(function (key) {
    if (!entry.tokens[key]) {
      failures.push("transcription: `" + type + "." + key +
        "` is transcribed but does not appear in the Parameters paragraph of \"" + entry.heading + "\"");
    }
  });
});
Object.keys(catalogue.byType).forEach(function (type) {
  if (!transcription[type]) {
    failures.push("transcription: docs/widget-catalogue.md declares type `" + type +
      "` and check-inert-parameters.js does not transcribe it");
  }
});

/* 2. Offered but undeclared — the direction that matters. */
Object.keys(pageParams).forEach(function (type) {
  var declared = transcription[type];
  if (!declared) {
    information.push("page offers type `" + type + "`, which is not a catalogue type — it must be a declared departure");
    return;
  }
  pageParams[type].forEach(function (key) {
    if (declared.indexOf(key) >= 0) { return; }
    if (departures[type + "." + key]) { return; }
    failures.push("offered-undeclared: the form offers `" + type + "." + key +
      "`, which docs/widget-catalogue.md does not declare");
  });
});

/* A recorded departure that is no longer offered is a stale entry in
 * the departures list, which would silently widen the exemption. */
Object.keys(departures).forEach(function (pair) {
  var type = pair.slice(0, pair.indexOf("."));
  var key = pair.slice(pair.indexOf(".") + 1);
  if (!pageParams[type] || pageParams[type].indexOf(key) < 0) {
    failures.push("stale departure: `" + pair + "` is recorded as a departure and the form no longer offers it");
  }
});

/* 3. Declared but not offered — reported, not failed. */
Object.keys(transcription).forEach(function (type) {
  var offered = pageParams[type];
  if (!offered) { return; }
  transcription[type].forEach(function (key) {
    if (offered.indexOf(key) < 0) {
      information.push("declared-not-offered: `" + type + "." + key + "`");
    }
  });
});

information.forEach(function (line) { console.log("  note  " + line); });
console.log("");
failures.forEach(function (line) { console.log("  FAIL  " + line); });
console.log("form-against-catalogue check — " + Object.keys(catalogue.byType).length +
  " catalogue entries, " + Object.keys(pageParams).length + " types offered by the form, " +
  failures.length + " failure(s), " + information.length + " note(s)");

process.exit(failures.length === 0 ? 0 : 1);
