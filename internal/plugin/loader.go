package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/pluginkind"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/opnsense"
	contractpkg "github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/fetch"
	"github.com/NaejEL/opnview/internal/plugin/runtime"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/widget"
)

// Discovering and loading plugins: the built-ins embedded in the binary, then the directories of
// <data-dir>/plugins/.
//
// BOTH GO THROUGH THE SAME LOADER AND THE SAME VALIDATOR, which is the proof that the format is
// the format: a built-in plugin is a directory of plugins/ in the source tree, embedded as it
// stands, and a third party's is the same directory dropped into the data directory. What
// differs is only the consequence of a refusal: a refused built-in is a defect of the build,
// which the test suite fails on; a refused third-party plugin is somebody's file, logged with
// its reason and skipped, and the service starts without it.
//
// NO ACTIVATION STEP AND NO ALLOWLIST (Decisions of specs/SPEC-backend-completion-plugins.md): a
// plugin found is loaded. A name a built-in already has is refused (name_collision) -- the built-in
// wins -- and so is a provider whose kind and key another provider has.

// The origins of a plugin.
const (
	// OriginBuiltin is a plugin embedded in the binary, from plugins/ in the source tree.
	OriginBuiltin = "builtin"
	// OriginDataDirectory is a plugin read from <data-dir>/plugins/ at start.
	OriginDataDirectory = "data_directory"
)

// PluginsDirectory is the directory of the data directory plugins are read from.
const PluginsDirectory = "plugins"

// The forms of a provider: served by the script, or declared in the manifest.
const (
	FormScript      = "script"
	FormDeclarative = "declarative"
	// FormPush is a provider its push entries feed, which reads nothing of the firewall.
	FormPush = "push"
)

// pushProviderCadenceSeconds is how often a push-fed provider's pass refreshes its availability.
const pushProviderCadenceSeconds = 60

// Options say where plugins are read from and what they are checked against.
type Options struct {
	// DataDir is the data directory; its plugins/ subdirectory is read. Empty reads none.
	DataDir string
	// Store is the database the plugins' tables are created in. Nil validates the declarations
	// and creates nothing; a collector creates them on its own store at its first probe round.
	Store *store.Store
	// CallTimeout bounds one call of a script function; zero is DefaultCallTimeout.
	CallTimeout time.Duration
	// Builtins is the tree of built-in plugins; nil is the one embedded in the binary.
	Builtins fs.FS
	// Settings is where the plugins' settings are read from; nil is Store.
	Settings SettingStore
	// Sealer opens and seals the secrets plugins declare as settings; nil leaves every secret
	// unread and refuses to write one.
	Sealer Sealer
	// FetchTimeout bounds one request of the fetch facility; zero is ten minutes.
	FetchTimeout time.Duration
	// FetchTransport is what the fetch facility sends through; nil is the product's own.
	FetchTransport http.RoundTripper
	// HostAnswers, when set, is handed everything a host function answers a script, by function
	// name: `opnview plugin test` searches it for credentials (BR-AC16). Nil in the product.
	HostAnswers func(function string, answer any)

	// pushes is what the load's push entries share, set by Load.
	pushes *pushHost
}

// Plugin is one loaded plugin, as the listing reports it.
type Plugin struct {
	Name        string
	Version     string
	DisplayName string
	Description string
	APIVersion  int
	Origin      string
	// Directory is where it was read from: plugins/<name> in the binary, or the path in the data
	// directory.
	Directory string
	Providers []ProviderInfo
	// WidgetDirectories are the directories of the widgets it carries, each holding the widget's
	// manifest.json and query, read by the widget service.
	WidgetDirectories []string
	// Tables are the tables it owns.
	Tables []plugindecl.PluginTable
	// Destinations are the hosts beyond the firewall it declares it contacts.
	Destinations []string
	// CorrectionConflicts are the endpoints whose mirror corrections lost to another plugin's
	// (mirror_correction_conflict): listed, never refused.
	CorrectionConflicts []CorrectionConflict
	// Events are what loading did beyond loading: a table set aside because its declaration
	// changed (store.PluginTableRebuilt).
	Events []plugintables.PluginTableEvent
	// Strings are its catalogue strings, by language and key, read from its locale/ directory.
	Strings map[string]map[string]string

	Manifest contractpkg.Manifest
	script   *runtime.Script
	// adapters are the plugin's providers, by kind, as their tests call them (testrun.go).
	adapters  map[string]adapter
	Endpoints map[string]opnsense.Endpoint
	// routes are each endpoint's routes by OPNsense version (versions.go).
	Routes    map[string][]runtime.Route
	OwnTables []plugindecl.PluginTable
	// datasets are its tables as datasets of the data catalogue (datasets.go).
	datasets []dataset.Declaration
	// widgets are the widgets it carries, as the widget service loads them.
	widgets []widget.Source
	// lives are its providers' live passes, by provider key (live.go).
	lives map[string]*liveBinding
	// pushes are its push entries (push.go), and pushHost the hub they are routed by once one runs.
	pushes   []*pushBinding
	pushHost *pushHost
}

