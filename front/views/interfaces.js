/*
 * opnview front -- the Interfaces view (F7-11): every interface discovered on the firewall, its
 * networks and how each is counted, the networks it overlaps on other interfaces, the link-local
 * rule, and the label the reader gives it.
 *
 * NOTHING HERE IS INFERRED. A network is detected or set by the reader; an edit is applied at the
 * collector's next derivation. A LABEL IS NEVER DERIVED FROM A DESCRIPTION: an interface with no
 * label has an empty field, not its description typed in for the reader. Nothing here changes the
 * firewall: these are opnview's own records of what the firewall reported.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms;
  var t = opnview.strings.t, tk = opnview.strings.tk;

  function load(context) {
    return context.api.get("/api/interface-networks").then(function (result) {
      context.state.failed = result.status !== 200;
      if (result.status === 200) { install(context, result.body); }
    });
  }

  function install(context, answer) {
    var model = context.state;
    model.answer = answer;
    model.cards = model.cards || {};
    (answer.interfaces || []).forEach(function (entry) {
      var card = model.cards[entry.id];
      if (!card) {
        card = model.cards[entry.id] = { network: { network: "" }, label: { user_label: "" }, labelDirty: false,
          refusal: null, labelRefusal: null, saved: null };
      }
      if (!card.labelDirty) { card.label.user_label = entry.user_label || ""; }
    });
  }

  function edit(context, entry, card, action, network, linkLocal) {
    card.refusal = null;
    card.saved = null;
    var body = { interface: entry.id, action: action };
    if (network !== undefined) { body.network = network; }
    if (linkLocal !== undefined) { body.link_local = linkLocal; }
    return context.api.post("/api/interface-networks", body).then(function (result) {
      if (result.status === 200) {
        card.saved = action;
        if (action === "add") { card.network.network = ""; }
        return load(context);
      }
      card.refusal = result.body || { condition: "internal_error" };
      return null;
    }).then(function () { context.rerender(); });
  }

  function saveLabel(context, entry, card) {
    var text = card.label.user_label.trim();
    card.labelRefusal = null;
    return context.api.post("/api/interfaces/" + encodeURIComponent(entry.id) + "/label",
      { user_label: text === "" ? null : text }).then(function (result) {
      if (result.status === 200) {
        card.labelDirty = false;
        card.saved = "label";
        return load(context);
      }
      card.labelRefusal = result.body || { condition: "internal_error" };
      return null;
    }).then(function () { context.rerender(); });
  }

  function networkRows(context, entry, card) {
    return (entry.networks || []).map(function (network) {
      var actions = [];
      if (network.removed) {
        actions.push(forms.button("restore", t("action.restore"), function () {
          edit(context, entry, card, "restore", network.network);
        }, { small: true }));
      } else {
        if (network.origin === "detected") {
          actions.push(forms.button("confirm", t("action.confirm"), function () {
            edit(context, entry, card, "confirm", network.network);
          }, { small: true }));
        }
        actions.push(forms.button("remove", t("action.remove"), function () {
          edit(context, entry, card, "remove", network.network);
        }, { small: true, destructive: true }));
      }
      return h("tr", { key: "network-" + network.network, "class": network.removed ? "is-flagged" : null }, [
        h("td", { "class": "mono" }, network.network),
        h("td", {}, tk("state.origin.", network.origin)),
        h("td", {}, network.counts ? forms.badge("counts", t("state.yes"), "ok")
          : forms.badge("counts", network.not_counting_reason ? tk("state.counts.", network.not_counting_reason) : t("state.no"),
            "gap")),
        h("td", { "class": "row-actions" }, actions)
      ]);
    });
  }

  function interfaceCard(context, entry) {
    var card = context.state.cards[entry.id];
    var error = card.refusal ? forms.refusal(card.refusal.condition) : null;
    var labelError = card.labelRefusal ? forms.refusal(card.labelRefusal.condition) : null;
    var heading = entry.user_label || entry.description || entry.identifier;
    var facts = h("dl", { "class": "facts" }, [
      forms.fact("identifier", t("interfaces.label.identifier"), h("span", { "class": "mono" }, entry.identifier)),
      entry.device ? forms.fact("device", t("interfaces.label.device"), h("span", { "class": "mono" }, entry.device)) : null,
      entry.description ? forms.fact("description", t("interfaces.label.description"), entry.description) : null,
      forms.fact("upstream", t("interfaces.label.upstream"), entry.upstream ? t("state.yes") : t("state.no"))
    ]);
    var labelForm = h("form", {
      key: "label", "class": "settings-form inline-form", novalidate: true,
      "data-dirty": card.labelDirty ? "true" : "false",
      on: { submit: function (event) { event.preventDefault(); saveLabel(context, entry, card); } }
    }, [
      forms.field("label-" + entry.id, t("field.interface_label"),
        forms.text("label-" + entry.id, card.label, "user_label", function () {
          card.labelDirty = true; card.labelRefusal = null; context.rerender();
        }, { error: labelError }), labelError),
      h("div", { "class": "form-actions" }, [
        forms.button("save-label", t("action.save"), null, { submit: true, primary: true, small: true }),
        card.saved === "label" ? h("span", { "class": "form-notice", role: "status" }, t("notice.interface_saved")) : null
      ])
    ]);
    if (entry.upstream) {
      return forms.card("interface-" + entry.id, heading, [facts, labelForm]);
    }
    var overlaps = (entry.overlaps || []).map(function (overlap) {
      return h("tr", { key: "overlap-" + overlap.network + "-" + overlap.other }, [
        h("td", { "class": "mono" }, overlap.network),
        h("td", { "class": "mono" }, overlap.other),
        h("td", {}, overlap.other_description)
      ]);
    });
    return forms.card("interface-" + entry.id, heading, [
      facts,
      labelForm,
      h("h3", { key: "networks-heading" }, t("interfaces.heading.networks")),
      (entry.networks || []).length === 0 ? h("p", { key: "no-network", "class": "muted" }, t("state.interface.no_network"))
        : h("div", { key: "networks", "class": "table-scroll" }, h("table", { "class": "data" }, [
          h("thead", {}, h("tr", {}, [h("th", {}, t("interfaces.column.network")), h("th", {}, t("interfaces.column.origin")),
            h("th", {}, t("interfaces.column.counts")), h("th", {}, t("interfaces.column.action"))])),
          h("tbody", {}, networkRows(context, entry, card))
        ])),
      h("form", {
        key: "add", "class": "settings-form inline-form", novalidate: true,
        "data-dirty": card.network.network ? "true" : "false",
        on: { submit: function (event) { event.preventDefault(); edit(context, entry, card, "add", card.network.network.trim()); } }
      }, [
        forms.field("network-" + entry.id, t("field.network"),
          forms.text("network-" + entry.id, card.network, "network", function () {
            card.refusal = null; context.rerender();
          }, { spellcheck: "false", error: error }), error),
        h("div", { "class": "form-actions" }, [
          forms.button("add", t("action.add"), null, { submit: true, small: true }),
          card.saved && card.saved !== "label" ? h("span", { "class": "form-notice", role: "status" },
            t("notice.interface_saved")) : null
        ])
      ]),
      h("div", { key: "link-local", "class": "form-field checkbox-field" }, [
        h("input", { id: "link-local-" + entry.id, type: "checkbox", checked: !!entry.link_local,
          on: { change: function (event) { edit(context, entry, card, "link_local", undefined, event.target.checked); } } }),
        h("label", { "for": "link-local-" + entry.id }, t("field.link_local"))
      ]),
      overlaps.length > 0 ? h("div", { key: "overlaps" }, [
        h("h3", {}, t("interfaces.heading.overlaps")),
        h("table", { "class": "data" }, [
          h("thead", {}, h("tr", {}, [h("th", {}, t("interfaces.column.network")),
            h("th", {}, t("interfaces.column.overlapping")), h("th", {}, t("interfaces.column.interface"))])),
          h("tbody", {}, overlaps)
        ])
      ]) : null
    ]);
  }

  opnview.views.register({
    id: "interfaces",
    path: "/interfaces",
    rail: "installation",
    order: 30,
    title: function () { return t("interfaces.title"); },
    load: load,
    render: function (context) {
      var model = context.state;
      if (!model.answer) {
        return model.failed ? h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" },
          t("state.read_failed"))) : h("p", { "class": "loading", role: "status" }, t("state.loading"));
      }
      if ((model.answer.interfaces || []).length === 0) {
        return h("div", { "class": "state state-gap" }, h("span", { "class": "state-badge" }, t("state.interface.none")));
      }
      return h("div", { "class": "cards" }, model.answer.interfaces.map(function (entry) {
        return interfaceCard(context, entry);
      }));
    }
  });
})(this);
