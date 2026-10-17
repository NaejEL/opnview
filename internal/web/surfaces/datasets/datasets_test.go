package datasets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/datasets"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// The example plugin of these tests: a meter with a kind of its own and one table, a sample
// dataset with a time column, two dimensions and a measure with a unit.
const (
	meterPlugin  = "example_meter"
	meterDataset = "example_meter.readings"
)

var meterFiles = map[string]string{
	"plugin.json": `{"api_version": 1, "name": "example_meter", "version": "0.1.0",
		"display_name": "Example meter", "script": "example_meter.js",
		"endpoints": [{"name": "status", "path": "/api/examplemeter/service/status", "method": "GET"}],
		"providers": [{"kind": "example_reading", "display_name": "Example meter",
			"cadence_seconds": 300, "min_cadence_seconds": 60}],
		"tables": [{"name": "readings", "nature": "sample",
			"columns": [
				{"name": "sampled_at", "type": "integer", "role": "time"},
				{"name": "meter", "type": "text"},
				{"name": "phase", "type": "text"},
				{"name": "value", "type": "real", "role": "measure", "unit": "watt"}],
			"primary_key": ["meter", "phase", "sampled_at"],
			"retention": {"column": "sampled_at", "seconds": 2592000}}]}`,
	"example_meter.js": `exports.example_reading = {
		probe: function () { return { result: { probe: "status", state: "unavailable",
			outcome: "plugin_not_installed" } }; },
		collect: function () { return { result: { probe: "status", state: "unavailable",
			outcome: "plugin_not_installed" }, rows: {}, state: {} }; } };`,
}

// reading is one row of the example table.
type reading struct {
	at           int64
	meter, phase string
	value        float64
}

// catalogueHarness is a signed-in server with the example plugin dropped into its data directory,
// and the readings it stored: three meters, two phases, a reading every ten minutes over the last
// sixteen hours but for a silent stretch, so some buckets hold no reading.
func catalogueHarness(t *testing.T) (*webtest.Harness, []reading) {
	t.Helper()
	dataDir := t.TempDir()
	for name, content := range meterFiles {
		path := filepath.Join(dataDir, plugin.PluginsDirectory, meterPlugin, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	harness := webtest.NewAdjusted(t, dataDir, true, false, func(options *web.Options) {
		loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: options.Store,
			Sealer: options.Box})
		if err != nil {
			t.Fatalf("loading the plugins: %v", err)
		}
		if _, found := loaded.Plugin(meterPlugin); !found {
			t.Fatalf("the example plugin was refused: %v", loaded.Refusals())
		}
		options.Plugins = loaded
	})
	harness.CompleteSetup()
	now := harness.Clock.Now().Unix()
	var readings []reading
	rows := []map[string]any{}
	for step := int64(1); step <= 96; step++ {
		if step >= 40 && step < 48 {
			continue // the silent stretch
		}
		for meterIndex, meter := range []string{"a", "b", "c"} {
			for phaseIndex, phase := range []string{"one", "two"} {
				value := float64(step%7) + float64(meterIndex)*10 + float64(phaseIndex)*0.5
				at := now - step*600
				readings = append(readings, reading{at: at, meter: meter, phase: phase, value: value})
				rows = append(rows, map[string]any{"sampled_at": at, "meter": meter, "phase": phase, "value": value})
			}
		}
	}
	if err := harness.Store.WritePluginPass(context.Background(), meterPlugin, meterPlugin,
		plugintables.PluginRows{"readings": rows}, nil, now); err != nil {
		t.Fatal(err)
	}
	return harness, readings
}

// query runs one catalogue query and returns its status and its body.
func query(t *testing.T, harness *webtest.Harness, name string, parameters any) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}
	answer := harness.Get("/api/datasets/" + name + "/query?" + widget.ParametersQueryKey + "=" +
		url.QueryEscape(string(encoded)))
	return answer.StatusCode, webtest.ReadBody(t, answer)
}

