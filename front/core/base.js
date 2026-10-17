/*
 * opnview front -- the namespace and the small helpers every other file uses.
 *
 * EVERY FRONT FILE IS A CLASSIC SCRIPT. No import, no export, no module syntax: the same file runs
 * unchanged in the browser, loaded by the shell's <script> elements, and in the interpreter the Go
 * tests run the front in (internal/front/frontjs). Each file is one function called with the
 * global object, and hangs what it defines on the one global the front owns, `opnview`.
 *
 * NO USER-VISIBLE STRING IS A LITERAL in any front file: every word a reader sees comes from the
 * string catalogue through opnview.strings (strings.js); names, addresses and figures are data.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var util = {};

  /* Whether an object has a member of its own. */
  util.has = function (object, key) {
    return object !== null && object !== undefined && Object.prototype.hasOwnProperty.call(object, key);
  };

  /* The members of an object, sorted. */
  util.keys = function (object) {
    return Object.keys(object || {}).sort();
  };

  /* A shallow copy of an object. */
  util.copy = function (object) {
    var copied = {};
    Object.keys(object || {}).forEach(function (key) { copied[key] = object[key]; });
    return copied;
  };

  /* Whether a value is a plain object (not an array, not null). */
  util.isObject = function (value) {
    return value !== null && typeof value === "object" && !Array.isArray(value);
  };

  /* The value at a path of members, or undefined. */
  util.get = function (object, path) {
    var current = object;
    for (var i = 0; i < path.length; i++) {
      if (current === null || current === undefined) { return undefined; }
      current = current[path[i]];
    }
    return current;
  };

  /* A path of the form /a/{b}/c filled with values, each encoded as one path segment. */
  util.path = function (pattern, values) {
    return pattern.replace(/\{([a-z_]+)\}/g, function (_, name) {
      return encodeURIComponent(String(values[name]));
    });
  };

  /* Storage of one browser: the dock's width and the syntax radio, and nothing else (A5). Every
   * read and write is guarded: a private window, a blocked site storage or a test without one must
   * leave the page working. */
  util.storage = {
    get: function (key) {
      try { return root.localStorage ? root.localStorage.getItem(key) : null; } catch (e) { return null; }
    },
    set: function (key, value) {
      try { if (root.localStorage) { root.localStorage.setItem(key, value); } } catch (e) { /* refused */ }
    }
  };

  opnview.util = util;

  /* The registry of views: each view file registers itself, so the shell holds no list of them. */
  var views = [];
  opnview.views = {
    register: function (view) { views.push(view); },
    all: function () { return views.slice(); }
  };
})(this);
