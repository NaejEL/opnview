/*
 * opnview front -- the shell: the router, the session gate and the layout (F7-7, F7-8).
 *
 * THE LAYOUT IS THE STEP-3 MOCKUP'S. A two-row header: identity and liveness on the first row --
 * the product's name, the time of the last answer from the server with its state, and the outage
 * indicator of GET /api/sources on every view -- and the controls on the second: the palette, the
 * mode, the language when two or more are installed, and signing out. A rail on the left: the
 * canvases, then the installation's own views, one click each. Below the rail's breakpoint the rail
 * becomes a sheet behind one control of the header, holding the same links. The header and the rail
 * stay on screen however tall the page is. No view is reachable only through a menu inside a menu.
 *
 * VIEWS REGISTER THEMSELVES (opnview.views.register): a view names its path, whether it is reached
 * without a session, where the rail lists it, how it loads its data and how it draws -- and, when it
 * has them, the controls it adds to the header and the panel it docks beside the main area (the
 * canvas's edit mode and its dock). The shell holds no list of them.
 *
 * THE RAIL'S DASHBOARD SECTION holds the editor -- the dock, opened beside the canvas -- and the
 * views that register under it: import and export.
 *
 * WITHOUT A SESSION ONLY THE SESSION VIEWS ARE SHOWN: setup while the installation has no account,
 * sign-in and the reset afterwards. Any other path sends the reader to the one that applies, and
 * back to the path they asked for once signed in. A 401 from any route mid-session does the same.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk, tp = opnview.strings.tp;

  /* How often the header asks for the outage indicator, in milliseconds. */
  var HEADER_POLL_MS = 60000;

  var state = {
    session: { account_exists: false, signed_in: false },
    path: "/",
    query: {},
    view: null,
    viewState: {},
    sheetOpen: false,
    sources: null,
    sourcesFailed: false,
    lastAnswerAt: null,
    dashboards: [],
    container: null,
    pollTimer: null,
    defaultOffer: null
  };

  /* ---------------------------------------------------------------------------------------------
   * Routing.
   * ------------------------------------------------------------------------------------------- */

  /* Matches a path against a view's pattern ("/canvas/{dashboard}/{canvas}"), answering its
   * parameters or null. */
  function match(pattern, path) {
    var names = [];
    var expression = new RegExp("^" + pattern.replace(/[.*+?^$()|[\]\\]/g, "\\$&").replace(/\{([a-z_]+)\}/g,
      function (_, name) { names.push(name); return "([^/]+)"; }) + "/?$");
    var found = expression.exec(path);
    if (!found) { return null; }
    var params = {};
    names.forEach(function (name, index) { params[name] = decodeURIComponent(found[index + 1]); });
    return params;
  }

  function findView(path) {
    var views = opnview.views.all();
    for (var i = 0; i < views.length; i++) {
      var patterns = views[i].paths || [views[i].path];
      for (var j = 0; j < patterns.length; j++) {
        var params = match(patterns[j], path);
        if (params) { return { view: views[i], params: params }; }
      }
    }
    return null;
  }

  function parseQuery(search) {
    var query = {};
    (search || "").replace(/^\?/, "").split("&").forEach(function (pair) {
      if (!pair) { return; }
      var cut = pair.indexOf("=");
      var name = decodeURIComponent(cut < 0 ? pair : pair.slice(0, cut));
      query[name] = cut < 0 ? "" : decodeURIComponent(pair.slice(cut + 1).replace(/\+/g, " "));
    });
    return query;
  }

  /* A path the front may send a reader back to: one of its own, never another origin. */
  function safeReturn(path) {
    return typeof path === "string" && /^\/(?!\/)/.test(path) && path.indexOf("\\") < 0 ? path : "/";
  }

  function viewFor(path) {
    var found = findView(path);
    var signedIn = state.session.signed_in;
    if (!signedIn) {
      if (found && found.view.public && found.view.available(state.session)) { return found; }
      var target = state.session.account_exists ? "/sign-in" : "/setup";
      var next = found && found.view.public ? "/" : path;
      return { redirect: target + (next && next !== "/" ? "?next=" + encodeURIComponent(next) : "") };
    }
    if (found && found.view.public) {
      return { redirect: safeReturn(state.query.next) };
    }
    if (!found) { return { view: notFoundView, params: {} }; }
    return found;
  }

  var notFoundView = {
    id: "not-found",
    title: function () { return t("view.not_found.title"); },
    render: function () {
      return h("div", { "class": "state state-gap" }, [
        h("span", { "class": "state-badge" }, t("view.not_found.state")),
        h("a", { href: "/", "class": "link-button", "data-route": "/" }, t("view.not_found.home"))
      ]);
    }
  };

  /* Moves to a path: the address bar changes, the view loads and draws. */
  function go(path, options) {
    var settings = options || {};
    var location = root.location;
    var cut = path.indexOf("?");
    var pathname = cut < 0 ? path : path.slice(0, cut);
    var search = cut < 0 ? "" : path.slice(cut);
    if (root.history && root.history.pushState) {
      if (settings.replace) { root.history.replaceState(null, "", path); } else { root.history.pushState(null, "", path); }
    } else if (location) {
      location.pathname = pathname;
    }
    return enter(pathname, search);
  }

  function enter(pathname, search) {
    state.path = pathname;
    state.query = parseQuery(search);
    state.sheetOpen = false;
    var resolved = viewFor(pathname);
    if (resolved.redirect) { return go(resolved.redirect, { replace: true }); }
    var view = resolved.view;
    if (state.view !== view || JSON.stringify(state.params) !== JSON.stringify(resolved.params)) {
      // A view left is told, so what it schedules stops with it.
      if (state.view && state.view.leave) { state.view.leave(viewContext()); }
      state.viewState = {};
    }
    state.view = view;
    state.params = resolved.params;
    render();
    var context = viewContext();
    var loading = view.load ? Promise.resolve(view.load(context)) : Promise.resolve();
    return loading.then(function () { render(); }, function () { render(); });
  }

  /* ---------------------------------------------------------------------------------------------
   * What a view is given.
   * ------------------------------------------------------------------------------------------- */

  function viewContext() {
    return {
      api: opnview.api,
      params: state.params || {},
      query: state.query,
      state: state.viewState,
      session: state.session,
      go: go,
      rerender: render,
      refresh: function () { render({ refresh: true }); },
      signedIn: signedIn,
      dashboards: function () { return state.dashboards; },
      reloadDashboards: loadDashboards
    };
  }

  /* After a sign-in or a setup: the session is held, the views open. */
  function signedIn(token) {
    opnview.api.useToken(token);
    state.session.signed_in = true;
    state.session.account_exists = true;
    return opnview.appearance.load().then(function () {
      opnview.appearance.apply();
      return opnview.strings.load(opnview.appearance.language());
    }).then(function () {
      startPolling();
      loadDashboards();
      return go(safeReturn(state.query.next), { replace: true });
    });
  }

  function signOut() {
    return opnview.api.post("/api/session/sign-out", {}).then(function () {
      return afterSignOut();
    });
  }

  function afterSignOut() {
    stopPolling();
    state.defaultOffer = null;
    // What one session edited and can undo is not the next one's.
    opnview.editing.reset();
    state.session.signed_in = false;
    state.sources = null;
    state.dashboards = [];
    return opnview.api.session().then(function (result) {
      if (result.status === 200 && result.body) { state.session = result.body; }
      return go("/sign-in", { replace: true });
    });
  }

  /* ---------------------------------------------------------------------------------------------
   * The header's indicator and the rail's canvases.
   * ------------------------------------------------------------------------------------------- */

  function loadSources() {
    return opnview.api.get("/api/sources").then(function (result) {
      if (result.status === 200 && result.body) {
        state.sources = result.body;
        state.sourcesFailed = false;
        state.lastAnswerAt = new Date();
      } else {
        state.sourcesFailed = true;
      }
      render({ refresh: true });
    }, function () {
      state.sourcesFailed = true;
      render({ refresh: true });
    });
  }

  /* The default dashboard is offered once a reader is signed in (F7-28): the server stores it on the
   * first ask ever, when no dashboard was created before, and never again. Asked once a page. */
  function offerDefault() {
    if (!state.defaultOffer) {
      state.defaultOffer = opnview.api.post("/api/dashboards/default", {}).then(function () { return null; },
        function () { return null; });
    }
    return state.defaultOffer;
  }

  function loadDashboards() {
    return offerDefault().then(function () { return opnview.api.get("/api/dashboards"); }).then(function (result) {
      state.dashboards = result.status === 200 && result.body && result.body.dashboards ? result.body.dashboards : [];
      render({ refresh: true });
      return state.dashboards;
    }, function () { return state.dashboards; });
  }

  function hidden() {
    return root.document && root.document.visibilityState === "hidden";
  }

  function startPolling() {
    stopPolling();
    var tick = function () {
      state.pollTimer = root.setTimeout(tick, HEADER_POLL_MS);
      if (!hidden() && state.session.signed_in) { loadSources(); }
    };
    loadSources();
    state.pollTimer = root.setTimeout(tick, HEADER_POLL_MS);
  }

  function stopPolling() {
    if (state.pollTimer !== null) { root.clearTimeout(state.pollTimer); }
    state.pollTimer = null;
  }

  /* ---------------------------------------------------------------------------------------------
   * The layout.
   * ------------------------------------------------------------------------------------------- */

  /* A link to a view: an anchor the shell follows itself, so the page is not reloaded. */
  function routeLink(key, path, label, attributes) {
    var options = { key: key, href: path, "data-route": path };
    Object.keys(attributes || {}).forEach(function (name) { options[name] = attributes[name]; });
    return h("a", options, label);
  }

  function outageIndicator() {
    var sources = state.sources;
    if (!sources) {
      return routeLink("outages", "/sources", t("indicator.unknown"), { "class": "outages outages-unknown" });
    }
    if (sources.outage_count > 0) {
      return routeLink("outages", "/sources", [
        h("span", { "class": "outages-label" }, t("indicator.outages")),
        h("span", { "class": "figure" }, opnview.format.number(sources.outage_count))
      ], { "class": "outages has-outages" });
    }
    return routeLink("outages", "/sources", t("indicator.no_outage"), { "class": "outages" });
  }

  function livePill() {
    var at = state.lastAnswerAt;
    return h("span", { key: "live", "class": "live-pill" + (state.sourcesFailed ? " is-stale" : ""),
      title: state.sourcesFailed ? t("header.live.failed") : t("header.live.answered") }, [
      h("span", { "class": "live-dot", "aria-hidden": "true" }),
      state.sourcesFailed ? h("span", { "class": "live-label" }, t("header.live.failed_short")) : null,
      at ? h("span", { "class": "live-clock" }, opnview.format.clock(at)) : null
    ]);
  }

  function appearanceControls() {
    var appearance = opnview.appearance;
    var model = appearance.state();
    var controls = [];
    controls.push(h("div", { key: "palette", "class": "control-group" }, [
      h("label", { "for": "palette-select" }, t("appearance.palette")),
      h("select", {
        id: "palette-select", "class": "btn", value: appearance.palette(),
        on: { change: function (event) { appearance.save({ palette: event.target.value }).then(function () { render(); }); } }
      }, model.palettes.map(function (entry) {
        return h("option", { key: entry.id, value: entry.id, selected: entry.id === appearance.palette() },
          appearance.paletteName(entry));
      }))
    ]));
    controls.push(h("div", { key: "mode", "class": "control-group" }, [
      h("label", { "for": "mode-select" }, t("appearance.mode")),
      h("select", {
        id: "mode-select", "class": "btn", value: appearance.mode(),
        on: { change: function (event) { appearance.save({ mode: event.target.value }).then(function () { render(); }); } }
      }, model.modes.map(function (mode) {
        return h("option", { key: mode, value: mode, selected: mode === appearance.mode() }, tk("appearance.mode.", mode));
      }))
    ]));
    // A control offered must change what is drawn: one language installed offers no choice.
    if (model.languages.length >= 2) {
      controls.push(h("div", { key: "language", "class": "control-group" }, [
        h("label", { "for": "language-select" }, t("appearance.language")),
        h("select", {
          id: "language-select", "class": "btn", value: appearance.language(),
          on: { change: function (event) {
            appearance.save({ language: event.target.value }).then(function () {
              return opnview.strings.load(appearance.language());
            }).then(function () { render(); });
          } }
        }, model.languages.map(function (entry) {
          return h("option", { key: entry.code, value: entry.code, selected: entry.code === appearance.language(),
            lang: entry.code }, entry.name);
        }))
      ]));
    }
    return controls;
  }

  function header() {
    var signed = state.session.signed_in;
    return h("header", { key: "header", "class": "app-header" }, [
      h("div", { "class": "header-row header-row-identity" }, [
        h("div", { "class": "brand" }, [
          routeLink("brand", "/", t("app.name"), { "class": "brand-name" })
        ]),
        signed ? livePill() : null,
        signed ? outageIndicator() : null
      ]),
      signed ? h("div", { "class": "header-row header-controls" }, [
        h("button", {
          key: "rail-toggle", id: "rail-toggle", type: "button", "class": "btn", "aria-haspopup": "dialog",
          "aria-expanded": state.sheetOpen ? "true" : "false",
          on: { click: function () { state.sheetOpen = true; render(); } }
        }, t("rail.sheet.open")),
        appearanceControls(),
        state.view && state.view.headerControls ? state.view.headerControls(viewContext()) : null,
        h("button", { key: "sign-out", type: "button", "class": "btn",
          on: { click: function () { signOut(); } } }, t("action.sign_out"))
      ]) : null
    ]);
  }

  function railCanvases(prefix) {
    var links = [];
    /* "/" shows the first canvas, so the first canvas is the current one there. */
    var first = true;
    state.dashboards.forEach(function (dashboard) {
      (dashboard.canvases || []).forEach(function (canvas) {
        var path = "/canvas/" + encodeURIComponent(dashboard.id) + "/" + encodeURIComponent(canvas.id);
        var current = state.path === path || (first && state.path === "/");
        first = false;
        links.push(routeLink(prefix + "-canvas-" + dashboard.id + "-" + canvas.id, path, canvas.title || canvas.id,
          { "class": "rail-link", "aria-current": current ? "true" : "false" }));
      });
    });
    if (links.length === 0) {
      links.push(h("p", { key: prefix + "-no-canvas", "class": "rail-empty" }, t("rail.canvases.none")));
    }
    return links;
  }

  /* The dashboard section: the editor, which opens the dock beside the canvas, then the views
   * registered under it. */
  function railDashboard(prefix) {
    var dock = opnview.editing.dock();
    var onCanvas = !!state.view && state.view.id === "canvas";
    var editor = h("button", { key: prefix + "-editor", type: "button", "class": "rail-link", id: prefix + "-editor",
      "aria-pressed": onCanvas && dock.open ? "true" : "false", on: { click: function () {
        state.sheetOpen = false;
        if (onCanvas && dock.open) { opnview.editing.closeDock(); render(); return; }
        opnview.editing.openDock(dock.tab || "document");
        if (onCanvas) { render(); } else { go("/"); }
      } } }, t("rail.editor"));
    return [editor].concat(railViews(prefix, "dashboard"));
  }

  function railViews(prefix, section) {
    return opnview.views.all().filter(function (view) { return view.rail === section; })
      .sort(function (a, b) { return a.order - b.order; })
      .map(function (view) {
        return routeLink(prefix + "-" + view.id, view.path, view.railTitle ? view.railTitle() : view.title(),
          { "class": "rail-link", "aria-current": state.view === view ? "true" : "false" });
      });
  }

  function railInstallation(prefix) {
    return opnview.views.all().filter(function (view) { return view.rail === "installation"; })
      .sort(function (a, b) { return a.order - b.order; })
      .map(function (view) {
        return routeLink(prefix + "-" + view.id, view.path, view.title(),
          { "class": "rail-link", "aria-current": state.view === view ? "true" : "false" });
      });
  }

  function railSections(prefix) {
    return [
      h("div", { key: prefix + "-canvases", "class": "rail-section" }, [
        h("div", { "class": "rail-section-title" }, t("rail.section.canvases")),
        railCanvases(prefix)
      ]),
      h("div", { key: prefix + "-dashboard", "class": "rail-section" }, [
        h("div", { "class": "rail-section-title" }, t("rail.section.dashboard")),
        railDashboard(prefix)
      ]),
      h("div", { key: prefix + "-installation", "class": "rail-section" }, [
        h("div", { "class": "rail-section-title" }, t("rail.section.installation")),
        railInstallation(prefix)
      ])
    ];
  }

  function rail() {
    return h("nav", { key: "rail", "class": "rail", "aria-label": t("rail.label") }, railSections("rail"));
  }

  function sheet() {
    if (!state.sheetOpen) { return null; }
    var close = function () { state.sheetOpen = false; render(); };
    return h("div", { key: "sheet", "class": "picker-backdrop",
      on: { click: function (event) { if (event.target === event.currentTarget) { close(); } } } }, [
      h("div", { "class": "picker", role: "dialog", "aria-modal": "true", "aria-label": t("rail.label") }, [
        h("div", { "class": "panel-head" }, [
          h("h2", { "class": "panel-title" }, t("rail.label")),
          h("button", { type: "button", "class": "btn btn-small", on: { click: close } }, t("action.close"))
        ]),
        h("div", { "class": "picker-list" }, railSections("sheet"))
      ])
    ]);
  }

  function main() {
    var view = state.view;
    var context = viewContext();
    var heading = view && view.heading !== false && view.title ? h("h1", { "class": "view-title" }, view.title()) : null;
    return h("main", { key: "main", "class": "main view-" + (view ? view.id : "none"), id: "main" }, [
      heading,
      view ? h("div", { key: "view-" + view.id, "class": "view-body" }, view.render(context)) : null
    ]);
  }

  function layout() {
    if (!state.session.signed_in) {
      return [header(), h("div", { key: "body", "class": "app-body app-body-session" }, [main()])];
    }
    var aside = state.view && state.view.aside ? state.view.aside(viewContext()) : null;
    return [header(), h("div", { key: "body", "class": "app-body" }, [rail(), main(), aside]), sheet()];
  }

  function render(options) {
    if (!state.container) { return; }
    if (state.view && state.view.title && root.document) {
      root.document.title = t("app.document_title", { view: state.view.title() });
    }
    opnview.view.render(state.container, layout(), options);
    syncHeaderHeight();
    // A view that lays itself out once drawn -- the canvas's grid -- does it now.
    if (state.view && state.view.rendered) { state.view.rendered(viewContext()); }
  }

  /* The header wraps onto more rows on a narrow window, and what stays under it -- the rail, the
   * canvas bar -- is placed below its measured height. The one property written is a length. */
  function syncHeaderHeight() {
    var header = state.container.firstChild;
    var element = root.document && root.document.documentElement;
    if (!header || !header.offsetHeight || !element || !element.style || !element.style.setProperty) { return; }
    var height = header.offsetHeight + "px";
    if (state.headerHeight !== height) {
      state.headerHeight = height;
      element.style.setProperty("--header-height", height);
    }
  }

  /* ---------------------------------------------------------------------------------------------
   * Starting.
   * ------------------------------------------------------------------------------------------- */

  /* Follows a click on a route link without reloading the page. */
  function followLinks(container) {
    container.addEventListener("click", function (event) {
      if (event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey || event.shiftKey ||
          event.altKey) {
        return;
      }
      var target = event.target;
      while (target && target !== container && !(target.getAttribute && target.getAttribute("data-route"))) {
        target = target.parentNode;
      }
      if (!target || target === container) { return; }
      event.preventDefault();
      go(target.getAttribute("data-route"));
    });
  }

  var shell = {
    /* Starts the front in a container: reads the session, the appearance and the strings, then
     * enters the address bar's path. */
    start: function (container) {
      state.container = container;
      followLinks(container);
      opnview.api.onUnauthorized(function () {
        if (!state.session.signed_in) { return; }
        var from = state.path;
        stopPolling();
        state.session.signed_in = false;
        opnview.api.session().then(function (result) {
          if (result.status === 200 && result.body) { state.session = result.body; }
          go("/sign-in" + (from && from !== "/" ? "?next=" + encodeURIComponent(from) : ""), { replace: true });
        });
      });
      if (root.addEventListener) {
        root.addEventListener("popstate", function () {
          var location = root.location;
          enter(location.pathname, location.search);
        });
        root.addEventListener("resize", function () {
          syncHeaderHeight();
          if (state.view && state.view.resized) { state.view.resized(viewContext()); }
        });
      }
      if (root.document && root.document.addEventListener) {
        var settle = function () { if (opnview.view.takeDeferred()) { render({ refresh: true }); } };
        root.document.addEventListener("focusout", function () { root.setTimeout(settle, 0); });
        root.document.addEventListener("selectionchange", settle);
        root.document.addEventListener("visibilitychange", function () {
          if (!hidden() && state.view && state.view.visible) { state.view.visible(viewContext()); }
        });
        root.document.addEventListener("keydown", function (event) {
          if (state.view && state.view.keydown && state.view.keydown(viewContext(), event)) { return; }
          if (event.key === "Escape" && state.sheetOpen) { state.sheetOpen = false; render(); }
        });
      }
      return opnview.api.session().then(function (result) {
        if (result.status === 200 && result.body) { state.session = result.body; }
        return opnview.appearance.load();
      }).then(function () {
        opnview.appearance.apply();
        return opnview.strings.load(opnview.appearance.language());
      }).then(function () {
        if (state.session.signed_in) {
          startPolling();
          loadDashboards();
        }
        var location = root.location;
        return enter(location.pathname, location.search);
      });
    },
    go: go,
    signedIn: signedIn,
    afterSignOut: afterSignOut,
    match: match,
    state: function () { return state; },
    /* The plural helper, re-exported for a view that counts. */
    tp: tp
  };

  opnview.shell = shell;
})(this);
