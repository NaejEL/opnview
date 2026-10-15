package widgets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/widget"
	envelopepkg "github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The refusal proof (c): an operator widget that breaks any rule is refused WITH ITS OWN NAMED
// REASON, the widgets beside it still load, the built-ins are still served, and the service
// keeps running. Nothing in this file may crash it.

// goodManifest and goodQuery are an operator widget that loads: the control every case sits
// beside.
const goodQuery = `SELECT column1 AS key, column2 AS value FROM (VALUES ('a', 1), ('b', 2))`

func goodManifest(widgetType string) string {
	return shapeManifest(widgetType, "ranked_bars", false, "")
}

// refusalCase is one bad widget.
type refusalCase struct {
	name string
	// directory is the directory name; the manifest's type is usually the same.
	directory string
	manifest  string
	query     string
	// skipQuery writes no query file; skipManifest writes no manifest.
	skipQuery, skipManifest bool
	code                    string
}

func refusalCases() []refusalCase {
	good := func(widgetType string) string { return goodManifest(widgetType) }
	withQuery := func(widgetType, query string) refusalCase {
		return refusalCase{directory: widgetType, manifest: good(widgetType), query: query}
	}
	cases := []refusalCase{
		{name: "no manifest", directory: "example_no_manifest", skipManifest: true, code: params.RefusalManifestUnreadable},
		{name: "not JSON", directory: "example_not_json", manifest: `{"type": `, query: goodQuery,
			code: params.RefusalManifestInvalid},
		{name: "an unknown field", directory: "example_unknown_field",
			manifest: strings.Replace(good("example_unknown_field"), `"queries":["query.sql"]`, `"queries":["query.sql"],"colour":"red"`, 1),
			query:    goodQuery, code: params.RefusalManifestInvalid},
		{name: "a bad type name", directory: "Example-Bad", manifest: good("Example-Bad"), query: goodQuery,
			code: params.RefusalManifestInvalid},
		{name: "a future manifest version", directory: "example_version",
			manifest: strings.Replace(good("example_version"), `"manifest_version":1`, `"manifest_version":2`, 1),
			query:    goodQuery, code: params.RefusalUnsupportedManifestVersion},
		{name: "an unknown shape", directory: "example_shape",
			manifest: strings.Replace(good("example_shape"), `"ranked_bars"`, `"pie"`, 1), query: goodQuery,
			code: params.RefusalUnknownShape},
		{name: "an unknown provider kind", directory: "example_provider",
			manifest: strings.Replace(good("example_provider"), `"providers":[]`, `"providers":["telepathy"]`, 1),
			query:    goodQuery, code: params.RefusalUnknownProviderKind},
		{name: "a primary that is not bound", directory: "example_primary",
			manifest: strings.Replace(good("example_primary"), `"providers":[]`, `"providers":[],"primary":"dns_lookup"`, 1),
			query:    goodQuery, code: params.RefusalManifestInvalid},
		{name: "an unknown parameter kind", directory: "example_param_kind",
			manifest: shapeManifest("example_param_kind", "ranked_bars", false,
				`{"key":"when","kind":"datetime","default":null,"class":"data"}`),
			query: goodQuery, code: params.RefusalUnknownParameterKind},
		{name: "an enum with no values", directory: "example_enum",
			manifest: shapeManifest("example_enum", "ranked_bars", false,
				`{"key":"mode","kind":"enum","default":"a","class":"data"}`),
			query: goodQuery, code: params.RefusalParameterVocabulary},
		{name: "an unknown parameter class", directory: "example_class",
			manifest: shapeManifest("example_class", "ranked_bars", false,
				`{"key":"mode","kind":"boolean","default":true,"class":"decorative"}`),
			query: goodQuery, code: params.RefusalParameterVocabulary},
		{name: "a reference of an unknown kind", directory: "example_ref_kind",
			manifest: shapeManifest("example_ref_kind", "ranked_bars", false,
				`{"key":"thing","kind":"reference","reference":"galaxy","default":null,"class":"data"}`),
			query: goodQuery, code: params.RefusalParameterVocabulary},
		{name: "a default outside the vocabulary", directory: "example_default",
			manifest: shapeManifest("example_default", "ranked_bars", false,
				`{"key":"mode","kind":"enum","values":["a","b"],"default":"c","class":"data"}`),
			query: goodQuery, code: params.RefusalBadDefault},
		{name: "a default of the wrong type", directory: "example_default_type",
			manifest: shapeManifest("example_default_type", "ranked_bars", false,
				`{"key":"count","kind":"integer","default":"many","class":"data"}`),
			query: goodQuery, code: params.RefusalBadDefault},
		{name: "a parameter named like a bound one", directory: "example_reserved",
			manifest: shapeManifest("example_reserved", "ranked_bars", false,
				`{"key":"from","kind":"integer","default":1,"class":"data"}`),
			query: goodQuery, code: params.RefusalReservedParameterKey},
		{name: "a reference not listed", directory: "example_references",
			manifest: shapeManifest("example_references", "ranked_bars", false,
				`{"key":"interfaces","kind":"reference_list","reference":"interface","default":null,"class":"data"}`),
			query: goodQuery, code: params.RefusalReferencesMismatch},
		{name: "a renderer that is not the reserved slot", directory: "example_renderer",
			manifest: strings.Replace(good("example_renderer"), `"queries":["query.sql"]`, `"queries":["query.sql"],"renderer":{"reserved":false}`, 1),
			query:    goodQuery, code: params.RefusalRendererInvalid},
		{name: "a time series that is not windowed", directory: "example_unwindowed",
			manifest: shapeManifest("example_unwindowed", "time_series", false, ""), query: goodQuery,
			code: params.RefusalManifestInvalid},
		{name: "a type that differs from its directory", directory: "example_directory",
			manifest: good("example_other_name"), query: goodQuery, code: params.RefusalTypeMismatchWithDirectory},
		{name: "a built-in's type", directory: "top_sites", manifest: good("top_sites"), query: goodQuery,
			code: params.RefusalTypeCollision},
		{name: "no query file", directory: "example_no_query", manifest: good("example_no_query"), skipQuery: true,
			code: params.RefusalQueryMissing},
		{name: "no query declared", directory: "example_no_queries",
			manifest: strings.Replace(good("example_no_queries"), `"queries":["query.sql"]`, `"queries":[]`, 1),
			query:    goodQuery, code: params.RefusalQueryMissing},
		{name: "two queries", directory: "example_two_queries",
			manifest: strings.Replace(good("example_two_queries"), `"queries":["query.sql"]`,
				`"queries":["query.sql","other.sql"]`, 1),
			query: goodQuery, code: params.RefusalTooManyQueries},
		{name: "a path as a query file", directory: "example_query_path",
			manifest: strings.Replace(good("example_query_path"), `"queries":["query.sql"]`,
				`"queries":["../query.sql"]`, 1),
			query: goodQuery, code: params.RefusalManifestInvalid},
		{name: "an empty query", directory: "example_empty_query", manifest: good("example_empty_query"),
			query: "  -- nothing\n", code: params.RefusalQueryMissing},

		// Statements: a second statement, however it is hidden, and every way to write.
		withQuery("example_two_statements", "SELECT 1 AS key, 1 AS value; SELECT 2 AS key, 2 AS value"),
		withQuery("example_hidden_delete", "SELECT 1 AS key, 1 AS value; DELETE FROM setting"),
		withQuery("example_delete", "DELETE FROM setting"),
		withQuery("example_insert", "INSERT INTO setting (key, value, updated_at) VALUES ('a', 'b', 1)"),
		withQuery("example_update", "UPDATE setting SET value = 'x'"),
		withQuery("example_replace", "REPLACE INTO setting (key, value, updated_at) VALUES ('a', 'b', 1)"),
		withQuery("example_drop", "DROP TABLE setting"),
		withQuery("example_create", "CREATE TABLE example_t (a INTEGER)"),
		withQuery("example_pragma", "PRAGMA writable_schema = 1"),
		withQuery("example_attach", "ATTACH DATABASE ':memory:' AS example_other"),
		withQuery("example_vacuum", "VACUUM"),
		withQuery("example_with_insert", "WITH source(a) AS (SELECT 1) INSERT INTO setting (key, value, updated_at) "+
			"SELECT 'a', 'b', a FROM source"),
		withQuery("example_with_delete", "WITH doomed(k) AS (SELECT key FROM setting) DELETE FROM setting WHERE key IN doomed"),
		withQuery("example_explain", "EXPLAIN SELECT 1 AS key, 1 AS value"),

		withQuery("example_not_planned", "SELECT no_such_column AS key, 1 AS value FROM flow"),
		withQuery("example_syntax", "SELEKT 1"),
		withQuery("example_unknown_table", "SELECT a AS key, 1 AS value FROM no_such_table"),
		withQuery("example_unbound", "SELECT :nobody AS key, 1 AS value"),
		withQuery("example_positional", "SELECT ? AS key, 1 AS value"),
		withQuery("example_from_unwindowed", "SELECT :from AS key, 1 AS value"),
		withQuery("example_missing_column", "SELECT 1 AS value"),
	}
	expected := map[string]string{
		"example_two_statements":  params.RefusalQueryMultipleStatements,
		"example_hidden_delete":   params.RefusalQueryMultipleStatements,
		"example_delete":          params.RefusalQueryNotReadOnly,
		"example_insert":          params.RefusalQueryNotReadOnly,
		"example_update":          params.RefusalQueryNotReadOnly,
		"example_replace":         params.RefusalQueryNotReadOnly,
		"example_drop":            params.RefusalQueryNotReadOnly,
		"example_create":          params.RefusalQueryNotReadOnly,
		"example_pragma":          params.RefusalQueryNotReadOnly,
		"example_attach":          params.RefusalQueryNotReadOnly,
		"example_vacuum":          params.RefusalQueryNotReadOnly,
		"example_with_insert":     params.RefusalQueryNotReadOnly,
		"example_with_delete":     params.RefusalQueryNotReadOnly,
		"example_explain":         params.RefusalQueryNotReadOnly,
		"example_not_planned":     params.RefusalQueryDoesNotPlan,
		"example_syntax":          params.RefusalQueryNotReadOnly,
		"example_unknown_table":   params.RefusalQueryDoesNotPlan,
		"example_unbound":         params.RefusalUnknownQueryParameter,
		"example_positional":      params.RefusalUnknownQueryParameter,
		"example_from_unwindowed": params.RefusalUnknownQueryParameter,
		"example_missing_column":  params.RefusalMissingColumn,
	}
	for index := range cases {
		if cases[index].code == "" {
			cases[index].name = cases[index].directory
			cases[index].code = expected[cases[index].directory]
		}
	}
	return cases
}

