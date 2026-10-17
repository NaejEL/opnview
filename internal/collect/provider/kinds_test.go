package provider

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestAddingAKindFileAddsAKind is PA-AC3's second half: a kind is a file. A tree holding the embedded
// kind files and one more file reads that kind, its attributes and its contract, with no Go change.
func TestAddingAKindFileAddsAKind(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{}
	if err := fs.WalkDir(kindFiles, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(kindFiles, path)
		tree[path] = &fstest.MapFile{Data: raw}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tree["kinds/example_meter_reading.json"] = &fstest.MapFile{Data: []byte(`{
  "kind": "example_meter_reading",
  "description": "A reading of an example meter.",
  "concurrent": true,
  "push": false,
  "bindable": true,
  "functions": [
    {"name": "probe", "arguments": "host", "returns": "probe_answer", "description": "Asks whether it answers."}
  ],
  "records": [
    {"name": "probe_answer", "description": "What probe returns.", "fields": [
      {"name": "result", "type": "record", "of": "result", "required": true, "description": "The result."},
      {"name": "level", "type": "integer", "min": 0, "max": 100, "description": "The level."}
    ]}
  ]
}`)}
	kinds, shared, err := ReadKinds(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != len(Kinds())+1 || len(shared) != len(SharedRecords()) {
		t.Fatalf("the tree holds %d kinds, want %d", len(kinds), len(Kinds())+1)
	}
	for _, kind := range kinds {
		if kind.Kind != "example_meter_reading" {
			continue
		}
		if !kind.Concurrent || kind.Push || !kind.Bindable {
			t.Errorf("the added kind's attributes are %+v", kind.AttributesOf())
		}
		record, found := LookupRecord(kind, "probe_answer")
		if !found {
			t.Fatal("the added kind's answer table is missing")
		}
		level, _ := record.Field("level")
		if level.Min == nil || *level.Min != 0 || level.Max == nil || *level.Max != 100 {
			t.Errorf("the added kind's bounds are %+v", level)
		}
		return
	}
	t.Fatal("the added kind file was not read")
}

// TestAKindFileThatContradictsItselfIsRefused: a pushable kind names the table its entries fill, a
// core-only kind has no contract, a record names only tables the file declares.
func TestAKindFileThatContradictsItselfIsRefused(t *testing.T) {
	t.Parallel()
	shared, err := fs.ReadFile(kindFiles, "shared_records.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"push_without_record": `{"kind": "push_without_record", "description": "x", "push": true,
			"functions": [{"name": "probe", "arguments": "host", "returns": "result", "description": "x"}]}`,
		"core_only_with_contract": `{"kind": "core_only_with_contract", "description": "x", "core_only": "derived",
			"functions": [{"name": "probe", "arguments": "host", "returns": "result", "description": "x"}]}`,
		"unknown_table": `{"kind": "unknown_table", "description": "x",
			"functions": [{"name": "probe", "arguments": "host", "returns": "nowhere", "description": "x"}]}`,
	} {
		tree := fstest.MapFS{"shared_records.json": &fstest.MapFile{Data: shared},
			"kinds/" + name + ".json": &fstest.MapFile{Data: []byte(content)}}
		if _, _, err := ReadKinds(tree); err == nil {
			t.Errorf("the kind file %s was accepted", name)
		}
	}
}

// TestTheKindFilesLoadAndCarryTheirAttributes: the embedded files read without error, and the
// attributes that replaced the closed lists are data.
func TestTheKindFilesLoadAndCarryTheirAttributes(t *testing.T) {
	t.Parallel()
	if err := KindsError(); err != nil {
		t.Fatal(err)
	}
	pushable, concurrent := 0, 0
	for _, kind := range Kinds() {
		if kind.Push {
			pushable++
		}
		if kind.Concurrent {
			concurrent++
		}
	}
	if pushable == 0 || concurrent == 0 {
		t.Errorf("the kind files mark %d kinds pushable and %d concurrent", pushable, concurrent)
	}
}

// TestThePerKindSourceLayerIsGone is PA-AC22: the per-kind packages, the source set and the plugin
// adapters are absent; a kind's record is defined once, in its kind file.
func TestThePerKindSourceLayerIsGone(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"../firewalllog", "../dhcplease", "../dnslookup", "../flowrecord", "../measurement", "../resolvercache",
		"../resolverlocaldata", "../securityevent", "../ipgeolocation", "../registrabledomain", "../sources",
		"../../plugin/adapters.go", "../../plugin/serviceadapters.go",
	} {
		if _, err := os.Stat(filepath.FromSlash(path)); err == nil {
			t.Errorf("%s still exists", path)
		}
	}
}
