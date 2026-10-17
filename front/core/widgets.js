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
 * reads the core performs for it, limited to the four read routes below.
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

  /* The routes a front-only widget's requests may name: reads, of these four, and nothing else. */
  var FRONT_ONLY_READS = [/^\/api\/widget\/[a-z0-9_]+(\?.*)?$/, /^\/api\/datasets(\?.*)?$/,
    /^\/api\/datasets\/[a-z0-9_.-]+\/query(\?.*)?$/, /^\/api\/live\/strip(\?.*)?$/];

  function allowedRead(path) {
    return typeof path === "string" && FRONT_ONLY_READS.some(function (pattern) { return pattern.test(path); });
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

  var widgets = {
    /* The registration call of a renderer file. */
    register: function (definition) {
      if (!definition || typeof definition.render !== "function") {
        throw new Error("widgets: a renderer registers a render function");
      }
      if (definition.type) { byType[definition.type] = definition; }
      if (definition.shape) { byShape[definition.shape] = definition; }
    },

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
