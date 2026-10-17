package account_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/account"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The account surface's JSON routes (C-AC9, C-AC4): each refusal with its code, under the access
// level and the CSRF rule of the route, and the change each makes.

// sendAnonymous posts a JSON body with the given proof and answers its status and condition.
func sendAnonymous(t *testing.T, h *webtest.Harness, path string, body map[string]any, token string) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	answer := h.PostJSON(path, string(encoded), token, "")
	defer func() { _ = answer.Body.Close() }()
	return answer.StatusCode, settingstest.DecodeJSON(t, webtest.ReadBody(t, answer))
}

// TestTheJSONSetupRefusesItsInputsAndCreatesTheAccount is C-AC9 for the setup: each
// refusal is answered with its code and creates nothing.
func TestTheJSONSetupRefusesItsInputsAndCreatesTheAccount(t *testing.T) {
	t.Parallel()
	password := webtest.RandomHex(t, 12)
	for _, refused := range []struct {
		what      string
		fields    map[string]string
		status    int
		condition string
	}{
		{"a wrong setup token", map[string]string{"setup_token": "wrong", "login": "operator",
			"password": password, "password_again": password}, http.StatusForbidden, "setup_token_refused"},
		{"no login", map[string]string{"login": "", "password": password, "password_again": password},
			http.StatusBadRequest, "login_required"},
		{"no password", map[string]string{"login": "operator", "password": "", "password_again": ""},
			http.StatusBadRequest, "password_required"},
		{"a short password", map[string]string{"login": "operator", "password": "short", "password_again": "short"},
			http.StatusBadRequest, "password_too_short"},
		{"two different passwords", map[string]string{"login": "operator", "password": password,
			"password_again": password + "x"}, http.StatusBadRequest, "password_mismatch"},
	} {
		t.Run(refused.what, func(t *testing.T) {
			h := webtest.New(t)
			body := map[string]any{}
			for key, value := range refused.fields {
				body[key] = value
			}
			if _, set := refused.fields["setup_token"]; !set {
				body["setup_token"] = h.SetupToken.Token()
			}
			status, answer := sendAnonymous(t, h, account.PathAPISetup, body, settingstest.APIToken(h))
			if status != refused.status || answer["condition"] != refused.condition {
				t.Errorf("the JSON setup answered %d %v rather than %d %s", status, answer, refused.status,
					refused.condition)
			}
			accounttest.AssertNoAccount(t, h)
		})
	}

	t.Run("without the form token it is refused before anything is read", func(t *testing.T) {
		h := webtest.New(t)
		status, answer := sendAnonymous(t, h, account.PathAPISetup, map[string]any{
			"setup_token": h.SetupToken.Token(), "login": "operator", "password": password,
			"password_again": password}, "")
		if status != http.StatusForbidden || answer["condition"] != web.ConditionCSRFRefused {
			t.Errorf("answered %d %v rather than 403 csrf_refused", status, answer)
		}
		accounttest.AssertNoAccount(t, h)
		if h.SetupToken.Spent() {
			t.Error("a setup refused for its form token spent the setup token")
		}
	})

	t.Run("a valid setup creates the account, signs in and closes the setup", func(t *testing.T) {
		h := webtest.New(t)
		if state := settingstest.GetJSON(h, account.PathAPISession); state["account_exists"] != false || state["signed_in"] != false {
			t.Errorf("a fresh installation's session state is %v", state)
		}
		login := "operator-" + webtest.RandomHex(t, 4)
		status, answer := sendAnonymous(t, h, account.PathAPISetup, map[string]any{
			"setup_token": h.SetupToken.Token(), "login": login, "password": password,
			"password_again": password}, settingstest.APIToken(h))
		if status != http.StatusOK || answer["signed_in"] != true || answer["csrf_token"] == "" {
			t.Fatalf("the setup answered %d %v", status, answer)
		}
		if _, found, err := h.Store.AccountByLogin(t.Context(), login); err != nil || !found {
			t.Fatalf("the account was not created (%v)", err)
		}
		if state := settingstest.GetJSON(h, account.PathAPISession); state["account_exists"] != true || state["signed_in"] != true {
			t.Errorf("after the setup the session state is %v", state)
		}
		status, answer = sendAnonymous(t, h, account.PathAPISetup, map[string]any{
			"setup_token": h.SetupToken.Token(), "login": "second", "password": password,
			"password_again": password}, settingstest.APIToken(h))
		if status != http.StatusForbidden || answer["condition"] != web.ConditionSetupClosed {
			t.Errorf("a second setup answered %d %v rather than 403 setup_closed", status, answer)
		}
	})
}

