package web_test

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"

	// The worked example of docs/extending.md. Importing it for its effect is the whole
	// procedure for linking a surface; nothing shipped is edited.
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	_ "github.com/NaejEL/opnview/internal/web/webtest/examples/hello"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The rule these tests hold: a kind of thing is a seam and an implementation is a directory
// behind it. A surface added as a directory -- a marker, a catalogue file and a handler -- is
// served, translated and guarded with no edit to any file the product ships; and a surface that
// cannot be served is refused with its own named reason while the server keeps running.

// TestASurfaceAddedByADirectoryIsServed: the example surface is only a directory, registered
// through the public contract, and the server mounts it.
func TestASurfaceAddedByADirectoryIsServed(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)

	if refused := h.Server.RefusedSurfaces(); len(refused) != 0 {
		t.Fatalf("a server over the shipped surfaces and the example refused %v", refused)
	}
	mounted := strings.Join(h.Server.MountedSurfaces(), " ")
	if !strings.Contains(mounted, "hello") {
		t.Fatalf("the example surface is not mounted: %s", mounted)
	}

	// Its data: one account, the one the harness created.
	answer := settingstest.GetJSON(h, "/api/hello")
	if answer["accounts"] != float64(1) {
		t.Errorf("the new surface answered %v, not the figure its handler computes", answer)
	}
	// Its own strings, from its own catalogue file, merged into the catalogue the front reads.
	strings := h.Server.CatalogueStrings(web.SourceLanguage)
	for _, key := range []string{"hello.title", "hello.greeting"} {
		if strings[key] == "" {
			t.Errorf("the surface's catalogue was not merged: %s is missing", key)
		}
	}
}

// TestASurfaceAddedByADirectoryIsGuardedLikeTheOthers: it declares an access level and the
// middleware enforces it, with no code of the surface's own.
func TestASurfaceAddedByADirectoryIsGuardedLikeTheOthers(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	anonymous := webtest.FreshClient(t, h)
	status, body := webtest.StatusAndBody(t, anonymous.Get("/api/hello"))
	if status != http.StatusUnauthorized || !strings.Contains(body, `"no_session"`) {
		t.Errorf("an unauthenticated request for the new surface answered %d %s, want 401 no_session",
			status, body)
	}
	for _, route := range h.Server.Routes() {
		if route.Pattern == "/api/hello" && route.Access != web.AccessAuthenticated {
			t.Errorf("the new surface's route is %s", route.Access)
		}
	}
}

// refusalCase is a surface that cannot be served and the reason it is refused with.
type refusalCase struct {
	name    string
	surface web.Surface
	reason  string
}

// goodFiles is a surface directory that is fine.
func goodFiles() fstest.MapFS {
	return fstest.MapFS{
		"locale/en.json": {Data: []byte(`{"language": "en", "strings": {"probe.title": "Probe"}}`)},
	}
}

func okayRoutes(host web.Host) []web.Route {
	return []web.Route{{Method: http.MethodGet, Pattern: "/api/probe", Access: web.AccessAuthenticated,
		JSON: true, Handler: func(http.ResponseWriter, *http.Request) {}}}
}

