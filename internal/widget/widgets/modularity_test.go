package widgets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The built-in widgets as a seam: the plugin tree is the only list of built-in widgets, and
// these tests make every way of breaking that fail by name.

// builtinDirectory is one built-in widget's directory: a built-in widget plugin's widgets/
// directory (plugins/<plugin>/widgets/<type>/ in the repository).
type builtinDirectory struct {
	Type string
	Path string
}

// pluginRoot is the built-in plugin tree on disk, found from this package's directory.
var pluginRoot = filepath.Join("..", "..", "..", "plugins")

// builtinDirectories lists every built-in widget's directory, sorted by type.
func builtinDirectories(t *testing.T) []builtinDirectory {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join(pluginRoot, "*", "widgets", "*", "manifest.json"))
	check(t, err)
	var directories []builtinDirectory
	for _, manifest := range manifests {
		directory := filepath.Dir(manifest)
		directories = append(directories, builtinDirectory{Type: filepath.Base(directory), Path: directory})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].Type < directories[j].Type })
	return directories
}

// builtinTypes is the types of builtinDirectories.
func builtinTypes(t *testing.T) []string {
	t.Helper()
	var types []string
	for _, directory := range builtinDirectories(t) {
		types = append(types, directory.Type)
	}
	return types
}

// TestNoWidgetIsCompiledIntoThisPackage: every built-in widget is a plugin, so no directory of this
// package holds a widget manifest, and no file of it registers a widget's behaviour.
func TestNoWidgetIsCompiledIntoThisPackage(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	check(t, err)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(entry.Name(), "manifest.json")); err == nil {
			t.Errorf("%s holds a widget manifest; a built-in widget is a plugin directory under plugins/", entry.Name())
		}
	}
}

// TestEveryBuiltinWidgetIsADirectoryWithAValidSelfContainedManifest: each directory holds a
// manifest that passes the same validation an operator widget does, names its own directory as
// its type, lists the query files it holds, holds no Go code, and names the script it holds.
func TestEveryBuiltinWidgetIsADirectoryWithAValidSelfContainedManifest(t *testing.T) {
	t.Parallel()
	directories := builtinDirectories(t)
	if len(directories) != 26 {
		t.Errorf("%d built-in widget directories, not the 26 presets of the catalogue", len(directories))
	}
	seen := map[string]bool{}
	for _, directory := range directories {
		if seen[directory.Type] {
			t.Errorf("the type %s has two directories", directory.Type)
		}
		seen[directory.Type] = true
		raw, err := os.ReadFile(filepath.Join(directory.Path, "manifest.json"))
		check(t, err)
		var manifest params.Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Errorf("%s: %v", directory.Path, err)
			continue
		}
		if refusal := params.ValidateManifest(manifest); refusal != nil {
			t.Errorf("%s: %v", directory.Path, refusal)
		}
		if manifest.Type != directory.Type {
			t.Errorf("%s declares the type %q", directory.Path, manifest.Type)
		}
		files, err := os.ReadDir(directory.Path)
		check(t, err)
		hasGo := false
		sqlFiles := map[string]bool{}
		scripts := map[string]bool{}
		for _, file := range files {
			name := file.Name()
			switch {
			case strings.HasSuffix(name, ".go"):
				hasGo = true
			case strings.HasSuffix(name, ".sql"):
				sqlFiles[name] = true
			case strings.HasSuffix(name, ".js"):
				scripts[name] = true
			}
		}
		if hasGo {
			t.Errorf("%s is a widget plugin and holds Go code", directory.Path)
		}
		if manifest.Script != "" && !scripts[manifest.Script] {
			t.Errorf("%s names the script %s, which the directory does not hold", directory.Path, manifest.Script)
		}
		for _, query := range manifest.Queries {
			if !sqlFiles[query] {
				t.Errorf("%s lists the query file %s, which the directory does not hold", directory.Path, query)
			}
			delete(sqlFiles, query)
		}
		for name := range sqlFiles {
			t.Errorf("%s holds %s, which its manifest does not list", directory.Path, name)
		}
	}
}

// TestTheServedBuiltinsAreExactlyTheDirectoriesOfTheTree: no widget is served that has no
// directory and no directory is silently unserved.
func TestTheServedBuiltinsAreExactlyTheDirectoriesOfTheTree(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	served := f.service.Types()
	want := builtinTypes(t)
	if strings.Join(served, ",") != strings.Join(want, ",") {
		t.Errorf("served %v, directories %v", served, want)
	}
	for _, widgetType := range served {
		preset, _ := f.service.Preset(widgetType)
		if preset.Origin != request.OriginBuiltin {
			t.Errorf("%s is %q", widgetType, preset.Origin)
		}
	}
}

// TestNoStatementFileFailedToRegister: every statements file of the tree parsed and no name is
// registered twice; a refusal here would surface only on the first read of a statement. A widget
// plugin's statements are registered when a service first loads it, so one is built first.
func TestNoStatementFileFailedToRegister(t *testing.T) {
	t.Parallel()
	newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	if failures := sqldb.RegistrationFailures(); len(failures) != 0 {
		t.Fatalf("statement registration failed: %v", failures)
	}
	names, err := sqldb.StatementNames()
	check(t, err)
	owners := map[string]int{}
	for _, name := range names {
		if strings.HasPrefix(name, "widget_") {
			owners[sqldb.StatementOwner(name)]++
		}
	}
	if owners["widget"] == 0 {
		t.Error("the widget core registered no statement")
	}
	for _, directory := range builtinDirectories(t) {
		matches, err := filepath.Glob(filepath.Join(directory.Path, "*.sql"))
		check(t, err)
		if len(matches) > 0 && owners["widget:"+directory.Type] == 0 {
			t.Errorf("%s holds statements and registered none", directory.Path)
		}
	}
}
