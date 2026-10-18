package frontjs_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/front"
	"github.com/NaejEL/opnview/internal/front/frontjs"

	// The surfaces' catalogue files are part of the merged catalogue a browser is served.
	_ "github.com/NaejEL/opnview/internal/web/surfaces"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// A fake server for the front: every route the views read answers a synthesised fixture.
//
// FIXTURE PROVENANCE. Every value below is invented for these tests: addresses from the
// documentation ranges (RFC 5737, RFC 3849), host and list names under RFC 2606's example names,
// person names that are nobody's. No value of any real network is here.

// englishCatalogue is the merged English catalogue a browser is served: the core's, every
// surface's, every built-in plugin's and the front's own.
var (
	catalogueOnce sync.Once
	catalogue     map[string]string
	catalogueErr  error
)

func englishCatalogue(t testing.TB) map[string]string {
	t.Helper()
	catalogueOnce.Do(func() {
		index, err := front.Build(front.Options{Builtin: opnview.BuiltinFront(),
			CoreStrings: func(language string) map[string]string {
				merged, err := webtest.ProductCatalogue(language)
				if err != nil {
					return map[string]string{}
				}
				return merged.Strings
			}})
		if err != nil {
			catalogueErr = err
			return
		}
		english, _ := index.Catalogue("en")
		catalogue = english.Strings
	})
	if catalogueErr != nil {
		t.Fatalf("building the catalogue: %v", catalogueErr)
	}
	return catalogue
}

// fakeServer answers the front's requests from per-route handlers, and keeps the session state.
type fakeServer struct {
	t             testing.TB
	mutex         sync.Mutex
	accountExists bool
	signedIn      bool
	appearance    map[string]any
	routes        map[string]func(frontjs.Request) frontjs.Answer
	// extraStrings are a test's own catalogue entries: a synthetic plugin's labels.
	extraStrings map[string]string
}

func newFakeServer(t testing.TB, accountExists, signedIn bool) *fakeServer {
	server := &fakeServer{t: t, accountExists: accountExists, signedIn: signedIn,
		appearance: map[string]any{"palette": nil, "mode": nil, "language": nil},
		routes:     map[string]func(frontjs.Request) frontjs.Answer{}}
	english := englishCatalogue(t)
	server.on("GET /api/session", func(frontjs.Request) frontjs.Answer {
		return ok(map[string]any{"account_exists": server.accountExists, "signed_in": server.signedIn,
			"csrf_token": "token-of-the-test"})
	})
	server.on("GET /api/appearance", func(frontjs.Request) frontjs.Answer { return ok(server.appearanceAnswer()) })
	server.on("POST /api/appearance", func(request frontjs.Request) frontjs.Answer {
		var change map[string]any
		_ = json.Unmarshal([]byte(request.Body), &change)
		for key, value := range change {
			server.appearance[key] = value
		}
		return ok(server.appearanceAnswer())
	})
	server.on("GET /api/strings/en", func(frontjs.Request) frontjs.Answer {
		merged := map[string]string{}
		for key, text := range english {
			merged[key] = text
		}
		server.mutex.Lock()
		for key, text := range server.extraStrings {
			merged[key] = text
		}
		server.mutex.Unlock()
		return ok(map[string]any{"language": "en", "strings": merged})
	})
	server.on("GET /api/sources", func(frontjs.Request) frontjs.Answer { return ok(sourcesFixture()) })
	server.on("GET /api/dashboards", func(frontjs.Request) frontjs.Answer {
		return ok(map[string]any{"dashboards": []any{}, "format": map[string]int{"current": 1, "minimum": 1}})
	})
	server.on("POST /api/session/sign-in", func(request frontjs.Request) frontjs.Answer {
		if !strings.Contains(request.Body, `"password":"correct horse battery"`) {
			return frontjs.Answer{Status: 401, Body: map[string]string{"condition": "sign_in_refused"}}
		}
		server.signedIn = true
		return ok(map[string]any{"signed_in": true, "csrf_token": "token-of-the-session"})
	})
	server.on("POST /api/session/sign-out", func(frontjs.Request) frontjs.Answer {
		server.signedIn = false
		return ok(map[string]any{"signed_in": false})
	})
	return server
}

