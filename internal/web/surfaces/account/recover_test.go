package account_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/web/surfaces/account"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
)

// pathSettingsAnswer is an authenticated read a session is tested against.
const pathSettingsAnswer = "/api/settings"

// postReset issues a reset carrying this reader's own form token.
func postReset(t *testing.T, h *webtest.Harness, fields map[string]string) *http.Response {
	t.Helper()
	return h.PostWithToken(account.PathAPIRecover, setupBody(t, fields))
}

// TestAForgottenPasswordIsRecoverableByTheKeyFileAndByNothingElse is AC15.
func TestAForgottenPasswordIsRecoverableByTheKeyFileAndByNothingElse(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, oldPassword := harness.CompleteSetup()
	newPassword := webtest.RandomHex(t, 12)

	keyFile, err := os.ReadFile(secret.KeyPath(harness.DataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}

	t.Run("without the key file it is refused", func(t *testing.T) {
		answer := postReset(t, webtest.FreshClient(t, harness), map[string]string{
			"login": login, "password": newPassword, "password_again": newPassword,
		})
		accounttest.AssertStatus(t, answer, http.StatusForbidden)
		assertPasswordIs(t, harness, login, oldPassword)
	})

	t.Run("with the wrong key file it is refused", func(t *testing.T) {
		// A well-formed key of the right length that is simply a different key. This is
		// the case a length check or a "not empty" check would let through.
		other := t.TempDir()
		if _, err := secret.Create(other); err != nil {
			t.Fatalf("generating another key file: %v", err)
		}
		wrong, err := os.ReadFile(secret.KeyPath(other))
		if err != nil {
			t.Fatalf("reading the other key file: %v", err)
		}
		answer := postReset(t, webtest.FreshClient(t, harness), map[string]string{
			"login": login, "key_file": string(wrong),
			"password": newPassword, "password_again": newPassword,
		})
		accounttest.AssertStatus(t, answer, http.StatusForbidden)
		assertPasswordIs(t, harness, login, oldPassword)
	})

	t.Run("with the key file it succeeds", func(t *testing.T) {
		answer := postReset(t, webtest.FreshClient(t, harness), map[string]string{
			"login": login, "key_file": string(keyFile),
			"password": newPassword, "password_again": newPassword,
		})
		accounttest.AssertStatus(t, answer, http.StatusOK)
		assertPasswordIs(t, harness, login, newPassword)

		// And the new password actually signs in, which the stored record alone does
		// not prove.
		signingIn := webtest.FreshClient(t, harness)
		signingIn.SignIn(login, newPassword)
	})

	t.Run("the reset destroys the sessions the account had", func(t *testing.T) {
		signedIn := webtest.FreshClient(t, harness)
		signedIn.SignIn(login, newPassword)
		if signedIn.SessionCookie() == "" {
			t.Fatal("signing in set no session cookie")
		}
		accounttest.AssertStatus(t, signedIn.Get(pathSettingsAnswer), http.StatusOK)

		later := webtest.RandomHex(t, 12)
		answer := postReset(t, webtest.FreshClient(t, harness), map[string]string{
			"login": login, "key_file": string(keyFile),
			"password": later, "password_again": later,
		})
		accounttest.AssertStatus(t, answer, http.StatusOK)

		stale := signedIn.Get(pathSettingsAnswer)
		defer func() { _ = stale.Body.Close() }()
		if stale.StatusCode != http.StatusUnauthorized {
			t.Errorf("a session issued before a password reset answered %d rather than "+
				"being turned away", stale.StatusCode)
		}
	})
}

// TestTheResetIsRefusedWhenThereIsNoKeyFileAtAll is the other half of "by nothing
// else": an installation with no key to possess authorises nothing.
func TestTheResetIsRefusedWhenThereIsNoKeyFileAtAll(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()
	keyFile, err := os.ReadFile(secret.KeyPath(harness.DataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}

	withoutKey := webtest.ReopenWithoutKeyFile(t, harness)
	replacement := webtest.RandomHex(t, 12)
	answer := postReset(t, withoutKey, map[string]string{
		"login": login, "key_file": string(keyFile),
		"password": replacement, "password_again": replacement,
	})
	accounttest.AssertStatus(t, answer, http.StatusForbidden)
	assertPasswordIs(t, withoutKey, login, password)
}

// assertPasswordIs fails unless the stored record verifies against expected and
// nothing else.
func assertPasswordIs(t *testing.T, harness *webtest.Harness, login, expected string) {
	t.Helper()
	stored, found, err := harness.Store.AccountByLogin(t.Context(), login)
	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	if !found {
		t.Fatalf("there is no account called %q", login)
	}
	if err := auth.VerifyPassword(stored.Password, expected); err != nil {
		t.Errorf("the stored password is not the expected one: %v", err)
	}
}
