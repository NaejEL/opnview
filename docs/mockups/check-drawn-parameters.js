/* ====================================================================
 * DRAWN-PARAMETER CHECK — run by hand, never part of `checks`.
 *
 * HOW TO RUN IT. Open `docs/mockups/canvas-mockup.html` from disk —
 * `file://` is the page's own constraint and the harness needs nothing
 * more — open the browser console, paste this whole file, and read the
 * table it prints. If the browser automation in use refuses `file://`,
 * a plain static server on loopback is the same page from the same
 * bytes; nothing here reads the origin. this file's own header
 * and the other two harnesses say the same thing in the same words.
 *
 * WHAT IT ASSERTS, AND WHY IT IS NOT check-inert-parameters.js.
 *
 * That one is a STATIC check: it asks whether the key is named anywhere
 * in the renderer's reachable source. It is a floor — a mention is the
 * cheapest evidence that is still evidence — and it cannot tell a read
 * that changes the drawing from a read that does not.
 *
 * This one is BEHAVIOURAL. For every widget the page's two documents
 * hold, for every parameter its entry offers, it sets the key to each
 * offered value AND to absent, renders through widgetContent(), and
 * compares the markup. A key whose every value produces byte-identical
 * output is inert IN FACT, whatever the source says. The two methods do
 * not produce the same number and neither is a correction of the other:
 * the static one over-counts keys that are read into a variable and
 * never used, the behavioural one over-counts keys whose card is in a
 * degraded state and drawing nothing for any value.
 *
 * ITS OUTPUT IS WHAT THE PAGE'S OWN `NOT_DRAWN` TABLE IS BUILT FROM.
 * The form marks every control this build does not draw with a chip,
 * and counts them at the foot of the Form tab; that table is this
 * harness's output, transcribed. Re-run it after any change to a
 * renderer and copy the block it prints.
 *
 * WHAT IT CANNOT ASSERT. That the change drawn is the RIGHT change.
 * Markup that differs is a control that does something; whether it does
 * the documented thing is read by eye.
 * ==================================================================== */

(function () {
  "use strict";

  /* Rendering reads the active canvas, so the probe moves the page's own
   * selection to the widget's canvas and puts it back at the end.
   * Nothing is committed: every render is of a COPY of the widget. */
  var savedCanvas = state.canvas;

  function copyOf(w) {
    return {
      id: w.id, type: w.type, title: w.title, placement: w.placement,
      parameters: JSON.parse(JSON.stringify(w.parameters))
    };
  }

  function renderOf(w) {
    var previous = AGG, out;
    AGG = aggForWidget(w);
    try { out = widgetContent(w).result.body; }
    catch (e) { out = "THREW: " + e.message; }
    finally { AGG = previous; }
    return out;
  }

  /* The values a key is tried at: every value the form offers, plus
   * absent, which is a state in its own right and the one the "not in
   * the file" entry writes. */
  function valuesFor(spec) {
    var out = [undefined];
    if (spec.kind === "enum" || spec.kind === "period") { return out.concat(spec.options); }
    if (spec.kind === "bool") { return out.concat([true, false]); }
    if (spec.kind === "int") { return out.concat([1, 2, 5, 20]); }
    if (spec.kind === "enumset") {
      out.push([]);
      spec.options.forEach(function (o) { out.push([o]); });
      out.push(spec.options.slice());
      return out;
    }
    if (spec.kind === "refs" || spec.kind === "ref") {
      var refs = [];
      if (spec.refKind === "interface") {
        refs = INTERFACES.map(function (s) {
          return { kind: "interface", by: "identifier", value: s.identifier, label: s.user_label };
        });
      } else if (spec.refKind === "owner") {
        refs = OWNERS.map(function (o) {
          return { kind: "owner", by: "display_name", value: o.display_name, label: o.display_name };
        });
      } else {
        refs = CLIENTS.map(function (d) {
          return { kind: "client", by: d.identity === "mac" ? "mac" : "hostname",
                   value: d.identity === "mac" ? d.mac : d.key, label: d.key };
        });
      }
      refs.forEach(function (r) { out.push(spec.kind === "ref" ? r : [r]); });
      if (spec.kind === "refs" && refs.length > 1) { out.push([refs[0], refs[refs.length - 1]]); }
      return out;
    }
    return out;
  }

  var drawn = [], notDrawn = [], noisy = [], types = {};

  function probe(entry, canvasIndex, w) {
    var spec2 = CATALOGUE[w.type];
    if (!spec2 || typeof RENDER[w.type] !== "function") { return; }
    state.canvas = canvasIndex;
    types[w.type] = true;

    /* Colour registries are filled on first sight, so the very first
     * render of a type can differ from the second for reasons that have
     * nothing to do with the key. Warm up, then take the baseline, then
     * confirm the baseline is stable before trusting any comparison. */
    var base = copyOf(w);
    renderOf(base);
    var baseline = renderOf(base);
    if (renderOf(base) !== baseline) {
      noisy.push({ widget: w.id, type: w.type, note: "renders differently twice running — not probed" });
      return;
    }

    (spec2.params || []).forEach(function (spec) {
      var changed = false;
      valuesFor(spec).forEach(function (value) {
        if (changed) { return; }
        var probeWidget = copyOf(w);
        if (value === undefined) { delete probeWidget.parameters[spec.key]; }
        else { probeWidget.parameters[spec.key] = value; }
        if (renderOf(probeWidget) !== baseline) { changed = true; }
      });
      (changed ? drawn : notDrawn).push({ type: w.type, widget: w.id, key: spec.key });
    });
  }

  /* CANVASES is the page's own list of canvas entries, each naming its
   * document and its index inside it, which is what state.canvas
   * selects. Walking it is what keeps the probe's idea of "the active
   * canvas" identical to the page's. */
  CANVASES.forEach(function (entry, canvasIndex) {
    var canvas = entry.doc.canvases[entry.index];
    (canvas.widgets || []).forEach(function (w) { probe(entry, canvasIndex, w); });
  });

  state.canvas = savedCanvas;

  /* The table the page's NOT_DRAWN constant is transcribed from, in the
   * shape it is written in. A key that is drawn on one card of a type
   * and not on another is DRAWN for the type: the chip says "this build
   * does not draw this control", and it must not say that of a control
   * the build does draw somewhere. */
  var drawnKeys = {};
  drawn.forEach(function (d) { drawnKeys[d.type + "." + d.key] = true; });
  var byType = {};
  notDrawn.forEach(function (d) {
    if (drawnKeys[d.type + "." + d.key]) { return; }
    if (!byType[d.type]) { byType[d.type] = {}; }
    byType[d.type][d.key] = true;
  });
  var block = Object.keys(byType).sort().map(function (t) {
    return "  " + t + ": [\"" + Object.keys(byType[t]).sort().join("\", \"") + "\"]";
  }).join(",\n");

  console.log("drawn-parameter check — " + Object.keys(types).length + " types, " +
              (drawn.length + notDrawn.length) + " widget/key pairs probed, " +
              drawn.length + " change the drawing, " + notDrawn.length + " do not" +
              (noisy.length ? ", " + noisy.length + " not probed" : ""));
  console.log("NOT_DRAWN, as the page writes it:\n{\n" + block + "\n}");
  if (notDrawn.length > 0) { console.table(notDrawn); }
  if (noisy.length > 0) { console.table(noisy); }
  return { drawn: drawn, notDrawn: notDrawn, notDrawnByType: byType };
})();
