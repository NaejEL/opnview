package store

import (
	"context"
	"encoding/json"
	"fmt"
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

// RecordProviderValues replaces the exported values of the given implementations with those given (PA6
// of the plugin-API amendment): two statements whatever their number. An implementation with no registry
// row yet records nothing; the registration writes its row first.
func (s *Store) RecordProviderValues(ctx context.Context, values []provider.ExportedValue,
	implementations []provider.Manifest, now int64) error {
	kinds := make([]string, 0, len(implementations))
	keys := make([]string, 0, len(implementations))
	for _, implementation := range implementations {
		kinds = append(kinds, implementation.Kind)
		keys = append(keys, implementation.Key)
	}
	valueKinds := make([]string, 0, len(values))
	valueKeys := make([]string, 0, len(values))
	names := make([]string, 0, len(values))
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		text, err := json.Marshal(value.Value)
		if err != nil {
			return fmt.Errorf("store: encoding the value %s of %s/%s: %w", value.Name, value.Kind, value.Key, err)
		}
		valueKinds = append(valueKinds, value.Kind)
		valueKeys = append(valueKeys, value.Key)
		names = append(names, value.Name)
		encoded = append(encoded, string(text))
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the providers' values: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx,
		"DELETE FROM provider_value AS v USING provider AS p, unnest(CAST($1 AS TEXT[]), CAST($2 AS TEXT[])) AS d (k, pk) "+
			"WHERE v.provider_id = p.id AND p.kind = d.k AND p.provider_key = d.pk", kinds, keys); err != nil {
		return fmt.Errorf("store: removing the providers' values: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO provider_value (provider_id, name, value, recorded_at) "+
			"SELECT p.id, d.n, CAST(d.v AS JSONB), $5 "+
			"FROM unnest(CAST($1 AS TEXT[]), CAST($2 AS TEXT[]), CAST($3 AS TEXT[]), CAST($4 AS TEXT[])) AS d (k, pk, n, v) "+
			"JOIN provider AS p ON p.kind = d.k AND p.provider_key = d.pk",
		valueKinds, valueKeys, names, encoded, now); err != nil {
		return fmt.Errorf("store: recording the providers' values: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the providers' values: %w", err)
	}
	return nil
}

// ActiveProviderValues reads the named values the active providers export, by name: what a core
// derivation reads of another source's facts (the Go NetFlow join reads pf's state timeouts so). A name
// no active provider exports is absent from the answer; two active providers exporting one name answer the
// lowest-numbered provider's.
func (s *Store) ActiveProviderValues(ctx context.Context, names []string) (map[string]json.RawMessage, error) {
	rows, err := s.SQL.QueryContext(ctx,
		"SELECT DISTINCT ON (v.name) v.name, v.value FROM provider_value AS v JOIN provider AS p ON p.id = v.provider_id "+
			"WHERE p.is_active AND v.name = ANY(CAST($1 AS TEXT[])) ORDER BY v.name, p.id", names)
	if err != nil {
		return nil, fmt.Errorf("store: reading the active providers' values: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := map[string]json.RawMessage{}
	for rows.Next() {
		var name string
		var value []byte
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("store: reading the active providers' values: %w", err)
		}
		values[name] = json.RawMessage(value)
	}
	return values, rows.Err()
}
