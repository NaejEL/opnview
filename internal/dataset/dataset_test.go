package dataset

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// TestTheCoreDeclaresTheFiveCustomChartSources: every source of custom_chart is a core dataset,
// read from its own directory, with its label, its relation and at least one measure.
func TestTheCoreDeclaresTheFiveCustomChartSources(t *testing.T) {
	core, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]Declaration{}
	for _, declaration := range core {
		names[declaration.Name] = declaration
	}
	for _, source := range []string{"traffic", "firewall_metric", "interface_throughput", "dns", "security_event"} {
		declaration, found := names[source]
		if !found {
			t.Errorf("the core does not declare %s", source)
			continue
		}
		if declaration.Origin != OriginCore || declaration.Label != "dataset."+source ||
			declaration.relation == "" || EarliestStatement(declaration) == "" {
			t.Errorf("%s is declared as %+v", source, declaration)
		}
		if Strings("en")[declaration.Label] == "" {
			t.Errorf("%s has no English label", source)
		}
		if _, timed := declaration.TimeColumn(); !timed {
			t.Errorf("%s has no time column", source)
		}
	}
}

// TestADeclarationTakesItsRolesDefaultsAndIsChecked: an undeclared role is a dimension, or time for
// the retention column; a measure without aggregations admits its role's, last only with a time
// column; and an ill-formed declaration is refused.
func TestADeclarationTakesItsRolesDefaultsAndIsChecked(t *testing.T) {
	declaration, err := Normalise(Declaration{Name: "example", Nature: NatureEvent,
		Freshness: Freshness{Kind: "example_kind"}, Retention: &Retention{Column: "at"},
		Columns: []Column{{Name: "at", Type: TypeInteger}, {Name: "host", Type: TypeText},
			{Name: "bytes", Type: TypeInteger, Role: RoleMeasure, Unit: "byte"}}})
	if err != nil {
		t.Fatal(err)
	}
	if declaration.Columns[0].Role != RoleTime || declaration.Columns[1].Role != RoleDimension ||
		!slices.Equal(declaration.Columns[1].Aggregations, []string{AggregationCount, AggregationCountDistinct}) ||
		!slices.Contains(declaration.Columns[2].Aggregations, AggregationLast) {
		t.Errorf("the defaults are %+v", declaration.Columns)
	}
	untimed, err := Normalise(Declaration{Name: "example", Nature: NatureSample, Freshness: Freshness{Kind: "k"},
		Columns: []Column{{Name: "value", Type: TypeReal, Role: RoleMeasure}}})
	if err != nil || slices.Contains(untimed.Columns[0].Aggregations, AggregationLast) {
		t.Errorf("a dataset without a time column admits %v (%v)", untimed.Columns[0].Aggregations, err)
	}
	for name, bad := range map[string]Declaration{
		"a nature outside the list": {Nature: "both", Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeText}}},
		"a text measure": {Nature: NatureEvent, Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeText, Role: RoleMeasure}}},
		"a unit on a dimension": {Nature: NatureEvent, Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeText, Unit: "byte"}}},
		"an aggregation a dimension cannot admit": {Nature: NatureEvent, Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeText, Aggregations: []string{AggregationSum}}}},
		"last without a time column": {Nature: NatureEvent, Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeReal, Role: RoleMeasure, Aggregations: []string{AggregationLast}}}},
		"a column named twice": {Nature: NatureEvent, Freshness: Freshness{Kind: "k"},
			Columns: []Column{{Name: "a", Type: TypeText}, {Name: "a", Type: TypeText}}},
	} {
		bad.Name = "example"
		if _, err := Normalise(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestACompiledStatementNamesOnlyDeclaredIdentifiersAndBindsEveryValue: whatever a query carries,
// the text holds the relation, the declared columns quoted, and parameters; every value is a
// bound argument.
func TestACompiledStatementNamesOnlyDeclaredIdentifiersAndBindsEveryValue(t *testing.T) {
	declaration, err := PluginDataset("example", "readings", `"plugin_example"."readings"`, NatureSample,
		Freshness{Kind: "example_kind"}, &Retention{Column: "sampled_at"}, []Column{
			{Name: "sampled_at", Type: TypeInteger, Role: RoleTime},
			{Name: "meter", Type: TypeText},
			{Name: "value", Type: TypeReal, Role: RoleMeasure}})
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"x": {"column": "meter"}, "y": [{"column": "value", "aggregation": "sum"}],
		"filters": [{"column": "meter", "op": "in", "value": ["a'); DROP TABLE flow; --", "b"]}],
		"split_by": null, "limit": 7}`
	query, refusal := Parse(payload, declaration)
	if refusal != nil {
		t.Fatal(refusal)
	}
	statement := Compile(declaration, query, Window{From: 100, To: 200})
	if strings.Contains(statement.Text, "DROP") || strings.Contains(statement.Text, "flow") {
		t.Errorf("a value reached the text: %s", statement.Text)
	}
	// Every value is bound, in the numbering of the text: the window first ($1, $2), then each value in the
	// order the statement names it -- an `in` list as one array (PA3 of the plugin-API amendment: one text
	// whatever the list's length).
	want := []any{int64(100), int64(200), []string{"a'); DROP TABLE flow; --", "b"}, int64(7)}
	if !reflect.DeepEqual(statement.Args, want) {
		t.Errorf("the arguments are %#v, want %#v", statement.Args, want)
	}
	for _, parameter := range []string{"$1", "$2", "$3", "$4"} {
		if !strings.Contains(statement.Text, parameter) {
			t.Errorf("the text does not bind %s: %s", parameter, statement.Text)
		}
	}
	if strings.Contains(statement.Text, "$5") {
		t.Errorf("the text binds more parameters than it was handed arguments: %s", statement.Text)
	}
	for _, identifier := range []string{`d."meter"`, `d."value"`, `d."sampled_at"`, `"plugin_example"."readings"`} {
		if !strings.Contains(statement.Text, identifier) {
			t.Errorf("the text does not name %s: %s", identifier, statement.Text)
		}
	}
}

// TestAQueryTakesItsDefaultsAndReportsWhatItDidNotApply: an empty query is a count on the time
// axis at the auto bucket of 24 h; a value outside its vocabulary takes its default and is
// reported; a key the query does not read is reported.
func TestAQueryTakesItsDefaultsAndReportsWhatItDidNotApply(t *testing.T) {
	core, _ := Core()
	var traffic Declaration
	for _, declaration := range core {
		if declaration.Name == "traffic" {
			traffic = declaration
		}
	}
	query, refusal := Parse("", traffic)
	if refusal != nil {
		t.Fatal(refusal)
	}
	timed, width, alignment := query.TimeAxis()
	if !timed || width != 900 || alignment != AlignmentEpoch || query.Period != DefaultPeriod ||
		len(query.Y) != 1 || query.Y[0].Aggregation != AggregationCount {
		t.Errorf("the defaults are %+v", query)
	}
	query, refusal = Parse(`{"period": "1y", "colour": "red", "x": {"column": "protocol"}, "order": "sideways"}`,
		traffic)
	if refusal != nil {
		t.Fatal(refusal)
	}
	if len(query.Invalid) != 2 || query.Invalid[0].Path != "order" || query.Invalid[1].Path != "period" ||
		query.Period != DefaultPeriod || query.Order != OrderDescending {
		t.Errorf("the invalid values are %+v", query.Invalid)
	}
	if _, reported := query.Unknown["colour"]; !reported {
		t.Errorf("an unknown key was not reported: %v", query.Unknown)
	}
}

// TestTheDocumentedRefusalsAreTheRefusals: the refusal table of docs/widget-api.md, "Data
// catalogue", lists exactly the codes the compiler refuses with (F-AC5).
func TestTheDocumentedRefusalsAreTheRefusals(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "widget-api.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "\n## Data catalogue\n")
	if start < 0 {
		t.Fatal("docs/widget-api.md has no Data catalogue section")
	}
	section := text[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	table := section[strings.Index(section, "| Condition | When |"):]
	var documented []string
	for _, line := range strings.Split(table, "\n")[2:] {
		if !strings.HasPrefix(line, "| `") {
			break
		}
		documented = append(documented, strings.Trim(strings.Split(line, "|")[1], " `"))
	}
	sort.Strings(documented)
	codes := RefusalCodes()
	sort.Strings(codes)
	if !slices.Equal(documented, codes) {
		t.Errorf("the document lists %v, the code refuses with %v", documented, codes)
	}
}