// answered decodes a 200 answer.
func answered(t *testing.T, status int, body string) datasets.DatasetResponse {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("the query answered %d: %.400s", status, body)
	}
	var response datasets.DatasetResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("the answer is not the envelope: %v", err)
	}
	return response
}

// timeSeries decodes a time-axis answer's data.
func timeSeries(t *testing.T, response datasets.DatasetResponse) datasets.DatasetTimeData {
	t.Helper()
	encoded, _ := json.Marshal(response.Data)
	var data datasets.DatasetTimeData
	if err := json.Unmarshal(encoded, &data); err != nil || len(data.Series) == 0 {
		t.Fatalf("the data is not a time axis: %s", encoded)
	}
	return data
}

// rowsOf decodes a dimension-axis answer's data.
func rowsOf(t *testing.T, response datasets.DatasetResponse) datasets.DatasetRowsData {
	t.Helper()
	encoded, _ := json.Marshal(response.Data)
	var data datasets.DatasetRowsData
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatalf("the data is not a dimension axis: %s", encoded)
	}
	return data
}

// TestTheListingCarriesTheCustomChartSourcesAndEveryPluginTable is P-AC15: the five custom_chart
// sources and the plugin's table are listed with every P8 field; no set-aside table, credential
// table or plugin state is in the payload; without a session the listing answers 401 in JSON.
func TestTheListingCarriesTheCustomChartSourcesAndEveryPluginTable(t *testing.T) {
	t.Parallel()
	harness, _ := catalogueHarness(t)
	// A table set aside by a changed declaration, which must not be listed.
	gone := plugindecl.PluginTable{Plugin: "example_gone", Table: "samples", TimeColumn: "sampled_at",
		Columns: []plugindecl.PluginColumn{{Name: "sampled_at", Type: "integer", Role: plugindecl.RoleTime},
			{Name: "value", Type: "real", Role: plugindecl.RoleMeasure}}}
	if _, err := harness.Store.EnsurePluginTable(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	gone.Columns = append(gone.Columns, plugindecl.PluginColumn{Name: "note", Type: "text", Nullable: true})
	if event, err := harness.Store.EnsurePluginTable(context.Background(), gone); err != nil || event == nil {
		t.Fatalf("setting the table aside gave %v, %v", event, err)
	}

	answer := harness.Get(webtest.PathDatasets)
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the listing answered %d: %.300s", answer.StatusCode, body)
	}
	var listing datasets.DatasetListing
	if err := json.Unmarshal([]byte(body), &listing); err != nil {
		t.Fatal(err)
	}
	byName := map[string]datasets.ListedDataset{}
	for _, listed := range listing.Datasets {
		byName[listed.Name] = listed
	}
	for _, source := range append(params.SeriesSources, meterDataset) {
		listed, found := byName[source]
		if !found {
			t.Errorf("the dataset %s is not listed", source)
			continue
		}
		if listed.Origin == "" || listed.Label == "" || listed.Nature == "" || listed.TimeColumn == nil ||
			listed.Freshness.ProviderKind == "" || listed.Freshness.Condition == "" || len(listed.Columns) == 0 {
			t.Errorf("%s is listed without a P8 field: %+v", source, listed)
		}
		measures := 0
		for _, column := range listed.Columns {
			if column.Role == dataset.RoleMeasure {
				measures++
				if len(column.Aggregations) == 0 {
					t.Errorf("the measure %s.%s admits no aggregation", source, column.Name)
				}
			}
		}
		if measures == 0 {
			t.Errorf("%s lists no measure", source)
		}
	}
	meter := byName[meterDataset]
	if meter.Origin != meterPlugin || meter.Label != "plugin.example_meter.dataset.readings" ||
		meter.Nature != dataset.NatureSample || *meter.TimeColumn != "sampled_at" ||
		meter.RetentionSeconds == nil || *meter.RetentionSeconds != 2592000 || meter.RetentionHorizonAt == nil ||
		meter.Freshness.ProviderKind != "example_reading" {
		t.Errorf("the plugin dataset is listed as %+v", meter)
	}
	value := meter.Columns[3]
	if value.Name != "value" || value.Role != dataset.RoleMeasure || value.Unit == nil || *value.Unit != "watt" ||
		value.Type != dataset.TypeReal || value.Nullable || value.CarriesDomain {
		t.Errorf("the measure is listed as %+v", value)
	}
	for _, excluded := range []string{`"account"`, `"session"`, "encrypted_credential", "plugin_state",
		plugindecl.AsideSchema, plugintables.AsidePrefix, "example_gone"} {
		if strings.Contains(body, excluded) {
			t.Errorf("the listing carries %s", excluded)
		}
	}
	if dns := byName["dns"]; !carriesDomain(dns, "domain") {
		t.Errorf("the dns dataset does not say its domain carries a domain name: %+v", dns.Columns)
	}

	anonymous, err := http.Get(harness.HTTP.URL + webtest.PathDatasets)
	if err != nil {
		t.Fatal(err)
	}
	anonymousBody := webtest.ReadBody(t, anonymous)
	if anonymous.StatusCode != http.StatusUnauthorized || !strings.Contains(anonymousBody, `"condition"`) ||
		strings.Contains(anonymousBody, meterDataset) {
		t.Errorf("without a session the listing answered %d: %.200s", anonymous.StatusCode, anonymousBody)
	}
	anonymous, err = http.Get(harness.HTTP.URL + "/api/datasets/" + meterDataset + "/query")
	if err != nil {
		t.Fatal(err)
	}
	if body := webtest.ReadBody(t, anonymous); anonymous.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(body, `"condition"`) {
		t.Errorf("without a session a query answered %d: %.200s", anonymous.StatusCode, body)
	}
}

