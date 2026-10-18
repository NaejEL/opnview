/*
 * opnview front -- the session views: the first account's setup, signing in, and the password reset
 * authorised by the key file (F7-7). They run on the JSON account API: GET /api/session answers the
 * form token a write carries, POST /api/account/setup and POST /api/session/sign-in answer a session
 * and its next token, POST /api/account/recover resets a password. Each refusal is shown against
 * the field it names.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h, forms = opnview.forms;
  var t = opnview.strings.t;

  /* The draft and the refusal of one session form, kept across renders. */
  function model(context, members) {
    if (!context.state.draft) {
      context.state.draft = {};
      members.forEach(function (member) { context.state.draft[member] = ""; });
    }
    return context.state;
  }

  function errorFor(form, member) {
    return form.refusal && form.refusal.field === member ? forms.refusal(form.refusal.condition) : null;
  }

  function generalError(form) {
    return form.refusal && !form.refusal.field
      ? h("p", { key: "general-error", "class": "field-error", role: "alert" }, forms.refusal(form.refusal.condition))
      : null;
  }

  function submit(context, form, path, onSuccess) {
    form.sending = true;
    form.refusal = null;
    context.rerender();
    return context.api.post(path, form.draft).then(function (result) {
      form.sending = false;
      if (result.status === 200) { return onSuccess(result.body); }
      form.refusal = result.body && result.body.condition ? result.body : { condition: "internal_error" };
      context.rerender();
      return null;
    });
  }

  function sessionCard(key, heading, body) {
    return h("section", { key: key, "class": "session-card" }, [h("h2", { "class": "session-heading" }, heading), body]);
  }

  function input(context, form, id, member, type, autocomplete) {
    return forms.text(id, form.draft, member, function () { form.refusal = null; context.rerender(); },
      { type: type, autocomplete: autocomplete, error: errorFor(form, member) });
  }

  opnview.views.register({
    id: "setup",
    path: "/setup",
    public: true,
    available: function (session) { return !session.account_exists; },
    title: function () { return t("setup.title"); },
    heading: false,
    render: function (context) {
      var form = model(context, ["setup_token", "login", "password", "password_again"]);
      return sessionCard("setup", t("setup.heading"), h("form", {
        "class": "session-form", novalidate: true,
        on: { submit: function (event) {
          event.preventDefault();
          submit(context, form, "/api/account/setup", function (body) { return opnview.shell.signedIn(body.csrf_token); });
        } }
      }, [
        forms.field("setup-token", t("field.setup_token"), input(context, form, "setup-token", "setup_token", "text", "off"),
          errorFor(form, "setup_token")),
        forms.field("setup-login", t("field.login"), input(context, form, "setup-login", "login", "text", "username"),
          errorFor(form, "login")),
        forms.field("setup-password", t("field.password"),
          input(context, form, "setup-password", "password", "password", "new-password"), errorFor(form, "password")),
        forms.field("setup-password-again", t("field.password_again"),
          input(context, form, "setup-password-again", "password_again", "password", "new-password"),
          errorFor(form, "password_again")),
        generalError(form),
        h("div", { "class": "form-actions" }, [
          forms.button("create", t("action.create_account"), null, { submit: true, primary: true, disabled: form.sending })
        ])
      ]));
    }
  });

  opnview.views.register({
    id: "sign-in",
    path: "/sign-in",
    public: true,
    available: function (session) { return session.account_exists; },
    title: function () { return t("sign_in.title"); },
    heading: false,
    render: function (context) {
      var form = model(context, ["login", "password"]);
      return sessionCard("sign-in", t("sign_in.heading"), h("form", {
        "class": "session-form", novalidate: true,
        on: { submit: function (event) {
          event.preventDefault();
          submit(context, form, "/api/session/sign-in", function (body) { return opnview.shell.signedIn(body.csrf_token); });
        } }
      }, [
        forms.field("sign-in-login", t("field.login"), input(context, form, "sign-in-login", "login", "text", "username"),
          errorFor(form, "login")),
        forms.field("sign-in-password", t("field.password"),
          input(context, form, "sign-in-password", "password", "password", "current-password"), errorFor(form, "password")),
        generalError(form),
        h("div", { "class": "form-actions" }, [
          forms.button("sign-in", t("action.sign_in"), null, { submit: true, primary: true, disabled: form.sending }),
          h("a", { key: "recover", href: "/recover", "data-route": "/recover", "class": "link-button" }, t("sign_in.recover"))
        ])
      ]));
    }
  });

  opnview.views.register({
    id: "recover",
    path: "/recover",
    public: true,
    available: function (session) { return session.account_exists; },
    title: function () { return t("recover.title"); },
    heading: false,
    render: function (context) {
      var form = model(context, ["key_file", "login", "password", "password_again"]);
      if (form.done) {
        return sessionCard("recover", t("recover.heading"), h("div", { "class": "session-form" }, [
          h("p", { "class": "form-notice", role: "status" }, t("notice.password_reset")),
          h("a", { href: "/sign-in", "data-route": "/sign-in", "class": "btn btn-primary" }, t("action.sign_in"))
        ]));
      }
      return sessionCard("recover", t("recover.heading"), h("form", {
        "class": "session-form", novalidate: true,
        on: { submit: function (event) {
          event.preventDefault();
          submit(context, form, "/api/account/recover", function () {
            form.done = true;
            form.draft = {};
            return opnview.api.session().then(function () { context.rerender(); });
          });
        } }
      }, [
        forms.field("recover-key-file", t("field.key_file"), h("textarea", {
          id: "recover-key-file", name: "key_file", rows: 3, spellcheck: "false", autocomplete: "off",
          "aria-invalid": errorFor(form, "key_file") ? "true" : null,
          value: form.draft.key_file,
          on: { input: function (event) { form.draft.key_file = event.target.value; form.refusal = null; context.rerender(); } }
        }), errorFor(form, "key_file")),
        forms.field("recover-login", t("field.login"), input(context, form, "recover-login", "login", "text", "username"),
          errorFor(form, "login")),
        forms.field("recover-password", t("field.new_password"),
          input(context, form, "recover-password", "password", "password", "new-password"), errorFor(form, "password")),
        forms.field("recover-password-again", t("field.password_again"),
          input(context, form, "recover-password-again", "password_again", "password", "new-password"),
          errorFor(form, "password_again")),
        generalError(form),
        h("div", { "class": "form-actions" }, [
          forms.button("reset", t("action.reset_password"), null, { submit: true, primary: true, disabled: form.sending }),
          h("a", { key: "sign-in", href: "/sign-in", "data-route": "/sign-in", "class": "link-button" }, t("action.sign_in"))
        ])
      ]));
    }
  });
})(this);
