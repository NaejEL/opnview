/*
 * opnview front -- when a card asks for its data again (F7-27; decision D21 as amended).
 *
 * ON AN EVENT, AT ONCE, ONLY THE WIDGETS IT CONCERNS (docs/widget-api.md, "Events"), read from the
 * widget registry: a `collection_pass` concerns the widgets whose `providers` include its `kind`; a
 * `live_sample` those, and every widget whose last answer carries a live figure; a `plugin_pass` the
 * widgets of that `plugin`; an `aggregate_refresh` every windowed widget whose window meets one of
 * its `ranges`; a `hello` after a reconnection and a `resync` every widget of the canvas shown.
 *
 * AT MOST ONE REQUEST PER WIDGET EVERY 2 SECONDS. An event arriving while a widget's last request is
 * younger than that is not lost: the widget is asked once more when the 2 s have passed.
 *
 * POLLING IS THE FALLBACK ONLY. While the stream is disconnected, every windowed widget is asked
 * again every 15 s. While the document is hidden nothing is sent; when it is shown again, every
 * widget is asked once.
 *
 * Only the canvas on screen is tracked: switching canvases stops every timer of the one left. Every
 * refresh goes through the reconciler: this file only says when, the canvas view fetches and
 * patches.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The shortest interval between two requests of one widget, in milliseconds. */
  var DEBOUNCE_MS = 2000;
  /* How often a windowed widget is asked again while the stream is disconnected. */
  var FALLBACK_MS = 15000;
  /* The span of each canvas period, in seconds, to tell whether a refreshed range meets a window. */
  var PERIOD_SECONDS = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };

  /* Whether one refreshed range of an aggregate_refresh meets the window a widget asked for. An
   * event naming no range, or a period this table does not know, meets every window. */
  function meets(ranges, period, at) {
    if (!Array.isArray(ranges) || ranges.length === 0) { return true; }
    var span = PERIOD_SECONDS[period];
    if (span === undefined || typeof at !== "number") { return true; }
    return ranges.some(function (range) {
      return !range || typeof range.to !== "number" || range.to >= at - span;
    });
  }

  function includes(list, value) {
    return Array.isArray(list) && value !== undefined && list.indexOf(value) >= 0;
  }

  /* Whether an event concerns one widget: entry is {id, row, parameters, answer}. */
  function concerns(event, entry) {
    var data = event.data || {};
    var row = entry.row || {};
    switch (event.type) {
      case "collection_pass":
        return includes(row.providers, data.kind);
      case "live_sample":
        return includes(row.providers, data.kind) || (!!entry.answer && opnview.canvasModel.liveCadence(entry.answer) !== null);
      case "plugin_pass":
        return !!row.plugin && row.plugin === data.plugin;
      case "aggregate_refresh":
        return !!row.windowed && meets(data.ranges, entry.parameters && entry.parameters.period, data.at);
      case "hello":
        return !!event.reconnected;
      case "resync":
        return true;
      default:
        return false;
    }
  }

  /* A scheduler. options: {fetch(id), entries() -> [{id, row, parameters, answer}], hidden(),
   * connection() -> "live" | "disconnected", setTimeout, clearTimeout}. */
  function create(options) {
    var later = options.setTimeout || function (fn, ms) { return root.setTimeout(fn, ms); };
    var cancel = options.clearTimeout || function (id) { root.clearTimeout(id); };
    var hidden = options.hidden || function () { return false; };
    var cooling = {}, again = {}, fallback = null, stopped = false;

    function entries() { return options.entries ? options.entries() : []; }

    /* Asks one widget now, or once its last request is 2 s old. Nothing while hidden. */
    function ask(id) {
      if (stopped || hidden()) { return; }
      if (cooling[id] !== undefined) { again[id] = true; return; }
      cool(id);
      options.fetch(id);
    }

    /* The 2 s after a request: an ask meanwhile is kept, and sent when they have passed. */
    function cool(id) {
      cooling[id] = later(function () {
        delete cooling[id];
        if (again[id]) {
          delete again[id];
          ask(id);
        }
      }, DEBOUNCE_MS);
    }

    function askAll(filter) {
      entries().forEach(function (entry) { if (!filter || filter(entry)) { ask(entry.id); } });
    }

    function stopFallback() {
      if (fallback !== null) { cancel(fallback); fallback = null; }
    }

    function startFallback() {
      if (fallback !== null || stopped) { return; }
      fallback = later(function tick() {
        fallback = later(tick, FALLBACK_MS);
        askAll(function (entry) { return !!(entry.row && entry.row.windowed); });
      }, FALLBACK_MS);
    }

    var scheduler = {
      /* An event of the stream: every widget it concerns is asked again. */
      event: function (event) {
        if (!event) { return; }
        askAll(function (entry) { return concerns(event, entry); });
      },
      /* The stream's state changed: polling stands in while it is disconnected. */
      connection: function (connection) {
        if (connection === "live") { stopFallback(); } else { startFallback(); }
      },
      /* A widget was asked outside this scheduler -- the canvas started, a form previews: its next
       * event-driven request waits the same 2 s. */
      asked: function (id) {
        if (stopped || cooling[id] !== undefined) { return; }
        cool(id);
      },
      /* Stops every timer of the canvas left. */
      stop: function () {
        stopped = true;
        stopFallback();
        Object.keys(cooling).forEach(function (id) { cancel(cooling[id]); });
        cooling = {};
        again = {};
      },
      /* The page is shown again: every widget is asked once. */
      shown: function () { askAll(); },
      /* Whether the fallback polling runs. */
      polling: function () { return fallback !== null; }
    };
    if ((options.connection ? options.connection() : "disconnected") !== "live") { startFallback(); }
    return scheduler;
  }

  opnview.scheduler = { create: create, concerns: concerns, DEBOUNCE_MS: DEBOUNCE_MS, FALLBACK_MS: FALLBACK_MS };
})(this);