func carriesDomain(listed datasets.ListedDataset, name string) bool {
	for _, column := range listed.Columns {
		if column.Name == name {
			return column.CarriesDomain
		}
	}
	return false
}

// treeDigest is a digest of every file under the given roots but the excluded directory, by
// relative path, so a test can show that what it did changed nothing else.
func treeDigest(t *testing.T, excluded string, roots ...string) string {
	t.Helper()
	var lines []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path == excluded {
					return filepath.SkipDir
				}
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			lines = append(lines, path+" "+hex.EncodeToString(sum[:]))
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// TestAPluginDatasetIsQueriedWithNothingButItsDirectory is P-AC16: the plugin's table is queried
// on its time axis with every aggregation it admits, then on a dimension axis with a filter and a
// split, and every figure equals an independent SQL computation over the table. Nothing but the
// plugin's own directory was written to make that possible: the source tree and the data directory
// hash the same before and after.
func TestAPluginDatasetIsQueriedWithNothingButItsDirectory(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{filepath.Join(repository, "internal"), filepath.Join(repository, "plugins")}
	before := treeDigest(t, "", sources...)
	harness, _ := catalogueHarness(t)
	pluginDirectory := filepath.Join(harness.DataDir, plugin.PluginsDirectory, meterPlugin)
	dataBefore := treeDigest(t, pluginDirectory, harness.DataDir)
	now := harness.Clock.Now().Unix()
	from := now - 86400
	db := harness.Store.DB()

	for _, aggregation := range []string{"sum", "mean", "min", "max", "last", "count"} {
		status, body := query(t, harness, meterDataset, map[string]any{"period": "24h",
			"x": map[string]any{"column": "sampled_at", "bucket": 3600},
			"y": []any{map[string]any{"column": "value", "aggregation": aggregation}}})
		data := timeSeries(t, answered(t, status, body))
		sqlAggregate := map[string]string{"sum": "sum(value)", "mean": "avg(value)", "min": "min(value)",
			"max": "max(value)", "count": "count(value)",
			"last": "(array_agg(value ORDER BY sampled_at DESC))[1]"}[aggregation]
		expected := map[int64]float64{}
		rows, err := db.Query("SELECT sampled_at - sampled_at % 3600, "+sqlAggregate+
			" FROM plugin_example_meter.readings WHERE sampled_at >= $1 AND sampled_at < $2 GROUP BY 1", from, now)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var start int64
			var value float64
			if err := rows.Scan(&start, &value); err != nil {
				t.Fatal(err)
			}
			expected[start] = value
		}
		_ = rows.Close()
		seen := 0
		for _, bucket := range data.Series[0].Buckets {
			start := bucket.From - bucket.From%3600
			want, present := expected[start]
			switch {
			case present && (bucket.Value == nil || math.Abs(*bucket.Value-want) > 1e-9):
				t.Errorf("%s: the bucket at %d reads %v, the table %v", aggregation, start, bucket.Value, want)
			case !present && (bucket.Value != nil || !bucket.Gap):
				t.Errorf("%s: the bucket at %d holds no reading and reads %v (gap %v)", aggregation, start,
					bucket.Value, bucket.Gap)
			case present:
				seen++
			}
		}
		if seen != len(expected) {
			t.Errorf("%s: %d buckets matched the table's %d", aggregation, seen, len(expected))
		}
	}

	status, body := query(t, harness, meterDataset, map[string]any{"period": "24h",
		"x":        map[string]any{"column": "meter"},
		"y":        []any{map[string]any{"column": "value", "aggregation": "sum"}},
		"filters":  []any{map[string]any{"column": "meter", "op": "in", "value": []string{"a", "c"}}},
		"split_by": map[string]any{"column": "phase", "limit": 1}})
	data := rowsOf(t, answered(t, status, body))
	type cell struct{ meter, phase string }
	expected := map[cell]float64{}
	rows, err := db.Query(`SELECT meter, CASE WHEN phase = (SELECT phase FROM plugin_example_meter.readings
		WHERE sampled_at >= $1 AND sampled_at < $2 AND meter IN ('a', 'c') GROUP BY phase ORDER BY sum(value) DESC,
		phase LIMIT 1) THEN phase ELSE '' END, sum(value)
		FROM plugin_example_meter.readings WHERE sampled_at >= $3 AND sampled_at < $4 AND meter IN ('a', 'c')
		GROUP BY 1, 2`, from, now, from, now)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key cell
		var sum float64
		if err := rows.Scan(&key.meter, &key.phase, &sum); err != nil {
			t.Fatal(err)
		}
		expected[key] = sum
	}
	_ = rows.Close()
	if data.Members != 2 || len(data.Rows) != len(expected) {
		t.Fatalf("the dimension axis has %d members and %d rows, the table %d cells: %+v", data.Members,
			len(data.Rows), len(expected), data.Rows)
	}
	for _, row := range data.Rows {
		key := cell{meter: fmt.Sprint(row.Key)}
		if row.Split != nil && !row.Split.Remainder {
			key.phase = fmt.Sprint(row.Split.Value)
		}
		want, present := expected[key]
		if !present || row.Values[0] == nil || math.Abs(*row.Values[0]-want) > 1e-9 {
			t.Errorf("the row %+v reads %v, the table %v (present %v)", key, row.Values[0], want, present)
		}
	}
	if data.Rows[0].Key != "c" {
		t.Errorf("the rows are not ranked by their sum: the first is %v", data.Rows[0].Key)
	}

	if after := treeDigest(t, "", sources...); after != before {
		t.Error("the source tree changed while a plugin dataset was added and queried")
	}
	if after := treeDigest(t, pluginDirectory, harness.DataDir); after != dataBefore {
		t.Error("a file outside the plugin's directory was written while its dataset was queried")
	}
}