// ProviderInfo is one provider a plugin declares.
type ProviderInfo struct {
	Kind              string         `json:"kind"`
	Key               string         `json:"key"`
	DisplayName       string         `json:"display_name"`
	Form              string         `json:"form"`
	ReadStrategy      string         `json:"read_strategy,omitempty"`
	CadenceSeconds    int            `json:"cadence_seconds,omitempty"`
	MinCadenceSeconds int            `json:"min_cadence_seconds,omitempty"`
	TriggeredBy       []string       `json:"triggered_by,omitempty"`
	Endpoints         []EndpointInfo `json:"endpoints"`
	// Live is the provider's live pass, absent when it declares none (live.go).
	Live *ListedLive `json:"live,omitempty"`
}

// EndpointInfo is one firewall endpoint a plugin reads.
type EndpointInfo struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Loaded is what one load produced: the source set every provider was added to, the plugins
// loaded, and those refused.
type Loaded struct {
	Set     *sources.Set
	Members []*Plugin
	Refused []contractpkg.Refusal
	push    *pushHost
}

// Plugins returns the loaded plugins, built-ins first, each group by name.
func (l *Loaded) Plugins() []*Plugin { return append([]*Plugin(nil), l.Members...) }

// Refusals returns the plugins refused, each with its closed code.
func (l *Loaded) Refusals() []contractpkg.Refusal {
	return append([]contractpkg.Refusal(nil), l.Refused...)
}

// Plugin returns a loaded plugin by name.
func (l *Loaded) Plugin(name string) (*Plugin, bool) {
	for _, plugin := range l.Members {
		if plugin.Name == name {
			return plugin, true
		}
	}
	return nil, false
}

// WidgetDirectories returns the widget directories of every loaded plugin, by plugin name.
func (l *Loaded) WidgetDirectories() map[string][]string {
	directories := map[string][]string{}
	for _, plugin := range l.Members {
		if len(plugin.WidgetDirectories) > 0 {
			directories[plugin.Name] = append([]string(nil), plugin.WidgetDirectories...)
		}
	}
	return directories
}

// Strings returns every loaded plugin's catalogue strings of one language, merged. A key two
// plugins both define keeps the built-in's, or the first by name.
func (l *Loaded) Strings(language string) map[string]string {
	merged := map[string]string{}
	for _, plugin := range l.Members {
		for key, text := range plugin.Strings[language] {
			if _, taken := merged[key]; !taken {
				merged[key] = text
			}
		}
	}
	return merged
}

// Tables returns every table of every loaded plugin.
func (l *Loaded) Tables() []plugindecl.PluginTable {
	var tables []plugindecl.PluginTable
	for _, plugin := range l.Members {
		tables = append(tables, plugin.OwnTables...)
	}
	return tables
}

var (
	builtinsOnce   sync.Once
	builtinsLoaded *Loaded
)

// BuiltinsLoaded returns the built-in plugins alone, loaded once per process with no data
// directory and no store. A refusal met loading them is in the set's failures, which the probe
// round reports and the suite fails on.
func BuiltinsLoaded() *Loaded {
	builtinsOnce.Do(func() {
		loaded, err := Load(context.Background(), Options{})
		if err != nil {
			loaded = &Loaded{Set: sources.New()}
			loaded.Set.NoteFailure(err)
		}
		builtinsLoaded = loaded
	})
	return builtinsLoaded
}

// Builtins returns the source set of the built-in plugins alone: what a collector built without a
// data directory reads -- every test that does not drop a plugin of its own.
func Builtins() *sources.Set { return BuiltinsLoaded().Set }

// Call runs one function a plugin's script exports under a namespace -- a kind, or the
// `helpers` a plugin exports for its own tests -- and returns its answer as Go values. session may
// be nil for a function that calls no host function.
func (p *Plugin) Call(ctx context.Context, session provider.Session, namespace, function string,
	arguments ...any) (any, error) {
	if p.script == nil {
		return nil, fmt.Errorf("plugin: %s has no script", p.Name)
	}
	answer, _, failed := p.script.CallAny(ctx, session, namespace, function, arguments...)
	if failed != nil {
		return nil, failed
	}
	return answer, nil
}

