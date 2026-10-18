/*
 * opnview front -- the widget registry: what draws each widget, and the call a renderer registers
 * itself with (specs/SPEC-step-7-frontend.md, A2 and A8; docs/front-plugins.md).
 *
 * NOTHING HERE NAMES A WIDGET. The loaded widgets come from GET /api/widgets: each row its type, its
 * shape, its manifest's parameters and, when it has one, the URL of its own renderer. The shapes'
 * renderers come from the same answer. Each is a classic script this file loads by URL -- from this
 * server only -- and each registers itself:
 *
 *   opnview.widgets.register({ type: "<type>", render: function (input) { return <view tree>; } });
 *   opnview.widgets.register({ shape: "<shape>", render: function (input) { ... } });
 *
 * A front-only widget registers `requests` beside `render`: a function of {parameters} answering the
 * reads the core performs for it, limited to the three read routes below.
 *
 * A RENDERER IS GIVEN (draw below): the widget's answer, its effective parameters, its registry row,
 * the card's size in CSS pixels, the string lookup (t, tp, tk, td), the formatters and a kit made for
 * this one drawing (front/core/kit.js). It answers a view tree. A widget with no renderer of its own
 * is drawn by its shape's.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var byType = {}, byShape = {};
  var state = { rows: [], shapes: [], refused: [], loaded: false, loading: null, scripts: {} };

  /* The routes a front-only widget's requests may name: reads, of these three, and nothing else. The
   * rolling strip is a widget, live_strip, read through the first. */
  var FRONT_ONLY_READS = [/^\/api\/widget\/[a-z0-9_]+(\?.*)?$/, /^\/api\/datasets(\?.*)?$/,
    /^\/api\/datasets\/[a-z0-9_]+(\.[a-z0-9_]+)*\/query(\?.*)?$/];

  /* A path is matched as written, so a dot component or an escape that a browser would resolve to
   * another route is refused before any pattern is tried: a dataset's name is dot-separated words,
   * never "." or "..", and nothing in the three routes is escaped. */
  function allowedRead(path) {
    if (typeof path !== "string") { return false; }
    var route = path.split("?")[0];
    if (route.indexOf("%") >= 0 || route.indexOf("\\") >= 0 || /(^|\/)\.\.?(\/|$)/.test(route)) { return false; }
    return FRONT_ONLY_READS.some(function (pattern) { return pattern.test(path); });
  }

  /* Loads one script of this server by URL, once. In a page without a document that can load one --
   * the interpreter of the tests, which runs every renderer itself -- it resolves at once. */
  function loadScript(url) {
    if (state.scripts[url]) { return state.scripts[url]; }
    var document = root.document;
    var promise = new Promise(function (resolve) {
      if (!document || !document.head || typeof document.createElement !== "function" || url.indexOf("/front/") !== 0) {
        resolve(false);
        return;
      }
      var script = document.createElement("script");
      script.src = url;
      script.onload = function () { resolve(true); };
      script.onerror = function () { resolve(false); };
      document.head.appendChild(script);
    });
    state.scripts[url] = promise;
    return promise;
  }

  /* WHAT A SCRIPT MAY REGISTER IS TAKEN FROM THE URL IT WAS LOADED FROM, never from what it says it
   * is: the index publishes a shape's renderer at /front/shapes/<shape>.js and a widget's at
   * /front/widgets/<type>/<entry>, so a widget's renderer can register its own type and nothing
   * else, a shape's renderer its own shape and nothing else, and a script loaded from anywhere
   * else -- or registering after it ran, when no script is current -- registers nothing. A type or
   * a shape already registered stays as it is: the first registration wins, as the built-in wins
   * every collision on the server. */
  var SHAPE_URL = /^\/front\/shapes\/([a-z0-9_]+)\.js$/;
  var WIDGET_URL = /^\/front\/widgets\/([a-z0-9_]+)\/[^\/]+$/;

  function ownerOf(script) {
    var src = script && (script.getAttribute ? script.getAttribute("src") : script.src);
    if (typeof src !== "string") { return null; }
    var path = src.replace(/^[a-z]+:\/\/[^\/]*/i, "").replace(/[?#].*$/, "");
    var found = SHAPE_URL.exec(path);
    if (found) { return { shape: found[1] }; }
    found = WIDGET_URL.exec(path);
    return found ? { type: found[1] } : null;
  }

  function refuse(reason, definition) {
    state.refusedRegistrations.push({ reason: reason, type: definition.type || null, shape: definition.shape || null });
    if (root.console && root.console.warn) {
      root.console.warn("widgets: a registration was refused (" + reason + ")");
    }
  }

  state.refusedRegistrations = [];

  var widgets = {
    /* The registration call of a renderer file. */
    register: function (definition) {
      if (!definition || typeof definition.render !== "function") {
        throw new Error("widgets: a renderer registers a render function");
      }
      var owner = ownerOf(root.document && root.document.currentScript);
      if (!owner) { refuse("not_loaded_by_the_core", definition); return false; }
      if (owner.shape) {
        if (definition.type || definition.shape !== owner.shape) { refuse("not_its_own_shape", definition); return false; }
        if (byShape[owner.shape]) { refuse("already_registered", definition); return false; }
        byShape[owner.shape] = definition;
        return true;
      }
      if (definition.shape) { refuse("not_its_own_shape", definition); return false; }
      if (definition.type !== owner.type) { refuse("not_its_own_type", definition); return false; }
      if (byType[owner.type]) { refuse("already_registered", definition); return false; }
      byType[owner.type] = definition;
      return true;
    },

    /* The registrations refused so far, with their reason: for the settings view and the tests. */
    refusedRegistrations: function () { return state.refusedRegistrations.slice(); },

    /* The renderer that draws a type: its own, else its shape's; null when neither is loaded. */
    renderer: function (type, shape) {
      return (type && byType[type]) || (shape && byShape[shape]) || null;
    },
    hasOwn: function (type) { return !!byType[type]; },
    shapeRenderer: function (shape) { return byShape[shape] || null; },

    rows: function () { return state.rows; },
    row: function (type) {
      for (var i = 0; i < state.rows.length; i++) {
        if (state.rows[i].type === type) { return state.rows[i]; }
      }
      return null;
    },
    loaded: function () { return state.loaded; },

    /* Reads the registry once, and loads every shape renderer and every widget's own renderer. */
    load: function (api) {
      if (state.loading) { return state.loading; }
      state.loading = api.get("/api/widgets").then(function (result) {
        if (result.status !== 200 || !result.body) {
          state.loading = null;
          return false;
        }
        state.rows = result.body.widgets || [];
        state.shapes = result.body.shapes || [];
        state.refused = result.body.refused || [];
        var urls = state.shapes.filter(function (shape) { return !byShape[shape.shape]; }).map(function (shape) { return shape.url; });
        state.rows.forEach(function (row) {
          if (row.renderer_url && !byType[row.type]) { urls.push(row.renderer_url); }
        });
        return Promise.all(urls.map(loadScript)).then(function () {
          state.loaded = true;
          return true;
        });
      }, function () {
        state.loading = null;
        return false;
      });
      return state.loading;
    },

    /* Installs a registry without a request: for a test that holds the rows already. */
    install: function (rows, shapes) {
      state.rows = rows || [];
      state.shapes = shapes || [];
      state.loaded = true;
      state.loading = Promise.resolve(true);
    },

    allowedRead: allowedRead,

    /* Draws one widget. input: {answer, parameters, row, size, widget, prefix}. Answers {tree,
     * subjects, tips, failed}: a renderer that throws, or answers what is not a tree, fails the
     * drawing, and the card shows the renderer-failure state. */
    draw: function (input) {
      var row = input.row || {};
      var renderer = widgets.renderer(row.type, row.shape || input.shape);
      if (!renderer) { return { tree: null, subjects: {}, tips: {}, failed: "renderer_missing" }; }
      var strings = opnview.strings;
      var kit = opnview.kit.create({ t: strings.t, tp: strings.tp, tk: strings.tk, td: strings.td,
        format: opnview.format, size: input.size, prefix: input.prefix });
      var tree;
      try {
        tree = renderer.render({
          answer: input.answer,
          data: input.answer ? input.answer.data : null,
          parameters: input.parameters || (input.answer && input.answer.parameters) || {},
          row: row,
          widget: input.widget || {},
          size: input.size || { width: 640, height: 320 },
          t: strings.t, tp: strings.tp, tk: strings.tk, td: strings.td,
          format: opnview.format,
          kit: kit
        });
      } catch (error) {
        if (root.console && root.console.error) { root.console.error(error); }
        return { tree: null, subjects: {}, tips: {}, failed: "renderer_failed" };
      }
      if (tree === null || tree === undefined || (typeof tree !== "object" && typeof tree !== "string")) {
        return { tree: null, subjects: {}, tips: {}, failed: "renderer_failed" };
      }
      return { tree: tree, subjects: kit.subjects(), tips: kit.tips(), failed: null };
    }
  };

  opnview.widgets = widgets;
})(this);
