package frontjs_test

import (
	"strings"
	"testing"
)

// Without a session the shell shows only the session views; signing in returns the reader to the
// route they asked for (F7-7; the browser half of F7-AC16 is the ergonomist's).

func TestWithoutASessionEveryRouteLandsOnSetupOrSignIn(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/", "/people", "/settings", "/canvas/1/main", "/no-such-view"} {
		server := newFakeServer(t, false, false)
		page := server.page(t, path)
		if got := page.Path(); !strings.HasPrefix(got, "/setup") {
			t.Errorf("with no account yet, %s landed on %s, not the setup", path, got)
		}
		if page.Count(`form.session-form input[name="setup_token"]`) != 1 {
			t.Errorf("the setup view is not drawn for %s:\n%s", path, page.Outline())
		}
		if page.Count("nav.rail") != 0 {
			t.Errorf("the rail is drawn without a session for %s", path)
		}

		server = newFakeServer(t, true, false)
		page = server.page(t, path)
		if got := page.Path(); !strings.HasPrefix(got, "/sign-in") {
			t.Errorf("with an account, %s landed on %s, not the sign-in", path, got)
		}
	}
}

func TestSigningInReturnsToTheRouteAskedFor(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, false)
	server.on("GET /api/owners", func(_ frontjsRequest) frontjsAnswer { return ok(map[string]any{"owners": []any{}}) })
	server.on("GET /api/clients", func(_ frontjsRequest) frontjsAnswer { return ok(map[string]any{"clients": []any{}}) })
	page := server.page(t, "/people")
	if got := page.Path(); got != "/sign-in?next=%2Fpeople" {
		t.Fatalf("the sign-in does not carry the route asked for: %s", got)
	}
	page.Input(`input[name="login"]`, "operator")
	page.Input(`input[name="password"]`, "wrong")
	page.Click(`button[type="submit"]`)
	if page.Count(`p.field-error`) != 1 {
		t.Fatalf("a refused sign-in names no refusal:\n%s", page.Outline())
	}
	page.Input(`input[name="password"]`, "correct horse battery")
	page.Click(`button[type="submit"]`)
	if got := page.Path(); got != "/people" {
		t.Fatalf("after signing in the reader is at %s, not /people:\n%s", got, page.Outline())
	}
	if page.Count("nav.rail") != 1 || page.Count("main.view-people") != 1 {
		t.Fatalf("the People view is not drawn in the layout:\n%s", page.Outline())
	}
	// The write carried the browser's form token and JSON, as the write rule requires.
	for _, write := range page.Writes() {
		if write.Headers["X-CSRF-Token"] == "" || write.Headers["Content-Type"] != "application/json" {
			t.Errorf("the write %s carried no form token or no JSON type: %v", write.Path, write.Headers)
		}
	}
}

func TestA401MidSessionReturnsToSignIn(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	server.on("GET /api/blocklists", func(_ frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"blocklists": []any{}, "purposes": []string{"advertising"}})
	})
	page := server.page(t, "/blocklists")
	if page.Count("main.view-blocklists") != 1 {
		t.Fatalf("the Blocklists view is not drawn:\n%s", page.Outline())
	}
	server.mutex.Lock()
	server.signedIn = false
	server.mutex.Unlock()
	page.Go("/sources")
	if got := page.Path(); got != "/sign-in?next=%2Fsources" {
		t.Fatalf("a 401 mid-session left the reader at %s", got)
	}
}
