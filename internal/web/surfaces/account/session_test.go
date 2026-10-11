package account_test

import (
	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/account"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSignInSucceedsAndItsFailuresAreIndistinguishable is AC8.
func TestSignInSucceedsAndItsFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()

	// Setup signed this reader in. Sign out so the sign-in path is exercised from
	// nothing.
	signOut := harness.Post(web.PathSignOut, url.Values{})
	webtest.AssertStatus(t, signOut, http.StatusSeeOther)

	t.Run("the right password yields a session", func(t *testing.T) {
		harness.SignIn(login, password)
		if harness.SessionCookie() == "" {
			t.Fatal("signing in set no session cookie")
		}
		answer := harness.Get(web.PathSettings)
		webtest.AssertStatus(t, answer, http.StatusOK)
	})

	t.Run("the two failures disclose the same thing", func(t *testing.T) {
		wrongPassword := webtest.FreshClient(t, harness).Post(web.PathSignIn,
			url.Values{"login": {login}, "password": {password + "x"}})
		unknownLogin := webtest.FreshClient(t, harness).Post(web.PathSignIn,
			url.Values{"login": {"nobody-" + webtest.RandomHex(t, 4)}, "password": {password}})

		wrongStatus, wrongBody := webtest.StatusAndBody(t, wrongPassword)
		unknownStatus, unknownBody := webtest.StatusAndBody(t, unknownLogin)

		if wrongStatus != unknownStatus {
			t.Errorf("a wrong password answered %d and an unknown login %d",
				wrongStatus, unknownStatus)
		}
		if wrongStatus != http.StatusUnauthorized {
			t.Errorf("a refused sign-in answered %d rather than 401", wrongStatus)
		}
		// The bodies differ only in the login put back into the form and in the form
		// token. Strip both and the remainder has to be identical, or the page is
		// telling somebody which logins exist.
		if strippedWrong, strippedUnknown := stripVarying(wrongBody), stripVarying(unknownBody); strippedWrong != strippedUnknown {
			t.Error("the two sign-in failures render differently, so the page discloses whether a login exists")
		}
	})

	t.Run("signing out invalidates the session and a replay is refused", func(t *testing.T) {
		harness.SignIn(login, password)
		token := harness.SessionCookie()
		if token == "" {
			t.Fatal("signing in set no session cookie")
		}
		answer := harness.Post(web.PathSignOut, url.Values{})
		webtest.AssertStatus(t, answer, http.StatusSeeOther)

		// The replay presents the exact token the browser was given, which is what an
		// attacker who copied a cookie has.
		replayed := webtest.FreshClient(t, harness)
		replayed.PresentSession(token)
		refused := replayed.Get(web.PathSettings)
		defer func() { _ = refused.Body.Close() }()
		if refused.StatusCode != http.StatusSeeOther {
			t.Fatalf("a replayed session answered %d rather than being turned away",
				refused.StatusCode)
		}
		if location := refused.Header.Get("Location"); location != web.PathSignIn {
			t.Errorf("a replayed session was sent to %q rather than to sign-in", location)
		}
	})
}

// TestASessionExpiresOnBothBoundsAndIsNeverRenewed is AC9.
//
// BOTH BOUNDS ARE ASSERTED, and each in the presence of the other: the idle window
// with plenty of absolute lifetime left, and the absolute cap with the session touched
// often enough that the idle window never closes. An implementation carrying only one
// of them passes one subtest and fails the other.
func TestASessionExpiresOnBothBoundsAndIsNeverRenewed(t *testing.T) {
	t.Parallel()
	lifetimes := auth.DefaultLifetimes()

	t.Run("the sliding idle window", func(t *testing.T) {
		harness := webtest.New(t)
		login, password := harness.CompleteSetup()
		harness.SignIn(login, password)

		// Just inside the window, repeatedly: the window slides, so the session lives
		// well past one window's worth of wall-clock time.
		for range 4 {
			harness.Clock.Advance(lifetimes.Idle - lifetimes.Idle/4)
			webtest.AssertStatus(t, harness.Get(web.PathSettings), http.StatusOK)
		}

		// Past it: refused, and refused as EXPIRED rather than as never having existed.
		harness.Clock.Advance(lifetimes.Idle)
		answer := harness.Get(web.PathSettings)
		defer func() { _ = answer.Body.Close() }()
		if answer.StatusCode != http.StatusSeeOther {
			t.Fatalf("an idle session answered %d rather than being turned away",
				answer.StatusCode)
		}

		// And it is not renewed by arriving: a second request finds nothing at all.
		if session := harness.SessionCookie(); session != "" {
			if _, err := harness.Server.Sessions().Validate(
				t.Context(), session, harness.Clock.Now()); err == nil {
				t.Error("an expired session still validates, so it was renewed rather than refused")
			}
		}
	})

	t.Run("the absolute cap", func(t *testing.T) {
		harness := webtest.New(t)
		login, password := harness.CompleteSetup()
		harness.SignIn(login, password)

		// Kept warm: a request every half-window, so the idle window never closes and
		// only the cap can end this session.
		step := lifetimes.Idle / 2
		for elapsed := step; elapsed < lifetimes.Absolute; elapsed += step {
			harness.Clock.Advance(step)
			webtest.AssertStatus(t, harness.Get(web.PathSettings), http.StatusOK)
		}

		harness.Clock.Advance(step)
		answer := harness.Get(web.PathSettings)
		defer func() { _ = answer.Body.Close() }()
		if answer.StatusCode != http.StatusSeeOther {
			t.Fatalf("a session past its absolute cap answered %d rather than being turned away",
				answer.StatusCode)
		}
	})
}

// TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest is AC10.
//
// IT ENUMERATES THE REGISTERED ROUTES rather than a list written here, so a route
// added later fails this test rather than shipping open. The membership of each set is
// pinned as well as the behaviour: a new route declared public without a decision
// changes the first set and fails.
func TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()

	// The declared public set, pinned. Changing it is changing this literal, which is
	// the decision being made explicitly.
	expectedPublic := map[string]bool{
		http.MethodGet + " " + web.PathSetup:      true,
		http.MethodPost + " " + web.PathSetup:     true,
		http.MethodGet + " " + web.PathSignIn:     true,
		http.MethodPost + " " + web.PathSignIn:    true,
		http.MethodGet + " " + web.PathRecover:    true,
		http.MethodGet + " " + web.PathStylesheet: true,
		// The JSON equivalents of the setup and sign-in pages (C5 of the backend-completion
		// cycle), public for the same reason the pages are, and the session state that hands a
		// reader with no session the form token those two writes carry.
		http.MethodGet + " " + account.PathAPISession: true,
		http.MethodPost + " " + account.PathAPISetup:  true,
		http.MethodPost + " " + account.PathAPISignIn: true,
	}
	// The routes authorised by possession of the key file rather than by a session: the reset
	// page's form and its JSON equivalent.
	expectedKeyFile := map[string]bool{
		http.MethodPost + " " + web.PathRecover:        true,
		http.MethodPost + " " + account.PathAPIRecover: true,
	}

	routes := harness.Server.Routes()
	if len(routes) == 0 {
		t.Fatal("the server registered no routes, so this assertion is vacuous")
	}

	seenPublic := map[string]bool{}
	seenKeyFile := map[string]bool{}
	for _, route := range routes {
		name := route.Method + " " + route.Pattern
		switch route.Access {
		case web.AccessPublic:
			seenPublic[name] = true
		case web.AccessKeyFile:
			seenKeyFile[name] = true
		case web.AccessAuthenticated:
			// It must actually refuse. A declared access level that the middleware does
			// not enforce is the failure this half catches.
			webtest.AssertRefusesUnauthenticated(t, harness, route)
		default:
			t.Errorf("%s declares no access level, so nobody decided who may reach it", name)
		}
	}

	webtest.AssertSameSet(t, "the explicitly public routes", expectedPublic, seenPublic)
	webtest.AssertSameSet(t, "the key-file routes", expectedKeyFile, seenKeyFile)

	// And a key-file route refuses an unauthenticated request that does not present
	// the key file, which is what puts it in the second of AC10's two sets.
	anonymous := webtest.FreshClient(t, harness)
	password := webtest.RandomHex(t, 12)
	answer := anonymous.Post(web.PathRecover, url.Values{
		"login": {"operator"}, "password": {password}, "password_again": {password},
	})
	webtest.AssertStatus(t, answer, http.StatusForbidden)
}

// TestEveryMutatingFormCarriesAFormToken is AC11.
func TestEveryMutatingFormCarriesAFormToken(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)

	t.Run("setup without a token", func(t *testing.T) {
		password := webtest.RandomHex(t, 12)
		// The page is loaded first, so the cookie exists and the only thing missing is
		// the field.
		_ = harness.FormToken(web.PathSetup)
		answer := harness.PostRaw(web.PathSetup, url.Values{
			"setup_token": {harness.SetupToken.Token()},
			"login":       {"operator"}, "password": {password}, "password_again": {password},
		})
		webtest.AssertStatus(t, answer, http.StatusForbidden)
		webtest.AssertNoAccount(t, harness)
		if harness.SetupToken.Spent() {
			t.Error("a submission refused for its form token still spent the setup token")
		}
	})

	login, password := harness.CompleteSetup()

	t.Run("settings without a token", func(t *testing.T) {
		answer := harness.PostRaw(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
			"theme": {string(web.ThemeDark)}})
		webtest.AssertStatus(t, answer, http.StatusForbidden)
	})

	t.Run("settings with another session's token", func(t *testing.T) {
		// A second browser, signed in as the same account: a real second session with a
		// real form token of its own.
		other := webtest.FreshClient(t, harness)
		other.SignIn(login, password)
		otherToken := other.FormToken(web.PathSettings)

		mine := harness.FormToken(web.PathSettings)
		if otherToken == mine {
			t.Fatal("the two sessions were issued the same form token, so this proves nothing")
		}

		answer := harness.PostRaw(web.PathSettings, url.Values{
			"csrf_token":              {otherToken},
			webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
			"theme":                   {string(web.ThemeDark)},
		})
		webtest.AssertStatus(t, answer, http.StatusForbidden)

		// And nothing was stored: the refusal happened before the handler ran.
		if theme := harness.Server.ReadTheme(t.Context()); theme != web.ThemeSystem {
			t.Errorf("a refused submission stored the theme %q", theme)
		}
	})

	t.Run("settings with its own token", func(t *testing.T) {
		answer := harness.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
			"theme": {string(web.ThemeDark)}})
		webtest.AssertStatus(t, answer, http.StatusSeeOther)
		if theme := harness.Server.ReadTheme(t.Context()); theme != web.ThemeDark {
			t.Errorf("an accepted submission stored the theme %q rather than dark", theme)
		}
	})
}

// stripVarying removes the two things two renderings of one page legitimately differ
// in: the form token, and the login put back into the field. What is left has to be
// identical, or the page is saying something about whether a login exists.
func stripVarying(body string) string {
	for _, marker := range []string{`name="csrf_token"`, `name="login"`} {
		for {
			start := strings.Index(body, marker)
			if start < 0 {
				break
			}
			end := strings.Index(body[start:], ">")
			if end < 0 {
				break
			}
			body = body[:start] + body[start+end:]
		}
	}
	return body
}