// TestEveryBadOperatorWidgetIsRefusedWithItsOwnReasonAndTheServiceKeepsRunning is the proof
// (c): malformed manifests, unknown shapes, bad vocabularies, mutating and multi-statement
// queries are each refused under the code that names the rule broken -- while a good widget
// written beside them loads and every built-in still answers.
func TestEveryBadOperatorWidgetIsRefusedWithItsOwnReasonAndTheServiceKeepsRunning(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 10, span: 3600})
	directory := t.TempDir()
	cases := refusalCases()
	for _, c := range cases {
		if c.code == "" {
			t.Fatalf("the case %q expects no code", c.directory)
		}
		folder := filepath.Join(directory, c.directory)
		check(t, os.MkdirAll(folder, 0o755))
		if !c.skipManifest {
			check(t, os.WriteFile(filepath.Join(folder, "manifest.json"), []byte(c.manifest), 0o644))
		}
		if !c.skipQuery {
			check(t, os.WriteFile(filepath.Join(folder, "query.sql"), []byte(c.query), 0o644))
		}
	}
	// A file among the directories is not a widget, and a hidden directory is skipped.
	check(t, os.WriteFile(filepath.Join(directory, "README.txt"), []byte("notes"), 0o644))
	check(t, os.MkdirAll(filepath.Join(directory, ".git"), 0o755))
	writeWidget(t, directory, "example_good", goodManifest("example_good"), goodQuery)

	service := f.operatorService(t, directory)

	refused := map[string]params.Refusal{}
	for _, refusal := range service.Refusals() {
		refused[refusal.Type] = refusal
	}
	for _, c := range cases {
		refusal, found := refused[c.directory]
		if !found {
			t.Errorf("%s: the widget %q was not refused", c.name, c.directory)
			continue
		}
		if refusal.Code != c.code {
			t.Errorf("%s: refused as %q (%s), want %q", c.name, refusal.Code, refusal.Detail, c.code)
		}
		if refusal.Detail == "" {
			t.Errorf("%s: the refusal carries no detail", c.name)
		}
		if params.Contains(service.Types(), c.directory) && c.directory != "top_sites" {
			t.Errorf("%s: a refused widget is served", c.name)
		}
	}
	if len(service.Refusals()) != len(cases) {
		t.Errorf("%d refusals for %d bad widgets: %v", len(service.Refusals()), len(cases), service.Refusals())
	}

	// The good widget loaded, and the service is up.
	if got := service.OperatorTypes(); len(got) != 1 || got[0] != "example_good" {
		t.Errorf("the operator widgets that loaded are %v", got)
	}
	status, envelope := serve(t, service, "example_good", nil)
	if status != http.StatusOK || envelope["condition"] != envelopepkg.ConditionReachable {
		t.Errorf("the good widget answered %d %v", status, envelope["condition"])
	}
	// Every built-in is as served as before, including the one an operator tried to shadow.
	for _, widgetType := range f.service.Types() {
		if status, _ := serve(t, service, widgetType, nil); status != http.StatusOK {
			t.Errorf("the built-in %s answered %d beside the refused widgets", widgetType, status)
		}
	}
	if preset, _ := service.Preset("top_sites"); preset.Origin != request.OriginBuiltin {
		t.Errorf("top_sites is now %q: an operator widget shadowed a built-in", preset.Origin)
	}
	// And nothing the refused queries held ran: the data is as the fixture wrote it.
	if f.scalar(t, `SELECT count(*) FROM setting`) == 0 {
		t.Error("the setting table was emptied by a refused query")
	}
	recorder := httptest.NewRecorder()
	service.ServeType(recorder, httptest.NewRequest(http.MethodGet, widget.PathPrefix+"no_such_widget", nil), "no_such_widget")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("an unknown type answered %d", recorder.Code)
	}
}

