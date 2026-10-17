/*
 * opnview front -- the People view (F7-13): the people the reader creates, and the machines the
 * reader assigns to them, on /api/owners and /api/clients. The mockup's People page.
 *
 * OWNERSHIP IS ASSIGNED, NEVER INFERRED. Every assignment names its client by id; nothing here
 * matches a machine to a person by its host name, its MAC, its vendor or its address, and nothing
 * suggests one. A client with an unstable identity is assigned like any other and marked as such.
 *
 * DELETING A PERSON CAN BE UNDONE. The deletion answers every assignment it cleared, and Undo sends
 * that answer back unchanged: the person is created again and each assignment restored with its
 * original instant. A machine assigned to somebody else in the meantime, or gone, is reported as not
 * restored, with why.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms, format = opnview.format;
  var t = opnview.strings.t, tk = opnview.strings.tk, tp = opnview.strings.tp;

  function load(context) {
    return Promise.all([context.api.get("/api/owners"), context.api.get("/api/clients")]).then(function (answers) {
      var model = context.state;
      model.failed = answers[0].status !== 200 || answers[1].status !== 200;
      if (!model.failed) {
        model.owners = answers[0].body.owners;
        model.clients = answers[1].body.clients;
      }
      model.add = model.add || { display_name: "" };
      model.filter = model.filter || { show: "all" };
      model.renaming = model.renaming || {};
    });
  }

  function refusalOf(result) {
    return result.body && result.body.condition ? result.body : { condition: "internal_error" };
  }

  function create(context) {
    var model = context.state;
    model.addRefusal = null;
    return context.api.post("/api/owners", { display_name: model.add.display_name }).then(function (result) {
      if (result.status === 200) {
        model.add.display_name = "";
        return load(context);
      }
      model.addRefusal = refusalOf(result);
      return null;
    }).then(function () { context.rerender(); });
  }

  function rename(context, owner) {
    var model = context.state, draft = model.renaming[owner.id];
    draft.refusal = null;
    return context.api.post("/api/owners/" + encodeURIComponent(owner.id) + "/rename",
      { display_name: draft.display_name }).then(function (result) {
      if (result.status === 200) {
        delete model.renaming[owner.id];
        return load(context);
      }
      draft.refusal = refusalOf(result);
      return null;
    }).then(function () { context.rerender(); });
  }

  function remove(context, owner) {
    var model = context.state;
    model.undone = null;
    return context.api.post("/api/owners/" + encodeURIComponent(owner.id) + "/delete", {}).then(function (result) {
      if (result.status === 200) {
        model.deleted = result.body;
        return load(context);
      }
      model.listRefusal = refusalOf(result);
      return null;
    }).then(function () { context.rerender(); });
  }

  function undo(context) {
    var model = context.state, deleted = model.deleted;
    return context.api.post("/api/owners/restore", deleted).then(function (result) {
      if (result.status === 200) {
        model.deleted = null;
        model.undone = result.body;
        return load(context);
      }
      model.listRefusal = refusalOf(result);
      return null;
    }).then(function () { context.rerender(); });
  }

  function assign(context, client, ownerID) {
    var model = context.state;
    model.assignRefusal = null;
    var request = ownerID === ""
      ? context.api.post("/api/clients/unassign", { clients: [client.id] })
      : context.api.post("/api/owners/" + encodeURIComponent(ownerID) + "/assign", { clients: [client.id] });
    return request.then(function (result) {
      if (result.status === 200) { return load(context); }
      model.assignRefusal = refusalOf(result);
      return null;
    }).then(function () { context.rerender(); });
  }

  function personBlock(context, owner) {
    var model = context.state, draft = model.renaming[owner.id];
    if (draft) {
      var error = draft.refusal ? forms.refusal(draft.refusal.condition) : null;
      return h("form", { key: "person-" + owner.id, "class": "person is-editing", novalidate: true, "data-dirty": "true",
        on: { submit: function (event) { event.preventDefault(); rename(context, owner); } } }, [
        h("input", { id: "rename-" + owner.id, type: "text", autocomplete: "off", value: draft.display_name,
          "aria-label": t("people.rename_label", { name: owner.display_name }), "aria-invalid": error ? "true" : null,
          on: { input: function (event) { draft.display_name = event.target.value; draft.refusal = null; context.rerender(); } } }),
        forms.button("save", t("action.save"), null, { submit: true, small: true, primary: true }),
        forms.button("cancel", t("action.cancel"), function () { delete model.renaming[owner.id]; context.rerender(); },
          { small: true }),
        error ? h("span", { "class": "field-error", role: "alert" }, error) : null
      ]);
    }
    return h("span", { key: "person-" + owner.id, "class": "person" }, [
      h("span", { "class": "person-name" }, owner.display_name),
      h("span", { "class": "person-count" }, tp("people.client_count", owner.client_count)),
      forms.button("rename", t("action.rename"), function () {
        model.renaming[owner.id] = { display_name: owner.display_name, refusal: null };
        context.rerender();
      }, { small: true, label: t("people.rename_label", { name: owner.display_name }) }),
      forms.button("remove", t("action.remove"), function () { remove(context, owner); },
        { small: true, destructive: true, label: t("people.remove_label", { name: owner.display_name }) })
    ]);
  }

  function undoPanel(context) {
    var model = context.state;
    if (model.deleted) {
      return h("div", { key: "undo", "class": "undo-panel", role: "status" }, [
        h("span", {}, t("people.deleted", { name: model.deleted.display_name })),
        model.deleted.cleared.length > 0 ? h("span", { "class": "muted" },
          tp("people.cleared_count", model.deleted.cleared.length)) : null,
        forms.button("undo", t("action.undo"), function () { undo(context); }, { small: true, primary: true })
      ]);
    }
    if (model.undone) {
      var notRestored = model.undone.not_restored || [];
      return h("div", { key: "undone", "class": "undo-panel", role: "status" }, [
        h("span", {}, t("people.restored", { name: model.undone.owner.display_name })),
        h("span", { "class": "muted" }, tp("people.restored_count", model.undone.restored.length)),
        notRestored.length > 0 ? h("div", { "class": "not-restored" }, [
          h("span", {}, tp("people.not_restored_count", notRestored.length)),
          h("ul", {}, notRestored.map(function (entry) {
            var client = (model.clients || []).filter(function (candidate) { return candidate.id === entry.client; })[0];
            return h("li", { key: "not-restored-" + entry.client }, [
              h("span", { "class": "mono" }, client ? clientName(client) : String(entry.client)),
              forms.badge("reason", tk("people.not_restored.", entry.reason), "gap")
            ]);
          }))
        ]) : null
      ]);
    }
    return null;
  }

  function clientName(client) {
    return client.hostname || client.identity_key;
  }

  function interfaceName(entry) {
    if (!entry) { return null; }
    return entry.user_label || entry.description || entry.identifier;
  }

  function clientRow(context, client) {
    var model = context.state;
    var id = "owner-" + client.id;
    var current = client.owner ? String(client.owner.id) : "";
    var choices = [{ value: "", label: t("people.nobody") }].concat(model.owners.map(function (owner) {
      return { value: String(owner.id), label: owner.display_name };
    }));
    return h("tr", { key: "client-" + client.id, "class": client.owner ? null : "is-unowned" }, [
      h("td", {}, [
        h("span", { "class": "client-name" }, clientName(client)),
        h("span", { "class": "client-identity" }, [
          client.mac ? h("span", { "class": "mono" }, client.mac) : null,
          client.last_address ? h("span", { "class": "mono" }, client.last_address) : null
        ])
      ]),
      h("td", {}, interfaceName(client.interface)),
      h("td", {}, [
        h("span", { "class": "mono" }, tk("people.identity.", client.identity_kind)),
        client.unstable_identity ? forms.badge("unstable", t("people.unstable_identity")) : null
      ]),
      h("td", { "class": "owner-cell" }, h("select", {
        id: id, "aria-label": t("people.owner_of", { name: clientName(client) }), value: current,
        on: { change: function (event) { assign(context, client, event.target.value); } }
      }, choices.map(function (choice) {
        return h("option", { key: "choice-" + choice.value, value: choice.value, selected: choice.value === current },
          choice.label);
      }))),
      h("td", { "class": "mono" }, client.owner_assigned_at === null ? h("span", { "class": "muted" }, t("people.never_assigned"))
        : format.instant(client.owner_assigned_at))
    ]);
  }

  opnview.views.register({
    id: "people",
    path: "/people",
    rail: "installation",
    order: 50,
    title: function () { return t("people.title"); },
    load: load,
    render: function (context) {
      var model = context.state;
      if (!model.owners) {
        return model.failed ? h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" },
          t("state.read_failed"))) : h("p", { "class": "loading", role: "status" }, t("state.loading"));
      }
      var unowned = model.clients.filter(function (client) { return !client.owner; });
      var rows = model.clients.slice().sort(function (a, b) {
        if (!a.owner !== !b.owner) { return a.owner ? 1 : -1; }
        return a.id - b.id;
      });
      if (model.filter.show === "unowned") { rows = rows.filter(function (client) { return !client.owner; }); }
      var addError = model.addRefusal ? forms.refusal(model.addRefusal.condition) : null;
      return h("div", { "class": "people-view" }, [
        h("section", { key: "people", "class": "people-card", "aria-label": t("people.heading.people") }, [
          h("div", { "class": "people-head" }, [
            h("h2", {}, t("people.heading.people")),
            forms.badge("count", tp("people.people_count", model.owners.length))
          ]),
          h("div", { "class": "people-list" }, model.owners.length === 0
            ? h("span", { "class": "muted" }, t("people.nobody_yet"))
            : model.owners.map(function (owner) { return personBlock(context, owner); })),
          undoPanel(context),
          model.listRefusal ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(model.listRefusal.condition)) : null,
          h("form", { "class": "people-add", novalidate: true, "data-dirty": model.add.display_name ? "true" : "false",
            on: { submit: function (event) { event.preventDefault(); create(context); } } }, [
            h("label", { "for": "person-name" }, t("people.add_label")),
            forms.text("person-name", model.add, "display_name", function () { model.addRefusal = null; context.rerender(); },
              { error: addError }),
            forms.button("add", t("action.add"), null, { submit: true, primary: true }),
            addError ? h("span", { "class": "field-error", id: "person-name-error", role: "alert" }, addError) : null
          ])
        ]),
        h("section", { key: "clients", "class": "people-card", "aria-label": t("people.heading.clients") }, [
          h("div", { "class": "people-head" }, [
            h("h2", {}, t("people.heading.clients")),
            forms.badge("unassigned", t("people.unassigned_count", { unassigned: format.number(unowned.length),
              total: format.number(model.clients.length) })),
            h("span", { "class": "people-filter" }, [
              h("label", { "for": "people-filter" }, t("people.filter")),
              forms.select("people-filter", model.filter, "show", [
                { value: "all", label: t("people.filter.all") },
                { value: "unowned", label: t("people.filter.unowned") }
              ], context.rerender)
            ])
          ]),
          model.assignRefusal ? h("p", { "class": "field-error", role: "alert" },
            forms.refusal(model.assignRefusal.condition)) : null,
          h("div", { "class": "people-scroll", tabindex: "0", role: "region", "aria-label": t("people.heading.clients") },
            h("table", { "class": "data people-table" }, [
              h("thead", {}, h("tr", {}, [h("th", {}, t("people.column.client")), h("th", {}, t("people.column.interface")),
                h("th", {}, t("people.column.identified_by")), h("th", {}, t("people.column.owner")),
                h("th", {}, t("people.column.assigned"))])),
              h("tbody", {}, rows.length === 0
                ? h("tr", { key: "none" }, h("td", { colspan: "5" }, h("span", { "class": "muted" },
                  model.clients.length === 0 ? t("people.no_client") : t("people.every_client_owned"))))
                : rows.map(function (client) { return clientRow(context, client); }))
            ]))
        ])
      ]);
    }
  });
})(this);
