/*
 * opnview front -- the canvases: one tab per canvas of every dashboard, in dashboard order then
 * canvas order, one click each, grouped under the dashboard titles in the tab bar itself when more
 * than one dashboard exists (F7-17). The root path opens the first canvas.
 *
 * THIS IS THE SHELL'S PART OF THE CANVAS. The grid, the cards and the widgets' drawings are built on
 * this view by the canvas lot; until a dashboard exists, the view names that state.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t;

  function canvasPath(dashboard, canvas) {
    return "/canvas/" + encodeURIComponent(dashboard.id) + "/" + encodeURIComponent(canvas.id);
  }

  function load(context) {
    return context.reloadDashboards().then(function (dashboards) {
      var model = context.state;
      var wanted = context.params;
      var first = null, chosen = null;
      dashboards.forEach(function (dashboard) {
        (dashboard.canvases || []).forEach(function (canvas) {
          if (!first) { first = { dashboard: dashboard, canvas: canvas }; }
          if (String(dashboard.id) === wanted.dashboard && canvas.id === wanted.canvas) {
            chosen = { dashboard: dashboard, canvas: canvas };
          }
        });
      });
      model.chosen = chosen || (wanted.dashboard ? null : first);
      model.missing = !!wanted.dashboard && !chosen;
      model.loaded = true;
      if (!model.chosen) { return null; }
      return context.api.get("/api/dashboards/" + encodeURIComponent(model.chosen.dashboard.id)).then(function (result) {
        model.document = result.status === 200 ? result.body.document : null;
      });
    });
  }

  function tabs(context) {
    var dashboards = context.dashboards();
    var chosen = context.state.chosen;
    var grouped = dashboards.length > 1;
    return h("div", { key: "tabs", "class": "tabs", role: "tablist", "aria-label": t("canvas.tabs.label") },
      dashboards.map(function (dashboard) {
        var links = (dashboard.canvases || []).map(function (canvas) {
          var selected = !!chosen && chosen.dashboard.id === dashboard.id && chosen.canvas.id === canvas.id;
          var path = canvasPath(dashboard, canvas);
          return h("a", { key: "tab-" + dashboard.id + "-" + canvas.id, "class": "tab", role: "tab", href: path,
            "data-route": path, "aria-selected": selected ? "true" : "false" }, canvas.title || canvas.id);
        });
        if (!grouped) { return links; }
        return h("div", { key: "group-" + dashboard.id, "class": "tab-group", role: "group",
          "aria-label": dashboard.title || String(dashboard.id) }, [
          h("span", { "class": "tab-group-title" }, dashboard.title || String(dashboard.id)),
          links
        ]);
      }));
  }

  function canvasOf(model) {
    if (!model.document || !model.chosen) { return null; }
    var canvases = model.document.canvases || [];
    for (var i = 0; i < canvases.length; i++) {
      if (canvases[i].id === model.chosen.canvas.id) { return canvases[i]; }
    }
    return null;
  }

  opnview.views.register({
    id: "canvas",
    paths: ["/", "/canvas/{dashboard}/{canvas}"],
    path: "/",
    title: function () { return t("canvas.title"); },
    heading: false,
    load: load,
    render: function (context) {
      var model = context.state;
      if (!model.loaded) { return h("p", { "class": "loading", role: "status" }, t("state.loading")); }
      var bar = h("div", { key: "bar", "class": "canvas-bar" }, [tabs(context)]);
      if (model.missing) {
        return [bar, h("div", { key: "state", "class": "state state-unresolved" },
          h("span", { "class": "state-badge" }, t("canvas.state.not_found")))];
      }
      if (!model.chosen) {
        return [bar, h("div", { key: "state", "class": "state state-gap" },
          h("span", { "class": "state-badge" }, t("canvas.state.no_dashboard")))];
      }
      var canvas = canvasOf(model);
      var widgets = canvas && Array.isArray(canvas.widgets) ? canvas.widgets : [];
      if (widgets.length === 0) {
        return [bar, h("div", { key: "state", "class": "state state-gap canvas-empty" },
          h("span", { "class": "state-badge" }, t("canvas.state.empty")))];
      }
      return [bar, h("div", { key: "cards", "class": "canvas-cards" }, widgets.map(function (widget, index) {
        return h("article", { key: "widget-" + (widget.id || index), "class": "card" },
          h("div", { "class": "card-head" }, h("h2", { "class": "card-title" }, widget.title || widget.type || widget.shape)));
      }))];
    }
  });
})(this);
