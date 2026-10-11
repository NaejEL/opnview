package plugin

import (
	"strings"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/push"
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
//
// A TABLE A PUSH ENTRY WRITES (PS13 of the push amendment) is an event dataset unless it says
// otherwise, its columns take the role their type implies when they state none -- an instant is
// time, a number a measure, anything else a dimension -- and it carries loss_detectable: true only
// when every entry writing into it accepts nothing but transports whose loss is detected.
func pluginDataset(plugin string, declaration TableDeclaration, table store.PluginTable,
	providers []ProviderInfo, pushes []*pushBinding) (dataset.Declaration, error) {
	var writers []*pushBinding
	for _, binding := range pushes {
		if binding.table.Table == table.Table {
			writers = append(writers, binding)
		}
	}
	columns := make([]dataset.Column, 0, len(declaration.Columns))
	for _, column := range declaration.Columns {
		role := column.Role
		if role == "" && len(writers) > 0 && column.Name != table.TimeColumn {
			role = push.Role(column.Type)
		}
		columns = append(columns, dataset.Column{Name: column.Name, Type: column.Type, Role: role,
			Unit: column.Unit, Nullable: column.Nullable, CarriesDomain: column.CarriesDomain,
			Aggregations: column.Aggregations})
	}
	var retention *dataset.Retention
	if table.TimeColumn != "" {
		retention = &dataset.Retention{Column: table.TimeColumn, Seconds: table.RetentionSeconds}
	}
	nature := declaration.Nature
	if nature == "" && len(writers) > 0 {
		nature = dataset.NatureEvent
	}
	built, err := dataset.PluginDataset(plugin, table.Table, quoteIdentifier(store.PluginSchema(plugin))+"."+
		quoteIdentifier(table.Table), nature, freshnessOf(plugin, providers), retention, columns)
	if err != nil || len(writers) == 0 {
		return built, err
	}
	detectable := true
	for _, writer := range writers {
		detectable = detectable && writer.lossDetectable()
	}
	built.LossDetectable = &detectable
	return built, nil
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
