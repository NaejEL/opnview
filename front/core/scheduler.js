/*
 * opnview front -- when a card asks for its data again (F7-27; decision D21).
 *
 * A WINDOWED WIDGET EVERY 60 SECONDS; A WIDGET CARRYING A LIVE FIGURE AT THE cadence_seconds ITS
 * ANSWER REPORTS (docs/widget-api.md, "Live figures"), which also keeps the live-interest lease
 * alive. Only the canvas on screen is tracked: switching canvases stops every timer of the one left.
 * While the document is hidden nothing is sent; a widget that fell due meanwhile is asked once when
 * the page is shown again.
 *
 * Every refresh goes through the reconciler: this file only says when, the canvas view fetches and
 * patches.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* How often a windowed widget is asked again, in milliseconds. */
  var WINDOWED_MS = 60000;
  /* The shortest interval ever kept, whatever an answer reports. */
  var FLOOR_MS = 2000;

  /* A scheduler. options: {fetch(id), hidden(), setTimeout, clearTimeout}. */
  function create(options) {
    var timers = {}, pending = {};
    var later = options.setTimeout || function (fn, ms) { return root.setTimeout(fn, ms); };
    var cancel = options.clearTimeout || function (id) { root.clearTimeout(id); };
    var hidden = options.hidden || function () { return false; };

    function clear(id) {
      if (timers[id] !== undefined) { cancel(timers[id]); delete timers[id]; }
    }

    var scheduler = {
      /* The interval one answer asks for: its live cadence, else the windowed one. */
      intervalFor: function (answer) {
        var cadence = opnview.canvasModel.liveCadence(answer);
        return cadence !== null ? Math.max(FLOOR_MS, cadence * 1000) : WINDOWED_MS;
      },
      /* Schedules the next request of one widget after an answer. */
      track: function (id, answer) {
        clear(id);
        delete pending[id];
        timers[id] = later(function () {
          delete timers[id];
          if (hidden()) {
            pending[id] = true;
            return;
          }
          options.fetch(id);
        }, scheduler.intervalFor(answer));
      },
      /* Stops one widget, or every widget. */
      stop: function (id) {
        if (id !== undefined) { clear(id); delete pending[id]; return; }
        Object.keys(timers).forEach(clear);
        pending = {};
      },
      /* The page is shown again: every widget that fell due while it was hidden is asked once. */
      shown: function () {
        var due = Object.keys(pending);
        pending = {};
        due.forEach(function (id) { options.fetch(id); });
      },
      tracked: function () { return Object.keys(timers).sort(); }
    };
    return scheduler;
  }

  opnview.scheduler = { create: create, WINDOWED_MS: WINDOWED_MS, FLOOR_MS: FLOOR_MS };
})(this);
