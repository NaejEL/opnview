package web

import (
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/secret"
)

// TestAForgottenPasswordIsRecoverableByTheKeyFileAndByNothingElse is AC15.
func TestAForgottenPasswordIsRecoverableByTheKeyFileAndByNothingElse(t *testing.T) {
	harness := newHarness(t)
	login, oldPassword := harness.completeSetup()
	newPassword := randomHex(t, 12)

	keyFile, err := os.ReadFile(secret.KeyPath(harness.dataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}

	t.Run("without the key file it is refused", func(t *testing.T) {
		anonymous := freshClient(t, harness)
		answer := anonymous.post(PathRecover, url.Values{
			"login": {login}, "password": {newPassword}, "password_again": {newPassword},
		})
		assertStatus(t, answer, http.StatusForbidden)
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
		anonymous := freshClient(t, harness)
		answer := anonymous.post(PathRecover, url.Values{
			"login": {login}, "key_file": {string(wrong)},
			"password": {newPassword}, "password_again": {newPassword},
		})
		assertStatus(t, answer, http.StatusForbidden)
		assertPasswordIs(t, harness, login, oldPassword)
	})

	t.Run("with the key file it succeeds", func(t *testing.T) {
		anonymous := freshClient(t, harness)
		answer := anonymous.post(PathRecover, url.Values{
			"login": {login}, "key_file": {string(keyFile)},
			"password": {newPassword}, "password_again": {newPassword},
		})
		assertStatus(t, answer, http.StatusSeeOther)
		assertPasswordIs(t, harness, login, newPassword)

		// And the new password actually signs in, which the stored record alone does
		// not prove.
		signingIn := freshClient(t, harness)
		signingIn.signIn(login, newPassword)
	})

	t.Run("the reset destroys the sessions the account had", func(t *testing.T) {
		signedIn := freshClient(t, harness)
		signedIn.signIn(login, newPassword)
		if signedIn.sessionCookie() == "" {
			t.Fatal("signing in set no session cookie")
		}
		assertStatus(t, signedIn.get(PathSettings), http.StatusOK)

		later := randomHex(t, 12)
		resetter := freshClient(t, harness)
		answer := resetter.post(PathRecover, url.Values{
			"login": {login}, "key_file": {string(keyFile)},
			"password": {later}, "password_again": {later},
		})
		assertStatus(t, answer, http.StatusSeeOther)

		stale := signedIn.get(PathSettings)
		defer func() { _ = stale.Body.Close() }()
		if stale.StatusCode != http.StatusSeeOther {
			t.Errorf("a session issued before a password reset answered %d rather than "+
				"being turned away", stale.StatusCode)
		}
	})
}

// TestTheResetIsRefusedWhenThereIsNoKeyFileAtAll is the other half of "by nothing
// else": an installation with no key to possess authorises nothing.
func TestTheResetIsRefusedWhenThereIsNoKeyFileAtAll(t *testing.T) {
	harness := newHarness(t)
	login, password := harness.completeSetup()
	keyFile, err := os.ReadFile(secret.KeyPath(harness.dataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}

	withoutKey := reopenWithoutKeyFile(t, harness)
	replacement := randomHex(t, 12)
	answer := withoutKey.post(PathRecover, url.Values{
		"login": {login}, "key_file": {string(keyFile)},
		"password": {replacement}, "password_again": {replacement},
	})
	assertStatus(t, answer, http.StatusForbidden)
	assertPasswordIs(t, withoutKey, login, password)
}

// assertPasswordIs fails unless the stored record verifies against expected and
// nothing else.
func assertPasswordIs(t *testing.T, harness *harness, login, expected string) {
	t.Helper()
	account, found, err := harness.store.AccountByLogin(t.Context(), login)
	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	if !found {
		t.Fatalf("there is no account called %q", login)
	}
	if err := auth.VerifyPassword(account.Password, expected); err != nil {
		t.Errorf("the stored password is not the expected one: %v", err)
	}
}
