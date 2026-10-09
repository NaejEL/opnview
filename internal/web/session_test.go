package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
)

// TestSignInSucceedsAndItsFailuresAreIndistinguishable is AC8.
func TestSignInSucceedsAndItsFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()

	// Setup signed this reader in. Sign out so the sign-in path is exercised from
	// nothing.
	signOut := harness.post(PathSignOut, url.Values{})
	assertStatus(t, signOut, http.StatusSeeOther)

	t.Run("the right password yields a session", func(t *testing.T) {
		harness.signIn(login, password)
		if harness.sessionCookie() == "" {
			t.Fatal("signing in set no session cookie")
		}
		answer := harness.get(PathSettings)
		assertStatus(t, answer, http.StatusOK)
	})

	t.Run("the two failures disclose the same thing", func(t *testing.T) {
		wrongPassword := freshClient(t, harness).post(PathSignIn,
			url.Values{"login": {login}, "password": {password + "x"}})
		unknownLogin := freshClient(t, harness).post(PathSignIn,
			url.Values{"login": {"nobody-" + randomHex(t, 4)}, "password": {password}})

		wrongStatus, wrongBody := statusAndBody(t, wrongPassword)
		unknownStatus, unknownBody := statusAndBody(t, unknownLogin)

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
		harness.signIn(login, password)
		token := harness.sessionCookie()
		if token == "" {
			t.Fatal("signing in set no session cookie")
		}
		answer := harness.post(PathSignOut, url.Values{})
		assertStatus(t, answer, http.StatusSeeOther)

		// The replay presents the exact token the browser was given, which is what an
		// attacker who copied a cookie has.
		replayed := freshClient(t, harness)
		replayed.presentSession(token)
		refused := replayed.get(PathSettings)
		defer func() { _ = refused.Body.Close() }()
		if refused.StatusCode != http.StatusSeeOther {
			t.Fatalf("a replayed session answered %d rather than being turned away",
				refused.StatusCode)
		}
		if location := refused.Header.Get("Location"); location != PathSignIn {
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
		harness := newHarness(t)
		login, password := harness.completeSetup()
		harness.signIn(login, password)

		// Just inside the window, repeatedly: the window slides, so the session lives
		// well past one window's worth of wall-clock time.
		for range 4 {
			harness.clock.advance(lifetimes.Idle - lifetimes.Idle/4)
			assertStatus(t, harness.get(PathSettings), http.StatusOK)
		}

		// Past it: refused, and refused as EXPIRED rather than as never having existed.
		harness.clock.advance(lifetimes.Idle)
		answer := harness.get(PathSettings)
		defer func() { _ = answer.Body.Close() }()
		if answer.StatusCode != http.StatusSeeOther {
			t.Fatalf("an idle session answered %d rather than being turned away",
				answer.StatusCode)
		}

		// And it is not renewed by arriving: a second request finds nothing at all.
		if session := harness.sessionCookie(); session != "" {
			if _, err := harness.server.sessions.Validate(
				t.Context(), session, harness.clock.Now()); err == nil {
				t.Error("an expired session still validates, so it was renewed rather than refused")
			}
		}
	})

	t.Run("the absolute cap", func(t *testing.T) {
		harness := newHarness(t)
		login, password := harness.completeSetup()
		harness.signIn(login, password)

		// Kept warm: a request every half-window, so the idle window never closes and
		// only the cap can end this session.
		step := lifetimes.Idle / 2
		for elapsed := step; elapsed < lifetimes.Absolute; elapsed += step {
			harness.clock.advance(step)
			assertStatus(t, harness.get(PathSettings), http.StatusOK)
		}

		harness.clock.advance(step)
		answer := harness.get(PathSettings)
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
	harness := newHarness(t)
	harness.completeSetup()

	// The declared public set, pinned. Changing it is changing this literal, which is
	// the decision being made explicitly.
	expectedPublic := map[string]bool{
		http.MethodGet + " " + PathSetup:      true,
		http.MethodPost + " " + PathSetup:     true,
		http.MethodGet + " " + PathSignIn:     true,
		http.MethodPost + " " + PathSignIn:    true,
		http.MethodGet + " " + PathRecover:    true,
		http.MethodGet + " " + PathStylesheet: true,
	}
	// The one route authorised by possession of the key file rather than by a session.
	expectedKeyFile := map[string]bool{
		http.MethodPost + " " + PathRecover: true,
	}

	routes := harness.server.Routes()
	if len(routes) == 0 {
		t.Fatal("the server registered no routes, so this assertion is vacuous")
	}

	seenPublic := map[string]bool{}
	seenKeyFile := map[string]bool{}
	for _, route := range routes {
		name := route.Method + " " + route.Pattern
		switch route.Access {
		case AccessPublic:
			seenPublic[name] = true
		case AccessKeyFile:
			seenKeyFile[name] = true
		case AccessAuthenticated:
			// It must actually refuse. A declared access level that the middleware does
			// not enforce is the failure this half catches.
			assertRefusesUnauthenticated(t, harness, route)
		default:
			t.Errorf("%s declares no access level, so nobody decided who may reach it", name)
		}
	}

	assertSameSet(t, "the explicitly public routes", expectedPublic, seenPublic)
	assertSameSet(t, "the key-file routes", expectedKeyFile, seenKeyFile)

	// And a key-file route refuses an unauthenticated request that does not present
	// the key file, which is what puts it in the second of AC10's two sets.
	anonymous := freshClient(t, harness)
	password := randomHex(t, 12)
	answer := anonymous.post(PathRecover, url.Values{
		"login": {"operator"}, "password": {password}, "password_again": {password},
	})
	assertStatus(t, answer, http.StatusForbidden)
}

// assertRefusesUnauthenticated fails unless the route turns away a request carrying
// no session.
func assertRefusesUnauthenticated(t *testing.T, harness *harness, route Route) {
	t.Helper()
	anonymous := freshClient(t, harness)
	var answer *http.Response
	if route.Method == http.MethodGet {
		answer = anonymous.get(route.Pattern)
	} else {
		// The form token comes from a page this anonymous client can actually load, so
		// the refusal under test is the missing session and not the missing token.
		answer = anonymous.postRaw(route.Pattern, url.Values{
			"csrf_token": {anonymous.formToken(PathSignIn)},
		})
	}
	defer func() { _ = answer.Body.Close() }()
	switch answer.StatusCode {
	case http.StatusSeeOther:
		if location := answer.Header.Get("Location"); location != PathSignIn &&
			location != PathSetup {
			t.Errorf("%s %s sent an unauthenticated reader to %q rather than to a sign-in surface",
				route.Method, route.Pattern, location)
		}
	case http.StatusUnauthorized, http.StatusForbidden:
	default:
		t.Errorf("%s %s answered %d to an unauthenticated request rather than refusing it",
			route.Method, route.Pattern, answer.StatusCode)
	}
}

// TestEveryMutatingFormCarriesAFormToken is AC11.
func TestEveryMutatingFormCarriesAFormToken(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)

	t.Run("setup without a token", func(t *testing.T) {
		password := randomHex(t, 12)
		// The page is loaded first, so the cookie exists and the only thing missing is
		// the field.
		_ = harness.formToken(PathSetup)
		answer := harness.postRaw(PathSetup, url.Values{
			"setup_token": {harness.setupToken.Token()},
			"login":       {"operator"}, "password": {password}, "password_again": {password},
		})
		assertStatus(t, answer, http.StatusForbidden)
		assertNoAccount(t, harness)
		if harness.setupToken.Spent() {
			t.Error("a submission refused for its form token still spent the setup token")
		}
	})

	login, password := harness.completeSetup()

	t.Run("settings without a token", func(t *testing.T) {
		answer := harness.postRaw(PathSettings, url.Values{"theme": {string(ThemeDark)}})
		assertStatus(t, answer, http.StatusForbidden)
	})

	t.Run("settings with another session's token", func(t *testing.T) {
		// A second browser, signed in as the same account: a real second session with a
		// real form token of its own.
		other := freshClient(t, harness)
		other.signIn(login, password)
		otherToken := other.formToken(PathSettings)

		mine := harness.formToken(PathSettings)
		if otherToken == mine {
			t.Fatal("the two sessions were issued the same form token, so this proves nothing")
		}

		answer := harness.postRaw(PathSettings, url.Values{
			"csrf_token": {otherToken},
			"theme":      {string(ThemeDark)},
		})
		assertStatus(t, answer, http.StatusForbidden)

		// And nothing was stored: the refusal happened before the handler ran.
		if theme := harness.server.readTheme(t.Context()); theme != ThemeSystem {
			t.Errorf("a refused submission stored the theme %q", theme)
		}
	})

	t.Run("settings with its own token", func(t *testing.T) {
		answer := harness.post(PathSettings, url.Values{"theme": {string(ThemeDark)}})
		assertStatus(t, answer, http.StatusOK)
		if theme := harness.server.readTheme(t.Context()); theme != ThemeDark {
			t.Errorf("an accepted submission stored the theme %q rather than dark", theme)
		}
	})
}

// freshClient returns a second browser against the same server: its own cookie jar,
// no session, no form token.
func freshClient(t *testing.T, from *harness) *harness {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	copied := *from
	copied.client = &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &copied
}

// presentSession puts a session token in this client's jar, which is what somebody
// who copied a cookie has.
func (h *harness) presentSession(token string) {
	h.t.Helper()
	target, err := url.Parse(h.http.URL)
	if err != nil {
		h.t.Fatalf("parsing the front-end address: %v", err)
	}
	h.client.Jar.SetCookies(target, []*http.Cookie{{Name: cookieSession, Value: token, Path: "/"}})
}

// statusAndBody reads a response once and closes it.
func statusAndBody(t *testing.T, answer *http.Response) (int, string) {
	t.Helper()
	body := readBody(t, answer)
	_ = answer.Body.Close()
	return answer.StatusCode, body
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

// assertSameSet fails unless two sets hold the same names.
func assertSameSet(t *testing.T, what string, expected, seen map[string]bool) {
	t.Helper()
	for name := range expected {
		if !seen[name] {
			t.Errorf("%s no longer include %s", what, name)
		}
	}
	for name := range seen {
		if !expected[name] {
			t.Errorf("%s gained %s, which nobody decided on", what, name)
		}
	}
}