func (s *fakeServer) appearanceAnswer() map[string]any {
	return map[string]any{
		"palette": s.appearance["palette"], "mode": s.appearance["mode"], "language": s.appearance["language"],
		"default_palette": "industrial",
		"palettes": []any{map[string]any{"id": "industrial", "name": map[string]string{"en": "Industrial"}},
			map[string]any{"id": "nord", "name": map[string]string{"en": "Nord"}}},
		"modes":     []string{"system", "light", "dark"},
		"languages": []any{map[string]string{"code": "en", "name": "English"}},
	}
}

func ok(body any) frontjs.Answer { return frontjs.Answer{Status: 200, Body: body} }

// The two types a handler is written with.
type (
	frontjsRequest = frontjs.Request
	frontjsAnswer  = frontjs.Answer
)

func (s *fakeServer) on(route string, handler func(frontjs.Request) frontjs.Answer) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.routes[route] = handler
}

// handle answers a request: a route the server knows, else 404; a request needing a session
// without one, 401.
func (s *fakeServer) handle(request frontjs.Request) frontjs.Answer {
	path := request.Path
	if cut := strings.Index(path, "?"); cut >= 0 {
		path = path[:cut]
	}
	s.mutex.Lock()
	handler, found := s.routes[request.Method+" "+path]
	signedIn := s.signedIn
	s.mutex.Unlock()
	public := map[string]bool{"/api/session": true, "/api/appearance": true, "/api/strings/en": true,
		"/api/session/sign-in": true, "/api/account/setup": true, "/api/account/recover": true}
	if !signedIn && !public[path] && !(request.Method == "GET" && path == "/api/appearance") {
		return frontjs.Answer{Status: 401, Body: map[string]string{"condition": "no_session"}}
	}
	if request.Method == "POST" && request.Headers["X-CSRF-Token"] == "" {
		return frontjs.Answer{Status: 403, Body: map[string]string{"condition": "csrf_refused"}}
	}
	if !found {
		return frontjs.Answer{Status: 404, Body: map[string]string{"condition": "unknown_route"}}
	}
	return handler(request)
}

// page starts the front against this server.
func (s *fakeServer) page(t testing.TB, path string) *frontjs.Runtime {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{Handler: s.handle})
	page.Start(path)
	return page
}

// sourcesFixture is GET /api/sources: one healthy provider, one in outage with bounds, one never
// checked.
func sourcesFixture() map[string]any {
	return map[string]any{
		"providers": []any{
			map[string]any{"kind": "firewall_log", "provider_key": "pf", "display_name": "",
				"availability": "healthy", "outcome": "healthy", "active": true, "selection": "auto", "in_outage": false,
				"checked_at": 1773478800, "probe": "probe_firewall_log", "detail": nil, "opnsense_pages": []any{},
				"since": map[string]any{"after_at": 1773475200, "by_at": 1773475260, "state": nil}},
			map[string]any{"kind": "security_event", "provider_key": "suricata", "display_name": "",
				"availability": "not_collecting", "outcome": "present_but_disabled", "active": false, "selection": "on",
				"in_outage": true, "checked_at": 1773478800, "probe": "probe_ids_status",
				"detail": "ids service status answered stopped",
				"opnsense_pages": []any{map[string]any{"url": "https://firewall.example/ui/ids", "page": "ids_administration",
					"label": "plugin.suricata.opnsense_page.ids_administration"}},
				"since": map[string]any{"after_at": nil, "by_at": nil, "state": "state_change_not_observed"}},
		},
		"outages":      []any{map[string]any{"kind": "security_event", "provider_key": "suricata"}},
		"outage_count": 1,
		"recent_gaps": []any{map[string]any{"kind": "firewall_log", "provider_key": "pf",
			"reason": "digest_outside_returned_window", "start_at": 1773470000, "end_at": 1773470600,
			"missed_seconds": 600, "detail": nil}},
		"recent_gap_window_seconds": 86400,
	}
}
