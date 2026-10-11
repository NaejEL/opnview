package plugin

import (
	"strings"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/store"
)

// Every table a loaded plugin declares is a dataset of the data catalogue (P8 of
// specs/SPEC-backend-completion-plugins.md), named <plugin>.<table>, its columns as the manifest
// declares them, and dated by the plugin's own provider. Nothing else is needed: dropping the
// plugin's directory is what adds the dataset.

// Datasets returns the datasets of every loaded plugin.
func (l *Loaded) Datasets() []dataset.Declaration {
	var result []dataset.Declaration
	for _, plugin := range l.plugins {
		result = append(result, plugin.datasets...)
	}
	return result
}

// pluginDataset is the dataset of one of a plugin's tables.
func pluginDataset(plugin string, declaration TableDeclaration, table store.PluginTable,
	providers []ProviderInfo) (dataset.Declaration, error) {
	columns := make([]dataset.Column, 0, len(declaration.Columns))
	for _, column := range declaration.Columns {
		columns = append(columns, dataset.Column{Name: column.Name, Type: column.Type, Role: column.Role,
			Unit: column.Unit, Nullable: column.Nullable, CarriesDomain: column.CarriesDomain,
			Aggregations: column.Aggregations})
	}
	var retention *dataset.Retention
	if table.TimeColumn != "" {
		retention = &dataset.Retention{Column: table.TimeColumn, Seconds: table.RetentionSeconds}
	}
	return dataset.PluginDataset(plugin, table.Table, quoteIdentifier(store.PluginSchema(plugin))+"."+
		quoteIdentifier(table.Table), declaration.Nature, freshnessOf(plugin, providers), retention, columns)
}

// freshnessOf is the provider that dates a plugin's datasets: its first provider of a kind of its
// own, which is what writes into its tables, or else its first provider. A plugin with no provider
// names its own name, which no provider row carries, so its condition reads state_unknown.
func freshnessOf(plugin string, providers []ProviderInfo) dataset.Freshness {
	for _, info := range providers {
		if !provider.IsCoreKind(info.Kind) {
			return dataset.Freshness{Kind: info.Kind, ProviderKey: info.Key}
		}
	}
	if len(providers) > 0 {
		return dataset.Freshness{Kind: providers[0].Kind, ProviderKey: providers[0].Key}
	}
	return dataset.Freshness{Kind: plugin}
}

// quoteIdentifier quotes a PostgreSQL identifier.
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