// Load reads the built-in plugins and then those of the data directory, validates each, and adds
// the providers of every plugin it accepts to a new source set. It fails only when a directory
// cannot be listed at all; a plugin it refuses is reported in Refusals, and the set's failures
// carry a refused built-in.
func Load(ctx context.Context, options Options) (*Loaded, error) {
	loaded := &Loaded{Set: sources.New(), push: &pushHost{}}
	options.pushes = loaded.push
	builtins := options.Builtins
	if builtins == nil {
		builtins = opnview.BuiltinPlugins()
	}
	taken := map[string]string{}
	entries, err := fs.ReadDir(builtins, ".")
	if err != nil {
		return nil, fmt.Errorf("plugin: reading the built-in plugins: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory, err := fs.Sub(builtins, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("plugin: reading the built-in plugin %s: %w", entry.Name(), err)
		}
		plugin, refusal := loadOne(ctx, loaded.Set, directory, entry.Name(), OriginBuiltin,
			path.Join(PluginsDirectory, entry.Name()), "", taken, options)
		if refusal != nil {
			refusal.Origin = OriginBuiltin
			loaded.Refused = append(loaded.Refused, *refusal)
			loaded.Set.NoteFailure(fmt.Errorf("the built-in %w", refusal))
			continue
		}
		taken[plugin.Name] = OriginBuiltin
		loaded.Members = append(loaded.Members, plugin)
	}
	if options.DataDir == "" {
		return loaded, nil
	}
	root := filepath.Join(options.DataDir, PluginsDirectory)
	entries, err = os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return loaded, nil
		}
		return nil, fmt.Errorf("plugin: reading %s: %w", root, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			notDirectory := contractpkg.Refuse(entry.Name(), contractpkg.RefusalManifestUnreadable,
				"%s is a file; a plugin is a directory holding %s", entry.Name(), contractpkg.ManifestFile)
			notDirectory.Origin = OriginDataDirectory
			loaded.Refused = append(loaded.Refused, *notDirectory)
			continue
		}
		onDisk := filepath.Join(root, entry.Name())
		plugin, refusal := loadOne(ctx, loaded.Set, os.DirFS(onDisk), entry.Name(), OriginDataDirectory, onDisk,
			onDisk, taken, options)
		if refusal != nil {
			refusal.Origin = OriginDataDirectory
			loaded.Refused = append(loaded.Refused, *refusal)
			continue
		}
		taken[plugin.Name] = OriginDataDirectory
		loaded.Members = append(loaded.Members, plugin)
	}
	return loaded, nil
}

// loadOne validates one plugin directory and, when it is accepted, adds its providers to the set,
// admits its endpoints and creates its tables. Nothing of a refused plugin is added anywhere.
func loadOne(ctx context.Context, set *sources.Set, directory fs.FS, name, origin, location, onDisk string,
	taken map[string]string, options Options) (*Plugin, *contractpkg.Refusal) {
	raw, err := fs.ReadFile(directory, contractpkg.ManifestFile)
	if err != nil {
		return nil, contractpkg.Refuse(name, contractpkg.RefusalManifestUnreadable, "%s cannot be read: %v", contractpkg.ManifestFile, err)
	}
	manifest, err := contractpkg.DecodeManifest(raw)
	if err != nil {
		return nil, contractpkg.Refuse(name, contractpkg.RefusalManifestInvalid, "%s is not a valid manifest: %v", contractpkg.ManifestFile, err)
	}
	if refusal := contractpkg.ValidateShape(manifest, name); refusal != nil {
		return nil, refusal
	}
	if owner, collides := taken[manifest.Name]; collides {
		return nil, contractpkg.Refuse(name, contractpkg.RefusalNameCollision,
			"a plugin named %q is already loaded (%s); the built-in wins and a later one is refused", manifest.Name, owner)
	}
	plugin := &Plugin{
		Name: manifest.Name, Version: manifest.Version, DisplayName: manifest.DisplayName,
		Description: manifest.Description, APIVersion: manifest.APIVersion, Origin: origin, Directory: location,
		Manifest: manifest, Strings: map[string]map[string]string{}, pushHost: options.pushes,
	}
	endpoints, routes, refusal := declaredEndpoints(manifest)
	if refusal != nil {
		return nil, refusal
	}
	plugin.Endpoints, plugin.Routes = endpoints, routes
	tables, refusal := declaredTables(manifest)
	if refusal != nil {
		return nil, refusal
	}
	plugin.OwnTables, plugin.Tables = tables, tables
	if refusal := checkSettings(manifest, taken); refusal != nil {
		return nil, refusal
	}
	if refusal := checkDestinations(manifest); refusal != nil {
		return nil, refusal
	}
	if refusal := loadScript(plugin, directory, options.CallTimeout); refusal != nil {
		return nil, refusal
	}
	if plugin.script != nil {
		if refusal := loadScriptVariants(plugin, directory); refusal != nil {
			return nil, refusal
		}
		plugin.script.Routes = plugin.Routes
		plugin.script.Extra = plugin.extensions(options)
		plugin.script.Observe = options.HostAnswers
	}
	providers, refusal := checkProviders(set, plugin)
	if refusal != nil {
		return nil, refusal
	}
	var exports map[string]map[string]string
	if plugin.script != nil {
		if exports, err = plugin.script.ExportsOf(); err != nil {
			return nil, contractpkg.Refuse(name, contractpkg.RefusalScriptDoesNotCompile, "%v", err)
		}
	}
	if plugin.pushes, refusal = checkPush(plugin, exports); refusal != nil {
		return nil, refusal
	}
	if refusal := loadWidgets(plugin, directory, location, onDisk, origin); refusal != nil {
		return nil, refusal
	}
	if refusal := loadStrings(plugin, directory, providers); refusal != nil {
		return nil, refusal
	}
	infos := make([]ProviderInfo, 0, len(providers))
	for _, declared := range providers {
		infos = append(infos, declared.info)
	}
	for index, table := range tables {
		declared, err := pluginDataset(name, manifest.Tables[index], table, infos, plugin.pushes)
		if err != nil {
			return nil, contractpkg.Refuse(name, contractpkg.RefusalTableInvalid, "the table %s is not a dataset the catalogue can serve: %v",
				table.Table, err)
		}
		plugin.datasets = append(plugin.datasets, declared)
	}

	// Everything is valid: the side effects, in an order that leaves nothing behind on a
	// failure but the one the store reports.
	if options.Store != nil {
		for _, table := range tables {
			event, err := options.Store.EnsurePluginTable(ctx, table)
			if err != nil {
				return nil, contractpkg.Refuse(name, contractpkg.RefusalTableInvalid, "%v", err)
			}
			if event != nil {
				plugin.Events = append(plugin.Events, *event)
			}
		}
	}
	for _, endpoint := range endpoints {
		if err := opnsense.AdmitDeclaredEndpoint(endpoint); err != nil {
			return nil, contractpkg.Refuse(name, contractpkg.RefusalEndpointInvalid, "%v", err)
		}
	}
	for _, declared := range routes {
		for _, held := range declared[1:] {
			if err := opnsense.AdmitDeclaredEndpoint(held.Endpoint); err != nil {
				return nil, contractpkg.Refuse(name, contractpkg.RefusalEndpointInvalid, "%v", err)
			}
		}
	}
	for _, declared := range providers {
		if err := declared.add(set); err != nil {
			return nil, contractpkg.Refuse(name, contractpkg.RefusalProviderKeyCollision, "%v", err)
		}
		if origin == OriginBuiltin {
			set.MarkBuiltin(declared.info.Kind, declared.info.Key)
		}
		plugin.Providers = append(plugin.Providers, declared.info)
	}
	for _, setting := range manifest.Settings {
		taken[settingClaim(StorageKey(manifest.Name, setting.Key))] = manifest.Name
	}
	plugin.Destinations = append([]string(nil), manifest.Destinations...)
	// What the plugin declares of its endpoints reaches whichever broker reads this set.
	set.AddDeclarer(plugin.declareTo)
	return plugin, nil
}

