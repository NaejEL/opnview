package webtest

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web"
)

// AssertRefusesUnauthenticated fails unless the route turns away a request carrying
// no session.
func AssertRefusesUnauthenticated(t *testing.T, harness *Harness, route web.Route) {
	t.Helper()
	anonymous := FreshClient(t, harness)
	var answer *http.Response
	if route.Method == http.MethodGet {
		answer = anonymous.Get(route.Pattern)
	} else {
		// The form token comes from a page this anonymous client can actually load, so
		// the refusal under test is the missing session and not the missing token.
		answer = anonymous.PostRaw(route.Pattern, url.Values{
			"csrf_token": {anonymous.FormToken(web.PathSignIn)},
		})
	}
	defer func() { _ = answer.Body.Close() }()
	switch answer.StatusCode {
	case http.StatusSeeOther:
		if location := answer.Header.Get("Location"); location != web.PathSignIn &&
			location != web.PathSetup {
			t.Errorf("%s %s sent an unauthenticated reader to %q rather than to a sign-in surface",
				route.Method, route.Pattern, location)
		}
	case http.StatusUnauthorized, http.StatusForbidden:
	default:
		t.Errorf("%s %s answered %d to an unauthenticated request rather than refusing it",
			route.Method, route.Pattern, answer.StatusCode)
	}
}

// FreshClient returns a second browser against the same server: its own cookie jar,
// no session, no form token.
func FreshClient(t *testing.T, from *Harness) *Harness {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	copied := *from
	copied.Client = &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &copied
}

// PresentSession puts a session token in this client's jar, which is what somebody
// who copied a cookie has.
func (h *Harness) PresentSession(token string) {
	h.T.Helper()
	target, err := url.Parse(h.HTTP.URL)
	if err != nil {
		h.T.Fatalf("parsing the front-end address: %v", err)
	}
	h.Client.Jar.SetCookies(target, []*http.Cookie{{Name: web.SessionCookieName, Value: token, Path: "/"}})
}

// StatusAndBody reads a response once and closes it.
func StatusAndBody(t *testing.T, answer *http.Response) (int, string) {
	t.Helper()
	body := ReadBody(t, answer)
	_ = answer.Body.Close()
	return answer.StatusCode, body
}

// AssertSameSet fails unless two sets hold the same names.
func AssertSameSet(t *testing.T, what string, expected, seen map[string]bool) {
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

// PageSource fetches one page's HTML.
func PageSource(t *testing.T, harness *Harness, path string) string {
	t.Helper()
	answer := harness.Get(path)
	body := ReadBody(t, answer)
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", path, answer.StatusCode)
	}
	return body
}

// Followed reads the answer to a submission: a 303 back to a surface is followed, and
// the page it lands on returned with the 303; any other answer is read as it is.
func Followed(t *testing.T, h *Harness, answer *http.Response) (int, string) {
	t.Helper()
	status, body := StatusAndBody(t, answer)
	if status != http.StatusSeeOther {
		return status, body
	}
	location := answer.Header.Get("Location")
	if cut := strings.Index(location, "#"); cut >= 0 {
		location = location[:cut]
	}
	if !strings.HasPrefix(location, "/") {
		t.Fatalf("a save redirected off the surface, to %q", location)
	}
	return status, PageSource(t, h, location)
}
