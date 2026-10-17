/*
 * opnview front -- the Collection view (F7-10): every figure and every action of the collection,
 * on GET /api/collection. One card per sized kind -- the source's state, what is in force, what the
 * window measured, the suggestion, the implementations and their selection -- one per polled kind,
 * and one per group of settings.
 *
 * SHORTENING THE RETENTION IS DESTRUCTIVE AND CONFIRMS. The server answers such a save 409
 * retention_confirmation_required with what is in force and what was asked, stores nothing, and the
 * view asks the reader to confirm before sending the save again with `confirm` equal to the new
 * horizon. Nothing else here deletes anything.
 *
 * THE SUGGESTION IS NEVER APPLIED BY ITSELF: "fill with the suggestion" puts its figures in the
 * fields, and the reader saves them or not.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms, format = opnview.format;
  var t = opnview.strings.t, tk = opnview.strings.tk, td = opnview.strings.td;

  var SELECTIONS = ["auto", "on", "off"];

  function load(context) {
    return context.api.get("/api/collection").then(function (result) {
      context.state.failed = result.status !== 200;
      if (result.status === 200) { install(context, result.body); }
    });
  }

  /* Keeps an answer and builds a draft per card that the reader has not touched. */
  function install(context, answer) {
    var model = context.state;
    model.answer = answer;
    model.cards = model.cards || {};
    answer.kinds.concat(answer.polled_kinds).forEach(function (kind) {
      var card = model.cards["kind-" + kind.kind];
      if (card && card.dirty) { return; }
      var selections = {};
      kind.implementations.forEach(function (implementation) { selections[implementation.provider_key] = implementation.selection; });
      model.cards["kind-" + kind.kind] = {
        draft: { interval_seconds: String(kind.in_force ? kind.in_force.interval_seconds : kind.interval_seconds),
          page_size: kind.in_force && kind.in_force.page_size !== null ? String(kind.in_force.page_size) : "",
          selections: selections },
        dirty: false, refusal: null, saved: card ? card.saved : false
      };
    });
    answer.settings.forEach(function (group) {
      var card = model.cards["settings-" + group.card];
      if (card && card.dirty) { return; }
      var draft = {};
      group.settings.forEach(function (setting) { draft[setting.key] = String(setting.value); });
      model.cards["settings-" + group.card] = { draft: draft, dirty: false, refusal: null, confirmation: null,
        saved: card ? card.saved : false };
    });
  }

  function provider(kind, key) {
    // A provider's name is composed from its registry row; the registry's own test holds every row of
    // a fresh schema to a catalogue entry.
    return td("provider." + kind + "." + key);
  }

  function availabilityBadge(key, code) {
    var tone = code === "healthy" ? "ok" : code === "not_collecting" ? "off" : code === "unavailable" ? "unavailable"
      : "gap";
    return forms.badge(key, tk("state.availability.", code), tone);
  }

  function rateText(rate) {
    return rate.per_second !== null && rate.per_second !== undefined ? format.number(rate.per_second, 3)
      : tk("state.sizing.", rate.state);
  }

  /* The selectors of a kind's implementations. */
  function implementationFields(context, kind, card, changed) {
    return kind.implementations.map(function (implementation) {
      var id = "selection-" + kind.kind + "-" + implementation.provider_key;
      return forms.field(id, provider(kind.kind, implementation.provider_key),
        forms.select(id, card.draft.selections, implementation.provider_key, SELECTIONS.map(function (selection) {
          return { value: selection, label: tk("state.sizing.selection_", selection) };
        }), changed),
        card.refusal && card.refusal.field === "selections." + implementation.provider_key
          ? forms.refusal(card.refusal.condition) : null,
        implementation.availability ? tk("state.availability.", implementation.availability) : null);
    });
  }

  function saveKind(context, kind, card, adjustable) {
    return function (event) {
      event.preventDefault();
      var body = { interval_seconds: Number(card.draft.interval_seconds), selections: card.draft.selections };
      if (adjustable) { body.page_size = Number(card.draft.page_size); }
      card.refusal = null;
      context.api.post("/api/collection/kinds/" + encodeURIComponent(kind.kind), body).then(function (result) {
        if (result.status === 200) {
          card.dirty = false;
          card.saved = true;
          install(context, result.body);
        } else {
          card.refusal = result.body || { condition: "internal_error" };
        }
        context.rerender();
      });
    };
  }

  function sizedKindCard(context, kind) {
    var card = context.state.cards["kind-" + kind.kind];
    var changed = function () { card.dirty = true; card.saved = false; card.refusal = null; context.rerender(); };
    var errorFor = function (member) {
      return card.refusal && card.refusal.field === member ? forms.refusal(card.refusal.condition) : null;
    };
    var measurement = kind.measurement, inForce = kind.in_force, suggestion = kind.suggestion;
    var fill = function () {
      if (suggestion.page_size !== null && kind.page_size_adjustable) { card.draft.page_size = String(suggestion.page_size); }
      if (suggestion.interval_seconds !== null) { card.draft.interval_seconds = String(suggestion.interval_seconds); }
      changed();
    };
    var facts = [
      forms.fact("source", t("collection.label.source_state"), [
        availabilityBadge("availability", kind.source_state.availability),
        h("span", { "class": "fact-aside" }, tk("state.sizing.selection_", kind.source_state.selection))
      ]),
      forms.fact("page-size", t("collection.label.page_size_in_force"), inForce.page_size === null
        ? t("state.sizing.page_not_adjustable") : format.number(inForce.page_size)),
      forms.fact("interval", t("collection.label.interval_in_force"), format.duration(inForce.interval_seconds)),
      inForce.page_ceiling !== null ? forms.fact("ceiling", t("collection.label.page_ceiling"),
        format.number(inForce.page_ceiling)) : null,
      inForce.page_state ? forms.fact("page-state", t("collection.label.page_state"),
        tk("state.sizing.", inForce.page_state)) : null
    ];
    var measured = [
      forms.fact("window", t("collection.label.window"), t("collection.window", {
        from: format.instant(measurement.window_start_at), to: format.instant(measurement.window_end_at) })),
      forms.fact("retention", t("collection.label.retention_horizon"), measurement.retention_seconds === null
        ? t("state.sizing.retention_unlimited") : format.duration(measurement.retention_seconds)),
      forms.fact("covered", t("collection.label.covered"), measurement.covered_from_at === null
        ? t("state.sizing.no_records_in_window") : t("collection.window", {
          from: format.instant(measurement.covered_from_at), to: format.instant(measurement.covered_to_at) })),
      forms.fact("records", t("collection.label.record_count"), format.number(measurement.record_count)),
      forms.fact("mean", t("collection.label.mean_rate_source"), rateText(measurement.mean_rate)),
      forms.fact("ingested", t("collection.label.mean_rate_ingested"), rateText(measurement.ingested_mean_rate)),
      forms.fact("peak", t("collection.label.peak_rate"), rateText(measurement.peak_rate)),
      forms.fact("gaps", t("collection.label.gap_count"), format.number(measurement.gap_count)),
      forms.fact("missed", t("collection.label.missed_seconds"), format.duration(measurement.missed_seconds))
    ];
    var gapRows = (measurement.gaps || []).map(function (gap) {
      return h("tr", { key: "gap-" + gap.reason }, [
        h("td", { "class": "diag" }, tk("gap_reason.", gap.reason)),
        h("td", { "class": "num" }, format.number(gap.count)),
        h("td", { "class": "num" }, format.duration(gap.missed_seconds))
      ]);
    });
    var suggested = [
      forms.fact("suggestion", t("collection.label.suggestion_state"), tk("state.sizing.", suggestion.state)),
      suggestion.page_size !== null ? forms.fact("suggested-page", t("collection.label.suggested_page_size"),
        format.number(suggestion.page_size)) : null,
      suggestion.interval_seconds !== null ? forms.fact("suggested-interval", t("collection.label.suggested_interval"),
        format.duration(suggestion.interval_seconds)) : null
    ];
    var hasSuggestion = suggestion.page_size !== null || suggestion.interval_seconds !== null;
    return forms.card("kind-" + kind.kind, tk("collection.kind.", kind.kind), [
      h("div", { key: "figures", "class": "card-columns" }, [
        h("div", { "class": "card-column" }, [h("h3", {}, t("collection.group.in_force")), h("dl", { "class": "facts" }, facts)]),
        h("div", { "class": "card-column" }, [h("h3", {}, t("collection.group.measurement")),
          h("dl", { "class": "facts" }, measured),
          gapRows.length > 0 ? h("table", { "class": "data" }, [
            h("thead", {}, h("tr", {}, [h("th", { "class": "diag" }, t("collection.label.gap_reason")),
              h("th", { "class": "num" }, t("collection.label.gap_count")),
              h("th", { "class": "num" }, t("collection.label.missed_seconds"))])),
            h("tbody", {}, gapRows)]) : null]),
        h("div", { "class": "card-column" }, [h("h3", {}, t("collection.group.suggestion")), h("dl", { "class": "facts" }, suggested)])
      ]),
      h("form", { key: "form", "class": "settings-form", novalidate: true, "data-dirty": card.dirty ? "true" : "false",
        on: { submit: saveKind(context, kind, card, kind.page_size_adjustable) } }, [
        forms.field("interval-" + kind.kind, t("field.poll_interval"),
          forms.text("interval-" + kind.kind, card.draft, "interval_seconds", changed,
            { type: "number", min: 1, step: 1, error: errorFor("interval_seconds") }),
          errorFor("interval_seconds"), format.duration(Number(card.draft.interval_seconds) || 0)),
        kind.page_size_adjustable ? forms.field("page-size-" + kind.kind, t("field.page_size"),
          forms.text("page-size-" + kind.kind, card.draft, "page_size", changed,
            { type: "number", min: 1, step: 1, error: errorFor("page_size") }), errorFor("page_size")) : null,
        h("h3", {}, t("collection.group.implementations")),
        implementationFields(context, kind, card, changed),
        card.refusal && !card.refusal.field
          ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(card.refusal.condition)) : null,
        h("div", { "class": "form-actions" }, [
          forms.button("save", t("action.save"), null, { submit: true, primary: true }),
          hasSuggestion ? forms.button("fill", t("action.fill_suggestion"), fill) : null,
          card.saved ? h("span", { "class": "form-notice", role: "status" }, t("notice.collection_saved")) : null
        ])
      ])
    ]);
  }

  function polledKindCard(context, kind) {
    var card = context.state.cards["kind-" + kind.kind];
    var changed = function () { card.dirty = true; card.saved = false; card.refusal = null; context.rerender(); };
    var error = card.refusal && card.refusal.field === "interval_seconds" ? forms.refusal(card.refusal.condition) : null;
    return forms.card("kind-" + kind.kind, tk("collection.kind.", kind.kind), h("form", {
      "class": "settings-form", novalidate: true, "data-dirty": card.dirty ? "true" : "false",
      on: { submit: saveKind(context, kind, card, false) }
    }, [
      forms.field("interval-" + kind.kind, t("field.poll_interval"),
        forms.text("interval-" + kind.kind, card.draft, "interval_seconds", changed,
          { type: "number", min: 1, step: 1, error: error }), error,
        format.duration(Number(card.draft.interval_seconds) || 0)),
      h("h3", {}, t("collection.group.implementations")),
      implementationFields(context, kind, card, changed),
      card.refusal && !card.refusal.field
        ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(card.refusal.condition)) : null,
      h("div", { "class": "form-actions" }, [
        forms.button("save", t("action.save"), null, { submit: true, primary: true }),
        card.saved ? h("span", { "class": "form-notice", role: "status" }, t("notice.collection_saved")) : null
      ])
    ]));
  }

  /* The body of a settings card's save: whole numbers, and the mode's word. */
  function settingsBody(group, card, confirm) {
    var body = {};
    group.settings.forEach(function (setting) {
      var text = card.draft[setting.key];
      body[setting.key] = typeof setting.value === "number" && /^-?\d+$/.test(String(text).trim())
        ? parseInt(String(text).trim(), 10) : text;
    });
    if (confirm !== undefined) { body.confirm = confirm; }
    return body;
  }

  function saveSettings(context, group, card, confirm) {
    card.refusal = null;
    return context.api.post("/api/collection/settings/" + encodeURIComponent(group.card),
      settingsBody(group, card, confirm)).then(function (result) {
      if (result.status === 200) {
        card.dirty = false;
        card.saved = true;
        card.confirmation = null;
        install(context, result.body);
      } else if (result.status === 409 && result.body && result.body.condition === "retention_confirmation_required") {
        card.confirmation = result.body;
      } else {
        card.refusal = result.body || { condition: "internal_error" };
      }
      context.rerender();
    });
  }

  function settingControl(context, group, card, setting, changed, error) {
    var id = "setting-" + setting.key;
    if (typeof setting.value === "number") {
      return forms.text(id, card.draft, setting.key, changed, { type: "number", min: 0, step: 1, error: error });
    }
    // The site-name mode is the one setting that is a word: the modes are the setting's own vocabulary.
    return forms.select(id, card.draft, setting.key, ["full", "no_domains"].map(function (mode) {
      return { value: mode, label: tk("collection.aggregate_mode.", mode) };
    }), changed);
  }

  function settingsCard(context, group) {
    var card = context.state.cards["settings-" + group.card];
    var changed = function () { card.dirty = true; card.saved = false; card.refusal = null; context.rerender(); };
    var confirmation = card.confirmation;
    var fields = group.settings.map(function (setting) {
      var error = card.refusal && card.refusal.field === setting.key ? forms.refusal(card.refusal.condition) : null;
      var hint = typeof setting.value === "number" && /_seconds$/.test(setting.key)
        ? format.duration(Number(card.draft[setting.key]) || 0) : null;
      return forms.field("setting-" + setting.key, tk("collection.setting.", setting.key),
        settingControl(context, group, card, setting, changed, error), error, hint);
    });
    return forms.card("settings-" + group.card, tk("collection.card.", group.card), h("form", {
      "class": "settings-form", novalidate: true, "data-dirty": card.dirty ? "true" : "false",
      on: { submit: function (event) { event.preventDefault(); saveSettings(context, group, card); } }
    }, [
      fields,
      card.refusal && !card.refusal.field
        ? h("p", { "class": "field-error", role: "alert" }, forms.refusal(card.refusal.condition)) : null,
      confirmation ? h("div", { key: "confirm", "class": "confirm-panel", role: "alertdialog",
        "aria-label": t("collection.confirm.title") }, [
        h("p", { "class": "confirm-title" }, t("collection.confirm.title")),
        h("dl", { "class": "facts" }, [
          forms.fact("now", t("collection.label.retention_now"), confirmation.in_force_retention_seconds === 0
            ? t("state.sizing.retention_unlimited") : format.duration(confirmation.in_force_retention_seconds)),
          forms.fact("requested", t("collection.label.retention_requested"),
            format.duration(confirmation.requested_retention_seconds))
        ]),
        h("p", { "class": "confirm-consequence" }, t("notice.retention_confirm")),
        h("div", { "class": "form-actions" }, [
          forms.button("confirm", t("action.confirm_retention"), function () {
            saveSettings(context, group, card, confirmation.requested_retention_seconds);
          }, { destructive: true }),
          forms.button("cancel", t("action.cancel"), function () { card.confirmation = null; context.rerender(); })
        ])
      ]) : null,
      h("div", { "class": "form-actions" }, [
        forms.button("save", t("action.save"), null, { submit: true, primary: true, disabled: !!confirmation }),
        card.saved ? h("span", { "class": "form-notice", role: "status" }, t("notice.settings_saved")) : null
      ])
    ]));
  }

  opnview.views.register({
    id: "collection",
    path: "/collection",
    rail: "installation",
    order: 20,
    title: function () { return t("collection.title"); },
    load: load,
    render: function (context) {
      var model = context.state;
      if (!model.answer) {
        return model.failed ? h("div", { "class": "state state-unavailable" }, h("span", { "class": "state-badge" },
          t("state.read_failed"))) : h("p", { "class": "loading", role: "status" }, t("state.loading"));
      }
      return h("div", { "class": "cards" }, [
        model.answer.kinds.map(function (kind) { return sizedKindCard(context, kind); }),
        model.answer.polled_kinds.map(function (kind) { return polledKindCard(context, kind); }),
        model.answer.settings.map(function (group) { return settingsCard(context, group); })
      ]);
    }
  });
})(this);
