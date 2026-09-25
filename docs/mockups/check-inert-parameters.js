/* ====================================================================
 * INERT-PARAMETER CHECK — run by hand, never part of `checks`.
 *
 * HOW TO RUN IT. Open `docs/mockups/canvas-mockup.html` from disk —
 * `file://` is the page's own constraint and the harness needs nothing
 * more — open the browser console, paste this whole file, and read the
 * table it prints. If the browser automation in use refuses `file://`,
 * a plain static server on loopback is the same page from the same
 * bytes; nothing here reads the origin. this file's own header
 * and the other harnesses say the same thing in the same words; the
 * note and the headers used to disagree.
 *
 * WHAT IT ASSERTS. For every widget in every canvas of every dashboard
 * document the page holds, each key present in that widget's
 * `parameters` object must be named somewhere inside the body of the
 * renderer that draws it — `RENDER[type]` — or inside a function that
 * renderer calls by name. A key the renderer never reads is a control
 * the form offers, the document records, and the canvas ignores: the
 * reader changes it, presses Apply, sees the document change and the
 * drawing stay put, and learns that the two surfaces are not the same
 * thing. That class of defect has been found three rounds running, so
 * it is checked by machine rather than by eye.
 *
 * It also checks the reverse direction on the two places the page
 * declares a key list of its own: CATALOGUE[type].params (what the
 * no-code form offers) and ADD_WIDGET_DEFAULTS (what a newly added
 * widget starts with). A default for a key the entry does not declare
 * is dead weight that will one day be offered.
 *
 * WHAT "DECLARED" IS KEYED TO, AND WHY IT MOVED.
 *
 * This test used to ask `CATALOGUE[type].params` — THE PAGE'S OWN FORM
 * LIST — whether a key was declared, and exempt everything else as a
 * kept unknown. So when the page's list was wrong the harness inherited
 * the error and exempted the evidence: `custom_chart` declares `axes`
 * in docs/widget-catalogue.md, the page's list omitted it, and the one
 * genuine inert parameter the check existed to find was filed as a
 * kept unknown and never counted. A measuring instrument keyed to the
 * thing it is measuring reports zero honestly and means nothing.
 *
 * It is keyed to CATALOGUE_DOC below, which is the catalogue
 * document's own declaration transcribed by hand, plus the six
 * departures this harness records by name. The
 * transcription is itself checked against docs/widget-catalogue.md by
 * `check-form-covers-catalogue.js`, which is a Node script because it
 * has to read the document; run that one whenever the catalogue moves.
 *
 * WHAT IT CANNOT ASSERT. That the key is read *correctly*, or that it
 * changes anything visible. A mention inside the renderer body is the
 * cheapest evidence that is still evidence; it is a floor, not a proof.
 * The ergonomist's behavioural check — set each key to each offered
 * value and to absent, render, and compare the markup — is the other
 * method, and it does not produce the same number.
 * ==================================================================== */

