package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/push"
)

// pushManifest is a valid plugin with one push entry into a table of its own, as JSON, for each test
// to break one way.
func pushManifest() map[string]any {
	return map[string]any{
		"api_version": 1, "name": "example_push", "version": "1.0.0", "display_name": "Example push",
		"tables": []any{map[string]any{"name": "lines", "columns": []any{
			map[string]any{"name": "logged_at", "type": "instant"},
			map[string]any{"name": "word", "type": "text"},
			map[string]any{"name": "count", "type": "integer", "nullable": true, "role": "measure"},
		}, "retention": map[string]any{"column": "logged_at"}}},
		"providers": []any{map[string]any{"kind": "example_push_lines", "display_name": "Example lines",
			"source": "push"}},
		"push": []any{map[string]any{"name": "lines", "protocol": "syslog",
			"filter":  map[string]any{"program": []any{"example-program"}},
			"pattern": `^(?P<word>\w+)(?: (?P<count>\d+))?$`, "output_table": "lines"}},
	}
}

// loadManifest loads one plugin from a manifest and optional script, as a built-in.
func loadManifest(t *testing.T, manifest map[string]any, script string) *Loaded {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"example_push/plugin.json": {Data: raw}}
	if script != "" {
		files["example_push/example.js"] = &fstest.MapFile{Data: []byte(script)}
	}
	loaded, err := Load(context.Background(), Options{Builtins: files})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// TestAValidPushDeclarationLoads: the base manifest loads, and its table is an event dataset with
// loss_detectable false, since it accepts UDP syslog.
func TestAValidPushDeclarationLoads(t *testing.T) {
	t.Parallel()
	loaded := loadManifest(t, pushManifest(), "")
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("refused: %+v", refusals)
	}
	datasets := loaded.Datasets()
	if len(datasets) != 1 || datasets[0].Nature != dataset.NatureEvent || datasets[0].LossDetectable == nil ||
		*datasets[0].LossDetectable {
		t.Fatalf("the dataset is %+v", datasets)
	}
	for _, column := range datasets[0].Columns {
		if column.Name == "logged_at" && column.Role != dataset.RoleTime {
			t.Errorf("the instant column has the role %s", column.Role)
		}
		if column.Name == "word" && column.Role != dataset.RoleDimension {
			t.Errorf("the text column has the role %s", column.Role)
		}
	}
	// Over TCP only, loss is detectable.
	manifest := pushManifest()
	manifest["push"].([]any)[0].(map[string]any)["transports"] = []any{"tcp"}
	tcp := loadManifest(t, manifest, "").Datasets()
	if tcp[0].LossDetectable == nil || !*tcp[0].LossDetectable {
		t.Errorf("a TCP-only table is not loss_detectable")
	}
}

