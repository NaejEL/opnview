package store

import (
	"context"
	"encoding/json"
	"time"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// RegisterProviderDeclarations writes what declarations say of the registry -- kinds with their
// attributes, providers' rows -- in a bounded number of statements (flows.RegisterDeclarations).
func (s *Store) RegisterProviderDeclarations(ctx context.Context, kinds []provider.KindAttributes,
	providers []provider.Manifest, now int64) error {
	rows := make([]vocab.ProviderKind, 0, len(kinds))
	for _, kind := range kinds {
		rows = append(rows, vocab.ProviderKind{Kind: kind.Kind, Concurrent: kind.Concurrent})
	}
	declared := make([]vocab.ProviderDeclaration, 0, len(providers))
	for _, manifest := range providers {
		declared = append(declared, vocab.ProviderDeclaration{Kind: manifest.Kind, Key: manifest.Key,
			DisplayName: manifest.DisplayName})
	}
	return s.RegisterDeclarations(ctx, rows, declared, now)
}

// RegisterBuiltinDeclarations writes the built-ins' registry rows -- the core kinds' files and the
// built-in plugins' manifests -- as loading them writes them. It is what a database built with no plugin
// loaded holds once the built-ins have loaded: the test template is built so (testdatabase.go), and a
// test holds the rows equal to the plugin loader's.
func (s *Store) RegisterBuiltinDeclarations(ctx context.Context) error {
	kinds, providers := provider.CoreDeclarations()
	pluginKinds, pluginProviders, err := provider.PluginDeclarations(opnview.BuiltinPlugins())
	if err != nil {
		return err
	}
	return s.RegisterProviderDeclarations(ctx, append(kinds, pluginKinds...), append(providers, pluginProviders...),
		time.Now().Unix())
}

// builtinDeclarationsText is the built-ins' registry rows as text, which the test template's name is a
// digest of with the schema: a built-in added or changed builds a new template.
func builtinDeclarationsText() ([]byte, error) {
	kinds, providers := provider.CoreDeclarations()
	pluginKinds, pluginProviders, err := provider.PluginDeclarations(opnview.BuiltinPlugins())
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Kinds     []provider.KindAttributes
		Providers []provider.Manifest
	}{append(kinds, pluginKinds...), append(providers, pluginProviders...)})
}
