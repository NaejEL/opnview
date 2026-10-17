package account_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/account"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
)

// pathFirewallSave is an authenticated write the form-token rule is tested against.
const pathFirewallSave = "/api/settings/firewall"

// TestSignInSucceedsAndItsFailuresAreIndistinguishable is AC8.
func TestSignInSucceedsAndItsFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()

	// Setup signed this reader in. Sign out so the sign-in path is exercised from
	// nothing.
	accounttest.AssertStatus(t, harness.PostWithToken(account.PathAPISignOut, "{}"), http.StatusOK)

	t.Run("the right password yields a session", func(t *testing.T) {
		harness.SignIn(login, password)
		if harness.SessionCookie() == "" {
			t.Fatal("signing in set no session cookie")
		}
		accounttest.AssertStatus(t, harness.Get(pathSettingsAnswer), http.StatusOK)
	})

	t.Run("the two failures disclose the same thing", func(t *testing.T) {
		wrongPassword := webtest.FreshClient(t, harness)
		unknownLogin := webtest.FreshClient(t, harness)
		wrongStatus, wrongBody := webtest.StatusAndBody(t, wrongPassword.PostWithToken(account.PathAPISignIn,
			setupBody(t, map[string]string{"login": login, "password": password + "x"})))
		unknownStatus, unknownBody := webtest.StatusAndBody(t, unknownLogin.PostWithToken(account.PathAPISignIn,
			setupBody(t, map[string]string{"login": "nobody-" + webtest.RandomHex(t, 4), "password": password})))

		if wrongStatus != unknownStatus {
			t.Errorf("a wrong password answered %d and an unknown login %d", wrongStatus, unknownStatus)
		}
		if wrongStatus != http.StatusUnauthorized {
			t.Errorf("a refused sign-in answered %d rather than 401", wrongStatus)
		}
		// The answers carry no login and no token: they have to be identical, or the route is
		// telling somebody which logins exist.
		if wrongBody != unknownBody {
			t.Errorf("the two sign-in failures answer differently (%q, %q), so the route discloses "+
				"whether a login exists", wrongBody, unknownBody)
		}
	})

	t.Run("signing out invalidates the session and a replay is refused", func(t *testing.T) {
		harness.SignIn(login, password)
		token := harness.SessionCookie()
		if token == "" {
			t.Fatal("signing in set no session cookie")
		}
		accounttest.AssertStatus(t, harness.PostWithToken(account.PathAPISignOut, "{}"), http.StatusOK)

		// The replay presents the exact token the browser was given, which is what an
		// attacker who copied a cookie has.
		replayed := webtest.FreshClient(t, harness)
		replayed.PresentSession(token)
		refused := replayed.Get(pathSettingsAnswer)
		defer func() { _ = refused.Body.Close() }()
		if refused.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a replayed session answered %d rather than being turned away", refused.StatusCode)
		}
	})
}

// assertExpired fails unless an authenticated read is refused as an EXPIRED session, which is a
// different answer from never having had one.
func assertExpired(t *testing.T, harness *webtest.Harness) {
	t.Helper()
	status, body := webtest.StatusAndBody(t, harness.Get(pathSettingsAnswer))
	if status != http.StatusUnauthorized {
		t.Fatalf("an expired session answered %d rather than being turned away", status)
	}
	if want := `"condition":"session_expired"`; !strings.Contains(body, want) {
		t.Errorf("an expired session was answered %s, without %s", body, want)
	}
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
			accounttest.AssertStatus(t, harness.Get(pathSettingsAnswer), http.StatusOK)
		}

		// Past it: refused, and refused as EXPIRED rather than as never having existed.
		harness.Clock.Advance(lifetimes.Idle)
		session := harness.SessionCookie()
		assertExpired(t, harness)

		// And it is not renewed by arriving: a second request finds nothing at all.
		if session != "" {
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
			accounttest.AssertStatus(t, harness.Get(pathSettingsAnswer), http.StatusOK)
		}

		harness.Clock.Advance(step)
		assertExpired(t, harness)
	})
}

// TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest is AC10.
//
// IT ENUMERATES THE REGISTERED ROUTES rather than a list written here, so a route
// added later fails this test rather than shipping open. The membership of each set is
// pinned as well as the behaviour: a new route declared public without a decision
// fails.
func TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()

	// The declared public set, pinned. Changing it is changing this literal, which is
	// the decision being made explicitly: the session state that hands a reader with no
	// session the form token, the setup and the sign-in.
	requiredPublic := map[string]bool{
		http.MethodGet + " " + account.PathAPISession: true,
		http.MethodPost + " " + account.PathAPISetup:  true,
		http.MethodPost + " " + account.PathAPISignIn: true,
	}
	// The front's public routes (specs/SPEC-step-7-frontend.md, A3 to A5), decided public: the
	// shell at every unmatched path, which carries no data; an unknown path under /api/, answered
	// in JSON and carrying no data; the front's assets; its registry; the string catalogue; and the
	// appearance read. They are the frontend surface's, which is built
	// beside this change, so each is accepted where it is mounted and not required here.
	frontPublic := map[string]bool{
		http.MethodGet + " /":                       true,
		http.MethodGet + " /api/":                   true,
		http.MethodGet + " /front/{path...}":        true,
		http.MethodGet + " /api/front":              true,
		http.MethodGet + " /api/strings/{language}": true,
		http.MethodGet + " /api/appearance":         true,
	}
	// The one route authorised by possession of the key file rather than by a session.
	expectedKeyFile := map[string]bool{
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
			if !requiredPublic[name] && !frontPublic[name] {
				t.Errorf("%s is public, and nobody decided it should be", name)
			}
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
	for name := range requiredPublic {
		if !seenPublic[name] {
			t.Errorf("the public routes no longer include %s", name)
		}
	}
	webtest.AssertSameSet(t, "the key-file routes", expectedKeyFile, seenKeyFile)

	// And the key-file route refuses an unauthenticated request that does not present the key
	// file, which is what puts it in the second of AC10's two sets.
	password := webtest.RandomHex(t, 12)
	answer := postReset(t, webtest.FreshClient(t, harness), map[string]string{
		"login": "operator", "password": password, "password_again": password,
	})
	accounttest.AssertStatus(t, answer, http.StatusForbidden)
}

// TestEveryMutatingFormCarriesAFormToken is AC11, for the JSON writes: a write without its token,
// or with another session's, is refused before its handler runs, and stores nothing.
func TestEveryMutatingFormCarriesAFormToken(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)

	t.Run("setup without a token", func(t *testing.T) {
		password := webtest.RandomHex(t, 12)
		// The session state is read first, so the cookie exists and the only thing missing
		// is the header.
		_ = harness.FormToken()
		answer := harness.PostJSON(account.PathAPISetup, setupBody(t, map[string]string{
			"setup_token": harness.SetupToken.Token(),
			"login":       "operator", "password": password, "password_again": password,
		}), "", "")
		accounttest.AssertStatus(t, answer, http.StatusForbidden)
		accounttest.AssertNoAccount(t, harness)
		if harness.SetupToken.Spent() {
			t.Error("a submission refused for its form token still spent the setup token")
		}
	})

	login, password := harness.CompleteSetup()
	firewallURL := harness.Fake.BaseURL()
	saveBody := setupBody(t, map[string]string{"url": firewallURL})

	assertURLNotStored := func(t *testing.T) {
		t.Helper()
		if value, present, err := harness.Store.Setting(t.Context(), config.KeyOPNsenseBaseURL); err != nil {
			t.Fatalf("reading the setting: %v", err)
		} else if present {
			t.Errorf("a refused write stored the firewall URL %q", value)
		}
	}

	t.Run("a write without a token", func(t *testing.T) {
		accounttest.AssertStatus(t, harness.PostJSON(pathFirewallSave, saveBody, "", ""), http.StatusForbidden)
		assertURLNotStored(t)
	})

	t.Run("a write with another session's token", func(t *testing.T) {
		// A second browser, signed in as the same account: a real second session with a
		// real form token of its own.
		other := webtest.FreshClient(t, harness)
		other.SignIn(login, password)
		otherToken := other.FormToken()

		mine := harness.FormToken()
		if otherToken == mine {
			t.Fatal("the two sessions were issued the same form token, so this proves nothing")
		}
		accounttest.AssertStatus(t, harness.PostJSON(pathFirewallSave, saveBody, otherToken, ""),
			http.StatusForbidden)
		// And nothing was stored: the refusal happened before the handler ran.
		assertURLNotStored(t)
	})

	t.Run("a write with its own token", func(t *testing.T) {
		accounttest.AssertStatus(t, harness.PostWithToken(pathFirewallSave, saveBody), http.StatusOK)
		if value, _, err := harness.Store.Setting(t.Context(), config.KeyOPNsenseBaseURL); err != nil ||
			value != firewallURL {
			t.Errorf("an accepted write stored %q (%v) rather than %q", value, err, firewallURL)
		}
	})
}