// TestASemicolonInAStringOrAComment is not a second statement: the check reads tokens, not
// substrings.
func TestASemicolonInAStringOrAComment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	directory := t.TempDir()
	writeWidget(t, directory, "example_semicolons", goodManifest("example_semicolons"),
		"-- a comment; with a semicolon; and DELETE FROM setting\n"+
			"SELECT 'a;b' AS key, /* ; DROP TABLE setting; */ 1 AS value, 'x;y' AS \"label;\" -- ; trailing\n;\n")
	service := f.operatorService(t, directory)
	if len(service.Refusals()) != 0 {
		t.Fatalf("a semicolon inside a string or a comment was taken for a statement: %v", service.Refusals())
	}
	_, envelope := serve(t, service, "example_semicolons", nil)
	bars := list(t, object(t, envelope, "data"), "bars")
	if len(bars) != 1 || bars[0].(map[string]any)["key"] != "a;b" {
		t.Errorf("the widget with semicolons in its text answered %v", envelope["data"])
	}
}

// TestAnOperatorDirectoryThatIsAFileIsRefusedWithoutStoppingTheService.
func TestAnOperatorDirectoryThatIsAFileIsRefusedWithoutStoppingTheService(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	file := filepath.Join(t.TempDir(), "widgets")
	check(t, os.WriteFile(file, []byte("not a directory"), 0o644))
	service := f.operatorService(t, file)
	refusals := service.Refusals()
	if len(refusals) != 1 || refusals[0].Code != params.RefusalNotADirectory {
		t.Errorf("a widgets path that is a file gave %v", refusals)
	}
	if len(service.Types()) != len(f.service.Types()) {
		t.Error("the built-ins were lost")
	}
}
