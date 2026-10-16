package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/dataset"
)

// TestAFixturePluginsTableIsClassifiedAndPlannedFromItsManifest is E29's classification half: the
// fixture plugin of the repository's testdata/plugins, copied into a temporary data directory's
// plugin tree as a third party adds one, loads; its table is growing because its manifest declares a
// time column -- the class sql/schema-checks.sh reads through ci/plugintables, from the declaration
// and never from a list -- and the data catalogue declares a dataset of it whose statements the plan
// sweep plans: a default query and an earliest-row statement. No file the product ships is touched.
func TestAFixturePluginsTableIsClassifiedAndPlannedFromItsManifest(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	source := filepath.Join("..", "..", "testdata", "plugins", "example_peers")
	target := filepath.Join(dataDir, PluginsDirectory, "example_peers")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o750)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, relative), content, 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(t.Context(), Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("the fixture plugin was refused: %+v", refusals)
	}
	member, found := loaded.Plugin("example_peers")
	if !found {
		t.Fatal("the fixture plugin was not loaded")
	}
	if len(member.OwnTables) != 1 || member.OwnTables[0].Table != "sighting" {
		t.Fatalf("the fixture plugin declares the tables %+v", member.OwnTables)
	}
	table := member.OwnTables[0]
	if !table.Growing() || table.TimeColumn != "seen_at" {
		t.Errorf("the table with a time column is not classified growing on seen_at: %+v", table)
	}
	var declared *dataset.Declaration
	for _, candidate := range loaded.Datasets() {
		if candidate.Name == "example_peers.sighting" {
			declared = &candidate
		}
	}
	if declared == nil {
		t.Fatal("the data catalogue declares no dataset of the fixture plugin's table")
	}
	query, refusal := dataset.Parse("", *declared)
	if refusal != nil {
		t.Fatalf("the dataset's default query is refused: %s", refusal.Code)
	}
	if compiled := dataset.Compile(*declared, query, dataset.Window{From: 0, To: 86400}); compiled.Text == "" {
		t.Error("the dataset's default query compiles to nothing for the sweep to plan")
	}
	if dataset.EarliestStatement(*declared) == "" {
		t.Error("the dataset of a table with a time column has no earliest-row statement for the sweep to plan")
	}
}