(function () {
  "use strict";

  /* ------------------------------------------------------------------
   * THE CATALOGUE DOCUMENT'S OWN DECLARATION.
   *
   * One line per "### " entry of docs/widget-catalogue.md, carrying the
   * parameter keys that entry's **Parameters** paragraph declares, in
   * the order it declares them, keyed by the entry's **Type** field.
   * Transcribed from the document and from nothing else — not from the
   * page, which is the thing under test.
   * ------------------------------------------------------------------ */
  var CATALOGUE_DOC = {
    interface_traffic_matrix: ["period", "scope", "measure", "interfaces", "show_rules"],
    traffic_over_time_by_scope: ["period", "scopes", "measure", "interfaces", "stacked"],
    interface_volume_ranking: ["period", "measure", "scope", "limit", "include_unlabelled"],
    client_volume_ranking: ["period", "interfaces", "identity", "limit", "include_unstable", "measure"],
    client_traffic_detail: ["client", "period", "action", "scope", "columns", "limit"],
    traffic_composition: ["period", "group_by", "measure", "slices", "interfaces", "clients", "scope", "show_other"],
    connection_tree: ["period", "root", "depth", "interfaces", "clients", "owners", "measure", "limit_per_level", "sort", "include_blocked"],
    owner_activity: ["period", "owners", "include_unassigned", "measure", "scope", "sort", "clients_per_card", "show_timeline"],
    top_sites: ["period", "interfaces", "clients", "limit", "min_flows", "group_by_registrable_domain"],
    sites_by_client: ["period", "interfaces", "clients", "sites_per_client", "min_flows", "show_attribution_rate"],
    attribution_rate_per_client: ["period", "interfaces", "clients", "limit", "sort"],
    passed_traffic_world_map: ["period", "interfaces", "scope", "size_by", "min_bytes", "show_unplaced", "cluster"],
    blocked_traffic_world_map: ["period", "engines", "interfaces", "endpoint", "size_by", "min_count", "show_unplaced", "cluster"],
    destination_countries: ["period", "interfaces", "limit", "series", "sort"],
    destination_operators: ["period", "interfaces", "limit", "series", "group"],
    traffic_sankey: ["period", "left", "right", "scope", "interfaces", "measure", "max_nodes", "min_share", "show_blocked"],
    unified_blocked_feed: ["period", "engines", "interfaces", "clients", "limit", "group_by"],
    blocked_by_firewall_rule: ["period", "interfaces", "clients", "limit", "include_automatic", "sort"],
    blocked_dns_lookups: ["period", "purposes", "blocklists", "interfaces", "clients", "limit", "min_count"],
    security_alerts_over_time: ["period", "severities", "interfaces", "clients", "bucket", "providers"],
    alerts_by_signature: ["period", "severities", "interfaces", "clients", "limit", "providers", "sort"],
    alerts_by_client_and_interface: ["period", "severities", "interfaces", "limit", "sort", "include_unmatched"],
    public_address: ["families", "gateways", "show_history", "history_limit"],
    firewall_health_overview: ["metrics", "period", "thresholds", "layout", "show_last_sampled"],
    interface_throughput: ["interfaces", "period", "direction", "measure", "stacked", "per_interface_axis"],
    custom_chart: ["period", "series", "axes", "legend", "tooltip", "annotations"],
    source_availability: ["kinds", "show_inactive", "compact"],
    aggregate_freshness: ["periods", "show_bytes", "compact"],
    unresolved_joins: ["period", "counters", "interfaces", "show_records"]
  };

  /* The six keys this page offers that the catalogue document does not
   * declare. Each is recorded as a departure in
   * this harness, and each is listed HERE BY NAME rather
   * than read out of the page, so that a seventh cannot be created by
   * adding a line to CATALOGUE and nothing else. */
  var DECLARED_DEPARTURES = {
    "traffic_over_time_by_scope.line_interpolation": true,
    "custom_chart.line_interpolation": true,
    "traffic_composition.line_interpolation": true,
    "traffic_composition.split_by": true,
    "traffic_composition.window_seconds": true,
    "owner_activity.destinations_per_owner": true
  };

  function isDeclared(type, key) {
    if (DECLARED_DEPARTURES[type + "." + key]) { return true; }
    var list = CATALOGUE_DOC[type];
    return !!list && list.indexOf(key) >= 0;
  }

  /* ------------------------------------------------------------------
   * THE SEARCHED TEXT IS CODE, NOT PROSE.
   *
   * The mention test is a word-bounded search over the renderer's
   * source, and the source of this page is about a third comments and
   * user-visible English. So `custom_chart.axes` counted as read
   * because `pairedAxisChart` carries the comment "Two unrelated
   * quantities on paired axes" and an `aria-label` reading "Two
   * quantities on paired axes" — three occurrences of the English word
   * "axes", not one read of the parameter. Nineteen keys were exempted
   * this way, on words like "scope", "period", "stacked" and
   * "interfaces", every one of which is ordinary English on this page.
   *
   * So the source is sanitised first: comments go entirely, and a
   * string literal keeps its text ONLY when the whole of it is an
   * identifier. That distinction is what the test needs and it is
   * exactly the right one — `refValues(w, "interfaces")` is how half
   * this page's renderers read a reference list, so `"interfaces"` must
   * survive, while "every interface this installation has" must not.
   *
   * This is a regex-free character scan rather than a parser: it knows
   * strings, template strings, both comment forms and regular-expression
   * literals, and it knows nothing else. It is a harness, and a
   * mis-scan shows up as a finding to be read rather than as a silent
   * exemption — which is the direction an instrument should fail in.
   * ------------------------------------------------------------------ */
  var REGEX_MAY_FOLLOW = "(,=:[!&|?{};+-*%~^<>";

  function sanitise(src) {
    var out = "", i = 0, n = src.length;
    function previousSignificant() {
      for (var j = out.length - 1; j >= 0; j--) {
        var c = out.charAt(j);
        if (c !== " " && c !== "\n" && c !== "\t" && c !== "\r") { return c; }
      }
      return "";
    }
    while (i < n) {
      var c = src.charAt(i), d = src.charAt(i + 1), j, end;
      if (c === "/" && d === "*") {
        end = src.indexOf("*/", i + 2);
        i = end < 0 ? n : end + 2; out += " "; continue;
      }
      if (c === "/" && d === "/") {
        end = src.indexOf("\n", i);
        i = end < 0 ? n : end; out += " "; continue;
      }
      if (c === '"' || c === "'" || c === "`") {
        var body = "";
        j = i + 1;
        while (j < n) {
          if (src.charAt(j) === "\\") { body += src.substr(j, 2); j += 2; continue; }
          if (src.charAt(j) === c) { break; }
          body += src.charAt(j); j++;
        }
        out += /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(body) ? c + body + c : c + c;
        i = j + 1; continue;
      }
      if (c === "/") {
        var prev = previousSignificant();
        if (prev === "" || REGEX_MAY_FOLLOW.indexOf(prev) >= 0) {
          var inClass = false;
          j = i + 1;
          while (j < n) {
            if (src.charAt(j) === "\\") { j += 2; continue; }
            if (src.charAt(j) === "[") { inClass = true; }
            else if (src.charAt(j) === "]") { inClass = false; }
            else if (src.charAt(j) === "/" && !inClass) { break; }
            else if (src.charAt(j) === "\n") { break; }
            j++;
          }
          out += " "; i = j + 1; continue;
        }
      }
      out += c; i++;
    }
    return out;
  }

  /* KEYS THE FRAME READS, NOT THE RENDERER.
   *
   * `period` is honoured for every widget and by none of them: the card
   * renderer is called with AGG already pointed at the widget's own
   * period — see renderWidget() and widgetPeriod() — so the key is read
   * exactly once, outside every RENDER function, and a search of
   * renderer bodies will never find it. It is listed here by name, with
   * where it is read, rather than passing on the strength of the
   * English word "period" appearing in a comment, which is how it used
   * to pass. Nothing else on this page is honoured this way. */
  var FRAME_READS = {
    period: "renderWidget() points AGG at widgetPeriod(w) before calling the renderer"
  };

  /* Renderers delegate. A key read inside a helper the renderer calls is
   * read by the renderer, so the search follows one level of named
   * global functions out of the renderer body — which is as far as this
   * page's renderers ever delegate. */
  function bodyOf(fn) { return typeof fn === "function" ? sanitise(Function.prototype.toString.call(fn)) : ""; }

  function reachableSource(entry) {
    var seen = {}, queue = [entry], out = [], guard = 0;
    while (queue.length > 0 && guard++ < 400) {
      var fn = queue.shift();
      var src = bodyOf(fn);
      if (src === "") { continue; }
      out.push(src);
      var names = src.match(/\b[A-Za-z_$][A-Za-z0-9_$]*\s*\(/g) || [];
      for (var i = 0; i < names.length; i++) {
        var name = names[i].replace(/\s*\($/, "");
        if (seen[name]) { continue; }
        seen[name] = true;
        var target = null;
        try { target = window[name]; } catch (e) { target = null; }
        if (typeof target === "function") { queue.push(target); }
      }
    }
    return out.join("\n");
  }

  var sourceCache = {};
  function sourceFor(type) {
    if (!Object.prototype.hasOwnProperty.call(sourceCache, type)) {
      sourceCache[type] = reachableSource(RENDER[type]);
    }
    return sourceCache[type];
  }

  function mentions(type, key) {
    if (FRAME_READS[key]) { return true; }
    var src = sourceFor(type);
    /* The key as a property name, as a bracket string, or as a
     * destructured identifier. Word-bounded so `sort` does not match
     * `sortKey` alone — `sortKey` is an assignment target, and the read
     * that fills it names the key. */
    return new RegExp("(\\.|\\[\"|\\['|\\b)" + key.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\b").test(src);
  }

  var findings = [];
  var notes = [];
  var checked = 0;

  function eachWidget(doc, docName) {
    (doc.canvases || []).forEach(function (canvas, ci) {
      (canvas.widgets || []).forEach(function (w) {
        var params = w.parameters || {};
        Object.keys(params).forEach(function (key) {
          checked++;
          if (typeof RENDER[w.type] !== "function") {
            /* A type this build does not know is not a defect: the
             * imported document deliberately carries one, and the page
             * draws the unknown-type state for it and writes the widget
             * back untouched on export. It is listed so that the count
             * is honest, and it is not counted as a failure. */
            checked--;
            notes.push({
              kind: "type not in this build", doc: docName, canvas: ci,
              widget: w.id, type: w.type, key: key
            });
            return;
          }
          /* A key the CATALOGUE DOCUMENT does not declare, and which is
           * not one of the six recorded departures, is a KEPT UNKNOWN:
           * the page states on the card that it does not know the
           * parameter and writes it back unchanged on export, which is
           * the behaviour docs/dashboard-format.md asks for. Counting it
           * as inert would punish the page for doing the documented
           * thing. */
          if (!isDeclared(w.type, key)) {
            checked--;
            notes.push({
              kind: "kept unknown", doc: docName, canvas: ci,
              widget: w.id, type: w.type, key: key
            });
            return;
          }
          if (!mentions(w.type, key)) {
            findings.push({
              kind: "inert parameter", doc: docName, canvas: ci,
              widget: w.id, type: w.type, key: key
            });
          }
        });
      });
    });
  }

  eachWidget(DOC_MAIN, "DOC_MAIN");
  eachWidget(DOC_IMPORTED, "DOC_IMPORTED");

  /* Every key the no-code form offers must also be read. A control that
   * is offered but inert is the same defect reached from the other
   * side, and it does not need a widget in a document to exist. This
   * direction IS keyed to the page's own list, and correctly so: what
   * is on trial here is the control the reader can touch. */
  Object.keys(CATALOGUE).forEach(function (type) {
    if (typeof RENDER[type] !== "function") { return; }
    (CATALOGUE[type].params || []).forEach(function (spec) {
      checked++;
      if (!mentions(type, spec.key)) {
        findings.push({ kind: "inert control", doc: "CATALOGUE", canvas: "-", widget: "-", type: type, key: spec.key });
      }
    });
  });

  /* A key the catalogue document declares that the page's form does not
   * offer at all. Not a finding — nothing inert is on screen — but it is
   * a fact twice stated the other way round, so
   * the harness prints it rather than leaving it to be remembered. */
  var notOffered = [];
  Object.keys(CATALOGUE).forEach(function (type) {
    var offered = (CATALOGUE[type].params || []).map(function (s) { return s.key; });
    (CATALOGUE_DOC[type] || []).forEach(function (key) {
      if (offered.indexOf(key) < 0) {
        notOffered.push({ kind: "declared, not offered", type: type, key: key });
      }
    });
  });

  /* A default for a key the entry does not declare. */
  if (typeof ADD_WIDGET_DEFAULTS === "object") {
    Object.keys(ADD_WIDGET_DEFAULTS).forEach(function (k) {
      if (k.indexOf(".") < 0) { return; }
      var type = k.slice(0, k.indexOf("."));
      var key = k.slice(k.indexOf(".") + 1);
      if (!CATALOGUE[type]) { return; }
      checked++;
      if (!isDeclared(type, key)) {
        findings.push({ kind: "undeclared default", doc: "ADD_WIDGET_DEFAULTS", canvas: "-", widget: "-", type: type, key: key });
      }
    });
  }

  function countOf(kind) {
    return findings.filter(function (f) { return f.kind === kind; }).length;
  }

  console.log("inert-parameter check — " + checked + " key/renderer pairs, " +
              findings.length + " finding(s) (" +
              countOf("inert control") + " inert controls, " +
              countOf("inert parameter") + " inert parameters, " +
              countOf("undeclared default") + " undeclared defaults), " +
              notes.length + " note(s), " +
              notOffered.length + " declared-but-not-offered key(s)");
  if (findings.length > 0) { console.table(findings); }
  if (notes.length > 0) { console.table(notes); }
  if (notOffered.length > 0) { console.table(notOffered); }
  return findings;
})();
