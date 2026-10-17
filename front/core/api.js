/*
 * opnview front -- the one way the front talks to the server.
 *
 * EVERY REQUEST GOES TO THIS PAGE'S OWN ORIGIN, by a path beginning with /api/: there is no other
 * host in the front, and the page's policy (connect-src 'self') refuses one anyway.
 *
 * A WRITE IS A JSON POST CARRYING THE FORM TOKEN in X-CSRF-Token (docs/widget-api.md, "Write
 * endpoints"). The token is the one GET /api/session answers -- the session's own once signed in,
 * the browser's own cookie's before -- and a sign-in or a setup answers the next one. A write
 * refused csrf_refused because the token went stale is asked again once with a fresh token.
 *
 * A 401 FROM ANY ROUTE MEANS THE SESSION IS GONE: the shell is told (onUnauthorized), sends the
 * reader to sign in, and back to the route they were on afterwards.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var state = { token: "", onUnauthorized: null };

  /* A body already written as JSON text. */
  function Raw(text) { this.text = text; }

  function answer(response) {
    return response.text().then(function (text) {
      var body = null;
      if (text) {
        try { body = JSON.parse(text); } catch (e) { body = null; }
      }
      // The text as the server sent it, beside its parse: a stored document is kept as its bytes.
      return { status: response.status, body: body, text: text };
    });
  }

  function guarded(result) {
    if (result.status === 401 && state.onUnauthorized) { state.onUnauthorized(result); }
    return result;
  }

  /* body is an object, written as JSON, or a body api.raw made, already written: a stored document
   * is sent back as the bytes it was read as, so an undo restores it byte for byte. */
  function send(method, path, body) {
    if (path.indexOf("/api/") !== 0) {
      return Promise.reject(new Error("api: only this server's /api/ routes are reached"));
    }
    var init = { method: method, credentials: "same-origin", headers: { "Accept": "application/json" } };
    if (method === "POST") {
      init.headers["Content-Type"] = "application/json";
      init.headers["X-CSRF-Token"] = state.token;
      init.body = body instanceof Raw ? body.text : JSON.stringify(body === undefined ? {} : body);
    }
    return root.fetch(path, init).then(answer);
  }

  var api = {
    /* A read: {status, body}. */
    get: function (path) {
      return send("GET", path).then(guarded);
    },
    /* A write: {status, body}. A stale token is fetched again and the write sent once more. */
    post: function (path, body) {
      return send("POST", path, body).then(function (result) {
        if (result.status === 403 && result.body && result.body.condition === "csrf_refused") {
          return api.session().then(function () { return send("POST", path, body); });
        }
        return result;
      }).then(guarded);
    },
    /* The session state, and the form token it carries. */
    session: function () {
      return send("GET", "/api/session").then(function (result) {
        if (result.status === 200 && result.body) { state.token = result.body.csrf_token || ""; }
        return result;
      });
    },
    /* A body already written as JSON text, sent as it is. */
    raw: function (text) { return new Raw(text); },
    /* Keeps the form token a setup or a sign-in answered. */
    useToken: function (token) { state.token = token || ""; },
    onUnauthorized: function (handler) { state.onUnauthorized = handler; }
  };

  opnview.api = api;
})(this);