// declaredEndpoints checks every endpoint a manifest declares and returns them by name.
func declaredEndpoints(manifest contractpkg.Manifest) (map[string]opnsense.Endpoint, map[string][]runtime.Route, *contractpkg.Refusal) {
	endpoints := map[string]opnsense.Endpoint{}
	routes := map[string][]runtime.Route{}
	for _, declaration := range manifest.Endpoints {
		if !contractpkg.NamePattern.MatchString(declaration.Name) {
			return nil, nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalEndpointInvalid, "the endpoint name %q is not a snake_case word",
				declaration.Name)
		}
		if _, duplicate := endpoints[declaration.Name]; duplicate {
			return nil, nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalEndpointInvalid, "the endpoint %q is declared twice",
				declaration.Name)
		}
		endpoint := opnsense.Endpoint{Path: declaration.Path, Method: declaration.Method, Stream: declaration.Stream,
			SurveySection: declaration.SurveySection, UpstreamURL: declaration.UpstreamURL, Note: declaration.Note}
		switch declaration.Body {
		case "", "none":
			endpoint.Encoding = opnsense.NoBody
		case "form":
			endpoint.Encoding = opnsense.FormBody
		case "json":
			endpoint.Encoding = opnsense.JSONBody
		default:
			return nil, nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalEndpointInvalid, "the endpoint %q has the body %q, not none, "+
				"form or json", declaration.Name, declaration.Body)
		}
		switch declaration.Argument {
		case "", "none":
			endpoint.Argument = opnsense.NoArgument
		case "interface_names":
			endpoint.Argument = opnsense.InterfaceNameArgument
		default:
			return nil, nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalEndpointInvalid, "the endpoint %q has the argument %q, not none "+
				"or interface_names", declaration.Name, declaration.Argument)
		}
		if endpoint.Method == "" {
			endpoint.Method = http.MethodGet
		}
		if refusal := opnsense.CheckDeclaredEndpoint(endpoint); refusal != nil {
			return nil, nil, contractpkg.Refuse(manifest.Name, refusal.Code, "the endpoint %q: %s", declaration.Name, refusal.Detail)
		}
		if reason := broker.CheckRead(endpoint.Method, endpoint.Path); reason != "" {
			return nil, nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalEndpointNotReadAction, "the endpoint %q: %s", declaration.Name, reason)
		}
		if refusal := checkBrokerKeys(manifest, declaration); refusal != nil {
			return nil, nil, refusal
		}
		// An endpoint opnview's own registry already holds is that entry, citation and note
		// included: one path is one endpoint, whoever reads it.
		for _, known := range opnsense.Registry() {
			if known.Path == endpoint.Path {
				endpoint = known
			}
		}
		endpoints[declaration.Name] = endpoint
		declared, refusal := declaredRoutes(manifest, declaration, endpoint)
		if refusal != nil {
			return nil, nil, refusal
		}
		routes[declaration.Name] = declared
	}
	return endpoints, routes, nil
}

