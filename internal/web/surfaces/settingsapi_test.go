package surfaces_test

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The Settings API's coverage (C5 of the backend-completion cycle, C-AC9 and C-AC4): every route of
// the five Go-rendered surfaces has its JSON equivalent in docs/widget-api.md, "Settings API", and
// every JSON route of them is guarded as a write must be. Each surface's own tests hold each JSON
// route to its page: the same change, the same refusals, the same figures.

// settingsAPISurfaces are the surfaces the step-7 application replaces.
var settingsAPISurfaces = []string{"account", "settings", "collection", "interfaces", "sources"}

// surfaceRoutes are the routes the five surfaces contribute, read from the registered surfaces
// themselves rather than from a list written here.
func surfaceRoutes(t *testing.T, harness *webtest.Harness) []web.Route {
	t.Helper()
	var routes []web.Route
	found := map[string]bool{}
	for _, surface := range web.RegisteredSurfaces() {
		for _, name := range settingsAPISurfaces {
			if surface.Name == name {
				found[name] = true
				routes = append(routes, surface.Routes(harness.Server)...)
			}
		}
	}
	for _, name := range settingsAPISurfaces {
		if !found[name] {
			t.Fatalf("the surface %q is not registered", name)
		}
	}
	return routes
}

// settingsAPITable reads the table of docs/widget-api.md, "Settings API": each page route with the
// JSON routes it maps to.
func settingsAPITable(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/widget-api.md")
	if err != nil {
		t.Fatalf("reading the API document: %v", err)
	}
	text := string(raw)
	start := strings.Index(text, "\n## Settings API\n")
	if start < 0 {
		t.Fatal(`docs/widget-api.md has no section "Settings API"`)
	}
	section := text[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	route := regexp.MustCompile("`(GET|POST) (/[^` ]*)`")
	table := map[string][]string{}
	inTable := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| Page route | JSON route |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 || strings.HasPrefix(cells[1], "---") {
			continue
		}
		page := route.FindStringSubmatch(cells[1])
		if page == nil {
			continue
		}
		for _, json := range route.FindAllStringSubmatch(cells[2], -1) {
			table[page[1]+" "+page[2]] = append(table[page[1]+" "+page[2]], json[1]+" "+json[2])
		}
	}
	if len(table) == 0 {
		t.Fatal("the Settings API table maps nothing, so this assertion is vacuous")
	}
	return table
}

// TestEveryPageRouteHasItsJSONEquivalentInTheSettingsAPITable is C-AC9's enumeration: every page
// route of the five surfaces is a row of the table, every JSON route a row names is served, and
// every JSON route of the five surfaces is named by some row.
func TestEveryPageRouteHasItsJSONEquivalentInTheSettingsAPITable(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	table := settingsAPITable(t)
	served := map[string]bool{}
	for _, route := range harness.Server.Routes() {
		served[route.Method+" "+route.Pattern] = true
	}
	named := map[string]bool{}
	for page, routes := range table {
		for _, json := range routes {
			named[json] = true
			if !served[json] {
				t.Errorf("the Settings API maps %s to %s, which no route serves", page, json)
			}
		}
	}
	pages := 0
	for _, route := range surfaceRoutes(t, harness) {
		name := route.Method + " " + route.Pattern
		if route.JSON {
			if !named[name] {
				t.Errorf("%s is a JSON route of a page's surface the Settings API table does not name", name)
			}
			continue
		}
		pages++
		if len(table[name]) == 0 {
			t.Errorf("the page route %s has no JSON equivalent in the Settings API table", name)
		}
	}
	if pages == 0 {
		t.Fatal("the five surfaces registered no page route, so this assertion is vacuous")
	}
}

// concrete is a route's pattern with each wildcard given a value a request can carry.
func concrete(pattern string) string {
	replacer := strings.NewReplacer("{kind}", "firewall_log", "{card}", "storage")
	return replacer.Replace(pattern)
}

// TestEveryJSONRouteOfThePagesIsGuardedAndARefusalChangesNothing is C-AC4 for the Settings API:
// every JSON write of the five surfaces refuses a request with no session (401, a JSON
// condition) unless it is public by decision, refuses a missing proof, a wrong one and a
// cross-origin one (403, csrf_refused), and changes no row when it refuses; no JSON GET changes
// a row.
func TestEveryJSONRouteOfThePagesIsGuardedAndARefusalChangesNothing(t *testing.T) {
	t.Parallel()
	harness := webtest.SignedInHarness(t)
	token := harness.APIToken()
	before := webtest.DatabaseBytesExcept(t, harness, "session")
	unchanged := func(what string) {
		t.Helper()
		if after := webtest.DatabaseBytesExcept(t, harness, "session"); after != before {
			t.Errorf("%s changed a row", what)
		}
	}
	condition := func(answer *http.Response) string {
		t.Helper()
		defer func() { _ = answer.Body.Close() }()
		decoded := webtest.DecodeJSON(t, webtest.ReadBody(t, answer))
		text, _ := decoded["condition"].(string)
		return text
	}
	var writes, reads []string
	for _, route := range surfaceRoutes(t, harness) {
		if !route.JSON {
			continue
		}
		path := concrete(route.Pattern)
		name := route.Method + " " + route.Pattern
		if route.Method == http.MethodGet {
			reads = append(reads, name)
			answer := harness.Get(path)
			webtest.Discard(t, answer)
			unchanged("GET " + path)
			continue
		}
		writes = append(writes, name)
		if route.Access == web.AccessAuthenticated {
			anonymous := webtest.FreshClient(t, harness)
			answer := anonymous.PostJSON(path, "{}", token, "")
			if answer.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s answered %d with no session rather than 401", name, answer.StatusCode)
			}
			if got := condition(answer); got != "no_session" {
				t.Errorf("%s refused no session with %q rather than no_session", name, got)
			}
			unchanged(name + " with no session")
		}
		for _, attempt := range []struct {
			what, token, origin string
		}{
			{"no proof", "", ""},
			{"a wrong proof", token + "x", ""},
			{"another origin", token, "http://elsewhere.invalid"},
		} {
			answer := harness.PostJSON(path, "{}", attempt.token, attempt.origin)
			if answer.StatusCode != http.StatusForbidden {
				t.Errorf("%s answered %d to %s rather than 403", name, answer.StatusCode, attempt.what)
			}
			// The setup is closed once an account exists, before anything else is looked at.
			want := web.ConditionCSRFRefused
			if route.RequiresNoAccount {
				want = web.ConditionSetupClosed
			}
			if got := condition(answer); got != want {
				t.Errorf("%s refused %s with %q rather than %s", name, attempt.what, got, want)
			}
			unchanged(name + " with " + attempt.what)
		}
	}
	sort.Strings(writes)
	if len(writes) == 0 || len(reads) == 0 {
		t.Fatalf("the five surfaces registered %d JSON writes and %d JSON reads, so this is vacuous",
			len(writes), len(reads))
	}
}
