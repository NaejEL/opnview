/*
 * opnview front -- the pieces every installation view is built from: a card, a field, a refusal,
 * and the ONE GENERIC SETTINGS FORM (F7-9).
 *
 * THE GENERIC FORM RENDERS A SETTING FROM ITS DECLARATION ALONE: its type (integer, number, boolean,
 * text, enum, secret), its default, its bounds, its choices and its label key, as the API exposes
 * it -- every loaded plugin's settings from GET /api/plugins, the live settings and the NetFlow
 * settings. No front code names a plugin or a setting: a plugin with new settings is drawn by
 * reading its declarations, and a value the declaration refuses is refused in the form before
 * anything is sent. A refusal the server answers is shown against the field it names.
 *
 * A SECRET IS WRITE-ONLY. Its value is never read from the server and never drawn: the form shows
 * whether one is set, offers an empty field to replace it, and sends it only when something was
 * typed. A form holding typed values that are not saved marks itself data-dirty, so a refresh leaves
 * it alone (view.js).
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t, tk = opnview.strings.tk, td = opnview.strings.td;

  var forms = {};

  /* A card of a view: a heading and its body. */
  forms.card = function (key, heading, body, extra) {
    return h("section", { key: key, id: "card-" + key, "class": "panel-card", "aria-label": heading }, [
      h("div", { "class": "panel-card-head" }, [h("h2", {}, heading)].concat(extra || [])),
      body
    ]);
  };

  /* The words for a refusal the server answered. */
  forms.refusal = function (condition) {
    return tk("error.", condition || "internal_error");
  };

  /* One labelled control, with its refusal under it when there is one. */
  forms.field = function (id, label, control, error, hint) {
    return h("div", { key: "field-" + id, "class": "form-field" + (error ? " is-refused" : "") }, [
      h("label", { "for": id }, [label, hint ? h("span", { "class": "hint" }, hint) : null]),
      control,
      error ? h("p", { "class": "field-error", id: id + "-error", role: "alert" }, error) : null
    ]);
  };

  /* A row of label and value, for a figure a card states. */
  forms.fact = function (key, label, value) {
    return h("div", { key: key, "class": "fact" }, [
      h("dt", {}, label),
      h("dd", {}, value)
    ]);
  };

  /* A text field bound to one member of a draft. */
  forms.text = function (id, draft, member, onChange, options) {
    var settings = options || {};
    return h("input", {
      id: id, name: member, type: settings.type || "text", autocomplete: settings.autocomplete || "off",
      "aria-describedby": settings.error ? id + "-error" : null,
      "aria-invalid": settings.error ? "true" : null,
      min: settings.min, max: settings.max, step: settings.step, spellcheck: settings.spellcheck,
      value: draft[member] === undefined || draft[member] === null ? "" : String(draft[member]),
      on: { input: function (event) { draft[member] = event.target.value; onChange(); } }
    });
  };

  /* A select bound to one member of a draft; options are [{value, label}]. */
  forms.select = function (id, draft, member, choices, onChange, label) {
    var current = draft[member] === undefined || draft[member] === null ? "" : String(draft[member]);
    return h("select", {
      id: id, name: member, "aria-label": label || null, value: current,
      on: { change: function (event) { draft[member] = event.target.value; onChange(); } }
    }, choices.map(function (choice) {
      return h("option", { key: "option-" + choice.value, value: choice.value, selected: choice.value === current },
        choice.label);
    }));
  };

  /* A button. */
  forms.button = function (key, label, onClick, options) {
    var settings = options || {};
    return h("button", {
      key: key, type: settings.submit ? "submit" : "button",
      "class": "btn" + (settings.primary ? " btn-primary" : "") + (settings.small ? " btn-small" : "") +
        (settings.destructive ? " btn-destructive" : ""),
      disabled: !!settings.disabled, "aria-label": settings.label || null,
      on: onClick ? { click: onClick } : {}
    }, label);
  };

  /* A badge naming a state in a word or two. tone: ok, off, unavailable, ref, gap or none. */
  forms.badge = function (key, text, tone) {
    return h("span", { key: key, "class": "badge" + (tone ? " badge-" + tone : "") }, text);
  };

  /* ---------------------------------------------------------------------------------------------
   * The generic settings form.
   * ------------------------------------------------------------------------------------------- */

  /* Whether a typed text is a value of a declaration, and the value to send when it is. */
  function parseValue(declaration, text) {
    var trimmed = typeof text === "string" ? text.trim() : text;
    switch (declaration.type) {
      case "integer":
        if (!/^-?\d+$/.test(String(trimmed))) { return { refusal: "invalid_setting_value" }; }
        return bounded(declaration, parseInt(trimmed, 10));
      case "number":
        var number = Number(trimmed);
        if (trimmed === "" || !isFinite(number)) { return { refusal: "invalid_setting_value" }; }
        return bounded(declaration, number);
      case "boolean":
        return { value: !!text };
      case "enum":
        if ((declaration.values || []).indexOf(text) < 0) { return { refusal: "invalid_setting_value" }; }
        return { value: text };
      case "secret":
        if (!text) { return { refusal: "invalid_setting_value" }; }
        return { value: text };
      default:
        return { value: typeof text === "string" ? text : String(text) };
    }
  }

  function bounded(declaration, value) {
    if (declaration.min !== undefined && declaration.min !== null && value < declaration.min) {
      return { refusal: "setting_out_of_range" };
    }
    if (declaration.max !== undefined && declaration.max !== null && value > declaration.max) {
      return { refusal: "setting_out_of_range" };
    }
    return { value: value };
  }

  function initialText(declaration) {
    if (declaration.type === "secret") { return ""; }
    if (declaration.type === "boolean") { return !!declaration.value; }
    return declaration.value === null || declaration.value === undefined ? "" : String(declaration.value);
  }

  /* A model of one form: its declarations, the draft, the refusals, and how it saves. labelOf names a
   * declaration; save sends {key: value} and answers {status, body}. */
  forms.settingsModel = function (id, declarations, labelOf, save) {
    var model = { id: id, declarations: declarations, labelOf: labelOf, save: save, draft: {}, errors: {},
      saved: false, sending: false };
    model.reset = function (fresh) {
      model.declarations = fresh || model.declarations;
      model.draft = {};
      model.declarations.forEach(function (declaration) { model.draft[declaration.key] = initialText(declaration); });
    };
    model.reset();
    model.dirty = function () {
      return model.declarations.some(function (declaration) {
        return model.draft[declaration.key] !== initialText(declaration);
      });
    };
    return model;
  };

  /* Submits the changed values of a model, refusing in the form what the declarations refuse. */
  forms.submitSettings = function (model, rerender) {
    var body = {}, refused = false;
    model.errors = {};
    model.saved = false;
    model.declarations.forEach(function (declaration) {
      var text = model.draft[declaration.key];
      if (text === initialText(declaration)) { return; }
      var parsed = parseValue(declaration, text);
      if (parsed.refusal) {
        model.errors[declaration.key] = parsed.refusal;
        refused = true;
        return;
      }
      body[declaration.key] = parsed.value;
    });
    if (refused || Object.keys(body).length === 0) {
      rerender();
      return Promise.resolve(null);
    }
    model.sending = true;
    rerender();
    return model.save(body).then(function (result) {
      model.sending = false;
      if (result.status === 200) {
        model.saved = true;
        rerender();
        return result;
      }
      var condition = result.body && result.body.condition;
      var key = result.body && (result.body.key || result.body.field);
      if (key && model.draft.hasOwnProperty(key)) {
        model.errors[key] = condition;
      } else {
        model.errors[""] = condition;
      }
      rerender();
      return result;
    });
  };

  function settingControl(model, declaration, id, onChange) {
    var error = model.errors[declaration.key];
    switch (declaration.type) {
      case "boolean":
        return h("input", { id: id, type: "checkbox", checked: !!model.draft[declaration.key],
          on: { change: function (event) { model.draft[declaration.key] = event.target.checked; onChange(); } } });
      case "enum":
        return forms.select(id, model.draft, declaration.key, (declaration.values || []).map(function (value) {
          return { value: value, label: value };
        }), onChange);
      case "secret":
        return forms.text(id, model.draft, declaration.key, onChange, { type: "password", autocomplete: "new-password",
          error: error });
      case "integer":
      case "number":
        return forms.text(id, model.draft, declaration.key, onChange, { type: "number", min: declaration.min,
          max: declaration.max, step: declaration.type === "integer" ? 1 : "any", error: error });
      default:
        return forms.text(id, model.draft, declaration.key, onChange, { error: error });
    }
  }

  /* Draws one model as a form. */
  forms.settingsForm = function (model, rerender) {
    var onChange = function () { model.saved = false; rerender(); };
    var fields = model.declarations.map(function (declaration) {
      var id = model.id + "-" + declaration.key;
      var hints = [];
      if (declaration.type === "secret") {
        hints.push(declaration.set ? t("state.secret.set") : t("state.secret.not_set"));
      } else if ((declaration.type === "integer" || declaration.type === "number") &&
          declaration.min !== undefined && declaration.min !== null) {
        hints.push(declaration.max !== undefined && declaration.max !== null
          ? t("settings.hint.range", { min: opnview.format.number(declaration.min, 6),
            max: opnview.format.number(declaration.max, 6) })
          : t("settings.hint.minimum", { min: opnview.format.number(declaration.min, 6) }));
      }
      var error = model.errors[declaration.key];
      return forms.field(id, model.labelOf(declaration), settingControl(model, declaration, id, onChange),
        error ? forms.refusal(error) : null, hints.join(" "));
    });
    var general = model.errors[""];
    return h("form", {
      key: "form-" + model.id, "class": "settings-form", "data-dirty": model.dirty() ? "true" : "false",
      novalidate: true,
      on: { submit: function (event) { event.preventDefault(); forms.submitSettings(model, rerender); } }
    }, [
      fields,
      general ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(general)) : null,
      h("div", { "class": "form-actions" }, [
        forms.button("save", t("action.save"), null, { submit: true, primary: true, disabled: model.sending }),
        model.saved ? h("span", { "class": "form-notice", role: "status" }, t("notice.settings_saved")) : null
      ])
    ]);
  };

  /* The label of a plugin's declared setting: its own catalogue key, else its key as data. */
  forms.declaredLabel = function (declaration) {
    return declaration.label ? td(declaration.label) : declaration.key;
  };

  opnview.forms = forms;
})(this);