// declaredTables checks every table a manifest declares.
func declaredTables(manifest contractpkg.Manifest) ([]plugindecl.PluginTable, *contractpkg.Refusal) {
	var tables []plugindecl.PluginTable
	seen := map[string]bool{}
	for _, declaration := range manifest.Tables {
		if seen[declaration.Name] {
			return nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalTableInvalid, "the table %q is declared twice", declaration.Name)
		}
		seen[declaration.Name] = true
		table := plugindecl.PluginTable{Plugin: manifest.Name, Table: declaration.Name,
			PrimaryKey: declaration.PrimaryKey}
		for _, column := range declaration.Columns {
			table.Columns = append(table.Columns, plugindecl.PluginColumn{Name: column.Name, Type: column.Type,
				Nullable: column.Nullable, CarriesDomain: column.CarriesDomain, Role: column.Role, Unit: column.Unit})
		}
		for _, index := range declaration.Indexes {
			table.Indexes = append(table.Indexes, plugindecl.PluginIndex{Columns: index.Columns, Unique: index.Unique})
		}
		if declaration.Retention != nil {
			table.TimeColumn = declaration.Retention.Column
			table.RetentionSeconds = declaration.Retention.Seconds
		}
		if err := plugindecl.ValidatePluginTable(table); err != nil {
			if errors.Is(err, plugindecl.ErrUniqueKeyWithoutTime) {
				return nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalUniqueKeyWithoutTime, "%v", err)
			}
			return nil, contractpkg.Refuse(manifest.Name, contractpkg.RefusalTableInvalid, "%v", err)
		}
		tables = append(tables, table)
	}
	return tables, nil
}

// loadScript reads and compiles the script a manifest names, and runs it once to see what it
// exports.
func loadScript(plugin *Plugin, directory fs.FS, timeout time.Duration) *contractpkg.Refusal {
	if plugin.Manifest.Script == "" {
		return nil
	}
	source, err := fs.ReadFile(directory, plugin.Manifest.Script)
	if err != nil {
		return contractpkg.Refuse(plugin.Name, contractpkg.RefusalScriptMissing, "%s cannot be read: %v", plugin.Manifest.Script, err)
	}
	compiled, err := runtime.Compile(plugin.Name, plugin.Manifest.Script, string(source), plugin.Endpoints, timeout)
	if err != nil {
		return contractpkg.Refuse(plugin.Name, contractpkg.RefusalScriptDoesNotCompile, "%v", err)
	}
	if _, err := compiled.ExportsOf(); err != nil {
		return contractpkg.Refuse(plugin.Name, contractpkg.RefusalScriptDoesNotCompile, "running %s to read its exports: %v",
			plugin.Manifest.Script, err)
	}
	compiled.Version = plugin.Manifest.Version
	compiled.Cadences = map[string]time.Duration{}
	for _, declaration := range plugin.Manifest.Providers {
		if declaration.CadenceSeconds > 0 {
			compiled.Cadences[declaration.Kind] = time.Duration(declaration.CadenceSeconds) * time.Second
		}
	}
	plugin.script = compiled
	return nil
}

// checkedProvider is one provider that passed every check, and how to add it.
type checkedProvider struct {
	info ProviderInfo
	add  func(set *sources.Set) error
}