// refused runs a query that must be refused and returns its condition and path.
func refused(t *testing.T, harness *webtest.Harness, name string, parameters any) (int, string, string) {
	t.Helper()
	status, body := query(t, harness, name, parameters)
	var decoded struct {
		Condition string `json:"condition"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the refusal is not JSON: %.300s", body)
	}
	return status, decoded.Condition, decoded.Path
}

// TestARefusedQueryNamesItsCodeAndReachesNoDatabase is P-AC17: a parameter carrying SQL text, an
// undeclared column, a quoted identifier, a semicolon or a comment marker is refused with its
// closed code; every refusal code and every bound has a case; and no refused query runs a
// statement. A text value carrying SQL is a value: it is bound, matches nothing, and changes
// nothing.
func TestARefusedQueryNamesItsCodeAndReachesNoDatabase(t *testing.T) {
	harness, _ := catalogueHarness(t)
	value := func(aggregation string) []any {
		return []any{map[string]any{"column": "value", "aggregation": aggregation}}
	}
	manyValues := make([]string, dataset.MaxFilterValues+1)
	for index := range manyValues {
		manyValues[index] = fmt.Sprint(index)
	}
	manyMeasures := make([]any, dataset.MaxSeries+1)
	for index := range manyMeasures {
		manyMeasures[index] = map[string]any{"column": "value", "aggregation": "mean"}
	}
	cases := []struct {
		name       string
		dataset    string
		parameters any
		status     int
		code       string
	}{
		{"an unknown dataset", "flow; DROP TABLE flow", map[string]any{}, http.StatusNotFound,
			dataset.RefusalUnknownDataset},
		{"SQL text as a column", meterDataset, map[string]any{"y": []any{map[string]any{
			"column": "value) FROM account --", "aggregation": "sum"}}}, http.StatusBadRequest,
			dataset.RefusalUnknownColumn},
		{"an undeclared column", meterDataset, map[string]any{"x": map[string]any{"column": "password_hash"}},
			http.StatusBadRequest, dataset.RefusalUnknownColumn},
		{"a quoted identifier", meterDataset, map[string]any{"split_by": map[string]any{"column": `"meter"`}},
			http.StatusBadRequest, dataset.RefusalUnknownColumn},
		{"a semicolon", meterDataset, map[string]any{"filters": []any{map[string]any{"column": "meter;",
			"op": "eq", "value": "a"}}}, http.StatusBadRequest, dataset.RefusalUnknownColumn},
		{"a comment marker", meterDataset, map[string]any{"y": value("sum -- ")}, http.StatusBadRequest,
			dataset.RefusalAggregationNotAdmitted},
		{"a malformed payload", meterDataset, "not an object", http.StatusBadRequest,
			dataset.RefusalMalformedParameters},
		{"a measure as x", meterDataset, map[string]any{"x": map[string]any{"column": "value"}},
			http.StatusBadRequest, dataset.RefusalColumnRoleNotAdmitted},
		{"an aggregation not admitted", meterDataset, map[string]any{"y": []any{map[string]any{
			"column": "meter", "aggregation": "sum"}}}, http.StatusBadRequest, dataset.RefusalAggregationNotAdmitted},
		{"an operator outside the list", meterDataset, map[string]any{"filters": []any{map[string]any{
			"column": "meter", "op": "= 'a' OR 1=1", "value": "a"}}}, http.StatusBadRequest,
			dataset.RefusalOperatorNotAdmitted},
		{"an operator the type does not admit", meterDataset, map[string]any{"filters": []any{map[string]any{
			"column": "meter", "op": "lt", "value": "a"}}}, http.StatusBadRequest, dataset.RefusalOperatorNotAdmitted},
		{"a value of the wrong type", meterDataset, map[string]any{"filters": []any{map[string]any{
			"column": "value", "op": "gt", "value": "1; DROP TABLE flow"}}}, http.StatusBadRequest,
			dataset.RefusalValueTypeMismatch},
		{"a bucket in words", meterDataset, map[string]any{"x": map[string]any{"column": "sampled_at",
			"bucket": "1 hour"}}, http.StatusBadRequest, dataset.RefusalBucketNotAdmitted},
		{"too many buckets", meterDataset, map[string]any{"period": "30d", "x": map[string]any{
			"column": "sampled_at", "bucket": 60}}, http.StatusBadRequest, dataset.RefusalTooManyBuckets},
		{"too many measures", meterDataset, map[string]any{"y": manyMeasures}, http.StatusBadRequest,
			dataset.RefusalTooManySeries},
		{"too many split values", meterDataset, map[string]any{"split_by": map[string]any{"column": "meter",
			"limit": dataset.MaxSeries + 1}}, http.StatusBadRequest, dataset.RefusalTooManySeries},
		{"too many rows", meterDataset, map[string]any{"x": map[string]any{"column": "meter"},
			"limit": dataset.MaxRows + 1}, http.StatusBadRequest, dataset.RefusalTooManyRows},
		{"too many filter values", meterDataset, map[string]any{"filters": []any{map[string]any{
			"column": "meter", "op": "in", "value": manyValues}}}, http.StatusBadRequest, dataset.RefusalTooManyRows},
	}
	covered := map[string]bool{}
	for _, testCase := range cases {
		before := reads.CatalogueStatementCount()
		var status int
		var code string
		if text, isText := testCase.parameters.(string); isText {
			answer := harness.Get("/api/datasets/" + url.PathEscape(testCase.dataset) + "/query?" +
				widget.ParametersQueryKey + "=" + url.QueryEscape(text))
			body := webtest.ReadBody(t, answer)
			status = answer.StatusCode
			var decoded struct {
				Condition string `json:"condition"`
			}
			_ = json.Unmarshal([]byte(body), &decoded)
			code = decoded.Condition
		} else {
			status, code, _ = refused(t, harness, url.PathEscape(testCase.dataset), testCase.parameters)
		}
		if status != testCase.status || code != testCase.code {
			t.Errorf("%s answered %d %q, want %d %q", testCase.name, status, code, testCase.status, testCase.code)
		}
		if after := reads.CatalogueStatementCount(); after != before {
			t.Errorf("%s ran %d statements", testCase.name, after-before)
		}
		covered[code] = true
	}
	for _, code := range documentedRefusalCodes(t) {
		if !covered[code] {
			t.Errorf("the refusal code %s has no case", code)
		}
	}

	status, body := query(t, harness, meterDataset, map[string]any{"x": map[string]any{"column": "meter"},
		"filters": []any{map[string]any{"column": "meter", "op": "eq", "value": "a'; DROP TABLE flow; --"}}})
	response := answered(t, status, body)
	if rows := rowsOf(t, response); len(rows.Rows) != 0 {
		t.Errorf("a value carrying SQL matched %+v", rows.Rows)
	}
	var flows int
	if err := harness.Store.DB().QueryRow("SELECT count(*) FROM flow").Scan(&flows); err != nil {
		t.Errorf("the flow table is gone: %v", err)
	}
}

// TestGapsAreGapsAndBucketsAlignOnTheAggregatePeriods is P-AC18: an empty bucket of a sample dataset
// is a gap; a covered empty bucket of an event dataset is zero and an uncovered one -- before the
// first row, or inside a collection gap -- is a gap; hour, day, ISO-week and month buckets start on
// the volume aggregates' boundaries; and under no_domains a query touching a domain is withheld.
func TestGapsAreGapsAndBucketsAlignOnTheAggregatePeriods(t *testing.T) {
	t.Parallel()
	harness, _ := catalogueHarness(t)
	now := harness.Clock.Now().Unix()

	status, body := query(t, harness, meterDataset, map[string]any{"period": "24h",
		"x": map[string]any{"column": "sampled_at", "bucket": 900},
		"y": []any{map[string]any{"column": "value", "aggregation": "mean"}}})
	silent := now - 45*600 // inside the silent stretch
	for _, bucket := range timeSeries(t, answered(t, status, body)).Series[0].Buckets {
		if bucket.From <= silent && silent < bucket.To && (!bucket.Gap || bucket.Value != nil) {
			t.Errorf("an empty bucket of a sample dataset reads %v (gap %v)", bucket.Value, bucket.Gap)
		}
	}

	// The filter log: flows every five minutes over the last twelve hours but for one quiet hour, and
	// a collection gap of the pf provider over another.
	quietFrom, quietTo := now-6*3600, now-5*3600
	gapFrom, gapTo := now-9*3600, now-8*3600
	var instants []int64
	for at := now - 12*3600; at < now; at += 300 {
		if (at >= quietFrom && at < quietTo) || (at >= gapFrom && at < gapTo) {
			continue
		}
		instants = append(instants, at)
	}
	collectiontest.StoreFlows(harness, instants...)
	collectiontest.RecordGap(harness, "firewall_log", "pf", vocab.GapDigestOutsideWindow, gapFrom, gapTo)
	status, body = query(t, harness, "traffic", map[string]any{"period": "24h",
		"x": map[string]any{"column": "observed_at", "bucket": 3600},
		"y": []any{map[string]any{"column": "connections", "aggregation": "sum"}}})
	response := answered(t, status, body)
	if response.Window == nil || response.Window.Covered == nil || response.Window.Covered.From != now-12*3600 {
		t.Fatalf("the traffic window is %+v", response.Window)
	}
	for _, bucket := range timeSeries(t, response).Series[0].Buckets {
		switch {
		case bucket.To <= now-12*3600:
			if !bucket.Gap || bucket.Value != nil {
				t.Errorf("a bucket before the first flow reads %v (gap %v)", bucket.Value, bucket.Gap)
			}
		case bucket.From >= gapFrom && bucket.To <= gapTo:
			if !bucket.Gap || bucket.Value != nil {
				t.Errorf("a bucket inside the collection gap reads %v (gap %v)", bucket.Value, bucket.Gap)
			}
		case bucket.From >= quietFrom && bucket.To <= quietTo:
			if bucket.Gap || bucket.Value == nil || *bucket.Value != 0 {
				t.Errorf("a covered bucket with no flow reads %v (gap %v), not a zero", bucket.Value, bucket.Gap)
			}
		}
	}

	for _, alignment := range []struct {
		period string
		bucket int64
		name   string
		starts func(int64) bool
	}{
		{"24h", 3600, dataset.AlignmentEpoch, func(at int64) bool { return at%3600 == 0 }},
		{"7d", 86400, dataset.AlignmentEpoch, func(at int64) bool { return at%86400 == 0 }},
		{"30d", 7 * 86400, dataset.AlignmentISOWeek, func(at int64) bool {
			instant := time.Unix(at, 0).UTC()
			return instant.Weekday() == time.Monday && at%86400 == 0
		}},
		{"30d", 30 * 86400, dataset.AlignmentCalendarMonth, func(at int64) bool {
			instant := time.Unix(at, 0).UTC()
			return instant.Day() == 1 && at%86400 == 0
		}},
	} {
		status, body := query(t, harness, "traffic", map[string]any{"period": alignment.period,
			"x": map[string]any{"column": "observed_at", "bucket": alignment.bucket}})
		data := timeSeries(t, answered(t, status, body))
		if data.X.Alignment != alignment.name {
			t.Errorf("a %d-second bucket is aligned %q, want %q", alignment.bucket, data.X.Alignment, alignment.name)
		}
		for index, bucket := range data.Series[0].Buckets {
			if index > 0 && !alignment.starts(bucket.From) {
				t.Errorf("a %d-second bucket starts at %s", alignment.bucket, time.Unix(bucket.From, 0).UTC())
			}
		}
	}

	if err := harness.Store.SetSetting(context.Background(), config.KeyAggregateMode, request.AggregateModeNoDomains,
		now); err != nil {
		t.Fatal(err)
	}
	status, body = query(t, harness, "dns", map[string]any{"split_by": map[string]any{"column": "domain"}})
	if withheld := answered(t, status, body); withheld.Condition != envelope.ConditionWithheldByAggregateMode ||
		withheld.Data != nil || !strings.Contains(body, `"data":null`) {
		t.Errorf("a query splitting on a domain under no_domains answered %s", body)
	}
	status, body = query(t, harness, "dns", map[string]any{"x": map[string]any{"column": "resolver"}})
	if open := answered(t, status, body); open.Condition == envelope.ConditionWithheldByAggregateMode {
		t.Errorf("a query touching no domain was withheld: %s", body)
	}
}

// TestACustomChartSeriesReadsAPluginDataset is P-AC19's new half: a custom_chart series whose
// source is the plugin's dataset answers in the full 5B envelope, its buckets equal to the
// catalogue query's.
func TestACustomChartSeriesReadsAPluginDataset(t *testing.T) {
	t.Parallel()
	harness, _ := catalogueHarness(t)
	encoded, _ := json.Marshal(map[string]any{"period": "24h", "series": []any{map[string]any{
		"source": meterDataset, "measure": "value", "aggregation": "max", "label": "Meter"}}})
	answer := harness.Get(widget.PathPrefix + "custom_chart?" + widget.ParametersQueryKey + "=" +
		url.QueryEscape(string(encoded)))
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("custom_chart answered %d: %.300s", answer.StatusCode, body)
	}
	var response envelope.Response
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.InvalidParameters) != 0 || len(response.UnknownParameters) != 0 {
		t.Errorf("the dataset series was not accepted: %+v %+v", response.InvalidParameters, response.UnknownParameters)
	}
	encodedData, _ := json.Marshal(response.Data)
	var chart struct {
		Series []traffic.Series `json:"series"`
	}
	if err := json.Unmarshal(encodedData, &chart); err != nil || len(chart.Series) != 1 {
		t.Fatalf("the chart is %s", encodedData)
	}
	series := chart.Series[0]
	if series.Window == nil || series.Gaps == nil || series.Availability == nil || series.Condition == "" ||
		series.Unit != "watt" || series.Measure != "value" || len(series.Buckets) != 96 {
		t.Errorf("the series is %+v", series)
	}
	status, catalogueBody := query(t, harness, meterDataset, map[string]any{"period": "24h",
		"x": map[string]any{"column": "sampled_at", "bucket": 900},
		"y": []any{map[string]any{"column": "value", "aggregation": "max"}}})
	expected := timeSeries(t, answered(t, status, catalogueBody)).Series[0].Buckets
	for index, bucket := range series.Buckets {
		want := expected[index]
		if bucket.From != want.From || bucket.Gap != want.Gap || (bucket.Value == nil) != (want.Value == nil) ||
			(bucket.Value != nil && *bucket.Value != *want.Value) {
			t.Errorf("bucket %d is %+v, the catalogue's %+v", index, bucket, want)
		}
	}
}

// documentedRefusalCodes are the codes of the refusal table of docs/widget-api.md, "Data catalogue",
// which internal/dataset's TestTheDocumentedRefusalsAreTheRefusals holds equal to the codes the
// compiler refuses with.
func documentedRefusalCodes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "widget-api.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "\n## Data catalogue\n")
	if start < 0 {
		t.Fatal("docs/widget-api.md has no Data catalogue section")
	}
	section := text[start+1:]
	header := strings.Index(section, "| Condition | When |")
	if header < 0 {
		t.Fatal("the Data catalogue section has no refusal table")
	}
	var codes []string
	for _, line := range strings.Split(section[header:], "\n")[2:] {
		if !strings.HasPrefix(line, "| `") {
			break
		}
		codes = append(codes, strings.Trim(strings.Split(line, "|")[1], " `"))
	}
	if len(codes) == 0 {
		t.Fatal("the refusal table lists no code")
	}
	return codes
}
