/*
 * opnview front -- the string catalogue.
 *
 * A KEY, NEVER A WORD. A view asks for a key and the catalogue answers the word in the active
 * language, then in English, the source language, and then the key itself marked as missing
 * (A4). The catalogues come from GET /api/strings/{language}, which merges the core's, the
 * surfaces', the plugins', the widgets' and the language file's strings on the server.
 *
 * FOUR WAYS TO ASK, so a test can read which keys a front file uses :
 *
 *   t("a.key", {name: value})    a literal key, its placeholders {name} filled;
 *   tp("a.key", count, {...})    a literal key with a plural form, a.key.one or a.key.other by the
 *                                active language's plural rule, {count} filled;
 *   tk("a.prefix.", part)        a key composed from a literal prefix and a value of the data --
 *                                a kind, a state, a condition -- so every key under the prefix the
 *                                data can reach must exist;
 *   td(key)                      a key the data itself carries -- a setting's label, a widget's
 *                                title_key -- which its owner's catalogue file holds.
 *
 * Placeholder values are data or formatted figures, inserted as text: nothing is ever parsed as
 * markup.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var MISSING = "⁇ ";

  var state = {
    language: "en",
    active: {},
    english: {},
    pseudo: false,
    missing: {}
  };

  function wrap(text) {
    return state.pseudo ? "⟦" + text + "⟧" : text;
  }

  function lookup(key) {
    if (Object.prototype.hasOwnProperty.call(state.active, key)) { return state.active[key]; }
    if (Object.prototype.hasOwnProperty.call(state.english, key)) { return state.english[key]; }
    return null;
  }

  function fill(text, params) {
    if (!params) { return text; }
    return text.replace(/\{([a-z_]+)\}/g, function (whole, name) {
      return Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : whole;
    });
  }

  function resolve(key, params) {
    var found = lookup(key);
    if (found === null) {
      state.missing[key] = true;
      return MISSING + key;
    }
    return wrap(fill(found, params));
  }

  /* The plural category of a count in the active language. */
  function pluralCategory(count) {
    if (typeof Intl !== "undefined" && Intl.PluralRules) {
      try { return new Intl.PluralRules(state.language).select(count); } catch (e) { /* fall through */ }
    }
    return count === 1 ? "one" : "other";
  }

  var strings = {
    t: function (key, params) { return resolve(key, params); },
    tp: function (key, count, params) {
      var merged = opnview.util.copy(params);
      if (!Object.prototype.hasOwnProperty.call(merged, "count")) {
        merged.count = opnview.format ? opnview.format.number(count) : String(count);
      }
      var category = pluralCategory(count);
      var specific = key + "." + category;
      return lookup(specific) !== null ? resolve(specific, merged) : resolve(key + ".other", merged);
    },
    tk: function (prefix, part, params) { return resolve(prefix + part, params); },
    td: function (key, params) { return resolve(key, params); },
    /* Whether a key resolves at all, for a view that names a state only when a word exists for it. */
    has: function (key) { return lookup(key) !== null; },
    language: function () { return state.language; },
    /* Installs the catalogues: the active language's and English's. */
    install: function (language, active, english) {
      state.language = language;
      state.active = active || {};
      state.english = english || {};
    },
    /* The keys asked for that no catalogue holds, for a test. */
    missing: function () { return Object.keys(state.missing).sort(); },
    /* The pseudo-language a test draws every view in: each resolved string wrapped in markers. */
    setPseudo: function (on) { state.pseudo = !!on; },
    /* Fetches and installs a language and English through the API. */
    load: function (language) {
      var api = opnview.api;
      var english = api.get("/api/strings/en");
      var active = language === "en" ? english : api.get("/api/strings/" + encodeURIComponent(language));
      return Promise.all([active, english]).then(function (answers) {
        var activeAnswer = answers[0], englishAnswer = answers[1];
        var usable = activeAnswer.status === 200 ? language : "en";
        strings.install(usable, activeAnswer.status === 200 ? activeAnswer.body.strings : englishAnswer.body.strings,
          englishAnswer.status === 200 ? englishAnswer.body.strings : {});
        return usable;
      });
    }
  };

  opnview.strings = strings;
})(this);