// checkProviders checks every provider a manifest declares and returns how to add each.
func checkProviders(set *sources.Set, plugin *Plugin) ([]checkedProvider, *contractpkg.Refusal) {
	var exports map[string]map[string]string
	if plugin.script != nil {
		var err error
		if exports, err = plugin.script.ExportsOf(); err != nil {
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalScriptDoesNotCompile, "%v", err)
		}
	}
	var checked []checkedProvider
	seen := map[[2]string]bool{}
	for _, declaration := range plugin.Manifest.Providers {
		key := declaration.Key
		if key == "" {
			key = plugin.Name
		}
		if !contractpkg.NamePattern.MatchString(key) {
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "the provider key %q is not a snake_case word", key)
		}
		if strings.TrimSpace(declaration.DisplayName) == "" {
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "the %s provider has no display_name",
				declaration.Kind)
		}
		switch declaration.Source {
		case "":
		case providerSourcePush:
			if provider.IsCoreKind(declaration.Kind) || declaration.Probe != nil || declaration.Read != nil ||
				declaration.Live != nil {
				return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "the %s provider has the source push, which "+
					"only a provider of a kind of the plugin's own with no probe, read or live pass may have",
					declaration.Kind)
			}
		default:
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "the %s provider has the source %q; the only "+
				"source a provider names is push", declaration.Kind, declaration.Source)
		}
		if reason, coreOnly := provider.CoreOnly(declaration.Kind); coreOnly {
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalCoreOnlyKind, "the kind %q is %s", declaration.Kind, reason)
		}
		contract, core := provider.ContractOf(declaration.Kind)
		if !core {
			if !contractpkg.NamePattern.MatchString(declaration.Kind) || len(plugin.OwnTables) == 0 {
				return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalUnknownKind, "the kind %q is not a core kind (%v), and a kind "+
					"of the plugin's own needs tables of its own to write into", declaration.Kind, provider.CoreKinds())
			}
			if owner, owned := set.DeclaredKindOwner(declaration.Kind); owned && owner != plugin.Name {
				return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalUnknownKind, "the kind %q is declared by the plugin %s",
					declaration.Kind, owner)
			}
			contract = declaredContract(declaration.Kind)
		}
		identity := [2]string{declaration.Kind, key}
		if seen[identity] || set.Taken(declaration.Kind, key) {
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalProviderKeyCollision,
				"the %s provider %q is already declared", declaration.Kind, key)
		}
		seen[identity] = true
		if declaration.Source == providerSourcePush && declaration.CadenceSeconds == 0 {
			// Its pass reads the hub, never the firewall: it refreshes the availability row.
			declaration.CadenceSeconds = pushProviderCadenceSeconds
		}
		if refusal := checkCadence(plugin.Name, declaration, core && declaration.Kind != "reconciled_state"); refusal != nil {
			return nil, refusal
		}
		if refusal := checkTriggers(plugin.Name, declaration, core && declaration.Kind != "reconciled_state"); refusal != nil {
			return nil, refusal
		}
		switch declaration.ReadStrategy {
		case "", "full_reread", "time_window", "watermark":
		default:
			return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "read_strategy %q is not full_reread, time_window "+
				"or watermark", declaration.ReadStrategy)
		}
		info := ProviderInfo{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName,
			ReadStrategy: declaration.ReadStrategy, CadenceSeconds: declaration.CadenceSeconds,
			MinCadenceSeconds: declaration.MinCadenceSeconds, TriggeredBy: declaration.TriggeredBy,
			Endpoints: endpointInfos(plugin.Endpoints)}
		base := adapter{plugin: plugin.Name, contract: contract, endpoints: plugin.Endpoints,
			scriptProbe: scriptEndpoint(plugin.Name, plugin.Manifest.Script)}
		if (declaration.Probe != nil || declaration.Read != nil) && declaration.Live != nil {
			_, refusal := checkLive(plugin, declaration, exports)
			return nil, refusal
		}
		if declaration.Source == providerSourcePush {
			info.Form = FormPush
			manifest := provider.Manifest{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName}
			declaration := declaration
			checked = append(checked, checkedProvider{info: info, add: func(set *sources.Set) error {
				if err := set.DeclareKind(manifest.Kind, plugin.Name); err != nil {
					return err
				}
				return set.AddDeclared(manifest, pluginkind.Registration{Plugin: plugin.Name,
					Cadence:    cadenceOf(declaration),
					MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second,
					Source:     pushProviderAdapter{plugin: plugin, kind: manifest.Kind, key: manifest.Key}})
			}})
			continue
		}
		if declaration.Probe != nil || declaration.Read != nil {
			info.Form = FormDeclarative
			form, refusal := checkDeclarative(plugin, declaration, contract, core)
			if refusal != nil {
				return nil, refusal
			}
			base.invoker = form
		} else {
			info.Form = FormScript
			if plugin.script == nil {
				return nil, contractpkg.Refuse(plugin.Name, contractpkg.RefusalScriptMissing, "the %s provider declares no probe and no read, "+
					"so a script serves it, and the manifest names none", declaration.Kind)
			}
			if refusal := checkExports(plugin.Name, exports, declaration.Kind, contract, core); refusal != nil {
				return nil, refusal
			}
			live, refusal := checkLive(plugin, declaration, exports)
			if refusal != nil {
				return nil, refusal
			}
			if live != nil {
				if plugin.lives == nil {
					plugin.lives = map[string]*liveBinding{}
				}
				plugin.lives[key] = live
				info.Live = listedLive(declaration)
			}
			base.invoker = scriptInvoker{script: plugin.script, namespace: declaration.Kind}
		}
		manifest := provider.Manifest{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName}
		declaration := declaration
		isCore := core
		checked = append(checked, checkedProvider{info: info, add: func(set *sources.Set) error {
			if !isCore {
				if err := set.DeclareKind(manifest.Kind, plugin.Name); err != nil {
					return err
				}
			}
			if plugin.adapters == nil {
				plugin.adapters = map[string]adapter{}
			}
			plugin.adapters[manifest.Kind] = base
			return addToSet(set, manifest, base, plugin, declaration)
		}})
	}
	return checked, nil
}

// checkCadence checks a provider's cadence: a core kind with a surveyed interval declares none,
// any other kind declares one, at or above its own lower bound.
func checkCadence(plugin string, declaration contractpkg.ProviderDeclaration, surveyed bool) *contractpkg.Refusal {
	if surveyed {
		if declaration.CadenceSeconds != 0 || declaration.MinCadenceSeconds != 0 {
			return contractpkg.Refuse(plugin, contractpkg.RefusalCadenceInvalid, "the %s kind runs at the interval the collection surface "+
				"sets, so its provider declares no cadence", declaration.Kind)
		}
		return nil
	}
	if declaration.CadenceSeconds <= 0 {
		return contractpkg.Refuse(plugin, contractpkg.RefusalCadenceInvalid, "the %s provider declares no cadence_seconds, which its kind "+
			"needs", declaration.Kind)
	}
	if declaration.MinCadenceSeconds < 0 || declaration.CadenceSeconds < declaration.MinCadenceSeconds {
		return contractpkg.Refuse(plugin, contractpkg.RefusalCadenceInvalid, "cadence_seconds %d is below min_cadence_seconds %d",
			declaration.CadenceSeconds, declaration.MinCadenceSeconds)
	}
	return nil
}

