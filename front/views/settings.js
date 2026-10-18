/*
 * opnview front -- the Settings view (F7-9): where the firewall's credentials enter the product, the
 * appearance, every declared setting of every loaded plugin -- the Public Suffix List's and NetFlow's
 * among them -- and of the live figures through ONE generic form, and the listing of the plugins and
 * the front's parts, loaded and refused, with their reasons.
 *
 * NO CONTROL HERE CAN CHANGE A FIREWALL SETTING. The firewall card stores opnview's own credentials
 * and verifies them by one read; the certificate fetch reads the certificate the firewall presents
 * and stores nothing until the reader pins it and saves.
 *
 * THE API SECRET IS WRITE-ONLY: its value is never read back, the card states whether one is stored
 * and readable, and an empty field keeps the stored one.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms;
  var t = opnview.strings.t, tk = opnview.strings.tk;

  function load(context) {
    var model = context.state;
    return Promise.all([
      context.api.get("/api/settings"),
      context.api.get("/api/plugins"),
      context.api.get("/api/live/settings"),
      context.api.get("/api/widgets")
    ]).then(function (answers) {
      model.settings = answers[0].status === 200 ? answers[0].body : null;
      model.plugins = answers[1].status === 200 ? answers[1].body.plugins : [];
      model.live = answers[2].status === 200 ? answers[2].body : null;
      model.registry = answers[3].status === 200 ? answers[3].body : null;
      model.failed = answers.some(function (answer) { return answer.status !== 200; });
      prepare(context);
    });
  }

  /* The drafts and the generic forms, built from the answers. */
  function prepare(context) {
    var model = context.state;
    var firewall = model.settings ? model.settings.firewall : null;
    model.firewall = model.firewall || { draft: {}, refusal: null, presented: null, sending: false, verification: null };
    if (firewall && !model.firewall.dirty) {
      model.firewall.draft = { url: firewall.url || "", api_key: firewall.api_key || "", api_secret: "",
        certificate_fingerprint: firewall.certificate_fingerprint || "" };
    }
    model.forms = [];
    (model.plugins || []).forEach(function (plugin) {
      if (plugin.state !== "loaded" || !plugin.settings || plugin.settings.length === 0) { return; }
      var save = function (body) {
        return context.api.post("/api/plugins/" + encodeURIComponent(plugin.name) + "/settings", body)
          .then(function (result) {
            if (result.status === 200) { form.reset(result.body.settings); }
            return result;
          });
      };
      var form = forms.settingsModel("plugin-" + plugin.name, plugin.settings, forms.declaredLabel, save);
      model.forms.push({ key: "plugin-" + plugin.name, heading: plugin.display_name || plugin.name, form: form });
    });
    if (model.live) {
      var declarations = function (settings) {
        return settings.map(function (setting) {
          return { key: setting.key, type: "integer", min: setting.minimum, max: setting.maximum,
            "default": setting["default"], value: setting.value, set: setting.set };
        });
      };
      var live = forms.settingsModel("live", declarations(model.live.settings), function (declaration) {
        return tk("settings.live.", declaration.key);
      }, function (body) {
        return context.api.post("/api/live/settings", body).then(function (result) {
          if (result.status === 200) { live.reset(declarations(result.body.settings)); }
          return result;
        });
      });
      model.forms.push({ key: "live", heading: t("settings.heading.live"), form: live });
    }
  }

  /* ---------------------------------------------------------------------------------------------
   * The firewall card.
   * ------------------------------------------------------------------------------------------- */

  function firewallCard(context) {
    var model = context.state, card = model.firewall, firewall = model.settings.firewall;
    var changed = function () { card.dirty = true; card.refusal = null; card.saved = false; context.rerender(); };
    var errorFor = function (member) {
      return card.refusal && card.refusal.field === member ? forms.refusal(card.refusal.condition) : null;
    };
    var save = function (event) {
      event.preventDefault();
      card.sending = true;
      card.refusal = null;
      context.rerender();
      context.api.post("/api/settings/firewall", card.draft).then(function (result) {
        card.sending = false;
        if (result.status === 200) {
          model.settings = result.body;
          card.dirty = false;
          card.saved = true;
          card.verification = result.body.firewall.verification;
          prepare(context);
        } else {
          card.refusal = result.body || { condition: "internal_error" };
        }
        context.rerender();
      });
    };
    var fetchCertificate = function () {
      card.refusal = null;
      card.presented = null;
      context.api.post("/api/settings/firewall/certificate", { url: card.draft.url }).then(function (result) {
        if (result.status === 200) {
          card.presented = result.body.presented_fingerprint;
        } else {
          card.refusal = result.body || { condition: "internal_error" };
        }
        context.rerender();
      });
    };
    var pin = function () {
      card.draft.certificate_fingerprint = card.presented;
      card.presented = null;
      changed();
    };
    return forms.card("firewall", t("settings.heading.firewall"), h("form", {
      "class": "settings-form", novalidate: true, "data-dirty": card.dirty ? "true" : "false",
      on: { submit: save }
    }, [
      forms.field("firewall-url", t("field.firewall_url"),
        forms.text("firewall-url", card.draft, "url", changed, { type: "url", error: errorFor("url") }), errorFor("url")),
      forms.field("firewall-api-key", t("field.api_key"),
        forms.text("firewall-api-key", card.draft, "api_key", changed, { error: errorFor("api_key") }), errorFor("api_key")),
      forms.field("firewall-api-secret", t("field.api_secret"),
        forms.text("firewall-api-secret", card.draft, "api_secret", changed,
          { type: "password", autocomplete: "new-password", error: errorFor("api_secret") }),
        errorFor("api_secret"), tk("state.credential.", firewall.credential_state)),
      forms.field("firewall-fingerprint", t("field.certificate_fingerprint"),
        h("div", { "class": "field-row" }, [
          forms.text("firewall-fingerprint", card.draft, "certificate_fingerprint", changed,
            { spellcheck: "false", error: errorFor("certificate_fingerprint") }),
          forms.button("fetch", t("action.fetch_certificate"), fetchCertificate, { small: true })
        ]), errorFor("certificate_fingerprint")),
      card.presented ? h("div", { key: "presented", "class": "presented", role: "status" }, [
        h("span", { "class": "presented-label" }, t("notice.certificate_presented")),
        h("code", { "class": "mono" }, card.presented),
        forms.button("pin", t("action.pin_certificate"), pin, { small: true })
      ]) : null,
      card.refusal && !card.refusal.field
        ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(card.refusal.condition)) : null,
      h("dl", { "class": "facts" }, [
        forms.fact("credentials", t("settings.label.credential_state"), tk("state.credential.", firewall.credential_state)),
        card.verification ? forms.fact("verification", t("settings.label.verification"),
          tk("state.verification.", card.verification)) : null
      ]),
      h("div", { "class": "form-actions" }, [
        forms.button("save", t("action.save"), null, { submit: true, primary: true, disabled: card.sending }),
        card.saved ? h("span", { "class": "form-notice", role: "status" }, t("notice.settings_saved")) : null
      ])
    ]));
  }

  /* ---------------------------------------------------------------------------------------------
   * The appearance card: the same three selectors as the header's.
   * ------------------------------------------------------------------------------------------- */

  function appearanceCard(context) {
    var appearance = opnview.appearance, model = appearance.state();
    var draft = { palette: appearance.palette(), mode: appearance.mode(), language: appearance.language() };
    var saveOne = function (member) {
      return function () {
        var change = {};
        change[member] = draft[member];
        appearance.save(change).then(function () {
          return member === "language" ? opnview.strings.load(appearance.language()) : null;
        }).then(function () { context.rerender(); });
      };
    };
    var fields = [
      forms.field("appearance-palette", t("appearance.palette"), forms.select("appearance-palette", draft, "palette",
        model.palettes.map(function (entry) { return { value: entry.id, label: appearance.paletteName(entry) }; }),
        saveOne("palette"))),
      forms.field("appearance-mode", t("appearance.mode"), forms.select("appearance-mode", draft, "mode",
        model.modes.map(function (mode) { return { value: mode, label: tk("appearance.mode.", mode) }; }),
        saveOne("mode")))
    ];
    if (model.languages.length >= 2) {
      fields.push(forms.field("appearance-language", t("appearance.language"),
        forms.select("appearance-language", draft, "language",
          model.languages.map(function (entry) { return { value: entry.code, label: entry.name }; }),
          saveOne("language"))));
    }
    return forms.card("appearance", t("settings.heading.appearance"), h("div", { "class": "settings-form" }, fields));
  }

  /* ---------------------------------------------------------------------------------------------
   * The plugins and the front's parts.
   * ------------------------------------------------------------------------------------------- */

  function partsCard(context) {
    var model = context.state;
    var plugins = model.plugins || [];
    var registry = model.registry || { refused: [], themes: [], languages: [] };
    var pluginRows = plugins.map(function (plugin) {
      return h("tr", { key: "plugin-" + plugin.name + "-" + plugin.origin,
        "class": plugin.state === "refused" ? "is-refused" : null }, [
        h("td", {}, [plugin.display_name || plugin.name, h("span", { "class": "ident" }, plugin.name)]),
        h("td", {}, tk("parts.origin.", plugin.origin)),
        h("td", {}, forms.badge("state", tk("parts.state.", plugin.state), plugin.state === "loaded" ? "ok" : "unavailable")),
        h("td", { "class": "diag" }, plugin.reason ? [h("span", { "class": "mono" }, plugin.reason.code),
          h("span", { "class": "ident" }, plugin.reason.detail)] : null)
      ]);
    });
    var partRows = [];
    registry.themes.forEach(function (theme) {
      partRows.push(h("tr", { key: "theme-" + theme.id }, [
        h("td", {}, tk("parts.part.", "theme")), h("td", { "class": "mono" }, theme.id),
        h("td", {}, tk("parts.origin.", theme.origin)),
        h("td", {}, forms.badge("state", t("parts.state.loaded"), "ok")), h("td", { "class": "ident" }, theme.file)
      ]));
    });
    registry.languages.forEach(function (language) {
      partRows.push(h("tr", { key: "language-" + language.code }, [
        h("td", {}, tk("parts.part.", "language")), h("td", { "class": "mono" }, language.code),
        h("td", {}, tk("parts.origin.", language.origin)),
        h("td", {}, forms.badge("state", t("parts.state.loaded"), "ok")),
        h("td", { "class": "diag" }, language.missing_strings.length > 0
          ? opnview.strings.tp("parts.missing_strings", language.missing_strings.length) : h("span", { "class": "ident" },
            language.file))
      ]));
    });
    registry.refused.forEach(function (refused, index) {
      partRows.push(h("tr", { key: "refused-" + index, "class": "is-refused" }, [
        h("td", {}, tk("parts.part.", refused.part)), h("td", { "class": "mono" }, refused.id),
        h("td", {}, t("parts.origin.data_directory")),
        h("td", {}, forms.badge("state", t("parts.state.refused"), "unavailable")),
        h("td", { "class": "diag" }, [h("span", { "class": "mono" }, refused.code),
          h("span", { "class": "ident" }, refused.path), h("span", { "class": "ident" }, refused.detail)])
      ]));
    });
    return forms.card("parts", t("settings.heading.parts"), [
      h("div", { key: "plugins", "class": "table-scroll" }, h("table", { "class": "data parts-table" }, [
        h("caption", {}, t("settings.parts.plugins")),
        h("thead", {}, h("tr", {}, [h("th", {}, t("parts.column.plugin")), h("th", {}, t("parts.column.origin")),
          h("th", {}, t("parts.column.state")), h("th", { "class": "diag" }, t("parts.column.reason"))])),
        h("tbody", {}, pluginRows)
      ])),
      h("div", { key: "front-parts", "class": "table-scroll" }, h("table", { "class": "data parts-table" }, [
        h("caption", {}, t("settings.parts.front")),
        h("thead", {}, h("tr", {}, [h("th", {}, t("parts.column.part")), h("th", {}, t("parts.column.id")),
          h("th", {}, t("parts.column.origin")), h("th", {}, t("parts.column.state")),
          h("th", { "class": "diag" }, t("parts.column.reason"))])),
        h("tbody", {}, partRows)
      ]))
    ]);
  }

  opnview.views.register({
    id: "settings",
    path: "/settings",
    rail: "installation",
    order: 10,
    title: function () { return t("settings.title"); },
    load: load,
    render: function (context) {
      var model = context.state;
      if (!model.settings) {
        return model.failed === undefined ? h("p", { "class": "loading", role: "status" }, t("state.loading"))
          : h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" }, t("state.read_failed")));
      }
      return h("div", { "class": "cards" }, [
        firewallCard(context),
        appearanceCard(context),
        (model.forms || []).map(function (entry) {
          return forms.card(entry.key, entry.heading, forms.settingsForm(entry.form, context.rerender));
        }),
        partsCard(context)
      ]);
    }
  });
})(this);
