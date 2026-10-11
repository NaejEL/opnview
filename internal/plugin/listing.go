package plugin

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// The plugin listing (A5 of the backend-completion cycle): every plugin discovered, built-ins
// included, with what it provides, what it reads, what it contacts, what it owns and what it lets
// the operator set. It is served as JSON by the plugins surface and is what the step-7 application
// draws its plugin page from.
//
// IT CARRIES NO SECRET. A secret setting reports only whether it is set; nothing here reads a
// credential, a ciphertext, a session or a form token.

// The states of a listed plugin.
const (
	// StateLoaded is a plugin loaded and in use.
	StateLoaded = "loaded"
	// StateRefused is a plugin the loader refused, with its closed code.
	StateRefused = "refused"
)

// ListedPlugin is one plugin as the listing reports it.
type ListedPlugin struct {
	Name         string                   `json:"name"`
	Version      string                   `json:"version,omitempty"`
	APIVersion   int                      `json:"api_version,omitempty"`
	DisplayName  string                   `json:"display_name,omitempty"`
	Description  string                   `json:"description,omitempty"`
	Origin       string                   `json:"origin"`
	State        string                   `json:"state"`
	Reason       *ListedRefusal           `json:"reason,omitempty"`
	Providers    []ProviderInfo           `json:"providers"`
	Destinations []string                 `json:"destinations"`
	Tables       []ListedTable            `json:"tables"`
	AsideTables  []store.AsideTable       `json:"aside_tables"`
	Widgets      []string                 `json:"widgets"`
	Settings     []SettingValue           `json:"settings"`
	Events       []store.PluginTableEvent `json:"events"`
	// Endpoints are the endpoints it reads, with what its manifest tells the broker of each and the
	// OPNsense versions each route serves; CorrectionConflicts are those whose mirror corrections lost
	// to another plugin's (mirror_correction_conflict), the plugin loading all the same.
	Endpoints           []ListedEndpoint     `json:"endpoints"`
	CorrectionConflicts []CorrectionConflict `json:"correction_conflicts"`
	// Push are its push declarations, each with its listener state and liveness once the receiver runs
	// (docs/plugins.md, "Push sources"); the API catalogue carries their counters.
	Push []PushEntry `json:"push"`
}

// ListedEndpoint is one endpoint a plugin reads, as the listing reports it.
type ListedEndpoint struct {
	Name          string        `json:"name"`
	Path          string        `json:"path"`
	Method        string        `json:"method"`
	MaxAgeSeconds float64       `json:"max_age_seconds,omitempty"`
	FloorSeconds  float64       `json:"floor_seconds,omitempty"`
	Routes        []ListedRoute `json:"routes"`
}

// ListedRoute is one route of an endpoint and the versions it serves; Versions is null for every
// version.
type ListedRoute struct {
	Path     string        `json:"path"`
	Method   string        `json:"method"`
	Versions *VersionRange `json:"versions"`
}

// ListedRefusal is why a plugin was refused.
type ListedRefusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// ListedTable is one table a plugin owns, with its row count.
type ListedTable struct {
	Name     string `json:"name"`
	Table    string `json:"table"`
	Rows     int64  `json:"rows"`
	Readable bool   `json:"readable"`
}

// ListingStore is what the listing reads of the database: the settings, whether a secret is set,
// and the row counts of the plugins' tables.
type ListingStore interface {
	SettingStore
	PluginTableRows(ctx context.Context, table store.PluginTable) (int64, error)
	AsideTables(ctx context.Context, plugin string) ([]store.AsideTable, error)
}

// Listing returns every plugin of a load: the loaded ones, built-ins first, then the refused ones,
// each group by name.
func (l *Loaded) Listing(ctx context.Context, database ListingStore) ([]ListedPlugin, error) {
	listed := make([]ListedPlugin, 0, len(l.plugins)+len(l.refusals))
	for _, plugin := range l.plugins {
		entry := ListedPlugin{Name: plugin.Name, Version: plugin.Version, APIVersion: plugin.APIVersion,
			DisplayName: plugin.DisplayName, Description: plugin.Description, Origin: plugin.Origin,
			State: StateLoaded, Providers: append([]ProviderInfo{}, plugin.Providers...),
			Destinations: append([]string{}, plugin.Destinations...), Tables: []ListedTable{},
			AsideTables: []store.AsideTable{}, Widgets: []string{},
			Events: append([]store.PluginTableEvent{}, plugin.Events...)}
		entry.Endpoints = plugin.listedEndpoints()
		entry.CorrectionConflicts = append([]CorrectionConflict{}, plugin.CorrectionConflicts...)
		entry.Push = plugin.listedPush()
		for _, table := range plugin.tables {
			rows, err := database.PluginTableRows(ctx, table)
			listedTable := ListedTable{Name: table.Table, Table: table.SQLName(), Rows: rows, Readable: err == nil}
			entry.Tables = append(entry.Tables, listedTable)
		}
		aside, err := database.AsideTables(ctx, plugin.Name)
		if err != nil {
			return nil, err
		}
		entry.AsideTables = append(entry.AsideTables, aside...)
		for _, directory := range plugin.WidgetDirectories {
			entry.Widgets = append(entry.Widgets, filepath.Base(directory))
		}
		sort.Strings(entry.Widgets)
		settings, err := plugin.SettingValues(ctx, database)
		if err != nil {
			return nil, err
		}
		entry.Settings = settings
		listed = append(listed, entry)
	}
	refusals := append([]Refusal(nil), l.refusals...)
	sort.SliceStable(refusals, func(i, j int) bool { return refusals[i].Plugin < refusals[j].Plugin })
	for _, refusal := range refusals {
		listed = append(listed, ListedPlugin{Name: refusal.Plugin, Origin: refusal.Origin, State: StateRefused,
			Reason: &ListedRefusal{Code: refusal.Code, Detail: refusal.Detail}, Providers: []ProviderInfo{},
			Destinations: []string{}, Tables: []ListedTable{}, AsideTables: []store.AsideTable{},
			Widgets: []string{}, Settings: []SettingValue{}, Events: []store.PluginTableEvent{},
			Endpoints: []ListedEndpoint{}, CorrectionConflicts: []CorrectionConflict{}, Push: []PushEntry{}})
	}
	return listed, nil
}

// listedEndpoints is the plugin's endpoints as the listing reports them, in the manifest's order.
func (p *Plugin) listedEndpoints() []ListedEndpoint {
	listed := []ListedEndpoint{}
	for _, declaration := range p.manifest.Endpoints {
		endpoint := p.endpoints[declaration.Name]
		entry := ListedEndpoint{Name: declaration.Name, Path: endpoint.Path, Method: endpoint.Method,
			MaxAgeSeconds: declaration.MaxAgeSeconds, FloorSeconds: declaration.FloorSeconds, Routes: []ListedRoute{}}
		for _, held := range p.routes[declaration.Name] {
			entry.Routes = append(entry.Routes, ListedRoute{Path: held.endpoint.Path, Method: held.endpoint.Method,
				Versions: held.versions})
		}
		listed = append(listed, entry)
	}
	return listed
}