// TestTheJSONSignInAndSignOut is C-AC9 for the sign-in and the sign-out: the same
// indistinguishable refusal, a session issued, and its row deleted on sign-out.
func TestTheJSONSignInAndSignOut(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	login, password := h.CompleteSetup()
	anonymous := webtest.FreshClient(t, h)
	token := settingstest.APIToken(anonymous)
	for _, wrong := range []map[string]any{
		{"login": login, "password": password + "x"},
		{"login": "nobody-" + webtest.RandomHex(t, 4), "password": password},
	} {
		status, answer := sendAnonymous(t, anonymous, account.PathAPISignIn, wrong, token)
		if status != http.StatusUnauthorized || answer["condition"] != "sign_in_refused" {
			t.Errorf("a wrong sign-in answered %d %v rather than 401 sign_in_refused", status, answer)
		}
	}
	status, answer := sendAnonymous(t, anonymous, account.PathAPISignIn,
		map[string]any{"login": login, "password": password}, token)
	if status != http.StatusOK || answer["signed_in"] != true {
		t.Fatalf("the sign-in answered %d %v", status, answer)
	}
	if anonymous.SessionCookie() == "" {
		t.Fatal("the sign-in set no session cookie")
	}
	settings := anonymous.Get("/api/settings")
	webtest.Discard(t, settings)
	if settings.StatusCode != http.StatusOK {
		t.Fatalf("the session the JSON sign-in issued is not accepted (%d)", settings.StatusCode)
	}
	cookie := anonymous.SessionCookie()
	status, answer = settingstest.SendJSON(anonymous, account.PathAPISignOut, map[string]any{})
	if status != http.StatusOK || answer["signed_in"] != false {
		t.Fatalf("the sign-out answered %d %v", status, answer)
	}
	replay := webtest.FreshClient(t, h)
	replay.PresentSession(cookie)
	after := replay.Get("/api/settings")
	webtest.Discard(t, after)
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("the signed-out session still answers %d", after.StatusCode)
	}
}

// TestTheJSONResetIsAuthorisedByTheKeyFileAndByNothingElse is C-AC9 and C-AC4 for the reset.
func TestTheJSONResetIsAuthorisedByTheKeyFileAndByNothingElse(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	login, oldPassword := h.CompleteSetup()
	newPassword := webtest.RandomHex(t, 12)
	keyFile, err := os.ReadFile(secret.KeyPath(h.DataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	anonymous := webtest.FreshClient(t, h)
	token := settingstest.APIToken(anonymous)
	reset := func(fields map[string]any) (int, map[string]any) {
		return sendAnonymous(t, anonymous, account.PathAPIRecover, fields, token)
	}
	if status, answer := reset(map[string]any{"login": login, "password": newPassword,
		"password_again": newPassword}); status != http.StatusForbidden || answer["condition"] != web.ConditionKeyFileRefused {
		t.Errorf("a reset without the key file answered %d %v", status, answer)
	}
	if status, answer := reset(map[string]any{"key_file": "not the key", "login": login, "password": newPassword,
		"password_again": newPassword}); status != http.StatusForbidden || answer["condition"] != web.ConditionKeyFileRefused {
		t.Errorf("a reset with a wrong key file answered %d %v", status, answer)
	}
	if status, answer := reset(map[string]any{"key_file": string(keyFile), "login": "nobody", "password": newPassword,
		"password_again": newPassword}); status != http.StatusBadRequest || answer["condition"] != "key_file_refused" {
		t.Errorf("a reset of a login that does not exist answered %d %v", status, answer)
	}
	if status, answer := reset(map[string]any{"key_file": string(keyFile), "login": login, "password": "short",
		"password_again": "short"}); status != http.StatusBadRequest || answer["condition"] != "password_too_short" {
		t.Errorf("a reset to a short password answered %d %v", status, answer)
	}
	assertPasswordIs(t, h, login, oldPassword)
	if status, answer := reset(map[string]any{"key_file": string(keyFile), "login": login, "password": newPassword,
		"password_again": newPassword}); status != http.StatusOK || answer["signed_in"] != false {
		t.Fatalf("a reset with the key file answered %d %v", status, answer)
	}
	assertPasswordIs(t, h, login, newPassword)
}