// cadenceOf is a provider's declared cadence.
func cadenceOf(declaration contractpkg.ProviderDeclaration) time.Duration {
	return time.Duration(declaration.CadenceSeconds) * time.Second
}

// checkExports checks that a script exports every function and value its kind's contract names.
func checkExports(plugin string, exports map[string]map[string]string, kind string, contract provider.Contract,
	core bool) *contractpkg.Refusal {
	members := exports[kind]
	required := []string{"probe", "collect"}
	if core {
		required = nil
		for _, function := range contract.Functions {
			required = append(required, function.Name)
		}
	}
	for _, function := range required {
		if members[function] != "function" {
			return contractpkg.Refuse(plugin, contractpkg.RefusalScriptMissingFunction, "the script does not export the function %s.%s",
				kind, function)
		}
	}
	for _, value := range contract.Values {
		if members[value.Name] != "value" {
			return contractpkg.Refuse(plugin, contractpkg.RefusalScriptMissingFunction, "the script does not export the value %s.%s",
				kind, value.Name)
		}
	}
	return nil
}

// endpointInfos lists a plugin's endpoints, by name.
func endpointInfos(endpoints map[string]opnsense.Endpoint) []EndpointInfo {
	infos := make([]EndpointInfo, 0, len(endpoints))
	for _, name := range runtime.SortedKeys(endpoints) {
		infos = append(infos, EndpointInfo{Name: name, Method: endpoints[name].Method, Path: endpoints[name].Path})
	}
	return infos
}

// loadWidgets checks that every widget a manifest names has its directory, widgets/<type>/, and
// keeps it for the widget service. The widget itself is validated by the widget service, which
// reports a refused widget with the widget's own code -- and, for a built-in, refuses to start.
func loadWidgets(plugin *Plugin, directory fs.FS, location, onDisk, origin string) *contractpkg.Refusal {
	for _, name := range plugin.Manifest.Widgets {
		relative := path.Join("widgets", name)
		info, err := fs.Stat(directory, relative)
		if err != nil || !info.IsDir() {
			return contractpkg.Refuse(plugin.Name, contractpkg.RefusalWidgetMissing, "the widget %q has no directory widgets/%s", name, name)
		}
		files, err := fs.Sub(directory, relative)
		if err != nil {
			return contractpkg.Refuse(plugin.Name, contractpkg.RefusalWidgetMissing, "the widget %q: %v", name, err)
		}
		where := path.Join(location, relative)
		if onDisk != "" {
			where = filepath.Join(onDisk, "widgets", name)
		}
		plugin.WidgetDirectories = append(plugin.WidgetDirectories, where)
		plugin.widgets = append(plugin.widgets, widget.Source{Plugin: plugin.Name, Type: name,
			Builtin: origin == OriginBuiltin, Location: where, Files: files})
	}
	return nil
}

// WidgetSources returns the widgets every loaded plugin carries, for the widget service: the
// built-ins' first, each plugin's in its manifest's order.
func (l *Loaded) WidgetSources() []widget.Source {
	sources := []widget.Source{}
	for _, plugin := range l.Members {
		sources = append(sources, plugin.widgets...)
	}
	return sources
}

// The built-in plugins' widgets are what a widget service serves when it is handed none: whatever
// builds one, the built-in widget plugins are served.
func init() {
	widget.RegisterBuiltinSources(func() []widget.Source { return BuiltinsLoaded().WidgetSources() })
}

// loadStrings reads a plugin's catalogue strings: locale/<language>.json, each in the format of
// opnview's own catalogue files, {"language": ..., "strings": {key: text}}. A plugin's strings
// are its own, carried in its directory, so adding one edits no
// shipped locale file; a key outside the plugin's namespace is refused.
func loadStrings(plugin *Plugin, directory fs.FS, providers []checkedProvider) *contractpkg.Refusal {
	owned := map[string]bool{}
	for _, declared := range providers {
		owned["provider."+declared.info.Kind+"."+declared.info.Key] = true
		if !provider.IsCoreKind(declared.info.Kind) {
			owned["collection.kind."+declared.info.Kind] = true
		}
	}
	entries, err := fs.ReadDir(directory, "locale")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(directory, path.Join("locale", entry.Name()))
		if err != nil {
			return contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "locale/%s cannot be read: %v", entry.Name(), err)
		}
		var catalogue struct {
			Language string            `json:"language"`
			Strings  map[string]string `json:"strings"`
		}
		if err := json.Unmarshal(raw, &catalogue); err != nil {
			return contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "locale/%s is not a catalogue file: %v",
				entry.Name(), err)
		}
		language := strings2Language(entry.Name())
		if catalogue.Language != language {
			return contractpkg.Refuse(plugin.Name, contractpkg.RefusalManifestInvalid, "locale/%s declares the language %q",
				entry.Name(), catalogue.Language)
		}
		for key := range catalogue.Strings {
			if !owned[key] && !strings.HasPrefix(key, "plugin."+plugin.Name+".") {
				return contractpkg.Refuse(plugin.Name, contractpkg.RefusalStringOutsideNamespace, "locale/%s defines %q, which is not "+
					"the plugin's own: its keys are plugin.%s.*, provider.<kind>.<key> of its providers and "+
					"collection.kind.<kind> of its own kinds", entry.Name(), key, plugin.Name)
			}
		}
		plugin.Strings[language] = catalogue.Strings
	}
	return nil
}

