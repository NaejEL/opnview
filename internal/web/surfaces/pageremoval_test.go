package surfaces_test

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The removal of the Go-rendered pages (specs/SPEC-step-7-frontend.md, F7-15 and F7-AC13): every
// route of the page-removal table is gone from the route table, every file it names is gone, and
// nothing refers to the machinery removed with them. A path a reader may have bookmarked stays
// valid only as a route of the front's shell.

// removedRoutes are the rows of the page-removal table.
var removedRoutes = []string{
	"GET /setup", "POST /setup",
	"GET /sign-in", "POST /sign-in",
	"GET /recover", "POST /recover",
	"POST /sign-out",
	"GET /settings", "POST /settings",
	"GET /collection", "POST /collection", "POST /collection/settings",
	"GET /interfaces", "POST /interfaces",
	"GET /sources",
	"GET /asset/style.css",
	"POST /api/settings/theme",
}

// shellPaths are the bookmarked page paths, which stay valid as routes of the front's shell.
var shellPaths = []string{"/setup", "/sign-in", "/recover", "/settings", "/collection", "/interfaces", "/sources"}

// removedFiles are the files the page-removal table and its list name, by their path from the
// repository root.
var removedFiles = []string{
	"internal/web/surfaces/account/template/setup.gohtml",
	"internal/web/surfaces/account/template/signin.gohtml",
	"internal/web/surfaces/account/template/recover.gohtml",
	"internal/web/surfaces/settings/template/settings.gohtml",
	"internal/web/surfaces/collection/template/collection.gohtml",
	"internal/web/surfaces/interfaces/template/interfaces.gohtml",
	"internal/web/surfaces/sources/template/sources.gohtml",
	"internal/web/asset/style.css",
	"internal/web/template/layout.gohtml",
	"internal/web/theme.go",
	"internal/web/render.go",
	"internal/web/base.go",
	"internal/web/asset.go",
	"internal/web/surfaces/html_test.go",
	"internal/web/surfaces/pages_golden_test.go",
	"internal/web/surfaces/testdata/golden_pages.json.gz",
	"internal/web/surfaces/style_test.go",
	"internal/web/surfaces/bundle_pages_test.go",
	"internal/web/webtest/bundle_pages.go",
	"internal/web/webtest/examples/hello/template/hello.gohtml",
}

// removedReferences are the identifiers of the machinery removed with the pages. A Go file that
// still names one refers to something that no longer exists, or brings it back.
var removedReferences = []string{
	"web.Base", "NewBase(", "RenderRefusal(", "RedirectWithError(", "RedirectWithNotice(",
	"RefuseSetupClosed(", "web.Page{", "web.Tab{", "web.Indicator", "IndicatorUnknown", "Pages: []web",
	"opnview_notice", "setFlash(", "takeFlash(", "FlashKeys(", "web.PathStylesheet", "web.PathSignOut",
	"web.PathSignIn", "web.PathRecover", "web.PathSettings", "web.PathRoot", "web.PageSignIn", "ReadTheme(", "KeyTheme", "ThemeChoices(",
	"ParseTheme(", "DocumentAttribute(", "TemplateSources(", "TemplateKeys(", "PageTemplates(",
	"Stylesheet()", ".gohtml", "PageSource(", "VisibleText(", "RenderedPage",
}

// treeRoot is the root of the working tree, from this package's directory.
const treeRoot = "../../.."

// TestTheRemovedPageRoutesAreGone: no row of the page-removal table is in the route table; a
// mutating page route answers nothing; and a bookmarked page path, where the shell is mounted,
// answers the shell rather than a page.
func TestTheRemovedPageRoutesAreGone(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	routes := map[string]bool{}
	for _, route := range h.Server.Routes() {
		routes[route.Method+" "+route.Pattern] = true
	}
	for _, removed := range removedRoutes {
		if routes[removed] {
			t.Errorf("%s is still a route", removed)
		}
	}
	for _, removed := range removedRoutes {
		method, path, _ := strings.Cut(removed, " ")
		if method != http.MethodPost {
			continue
		}
		answer := h.PostWithToken(path, "{}")
		status, _ := webtest.StatusAndBody(t, answer)
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s answered %d: a removed page route still answers", path, status)
		}
	}
	// The shell answers every unmatched path once the front is mounted. Before it is, nothing
	// answers them; either way no page and no redirect to one is drawn.
	shell := routes["GET /"]
	for _, path := range shellPaths {
		answer := h.Get(path)
		status, body := webtest.StatusAndBody(t, answer)
		switch {
		case shell && (status != http.StatusOK || !strings.Contains(answer.Header.Get("Content-Type"), "text/html")):
			t.Errorf("GET %s answered %d %q rather than the shell", path, status, answer.Header.Get("Content-Type"))
		case !shell && status != http.StatusNotFound:
			t.Errorf("GET %s answered %d with no shell mounted: %s", path, status, body)
		}
		if location := answer.Header.Get("Location"); location != "" {
			t.Errorf("GET %s redirects to %q", path, location)
		}
	}
}

// TestTheRemovedPageFilesAreGone: every file the page-removal list names is gone.
func TestTheRemovedPageFilesAreGone(t *testing.T) {
	t.Parallel()
	for _, path := range removedFiles {
		if _, err := os.Stat(filepath.Join(treeRoot, path)); err == nil {
			t.Errorf("%s still exists", path)
		} else if !os.IsNotExist(err) {
			t.Errorf("%s cannot be checked: %v", path, err)
		}
	}
}

// TestNothingRefersToTheRemovedPageMachinery: no Go file of the tree names a type, a function or a
// constant removed with the pages. This file names them to look for them, and is left out.
func TestNothingRefersToTheRemovedPageMachinery(t *testing.T) {
	t.Parallel()
	scanned := 0
	for _, directory := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(treeRoot, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || entry.Name() == "pageremoval_test.go" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for _, reference := range removedReferences {
				if strings.Contains(string(raw), reference) {
					t.Errorf("%s refers to %q, which was removed with the pages", filepath.ToSlash(path), reference)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", directory, err)
		}
	}
	if scanned == 0 {
		t.Fatal("no Go file was scanned, so this assertion is vacuous")
	}
}