// TestACoreDatasetIsADirectoryAndNothingElse is the modularity rule for the core's datasets: a tree
// holding one more directory -- its declaration, its relation and its label -- yields one more
// dataset, with no list, switch or registry edited.
func TestACoreDatasetIsADirectoryAndNothingElse(t *testing.T) {
	tree := fstest.MapFS{}
	err := fs.WalkDir(coreFiles, "core", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(coreFiles, name)
		tree[name] = &fstest.MapFile{Data: content}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := readTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	tree["core/example_counter/dataset.json"] = &fstest.MapFile{Data: []byte(`{"nature": "event",
		"freshness": {"kind": "example_kind"}, "retention": {"column": "at"},
		"columns": [{"name": "at", "type": "integer", "role": "time"},
			{"name": "hits", "type": "integer", "role": "measure", "unit": "hit"}]}`)}
	tree["core/example_counter/relation.sql"] = &fstest.MapFile{Data: []byte(
		"SELECT at, hits FROM example WHERE at >= :from AND at < :to\n")}
	tree["core/example_counter/locale/en.json"] = &fstest.MapFile{Data: []byte(
		`{"language": "en", "strings": {"dataset.example_counter": "Example counter"}}`)}
	after, texts, err := readTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 || texts["en"]["dataset.example_counter"] != "Example counter" {
		t.Fatalf("the added directory gave %d datasets for %d (%v)", len(after), len(before), texts["en"])
	}
	found := false
	for _, declaration := range after {
		if declaration.Name == "example_counter" {
			found = declaration.Origin == OriginCore && declaration.Columns[1].Unit == "hit"
		}
	}
	if !found {
		t.Error("the added directory is not a core dataset")
	}
}
