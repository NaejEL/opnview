package listing

import (
	"context"
	"path/filepath"
	"sort"

	pluginpkg "github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
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
	Name         string                          `json:"name"`
	Version      string                          `json:"version,omitempty"`
	APIVersion   int                             `json:"api_version,omitempty"`
	DisplayName  string                          `json:"display_name,omitempty"`
	Description  string                          `json:"description,omitempty"`
	Origin       string                          `json:"origin"`
	State        string                          `json:"state"`
	Reason       *ListedRefusal                  `json:"reason,omitempty"`
	Providers    []pluginpkg.ProviderInfo        `json:"providers"`
	Destinations []string                        `json:"destinations"`
	Tables       []ListedTable                   `json:"tables"`
	AsideTables  []plugintables.AsideTable       `json:"aside_tables"`
	Widgets      []string                        `json:"widgets"`
	Settings     []pluginpkg.SettingValue        `json:"settings"`
	Events       []plugintables.PluginTableEvent `json:"events"`
	// Endpoints are the endpoints it reads, with what its manifest tells the broker of each and the
	// OPNsense versions each route serves; CorrectionConflicts are those whose mirror corrections lost
	// to another plugin's (mirror_correction_conflict), the plugin loading all the same.
	Endpoints           []ListedEndpoint               `json:"endpoints"`
	CorrectionConflicts []pluginpkg.CorrectionConflict `json:"correction_conflicts"`
	// Push are its push declarations, each with its listener state and liveness once the receiver runs
	// (docs/plugins.md, "Push sources"); the API catalogue carries their counters.
	Push []pluginpkg.PushEntry `json:"push"`
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
	Path     string                 `json:"path"`
	Method   string                 `json:"method"`
	Versions *contract.VersionRange `json:"versions"`
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
	pluginpkg.SettingStore
	PluginTableRows(ctx context.Context, table plugindecl.PluginTable) (int64, error)
	AsideTables(ctx context.Context, plugin string) ([]plugintables.AsideTable, error)
}

// Listing returns every plugin of a load: the loaded ones, built-ins first, then the refused ones,
// each group by name.
func Listing(l *pluginpkg.Loaded, ctx context.Context, database ListingStore) ([]ListedPlugin, error) {
	listed := make([]ListedPlugin, 0, len(l.Members)+len(l.Refused))
	for _, plugin := range l.Members {
		entry := ListedPlugin{Name: plugin.Name, Version: plugin.Version, APIVersion: plugin.APIVersion,
			DisplayName: plugin.DisplayName, Description: plugin.Description, Origin: plugin.Origin,
			State: StateLoaded, Providers: append([]pluginpkg.ProviderInfo{}, plugin.Providers...),
			Destinations: append([]string{}, plugin.Destinations...), Tables: []ListedTable{},
			AsideTables: []plugintables.AsideTable{}, Widgets: []string{},
			Events: append([]plugintables.PluginTableEvent{}, plugin.Events...)}
		entry.Endpoints = listedEndpoints(plugin)
		entry.CorrectionConflicts = append([]pluginpkg.CorrectionConflict{}, plugin.CorrectionConflicts...)
		entry.Push = plugin.ListedPush()
		for _, table := range plugin.OwnTables {
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
	refusals := append([]contract.Refusal(nil), l.Refused...)
	sort.SliceStable(refusals, func(i, j int) bool { return refusals[i].Plugin < refusals[j].Plugin })
	for _, refusal := range refusals {
		listed = append(listed, ListedPlugin{Name: refusal.Plugin, Origin: refusal.Origin, State: StateRefused,
			Reason: &ListedRefusal{Code: refusal.Code, Detail: refusal.Detail}, Providers: []pluginpkg.ProviderInfo{},
			Destinations: []string{}, Tables: []ListedTable{}, AsideTables: []plugintables.AsideTable{},
			Widgets: []string{}, Settings: []pluginpkg.SettingValue{}, Events: []plugintables.PluginTableEvent{},
			Endpoints: []ListedEndpoint{}, CorrectionConflicts: []pluginpkg.CorrectionConflict{}, Push: []pluginpkg.PushEntry{}})
	}
	return listed, nil
}

// listedEndpoints is the plugin's endpoints as the listing reports them, in the manifest's order.
func listedEndpoints(p *pluginpkg.Plugin) []ListedEndpoint {
	listed := []ListedEndpoint{}
	for _, declaration := range p.Manifest.Endpoints {
		endpoint := p.Endpoints[declaration.Name]
		entry := ListedEndpoint{Name: declaration.Name, Path: endpoint.Path, Method: endpoint.Method,
			MaxAgeSeconds: declaration.MaxAgeSeconds, FloorSeconds: declaration.FloorSeconds, Routes: []ListedRoute{}}
		for _, held := range p.Routes[declaration.Name] {
			entry.Routes = append(entry.Routes, ListedRoute{Path: held.Endpoint.Path, Method: held.Endpoint.Method,
				Versions: held.Versions})
		}
		listed = append(listed, entry)
	}
	return listed
}