// TestEveryPushRefusalHasItsCode is PS-AC10: each refusal of PS3 and of A5 as amended, with its code.
func TestEveryPushRefusalHasItsCode(t *testing.T) {
	t.Parallel()
	entry := func(manifest map[string]any) map[string]any {
		return manifest["push"].([]any)[0].(map[string]any)
	}
	cases := []struct {
		code   string
		mutate func(map[string]any)
		script string
	}{
		{RefusalPushProtocolUnknown, func(m map[string]any) { entry(m)["protocol"] = "sflow" }, ""},
		{RefusalPushFilterInvalid, func(m map[string]any) { entry(m)["filter"] = map[string]any{"program": []any{}} }, ""},
		{RefusalPushPatternInvalid, func(m map[string]any) { entry(m)["pattern"] = `(?P<word>a(b` }, ""},
		{RefusalPushPatternInvalid, func(m map[string]any) { entry(m)["pattern"] = `(?P<not_a_column>\w+)` }, ""},
		{RefusalTableInvalid, func(m map[string]any) {
			m["tables"].([]any)[0].(map[string]any)["columns"].([]any)[1].(map[string]any)["type"] = "json"
		}, ""},
		{RefusalPushTableWithoutInstant, func(m map[string]any) {
			table := m["tables"].([]any)[0].(map[string]any)
			table["columns"].([]any)[0].(map[string]any)["type"] = "integer"
		}, ""},
		{RefusalPushDestinationInvalid, func(m map[string]any) { entry(m)["kind"] = "firewall_log" }, ""},
		{RefusalPushDestinationInvalid, func(m map[string]any) { delete(entry(m), "output_table") }, ""},
		{RefusalPushReplacesUnknown, func(m map[string]any) { entry(m)["replaces"] = "no_such_endpoint" }, ""},
		{RefusalPushElementUnknown, func(m map[string]any) {
			entry(m)["fields"] = map[string]any{"word": "no_such_field"}
		}, ""},
		{RefusalPushElementUnknown, func(m map[string]any) {
			entry(m)["protocol"] = "netflow"
			delete(entry(m), "pattern")
			entry(m)["filter"] = map[string]any{"require": []any{"notAnInformationElement"}}
		}, ""},
		{RefusalPushLivenessBelowFloor, func(m map[string]any) { entry(m)["liveness_seconds"] = 1 }, ""},
		{RefusalPushProviderMissing, func(m map[string]any) {
			m["providers"].([]any)[0].(map[string]any)["source"] = ""
			m["providers"].([]any)[0].(map[string]any)["cadence_seconds"] = 60
			m["script"] = "example.js"
		}, `exports.example_push_lines = { probe: function () {}, collect: function () {} };`},
		{RefusalPushColumnUnfilled, func(m map[string]any) { entry(m)["pattern"] = `^(?P<count>\d+)$` }, ""},
		{RefusalPushParseMissing, func(m map[string]any) {
			delete(entry(m), "pattern")
			entry(m)["parse"] = "lines"
			m["script"] = "example.js"
		}, `exports.push = {};`},
		{RefusalPushDeclarationInvalid, func(m map[string]any) { entry(m)["transports"] = []any{"sctp"} }, ""},
		{RefusalPushDeclarationInvalid, func(m map[string]any) {
			entry(m)["parse"] = "lines"
			m["script"] = "example.js"
		}, `exports.push = { lines: function (m) { return m; } };`},
	}
	for _, c := range cases {
		manifest := pushManifest()
		c.mutate(manifest)
		loaded := loadManifest(t, manifest, c.script)
		refusals := loaded.Refusals()
		if len(refusals) != 1 || refusals[0].Code != c.code {
			t.Errorf("want %s, got %+v", c.code, refusals)
		}
	}
}

// TestACoreKindEntryMustDeclareItsReplacement is PS-AC14's last bullet: an entry delivering a core
// kind its plugin also polls, with no replaces, is refused; the built-in pf declares one, and its
// parity test (plugins/pf/tests/pf_push.test.js) passes.
func TestACoreKindEntryMustDeclareItsReplacement(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugins", "pf", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest["push"].([]any)[0].(map[string]any), "replaces")
	manifest["name"] = "example_push"
	manifest["script"] = "example.js"
	script, err := os.ReadFile(filepath.Join("..", "..", "plugins", "pf", "pf.js"))
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadManifest(t, manifest, string(script))
	if refusals := loaded.Refusals(); len(refusals) != 1 || refusals[0].Code != RefusalPushKindWithoutReplacement {
		t.Errorf("refusals %+v", refusals)
	}
}

// TestBuiltinPushDeclarationsRestOnVerifiedFacts is PS-AC1's second clause and PS-AC16's: every
// built-in push declaration names the survey's push section, that section exists and none of the
// facts it rests on is still unverified, every declaration that replaces polling has a parity test,
// and no built-in subscribes to NetFlow, whose observation point the survey found not exported (S11).
func TestBuiltinPushDeclarationsRestOnVerifiedFacts(t *testing.T) {
	t.Parallel()
	survey, err := os.ReadFile(filepath.Join("..", "..", "docs", "opnsense-api-survey.md"))
	if err != nil {
		t.Fatal(err)
	}
	loaded := BuiltinsLoaded()
	declarations := 0
	for _, plugin := range loaded.Plugins() {
		for _, entry := range plugin.PushEntries() {
			declarations++
			if entry.SurveySection == "" || !strings.Contains(string(survey), "## "+entry.SurveySection) {
				t.Errorf("%s.%s names the survey section %q, which does not exist", plugin.Name, entry.Name,
					entry.SurveySection)
			}
			if entry.Protocol == "netflow" {
				t.Errorf("%s.%s subscribes to NetFlow", plugin.Name, entry.Name)
			}
			if entry.Replaces != "" {
				test := filepath.Join("..", "..", "plugins", plugin.Name, "tests", plugin.Name+"_push.test.js")
				body, err := os.ReadFile(test)
				if err != nil || !strings.Contains(string(body), "give equal records but for the identity") {
					t.Errorf("%s.%s replaces %s with no parity test in %s", plugin.Name, entry.Name, entry.Replaces, test)
				}
			}
		}
	}
	if declarations == 0 {
		t.Fatal("no built-in push declaration was found, so this assertion is vacuous")
	}
	// The facts the built-ins rest on: S1-S4 and S8 (syslog targets, framing, programs, filterlog,
	// Unbound's lines). Each row of the summary table must read verified.
	for _, row := range []string{"| S1 |", "| S2 |", "| S3 |", "| S4 |", "| S8 |"} {
		index := strings.Index(string(survey), row)
		if index < 0 {
			t.Errorf("the survey has no row %s", row)
			continue
		}
		line := string(survey)[index:]
		line = line[:strings.Index(line, "\n")]
		if strings.Contains(line, "UNVERIFIED") || !strings.Contains(line, "verified") {
			t.Errorf("the built-ins rest on an unverified fact: %s", line)
		}
	}
}

