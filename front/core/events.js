/*
 * opnview front -- the event stream: what the server stored, as it is committed (decision D21 as
 * amended; docs/widget-api.md, "Events").
 *
 * ONE STREAM PER PAGE, OPEN WHILE SIGNED IN. The shell opens it once a session is held and closes it
 * on sign-out or a 401; a view listens to it and decides which of its widgets an event concerns. The
 * stream carries no figure: an event only says that something was stored, and the widget is asked
 * again through the widget API.
 *
 * THE STATE IS live OR disconnected. The page is live from the stream's `hello` until it fails. An
 * `end` is the stream's own lifetime elapsing: it is opened again after the retry the stream states,
 * and stays live meanwhile -- the `hello` that follows a reopening asks every widget once, so what
 * was stored in between is not lost. An error closes the stream (the browser's own reconnection is
 * not relied on) and opens it again after a back-off that doubles from 2 s up to 30 s; the page is
 * disconnected until a `hello` is heard again.
 *
 * Every timer goes through root.setTimeout, so a test drives it with a fake clock.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  /* The retry the stream states (docs/widget-api.md, "Events", Bounds), in milliseconds. */
  var RETRY_MS = 2000;
  /* The longest wait between two attempts after errors. */
  var BACKOFF_MAX_MS = 30000;
  /* Every event the stream names. */
  var TYPES = ["hello", "collection_pass", "plugin_pass", "aggregate_refresh", "live_sample", "resync", "end"];

  var state = {
    source: null,
    wanted: false,
    connection: "disconnected",
    failures: 0,
    heard: 0,
    timer: null,
    listeners: [],
    watchers: []
  };

  function setConnection(connection) {
    if (state.connection === connection) { return; }
    state.connection = connection;
    state.watchers.slice().forEach(function (watcher) { watcher(connection); });
  }

  function deliver(event) {
    state.listeners.slice().forEach(function (listener) { listener(event); });
  }

  function cancelTimer() {
    if (state.timer !== null) { root.clearTimeout(state.timer); state.timer = null; }
  }

  function closeSource() {
    if (state.source) {
      var source = state.source;
      state.source = null;
      try { source.close(); } catch (e) { /* a source already closed */ }
    }
  }

  function later(ms) {
    cancelTimer();
    state.timer = root.setTimeout(function () {
      state.timer = null;
      if (state.wanted) { connect(); }
    }, ms);
  }

  /* An error, or a stream that could not be opened: wait longer each time, disconnected. */
  function failed() {
    closeSource();
    setConnection("disconnected");
    if (!state.wanted) { return; }
    var delay = Math.min(BACKOFF_MAX_MS, RETRY_MS * Math.pow(2, state.failures));
    state.failures += 1;
    later(delay);
  }

  function received(type, message, source) {
    if (source !== state.source) { return; }
    var data = {};
    if (message && typeof message.data === "string" && message.data !== "") {
      try { data = JSON.parse(message.data) || {}; } catch (e) { data = {}; }
    }
    var event = { type: type, data: data };
    if (type === "hello") {
      event.reconnected = state.heard > 0;
      state.heard += 1;
      state.failures = 0;
      setConnection("live");
    } else if (type === "end") {
      // The stream's lifetime elapsed: opened again after the retry it states, still live.
      closeSource();
      later(RETRY_MS);
    }
    deliver(event);
  }

  function connect() {
    closeSource();
    var source = opnview.api.events();
    if (!source) { failed(); return; }
    state.source = source;
    TYPES.forEach(function (type) {
      source.addEventListener(type, function (message) { received(type, message, source); });
    });
    source.onerror = function () {
      if (source === state.source) { failed(); }
    };
  }

  opnview.events = {
    RETRY_MS: RETRY_MS,
    BACKOFF_MAX_MS: BACKOFF_MAX_MS,
    /* Opens the stream, once: a second call while it is wanted changes nothing. */
    open: function () {
      if (state.wanted) { return; }
      state.wanted = true;
      state.failures = 0;
      state.heard = 0;
      connect();
    },
    /* Closes the stream and stops every attempt to open it again. */
    close: function () {
      state.wanted = false;
      cancelTimer();
      closeSource();
      setConnection("disconnected");
    },
    /* "live" or "disconnected". */
    connection: function () { return state.connection; },
    /* listener({type, data, reconnected}) is called on every event; answers its removal. */
    listen: function (listener) {
      state.listeners.push(listener);
      return function () { state.listeners = state.listeners.filter(function (one) { return one !== listener; }); };
    },
    /* watcher(connection) is called when the state changes; answers its removal. */
    watch: function (watcher) {
      state.watchers.push(watcher);
      return function () { state.watchers = state.watchers.filter(function (one) { return one !== watcher; }); };
    }
  };
})(this);
