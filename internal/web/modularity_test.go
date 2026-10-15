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
)

// The rule these tests hold: a kind of thing is a seam and an implementation is a directory
// behind it. A surface added as a directory -- a marker, a template, a catalogue file and a
// handler -- is served, translated, guarded and linked from the top bar with no edit to any
// file the product ships; and a surface that cannot be served is refused with its own named
// reason while the server keeps running.

// TestASurfaceAddedByADirectoryIsServedInsideTheLayout: the example surface is only a
// directory, registered through the public contract, and the server mounts it.
func TestASurfaceAddedByADirectoryIsServedInsideTheLayout(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)

	if refused := h.Server.RefusedSurfaces(); len(refused) != 0 {
		t.Fatalf("a server over the shipped surfaces and the example refused %v", refused)
	}
	mounted := strings.Join(h.Server.MountedSurfaces(), " ")
	if !strings.Contains(mounted, "hello") {
		t.Fatalf("the example surface is not mounted: %s", mounted)
	}

	status, page := webtest.StatusAndBody(t, h.Get("/hello"))
	if status != http.StatusOK {
		t.Fatalf("the new surface answered %d", status)
	}
	// Inside the layout: the page's class on <main>, the product name in the bar, and the
	// sign-out control of a signed-in reader.
	for _, want := range []string{`<main class="column surface-hello">`, `class="top-bar"`,
		`action="` + web.PathSignOut + `"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the new surface is not drawn inside the layout: %q is missing", want)
		}
	}
	// Its own strings, from its own catalogue file, resolved by the shared catalogue.
	catalogue := h.Server.Catalogue()
	for _, key := range []web.MessageKey{"hello.heading", "hello.greeting"} {
		text, err := catalogue.Translate(key)
		if err != nil {
			t.Fatalf("the surface's catalogue was not merged: %v", err)
		}
		if !strings.Contains(page, text) {
			t.Errorf("the page does not show %q (%s)", text, key)
		}
	}
	// Its data: one account, the one the harness created.
	if !strings.Contains(page, `<span class="figure">1</span>`) {
		t.Error("the page does not show the figure its handler computed")
	}
	// Its tab, on a page that is not its own, and the tab of the page being read marked.
	settings := webtest.PageSource(t, h, web.PathSettings)
	if !strings.Contains(settings, `<a href="/hello">`) {
		t.Error("the new surface's tab is not in the top bar of another page")
	}
	if !strings.Contains(page, `<a href="/hello" aria-current="page">`) {
		t.Error("the new surface's own page does not mark its tab current")
	}
}

// TestASurfaceAddedByADirectoryIsGuardedLikeTheOthers: it declares an access level and the
// middleware enforces it, with no code of the surface's own.
func TestASurfaceAddedByADirectoryIsGuardedLikeTheOthers(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	anonymous := webtest.FreshClient(t, h)
	answer := anonymous.Get("/hello")
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusSeeOther || answer.Header.Get("Location") != web.PathSignIn {
		t.Errorf("an unauthenticated request for the new surface answered %d to %q, want a redirect to sign-in",
			answer.StatusCode, answer.Header.Get("Location"))
	}
	for _, route := range h.Server.Routes() {
		if route.Pattern == "/hello" && route.Access != web.AccessAuthenticated {
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

// page is a minimal valid template.
const page = `{{define "body" -}}<p>{{t "probe.heading"}}</p>{{- end}}`

// goodFiles is a surface directory that is fine.
func goodFiles() fstest.MapFS {
	return fstest.MapFS{
		"template/probe.gohtml": {Data: []byte(page)},
		"locale/en.json": {Data: []byte(`{"language": "en", "strings": {"probe.title": "Probe",` +
			` "probe.heading": "Probe"}}`)},
	}
}

func okayRoutes(host web.Host) []web.Route {
	return []web.Route{{Method: http.MethodGet, Pattern: "/probe", Access: web.AccessAuthenticated,
		Handler: func(http.ResponseWriter, *http.Request) {}}}
}

// TestASurfaceThatCannotBeServedIsRefusedWithItsOwnReasonAndTheServerKeepsRunning: each
// defect has its own named reason, the surface is left out, and the rest of the product
// answers.
func TestASurfaceThatCannotBeServedIsRefusedWithItsOwnReasonAndTheServerKeepsRunning(t *testing.T) {
	t.Parallel()
	probe := func(name string, mutate func(*web.Surface, fstest.MapFS)) web.Surface {
		files := goodFiles()
		surface := web.Surface{Name: name, Files: files,
			Pages:  []web.Page{{Name: "probe", TitleKey: "probe.title"}},
			Routes: okayRoutes}
		mutate(&surface, files)
		return surface
	}
	cases := []refusalCase{
		{"no name", probe("", func(*web.Surface, fstest.MapFS) {}), "declares no name"},
		{"a missing template", probe("missing-template", func(s *web.Surface, files fstest.MapFS) {
			delete(files, "template/probe.gohtml")
		}), "has no template"},
		{"a template that does not compile", probe("broken-template", func(s *web.Surface, files fstest.MapFS) {
			files["template/probe.gohtml"] = &fstest.MapFile{Data: []byte(`{{define "body"}}{{if}}{{end}}`)}
		}), "does not compile"},
		{"a malformed catalogue", probe("broken-catalogue", func(s *web.Surface, files fstest.MapFS) {
			files["locale/en.json"] = &fstest.MapFile{Data: []byte(`{"language": "en", "strings": `)}
		}), "is malformed"},
		{"a catalogue key the core defines", probe("shadowing-catalogue", func(s *web.Surface, files fstest.MapFS) {
			files["locale/en.json"] = &fstest.MapFile{Data: []byte(`{"language": "en", "strings": {"app.name": "x"}}`)}
		}), "already defined by the core"},
		{"a route another surface serves", probe("duplicate-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: web.PathSignIn, Access: web.AccessPublic,
					Handler: func(http.ResponseWriter, *http.Request) {}}}
			}
		}), "is already served by"},
		{"a route with no access level", probe("open-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: "/open",
					Handler: func(http.ResponseWriter, *http.Request) {}}}
			}
		}), "declares no access level"},
		{"a route with no handler", probe("handlerless-route", func(s *web.Surface, files fstest.MapFS) {
			s.Routes = func(web.Host) []web.Route {
				return []web.Route{{Method: http.MethodGet, Pattern: "/nothing", Access: web.AccessPublic}}
			}
		}), "has no handler"},
		{"a tab for a page it does not have", probe("lost-tab", func(s *web.Surface, files fstest.MapFS) {
			s.Tab = &web.Tab{Page: "elsewhere", Path: "/probe", TitleKey: "probe.title"}
		}), "which the surface does not declare"},
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
	status, _ := webtest.StatusAndBody(t, h.Get(web.PathSignIn))
	if status != http.StatusOK {
		t.Errorf("the sign-in page answered %d beside the refused surfaces", status)
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
		Pages: []web.Page{{Name: "probe", TitleKey: "probe.title"}},
		Routes: func(host web.Host) []web.Route {
			return []web.Route{{Method: http.MethodGet, Pattern: "/probe", Access: web.AccessAuthenticated,
				Handler: func(writer http.ResponseWriter, request *http.Request) {
					base, err := host.NewBase(writer, request, web.SessionFrom(request), "probe.title")
					if err != nil {
						host.FailInternal(writer, request, err)
						return
					}
					if err := host.Render(writer, http.StatusOK, "probe", &base); err != nil {
						host.FailInternal(writer, request, err)
					}
				}}}
		}}
	h := webtest.NewAdjusted(t, t.TempDir(), true, false, func(options *web.Options) {
		options.Surfaces = append(options.Surfaces, surface)
	})
	h.CompleteSetup()
	if refused := h.Server.RefusedSurfaces(); len(refused) != 0 {
		t.Fatalf("a valid surface was refused: %v", refused)
	}
	status, body := webtest.StatusAndBody(t, h.Get("/probe"))
	if status != http.StatusOK || !strings.Contains(body, "<p>Probe</p>") {
		t.Errorf("the surface answered %d with %q", status, body)
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
		"webtest/examples/hello/template/hello.gohtml",
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