// strings2Language is a locale file's language: its name without .json.
func strings2Language(file string) string { return strings.TrimSuffix(file, ".json") }

// settingClaim is how a stored setting key is held among the loader's claims, apart from the
// plugin names.
func settingClaim(key string) string { return "setting " + key }

// checkDestinations checks the destinations a manifest declares: each a scheme and a host, and
// nothing else.
func checkDestinations(manifest contractpkg.Manifest) *contractpkg.Refusal {
	for _, destination := range manifest.Destinations {
		parsed, err := url.Parse(destination)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.User != nil {
			return contractpkg.Refuse(manifest.Name, contractpkg.RefusalManifestInvalid, "the destination %q is not a scheme and a host, "+
				"such as https://example.org", destination)
		}
	}
	return nil
}

// extensions are what a plugin's script may reach beyond the firewall, given the options it was
// loaded with.
func (p *Plugin) extensions(options Options) *runtime.Extensions {
	extra := &runtime.Extensions{}
	settingStore := options.Settings
	if settingStore == nil && options.Store != nil {
		settingStore = options.Store
	}
	if settingStore != nil && len(p.Manifest.Settings) > 0 {
		database, sealer := settingStore, options.Sealer
		extra.Settings = func(ctx context.Context) (map[string]any, map[string]string, error) {
			return p.ReadSettings(ctx, database, sealer)
		}
	}
	if options.Store != nil && len(p.OwnTables) > 0 {
		database := options.Store
		own := map[string]bool{}
		for _, table := range p.OwnTables {
			own[table.SQLName()] = true
		}
		extra.Query = func(ctx context.Context, text string, parameters map[string]any) ([]map[string]any, error) {
			names := make([]string, 0, len(parameters))
			for name := range parameters {
				names = append(names, name)
			}
			if _, err := database.CheckQuery(ctx, text, names); err != nil {
				return nil, err
			}
			reads, err := database.Reads(ctx, text, names)
			if err != nil {
				return nil, err
			}
			for _, table := range reads.Tables {
				if !own[table] {
					return nil, fmt.Errorf("the query reads %s, which is not a table of the plugin %s", table, p.Name)
				}
			}
			return database.ReadRows(ctx, text, parameters)
		}
	}
	if options.DataDir != "" {
		extra.Facility = fetch.New(p.Name, fetch.DataDirectory(options.DataDir, p.Name), p.Manifest.Destinations,
			options.FetchTimeout, options.FetchTransport)
	} else if len(p.Manifest.Destinations) > 0 {
		extra.Facility = fetch.New(p.Name, "", p.Manifest.Destinations, options.FetchTimeout, options.FetchTransport)
	}
	return extra
}

// checkTriggers checks a provider's triggered_by: only a provider that states its own cadence may
// name notifications, each a channel the core publishes, alone or with a subject.
func checkTriggers(plugin string, declaration contractpkg.ProviderDeclaration, surveyed bool) *contractpkg.Refusal {
	if len(declaration.TriggeredBy) == 0 {
		return nil
	}
	if surveyed {
		return contractpkg.Refuse(plugin, contractpkg.RefusalTriggerInvalid, "the %s kind runs at the interval the collection surface "+
			"sets, so its provider names no trigger", declaration.Kind)
	}
	for _, name := range declaration.TriggeredBy {
		channel, subject, hasSubject := strings.Cut(name, ":")
		if !slices.Contains(sqldb.Channels(), channel) || (hasSubject && !contractpkg.NamePattern.MatchString(subject)) {
			return contractpkg.Refuse(plugin, contractpkg.RefusalTriggerInvalid, "triggered_by names %q, which is not a channel the core "+
				"publishes, alone or followed by a subject", name)
		}
	}
	return nil
}

// pluginDataset is the dataset of one of a plugin's tables.
//
// A TABLE A PUSH ENTRY WRITES (PS13 of the push amendment) is an event dataset unless it says
// otherwise, its columns take the role their type implies when they state none -- an instant is
// time, a number a measure, anything else a dimension -- and it carries loss_detectable: true only
// when every entry writing into it accepts nothing but transports whose loss is detected.
func pluginDataset(plugin string, declaration contractpkg.TableDeclaration, table plugindecl.PluginTable,
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
	built, err := dataset.PluginDataset(plugin, table.Table, quoteIdentifier(plugindecl.PluginSchema(plugin))+"."+
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

// Datasets returns the datasets of every loaded plugin.
func (l *Loaded) Datasets() []dataset.Declaration {
	var result []dataset.Declaration
	for _, plugin := range l.Members {
		result = append(result, plugin.datasets...)
	}
	return result
}
