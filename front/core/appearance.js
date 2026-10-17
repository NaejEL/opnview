/*
 * opnview front -- the appearance: the palette, the mode and the language (A5, decision D9).
 *
 * THE INSTALLATION'S CHOICE PERSISTS ON THE SERVER (GET and POST /api/appearance), so it is the same
 * in every browser that opens the interface. AN ABSENT VALUE IS THE FIRST-LAUNCH BEHAVIOUR, decided
 * here and never written back: the mode follows the operating system's colour-scheme preference --
 * and keeps following it while the page is open -- the palette is the default one, and the language
 * is the first of the browser's languages that is installed, else English.
 *
 * A PALETTE IS DRAWN BY ONE ATTRIBUTE. The document's data-theme is "<palette>-<mode>", and the
 * generated stylesheet (/front/palettes.css) holds every installed theme's two modes keyed by it;
 * no colour is ever set from a script.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var state = {
    palette: null, mode: null, language: null,
    defaultPalette: "", palettes: [], modes: [], languages: []
  };
  var listening = false;

  function systemPrefersDark() {
    try {
      return !!(root.matchMedia && root.matchMedia("(prefers-color-scheme: dark)").matches);
    } catch (e) {
      return false;
    }
  }

  function installed(list, value) {
    return list.some(function (entry) { return (entry.id || entry.code) === value; });
  }

  var appearance = {
    state: function () { return state; },
    /* The palette drawn: the stored one when installed, else the default. */
    palette: function () {
      return state.palette && installed(state.palettes, state.palette) ? state.palette : state.defaultPalette;
    },
    /* The mode chosen: system, light or dark. */
    mode: function () {
      return state.mode && state.modes.indexOf(state.mode) >= 0 ? state.mode : "system";
    },
    /* The mode drawn: light or dark. */
    effectiveMode: function () {
      var mode = appearance.mode();
      if (mode === "system") { return systemPrefersDark() ? "dark" : "light"; }
      return mode;
    },
    /* The language drawn: the stored one when installed, else the browser's first installed, else
     * English. */
    language: function () {
      if (state.language && installed(state.languages, state.language)) { return state.language; }
      var navigator = root.navigator || {};
      var wanted = (navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language])
        .filter(function (code) { return typeof code === "string" && code; });
      for (var i = 0; i < wanted.length; i++) {
        if (installed(state.languages, wanted[i])) { return wanted[i]; }
        var primary = wanted[i].split("-")[0];
        if (installed(state.languages, primary)) { return primary; }
      }
      return "en";
    },
    /* Writes the attribute the generated stylesheet keys on. */
    apply: function () {
      var element = root.document.documentElement;
      element.setAttribute("data-theme", appearance.palette() + "-" + appearance.effectiveMode());
      element.setAttribute("lang", appearance.language());
      if (!listening && root.matchMedia) {
        listening = true;
        try {
          var query = root.matchMedia("(prefers-color-scheme: dark)");
          var follow = function () { if (appearance.mode() === "system") { appearance.apply(); } };
          if (query.addEventListener) { query.addEventListener("change", follow); }
        } catch (e) { /* no media query: the mode follows nothing more than it did */ }
      }
    },
    /* Reads the stored values and what is offered. */
    load: function () {
      return opnview.api.get("/api/appearance").then(function (result) {
        if (result.status === 200 && result.body) {
          state.palette = result.body.palette;
          state.mode = result.body.mode;
          state.language = result.body.language;
          state.defaultPalette = result.body.default_palette;
          state.palettes = result.body.palettes || [];
          state.modes = result.body.modes || [];
          state.languages = result.body.languages || [];
        }
        return state;
      });
    },
    /* Saves a change of one or more values (null clears one) and draws it. */
    save: function (change) {
      return opnview.api.post("/api/appearance", change).then(function (result) {
        if (result.status === 200 && result.body) {
          state.palette = result.body.palette;
          state.mode = result.body.mode;
          state.language = result.body.language;
          appearance.apply();
        }
        return result;
      });
    },
    /* The name of a palette in the active language, else in English. */
    paletteName: function (entry) {
      var language = opnview.strings.language();
      return (entry.name && (entry.name[language] || entry.name.en)) || entry.id;
    }
  };

  opnview.appearance = appearance;
})(this);