// TestASurfaceThatCannotBeServedIsRefusedWithItsOwnReasonAndTheServerKeepsRunning: each
// defect has its own named reason, the surface is left out, and the rest of the product
// answers.
func TestASurfaceThatCannotBeServedIsRefusedWithItsOwnReasonAndTheServerKeepsRunning(t *testing.T) {
	t.Parallel()
	probe := func(name string, mutate func(*web.Surface, fstest.MapFS)) web.Surface {
		files := goodFiles()
		surface := web.Surface{Name: name, Files: files, Routes: okayRoutes}
		mutate(&surface, files)
		return surface
	}
	cases := []refusalCase{
		{"no name", probe("", func(*web.Surface, fstest.MapFS) {}), "declares no name"},
		{"a malformed catalogue", probe("broken-catalogue", func(s *web.Surface, files fstest.MapFS) {
			files["locale/en.json"] = &fstest.MapFile{Data: []byte(`{"language": "en", "strings": `)}
		}), "is malformed"},
		{"a catalogue key the core defines", probe("shadowing-catalogue", func(s *web.Surface, files fstest.MapFS) {
			files["locale/en.json"] = &fstest.MapFile{Data: []byte(`{"language": "en", "strings": {"app.name": "x"}}`)}
		}), "already defined by the core"},
		{"a route another surface serves", probe("duplicate-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: webtest.PathAPISession, Access: web.AccessPublic,
					Handler: func(http.ResponseWriter, *http.Request) {}}}
			}
		}), "is already served by"},
		{"a route with no access level", probe("open-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: "/api/open",
					Handler: func(http.ResponseWriter, *http.Request) {}}}
			}
		}), "declares no access level"},
		{"a route with no handler", probe("handlerless-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: "/api/nothing", Access: web.AccessPublic}}
			}
		}), "has no handler"},
		{"a routes function that panics", probe("panicking-routes", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route { panic("boom") }
		}), "building its routes failed"},
		{"a name another surface has", probe("account", func(s *web.Surface, files fstest.MapFS) {}),
			"is already taken by another surface"},
	}

	surfaces := make([]web.Surface, 0, len(cases))
	for _, c := range cases {
		surfaces = append(surfaces, c.surface)
	}
	h := webtest.NewAdjusted(t, t.TempDir(), true, false, func(options *web.Options) {
		options.Surfaces = append(options.Surfaces, surfaces...)
	})

	refused := h.Server.RefusedSurfaces()
	for _, c := range cases {
		found := false
		for _, refusal := range refused {
			if strings.Contains(refusal.Reason, c.reason) && (refusal.Surface == c.surface.Name) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no refusal of %q carries %q; refusals: %v", c.name, c.surface.Name, c.reason, refused)
		}
	}
	// The product answers all the same, and the surfaces that were fine are still mounted.
	status, _ := webtest.StatusAndBody(t, h.Get(webtest.PathAPISession))
	if status != http.StatusOK {
		t.Errorf("the session state answered %d beside the refused surfaces", status)
	}
	if mounted := strings.Join(h.Server.MountedSurfaces(), " "); !strings.Contains(mounted, "account") ||
		!strings.Contains(mounted, "sources") {
		t.Errorf("the shipped surfaces are not all mounted: %s", mounted)
	}
}

// TestAValidSurfaceMountedBesideTheShippedOnesIsServed: a surface defined with no Go file of
// its own beyond a handler, over an in-memory directory, is served through
// Options.Surfaces -- the way a test mounts the surface it is about.
func TestAValidSurfaceMountedBesideTheShippedOnesIsServed(t *testing.T) {
	t.Parallel()
	surface := web.Surface{Name: "probe", Files: goodFiles(),
		Routes: func(host web.Host) []web.Route {
			return []web.Route{{Method: http.MethodGet, Pattern: "/api/probe", Access: web.AccessAuthenticated,
				JSON: true, Handler: func(writer http.ResponseWriter, request *http.Request) {
					web.WriteJSON(writer, http.StatusOK, map[string]string{"probe": "answered"})
				}}}
		}}
	h := webtest.NewAdjusted(t, t.TempDir(), true, false, func(options *web.Options) {
		options.Surfaces = append(options.Surfaces, surface)
	})
	h.CompleteSetup()
	if refused := h.Server.RefusedSurfaces(); len(refused) != 0 {
		t.Fatalf("a valid surface was refused: %v", refused)
	}
	status, body := webtest.StatusAndBody(t, h.Get("/api/probe"))
	if status != http.StatusOK || !strings.Contains(body, `"probe":"answered"`) {
		t.Errorf("the surface answered %d with %q", status, body)
	}
	if h.Server.CatalogueStrings(web.SourceLanguage)["probe.title"] != "Probe" {
		t.Error("the surface's catalogue was not merged")
	}
}

// TestTheDocumentationShowsTheWorkedExampleVerbatim: the example docs/extending.md walks
// through is the example this package tests, file for file, so the documentation cannot
// describe a surface that does not work.
func TestTheDocumentationShowsTheWorkedExampleVerbatim(t *testing.T) {
	t.Parallel()
	documentation, err := os.ReadFile("../../docs/extending.md")
	if err != nil {
		t.Fatalf("reading the documentation: %v", err)
	}
	for _, path := range []string{
		"webtest/examples/hello/surface.json",
		"webtest/examples/hello/surface.go",
		"webtest/examples/hello/locale/en.json",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the example %s: %v", path, err)
		}
		if !strings.Contains(string(documentation), strings.TrimRight(string(raw), "\n")) {
			t.Errorf("docs/extending.md does not carry %s verbatim", path)
		}
	}
}
