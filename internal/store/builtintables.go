package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/plugintables"
)

// The built-in plugins' tables and relations, as a started service has them (ADR 0020). The test
// template is built with them, so every test database holds the installation model the core's Go
// still writes through the bridge; the plugin loader then finds each table as declared and creates
// nothing. The manifests are read from the embedded built-ins and converted by the contract's own
// conversion, the loader's, so the two never disagree.

// builtinManifests reads every built-in manifest, in load order: a required plugin before the plugins
// requiring it, by name among the ready ones.
func builtinManifests() ([]contract.Manifest, map[string]fs.FS, error) {
	tree := opnview.BuiltinPlugins()
	entries, err := fs.ReadDir(tree, ".")
	if err != nil {
		return nil, nil, err
	}
	manifests := map[string]contract.Manifest{}
	directories := map[string]fs.FS{}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory, err := fs.Sub(tree, entry.Name())
		if err != nil {
			return nil, nil, err
		}
		raw, err := fs.ReadFile(directory, contract.ManifestFile)
		if err != nil {
			continue
		}
		manifest, err := contract.DecodeManifest(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("the built-in %s: %w", entry.Name(), err)
		}
		manifests[entry.Name()] = manifest
		directories[entry.Name()] = directory
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	loaded := map[string]bool{}
	var ordered []contract.Manifest
	for len(names) > 0 {
		progressed := false
		for index := 0; index < len(names); index++ {
			name := names[index]
			ready := true
			for _, required := range manifests[name].Requires {
				if _, builtin := manifests[required]; builtin && !loaded[required] {
					ready = false
				}
			}
			if ready {
				ordered = append(ordered, manifests[name])
				loaded[name] = true
				names = append(names[:index], names[index+1:]...)
				progressed = true
				index--
			}
		}
		if !progressed {
			return nil, nil, fmt.Errorf("the built-ins %v require each other", names)
		}
	}
	return ordered, directories, nil
}

// EnsureBuiltinTables creates the built-in plugins' declared tables and applies their relation files, as
// loading them does; a table already declared is kept.
func (s *Store) EnsureBuiltinTables(ctx context.Context) error {
	manifests, directories, err := builtinManifests()
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		tables, refusal := contract.DeclaredTables(manifest)
		if refusal != nil {
			return fmt.Errorf("the built-in %s: %w", manifest.Name, refusal)
		}
		for _, table := range tables {
			if _, err := s.EnsurePluginTable(ctx, table); err != nil {
				return fmt.Errorf("the built-in %s: %w", manifest.Name, err)
			}
		}
		var files []plugintables.RelationFile
		for _, name := range manifest.Relations {
			raw, err := fs.ReadFile(directories[manifest.Name], name)
			if err != nil {
				return fmt.Errorf("the built-in %s: the relation file %s: %w", manifest.Name, name, err)
			}
			files = append(files, plugintables.RelationFile{Name: name, Text: string(raw)})
		}
		if err := s.ApplyRelations(ctx, manifest.Name, files); err != nil {
			return fmt.Errorf("the built-in %s: %w", manifest.Name, err)
		}
	}
	return nil
}

// builtinTablesText is what the built-ins declare of tables and relations, for the test template's
// name: a change to any of it builds a new template.
func builtinTablesText() ([]byte, error) {
	manifests, directories, err := builtinManifests()
	if err != nil {
		return nil, err
	}
	var text []byte
	for _, manifest := range manifests {
		raw, err := fs.ReadFile(directories[manifest.Name], contract.ManifestFile)
		if err != nil {
			return nil, err
		}
		text = append(text, raw...)
		for _, name := range manifest.Relations {
			relation, err := fs.ReadFile(directories[manifest.Name], name)
			if err != nil {
				return nil, err
			}
			text = append(text, relation...)
		}
	}
	return text, nil
}
