/*
 * opnview front -- the Blocklists view (F7-14, decision D17): every list the resolver refused a lookup
 * against, and the purpose the reader gives each, on /api/blocklists.
 *
 * A PURPOSE IS NEVER DERIVED FROM A NAME. A list with no purpose shows none, whatever it is called;
 * the purposes offered are the server's vocabulary, and assigning or clearing one is the reader's
 * statement, dated when it is made.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms, format = opnview.format;
  var t = opnview.strings.t, tk = opnview.strings.tk;

  function load(context) {
    return context.api.get("/api/blocklists").then(function (result) {
      context.state.failed = result.status !== 200;
      if (result.status === 200) { context.state.answer = result.body; }
    });
  }

  function assign(context, list, purpose) {
    var model = context.state;
    model.refusal = null;
    return context.api.post("/api/blocklists/" + encodeURIComponent(list.id) + "/purpose",
      { purpose: purpose === "" ? null : purpose }).then(function (result) {
      if (result.status === 200) { return load(context); }
      model.refusal = result.body && result.body.condition ? result.body : { condition: "internal_error" };
      return null;
    }).then(function () { context.rerender(); });
  }

  opnview.views.register({
    id: "blocklists",
    path: "/blocklists",
    rail: "installation",
    order: 60,
    title: function () { return t("blocklists.title"); },
    load: load,
    render: function (context) {
      var model = context.state, answer = model.answer;
      if (!answer) {
        return model.failed ? h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" },
          t("state.read_failed"))) : h("p", { "class": "loading", role: "status" }, t("state.loading"));
      }
      if (answer.blocklists.length === 0) {
        return h("div", { "class": "state state-gap" }, h("span", { "class": "state-badge" }, t("blocklists.none")));
      }
      var rows = answer.blocklists.map(function (list) {
        var current = list.purpose || "";
        var id = "purpose-" + list.id;
        return h("tr", { key: "list-" + list.id }, [
          h("td", { "class": "mono" }, list.name),
          h("td", {}, h("select", {
            id: id, "aria-label": t("blocklists.purpose_of", { name: list.name }), value: current,
            on: { change: function (event) { assign(context, list, event.target.value); } }
          }, [h("option", { key: "none", value: "", selected: current === "" }, t("blocklists.no_purpose"))]
            .concat(answer.purposes.map(function (purpose) {
              return h("option", { key: purpose, value: purpose, selected: purpose === current },
                tk("blocklists.purpose.", purpose));
            })))),
          h("td", {}, list.purpose_assigned_at === null ? h("span", { "class": "muted" }, t("blocklists.never_assigned"))
            : format.instant(list.purpose_assigned_at)),
          h("td", {}, format.instant(list.first_seen_at)),
          h("td", {}, format.instant(list.last_seen_at))
        ]);
      });
      return h("div", { "class": "cards" }, forms.card("blocklists", t("blocklists.heading"), [
        model.refusal ? h("p", { key: "refusal", "class": "field-error", role: "alert" }, forms.refusal(model.refusal.condition))
          : null,
        h("div", { key: "table", "class": "table-scroll" }, h("table", { "class": "data" }, [
          h("thead", {}, h("tr", {}, [h("th", {}, t("blocklists.column.list")), h("th", {}, t("blocklists.column.purpose")),
            h("th", {}, t("blocklists.column.assigned")), h("th", {}, t("blocklists.column.first_seen")),
            h("th", {}, t("blocklists.column.last_seen"))])),
          h("tbody", {}, rows)
        ]))
      ]));
    }
  });
})(this);