// TestThePushedDigestPrefixIsTheCollectors: the plugin package writes the prefix the collector reads.
func TestThePushedDigestPrefixIsTheCollectors(t *testing.T) {
	t.Parallel()
	if !strings.HasPrefix(pushedDigest(push.Record{Fields: map[string]any{"message": "x"}}), "push:") ||
		pushedDigestPrefix != "push:" {
		t.Error("the pushed digest prefix is not push:")
	}
}

// TestAParseFunctionIsTakenFromTheScriptOfTheFirewallsVersion is PS-AC27: a push entry under two
// version ranges calls the parse function of the script the firewall's version selects, and a version
// no range covers fails its batch as firewall_version_unsupported.
func TestAParseFunctionIsTakenFromTheScriptOfTheFirewallsVersion(t *testing.T) {
	t.Parallel()
	manifest := pushManifest()
	entry := manifest["push"].([]any)[0].(map[string]any)
	delete(entry, "pattern")
	entry["parse"] = "lines"
	manifest["script"] = "current.js"
	manifest["script_versions"] = map[string]any{"from": "26.7", "before": "27.1"}
	manifest["scripts"] = []any{map[string]any{"versions": map[string]any{"from": "26.1", "before": "26.7"},
		"script": "older.js"}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parse := func(word string) string {
		return `exports.push = { lines: function (messages) { return messages.map(function (m) {
			return { logged_at: m.timestamp, word: "` + word + `" }; }); } };`
	}
	loaded, err := Load(context.Background(), Options{Builtins: fstest.MapFS{
		"example_push/plugin.json": {Data: raw},
		"example_push/current.js":  {Data: []byte(parse("current"))},
		"example_push/older.js":    {Data: []byte(parse("older"))},
	}})
	if err != nil || len(loaded.Refusals()) != 0 {
		t.Fatalf("loading: %v %+v", err, loaded.Refusals())
	}
	plugin, _ := loaded.Plugin("example_push")
	records := []push.Record{{RouteKey: "example-program", Fields: map[string]any{"message": "x",
		"timestamp": time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}, Instant: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}}
	for version, want := range map[string]string{"26.7.3": "current", "26.1.9": "older", "25.7.1": ""} {
		gate := broker.New(opnsense.NewClient(opnsense.Credentials{}, nil, time.Second), nil, broker.Options{})
		gate.SetFirewallVersion(version)
		session := provider.NewHost(gate, func(context.Context) (decode.Normaliser, error) {
			return decode.Normaliser{}, nil
		}, func() int64 { return 0 })
		sink := &tableSink{binding: plugin.pushes[0], plugin: plugin, environment: PushEnvironment{Session: session}}
		prepared := sink.Prepare(context.Background(), records)
		gate.Close()
		if want == "" {
			if prepared.Failed != 1 || prepared.Counts[string(failureVersionUnsupported)] != 1 {
				t.Errorf("version %s: %+v", version, prepared)
			}
			continue
		}
		rows := prepared.Payload.(tableBatch).rows
		if len(rows) != 1 || rows[0]["word"] != want {
			t.Errorf("version %s parsed with %v, want the %s script", version, rows, want)
		}
	}
}
