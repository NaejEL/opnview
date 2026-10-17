package provider

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// What a set of declarations writes into the registry: each kind's attribute and each provider's row.
// The plugin loader writes them for the plugins it loads (store.RegisterDeclarations); a database built
// with no plugin loaded -- a test template -- writes the built-ins' from their manifests read here, which
// a test holds equal to the loader's.

// CoreDeclarations are the core kinds' attributes and the registry rows the core kinds' files list
// themselves: a core-only kind's historical implementation, which no plugin declares.
func CoreDeclarations() ([]KindAttributes, []Manifest) {
	var kinds []KindAttributes
	var providers []Manifest
	for _, contract := range Kinds() {
		kinds = append(kinds, contract.AttributesOf())
		for _, registered := range contract.Registered {
			providers = append(providers, Manifest{Kind: contract.Kind, Key: registered.Key,
				DisplayName: registered.DisplayName})
		}
	}
	return kinds, providers
}

// SetDeclarations are a provider set's registrations: every kind it knows with its attributes, and the
// registry rows of its implementations and of the core kinds' files.
func SetDeclarations(set *Set) ([]KindAttributes, []Manifest) {
	_, providers := CoreDeclarations()
	return set.AllAttributes(), append(providers, set.Declared()...)
}

// manifestDeclarations is the part of a plugin.json registration reads.
type manifestDeclarations struct {
	Name      string `json:"name"`
	Providers []struct {
		Kind        string `json:"kind"`
		Key         string `json:"key"`
		DisplayName string `json:"display_name"`
	} `json:"providers"`
	Kinds []struct {
		Kind       string `json:"kind"`
		Concurrent bool   `json:"concurrent"`
		Push       bool   `json:"push"`
		Bindable   *bool  `json:"bindable"`
	} `json:"kinds"`
}

// PluginDeclarations reads what the plugins of a tree -- one directory each, holding its plugin.json --
// declare of the registry: their own kinds with their attributes, defaults included, and their
// providers' rows. It reads only those keys and validates nothing else: the plugin loader does.
func PluginDeclarations(plugins fs.FS) ([]KindAttributes, []Manifest, error) {
	entries, err := fs.ReadDir(plugins, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("provider: reading the plugins: %w", err)
	}
	var kinds []KindAttributes
	var providers []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(plugins, path.Join(entry.Name(), "plugin.json"))
		if err != nil {
			return nil, nil, fmt.Errorf("provider: reading %s/plugin.json: %w", entry.Name(), err)
		}
		var manifest manifestDeclarations
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, nil, fmt.Errorf("provider: %s/plugin.json: %w", entry.Name(), err)
		}
		own := map[string]KindAttributes{}
		for _, declaration := range manifest.Providers {
			key := declaration.Key
			if key == "" {
				key = manifest.Name
			}
			providers = append(providers, Manifest{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName})
			if !IsCoreKind(declaration.Kind) {
				own[declaration.Kind] = KindAttributes{Kind: declaration.Kind, Plugin: manifest.Name, Bindable: true}
			}
		}
		for _, declared := range manifest.Kinds {
			attributes := KindAttributes{Kind: declared.Kind, Plugin: manifest.Name, Concurrent: declared.Concurrent,
				Push: declared.Push, Bindable: declared.Bindable == nil || *declared.Bindable}
			own[declared.Kind] = attributes
		}
		for _, attributes := range own {
			kinds = append(kinds, attributes)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].Kind != providers[j].Kind {
			return providers[i].Kind < providers[j].Kind
		}
		return providers[i].Key < providers[j].Key
	})
	return kinds, providers, nil
}
