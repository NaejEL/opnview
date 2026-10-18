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
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/fetch"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
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
	// catalogue is the data catalogue the load's scripts read, set by Load.
	catalogue *catalogueHolder
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

	Manifest contract.Manifest
	script   *scripthost.Script
	// implementations are the plugin's providers, by kind, as their tests call them (harnesscalls.go).
	implementations map[string]provider.Implementation
	Endpoints       map[string]opnsense.Endpoint
	// Routes are each endpoint's routes by OPNsense version (versions.go).
	Routes    map[string][]scripthost.Route
	OwnTables []plugindecl.PluginTable
	// datasets are its tables as datasets of the data catalogue (datasets.go).
	datasets []dataset.Declaration
	// Widgets are the widgets it carries, as the widget service loads them.
	Widgets []widget.Source
	// lives are its providers' live passes, by provider key (live.go).
	lives map[string]*liveBinding
	// pushes are its push entries (push.go), and pushHost the hub they are routed by once one runs.
	pushes   []*pushBinding
	pushHost *pushHost
	// declaredDatasets are the datasets its scripts may read (datasetaccess.go).
	declaredDatasets DeclaredDatasets
	// derivations are its derivations (derivation.go).
	derivations []*derivation
	// statements are the statements its statement files hold, by name, and relations its relation files
	// (sqlfiles.go, ADR 0018).
	statements map[string]string
	relations  []plugintables.RelationFile
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
	Set     *provider.Set
	Members []*Plugin
	Refused []contract.Refusal
	push    *pushHost
}

// Plugins returns the loaded plugins, built-ins first, each group by name.
func (l *Loaded) Plugins() []*Plugin { return append([]*Plugin(nil), l.Members...) }

// Refusals returns the plugins refused, each with its closed code.
func (l *Loaded) Refusals() []contract.Refusal {
	return append([]contract.Refusal(nil), l.Refused...)
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
			loaded = &Loaded{Set: provider.NewSet()}
			loaded.Set.NoteFailure(err)
		}
		builtinsLoaded = loaded
	})
	return builtinsLoaded
}

// Builtins returns the source set of the built-in plugins alone: what a collector built without a
// data directory reads -- every test that does not drop a plugin of its own.
func Builtins() *provider.Set { return BuiltinsLoaded().Set }

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
	loaded, err := load(ctx, options)
	if err != nil || options.Store == nil {
		return loaded, err
	}
	// THE DECLARATIONS WRITE THE REGISTRY (PA2 of the plugin-API amendment): the DDL seeds no kind and no
	// provider, so loading for a store writes every kind's attribute and every provider's row.
	kinds, providers := provider.SetDeclarations(loaded.Set)
	if err := options.Store.RegisterProviderDeclarations(ctx, kinds, providers, time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("plugin: registering the declarations: %w", err)
	}
	if err := options.Store.RegisterMeasures(ctx, loaded.Measures(), time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("plugin: registering the measures: %w", err)
	}
	values, implementations := provider.SetValues(loaded.Set)
	if err := options.Store.RecordProviderValues(ctx, values, implementations, time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("plugin: recording the providers' values: %w", err)
	}
	return loaded, nil
}

// load reads the plugins and builds the set, writing nothing of the registry.
func load(ctx context.Context, options Options) (*Loaded, error) {
	loaded := &Loaded{Set: provider.NewSet(), push: &pushHost{}}
	if err := provider.KindsError(); err != nil {
		loaded.Set.NoteFailure(fmt.Errorf("the core kind files: %w", err))
	}
	options.pushes = loaded.push
	options.catalogue = &catalogueHolder{loaded: loaded}
	builtins := options.Builtins
	if builtins == nil {
		builtins = opnview.BuiltinPlugins()
	}
	taken := map[string]string{}
	entries, err := fs.ReadDir(builtins, ".")
	if err != nil {
		return nil, fmt.Errorf("plugin: reading the built-in plugins: %w", err)
	}
	// Built-ins load in name order, a required plugin before the plugins requiring it (ADR 0018).
	builtinNames := []string{}
	builtinRequires := map[string][]string{}
	builtinDirectories := map[string]fs.FS{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory, err := fs.Sub(builtins, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("plugin: reading the built-in plugin %s: %w", entry.Name(), err)
		}
		builtinNames = append(builtinNames, entry.Name())
		builtinRequires[entry.Name()] = manifestRequires(directory)
		builtinDirectories[entry.Name()] = directory
	}
	for _, name := range orderByRequires(builtinNames, builtinRequires, taken) {
		directory := builtinDirectories[name]
		plugin, refusal := LoadOne(ctx, loaded.Set, directory, name, OriginBuiltin,
			path.Join(PluginsDirectory, name), "", taken, options)
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
	// The data directory's plugins load in the same order: by name, a required plugin first.
	dataNames := []string{}
	dataRequires := map[string][]string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			notDirectory := contract.Refuse(entry.Name(), contract.RefusalManifestUnreadable,
				"%s is a file; a plugin is a directory holding %s", entry.Name(), contract.ManifestFile)
			notDirectory.Origin = OriginDataDirectory
			loaded.Refused = append(loaded.Refused, *notDirectory)
			continue
		}
		dataNames = append(dataNames, entry.Name())
		dataRequires[entry.Name()] = manifestRequires(os.DirFS(filepath.Join(root, entry.Name())))
	}
	for _, name := range orderByRequires(dataNames, dataRequires, taken) {
		onDisk := filepath.Join(root, name)
		plugin, refusal := LoadOne(ctx, loaded.Set, os.DirFS(onDisk), name, OriginDataDirectory, onDisk,
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

// LoadOne validates one plugin directory and, when it is accepted, adds its providers to the set,
// admits its endpoints and creates its tables. Nothing of a refused plugin is added anywhere.
func LoadOne(ctx context.Context, set *provider.Set, directory fs.FS, name, origin, location, onDisk string,
	taken map[string]string, options Options) (*Plugin, *contract.Refusal) {
	raw, err := fs.ReadFile(directory, contract.ManifestFile)
	if err != nil {
		return nil, contract.Refuse(name, contract.RefusalManifestUnreadable, "%s cannot be read: %v", contract.ManifestFile, err)
	}
	manifest, err := contract.DecodeManifest(raw)
	if err != nil {
		return nil, contract.Refuse(name, contract.RefusalManifestInvalid, "%s is not a valid manifest: %v", contract.ManifestFile, err)
	}
	if refusal := contract.ValidateShape(manifest, name); refusal != nil {
		return nil, refusal
	}
	if owner, collides := taken[manifest.Name]; collides {
		return nil, contract.Refuse(name, contract.RefusalNameCollision,
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
	tables, refusal := contract.DeclaredTables(manifest)
	if refusal != nil {
		return nil, refusal
	}
	plugin.OwnTables, plugin.Tables = tables, tables
	if refusal := checkRequires(manifest, taken); refusal != nil {
		return nil, refusal
	}
	if plugin.statements, refusal = checkStatementFiles(plugin, directory); refusal != nil {
		return nil, refusal
	}
	if plugin.relations, refusal = checkRelationFiles(plugin, directory); refusal != nil {
		return nil, refusal
	}
	if refusal := checkSettings(manifest, taken); refusal != nil {
		return nil, refusal
	}
	if refusal := checkDestinations(manifest); refusal != nil {
		return nil, refusal
	}
	if refusal := checkEnablePages(manifest); refusal != nil {
		return nil, refusal
	}
	if refusal := checkMeasures(manifest); refusal != nil {
		return nil, refusal
	}
	if refusal := checkSampleMetrics(manifest); refusal != nil {
		return nil, refusal
	}
	if plugin.declaredDatasets, refusal = checkDatasets(manifest, directory); refusal != nil {
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
			return nil, contract.Refuse(name, contract.RefusalScriptDoesNotCompile, "%v", err)
		}
	}
	if plugin.pushes, refusal = checkPush(plugin, exports); refusal != nil {
		return nil, refusal
	}
	if plugin.derivations, refusal = checkDerivations(plugin, exports, providers, options); refusal != nil {
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
			return nil, contract.Refuse(name, contract.RefusalTableInvalid, "the table %s is not a dataset the catalogue can serve: %v",
				table.Table, err)
		}
		plugin.datasets = append(plugin.datasets, declared)
	}

	// Everything is valid: the side effects, in an order that leaves nothing behind on a
	// failure but the one the store reports.
	// A load given no store validates and serves nothing, so its measures reach no widget: a measure is
	// registered for the process by the load that serves it, as it is registered in that load's store.
	if options.Store != nil {
		if refusal := registerMeasures(manifest); refusal != nil {
			return nil, refusal
		}
		if refusal := registerSampleMetrics(manifest); refusal != nil {
			return nil, refusal
		}
	}
	if options.Store != nil {
		for _, table := range tables {
			event, err := options.Store.EnsurePluginTable(ctx, table)
			if err != nil {
				return nil, contract.Refuse(name, contract.RefusalTableInvalid, "%v", err)
			}
			if event != nil {
				plugin.Events = append(plugin.Events, *event)
			}
		}
	}
	if refusal := applySQL(ctx, plugin, options); refusal != nil {
		return nil, refusal
	}
	for _, endpoint := range endpoints {
		if err := opnsense.AdmitDeclaredEndpoint(endpoint); err != nil {
			return nil, contract.Refuse(name, contract.RefusalEndpointInvalid, "%v", err)
		}
	}
	for _, declared := range routes {
		for _, held := range declared[1:] {
			if err := opnsense.AdmitDeclaredEndpoint(held.Endpoint); err != nil {
				return nil, contract.Refuse(name, contract.RefusalEndpointInvalid, "%v", err)
			}
		}
	}
	for _, declared := range providers {
		if err := declared.add(set); err != nil {
			return nil, contract.Refuse(name, contract.RefusalProviderKeyCollision, "%v", err)
		}
		if origin == OriginBuiltin {
			set.MarkBuiltin(declared.info.Kind, declared.info.Key)
		}
		plugin.Providers = append(plugin.Providers, declared.info)
	}
	for _, setting := range manifest.Settings {
		taken[settingClaim(StorageKey(manifest.Name, setting.Key))] = manifest.Name
	}
	for _, derived := range plugin.derivations {
		set.AddDerivation(derived)
	}
	plugin.Destinations = append([]string(nil), manifest.Destinations...)
	// What the plugin declares of its endpoints reaches whichever broker reads this set.
	set.AddDeclarer(func(gate provider.Gate) {
		if brokered, isBroker := gate.(*broker.Broker); isBroker {
			plugin.DeclareTo(brokered)
		}
	})
	return plugin, nil
}

// declaredEndpoints checks every endpoint a manifest declares and returns them by name.
func declaredEndpoints(manifest contract.Manifest) (map[string]opnsense.Endpoint, map[string][]scripthost.Route, *contract.Refusal) {
	endpoints := map[string]opnsense.Endpoint{}
	routes := map[string][]scripthost.Route{}
	for _, declaration := range manifest.Endpoints {
		if !contract.NamePattern.MatchString(declaration.Name) {
			return nil, nil, contract.Refuse(manifest.Name, contract.RefusalEndpointInvalid, "the endpoint name %q is not a snake_case word",
				declaration.Name)
		}
		if _, duplicate := endpoints[declaration.Name]; duplicate {
			return nil, nil, contract.Refuse(manifest.Name, contract.RefusalEndpointInvalid, "the endpoint %q is declared twice",
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
			return nil, nil, contract.Refuse(manifest.Name, contract.RefusalEndpointInvalid, "the endpoint %q has the body %q, not none, "+
				"form or json", declaration.Name, declaration.Body)
		}
		switch declaration.Argument {
		case "", "none":
			endpoint.Argument = opnsense.NoArgument
		case "interface_names":
			endpoint.Argument = opnsense.InterfaceNameArgument
		default:
			return nil, nil, contract.Refuse(manifest.Name, contract.RefusalEndpointInvalid, "the endpoint %q has the argument %q, not none "+
				"or interface_names", declaration.Name, declaration.Argument)
		}
		if endpoint.Method == "" {
			endpoint.Method = http.MethodGet
		}
		if refusal := opnsense.CheckDeclaredEndpoint(endpoint); refusal != nil {
			return nil, nil, contract.Refuse(manifest.Name, refusal.Code, "the endpoint %q: %s", declaration.Name, refusal.Detail)
		}
		if reason := broker.CheckRead(endpoint.Method, endpoint.Path); reason != "" {
			return nil, nil, contract.Refuse(manifest.Name, contract.RefusalEndpointNotReadAction, "the endpoint %q: %s", declaration.Name, reason)
		}
		if refusal := checkBrokerKeys(manifest, declaration); refusal != nil {
			return nil, nil, refusal
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

// loadScript reads and compiles the script a manifest names, and runs it once to see what it
// exports.
func loadScript(plugin *Plugin, directory fs.FS, timeout time.Duration) *contract.Refusal {
	if plugin.Manifest.Script == "" {
		return nil
	}
	source, err := fs.ReadFile(directory, plugin.Manifest.Script)
	if err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalScriptMissing, "%s cannot be read: %v", plugin.Manifest.Script, err)
	}
	compiled, err := scripthost.Compile(plugin.Name, plugin.Manifest.Script, string(source), plugin.Endpoints, timeout)
	if err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalScriptDoesNotCompile, "%v", err)
	}
	if _, err := compiled.ExportsOf(); err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalScriptDoesNotCompile, "running %s to read its exports: %v",
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
	add  func(set *provider.Set) error
}

// ownKindAttributes are the attributes of a kind the plugin declares for itself: its manifest's `kinds`
// entry, or the defaults.
func ownKindAttributes(plugin *Plugin, kind string) provider.KindAttributes {
	attributes := provider.KindAttributes{Kind: kind, Plugin: plugin.Name, Bindable: true}
	for _, declared := range plugin.Manifest.Kinds {
		if declared.Kind == kind {
			attributes.Concurrent, attributes.Push = declared.Concurrent, declared.Push
			if declared.Bindable != nil {
				attributes.Bindable = *declared.Bindable
			}
		}
	}
	return attributes
}

// checkKinds checks the manifest's `kinds`: each a kind of the plugin's own that one of its providers
// names, listed once.
func checkKinds(plugin *Plugin) *contract.Refusal {
	provided := map[string]bool{}
	for _, declaration := range plugin.Manifest.Providers {
		provided[declaration.Kind] = true
	}
	seen := map[string]bool{}
	for _, declared := range plugin.Manifest.Kinds {
		switch {
		case provider.IsCoreKind(declared.Kind):
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "kinds names %q, a core kind, whose "+
				"attributes are its kind file's", declared.Kind)
		case !provided[declared.Kind]:
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "kinds names %q, which none of the "+
				"plugin's providers implements", declared.Kind)
		case seen[declared.Kind]:
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "kinds names %q twice", declared.Kind)
		}
		seen[declared.Kind] = true
	}
	return nil
}

// checkProviders checks every provider a manifest declares and returns how to add each.
func checkProviders(set *provider.Set, plugin *Plugin) ([]checkedProvider, *contract.Refusal) {
	if refusal := checkKinds(plugin); refusal != nil {
		return nil, refusal
	}
	var exports map[string]map[string]string
	if plugin.script != nil {
		var err error
		if exports, err = plugin.script.ExportsOf(); err != nil {
			return nil, contract.Refuse(plugin.Name, contract.RefusalScriptDoesNotCompile, "%v", err)
		}
	}
	var checked []checkedProvider
	seen := map[[2]string]bool{}
	for _, declaration := range plugin.Manifest.Providers {
		key := declaration.Key
		if key == "" {
			key = plugin.Name
		}
		if !contract.NamePattern.MatchString(key) {
			return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "the provider key %q is not a snake_case word", key)
		}
		if strings.TrimSpace(declaration.DisplayName) == "" {
			return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "the %s provider has no display_name",
				declaration.Kind)
		}
		if reason, coreOnly := provider.CoreOnly(declaration.Kind); coreOnly {
			return nil, contract.Refuse(plugin.Name, contract.RefusalCoreOnlyKind, "the kind %q is %s", declaration.Kind, reason)
		}
		kindContract, core := provider.ContractOf(declaration.Kind)
		var own provider.KindAttributes
		if !core {
			if !contract.NamePattern.MatchString(declaration.Kind) || len(plugin.OwnTables) == 0 {
				return nil, contract.Refuse(plugin.Name, contract.RefusalUnknownKind, "the kind %q is not a core kind (%v), and a kind "+
					"of the plugin's own needs tables of its own to write into", declaration.Kind, provider.CoreKinds())
			}
			if owner, owned := set.DeclaredKindOwner(declaration.Kind); owned && owner != plugin.Name {
				return nil, contract.Refuse(plugin.Name, contract.RefusalUnknownKind, "the kind %q is declared by the plugin %s",
					declaration.Kind, owner)
			}
			kindContract = declaredContract(declaration.Kind)
			own = ownKindAttributes(plugin, declaration.Kind)
		}
		switch declaration.Source {
		case "":
		case providerSourcePush:
			if core || !own.Push || declaration.Probe != nil || declaration.Read != nil || declaration.Live != nil {
				return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "the %s provider has the source push, which "+
					"only a provider of a kind of the plugin's own declared with push, and with no probe, read or live pass, "+
					"may have", declaration.Kind)
			}
		default:
			return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "the %s provider has the source %q; the only "+
				"source a provider names is push", declaration.Kind, declaration.Source)
		}
		identity := [2]string{declaration.Kind, key}
		if seen[identity] || set.Taken(declaration.Kind, key) {
			return nil, contract.Refuse(plugin.Name, contract.RefusalProviderKeyCollision,
				"the %s provider %q is already declared", declaration.Kind, key)
		}
		seen[identity] = true
		if declaration.Source == providerSourcePush && declaration.CadenceSeconds == 0 {
			// Its pass reads the hub, never the firewall: it refreshes the availability row.
			declaration.CadenceSeconds = pushProviderCadenceSeconds
		}
		surveyed := core && !kindContract.OwnCadence
		if refusal := checkCadence(plugin.Name, declaration, surveyed); refusal != nil {
			return nil, refusal
		}
		if refusal := checkTriggers(plugin.Name, declaration, surveyed); refusal != nil {
			return nil, refusal
		}
		switch declaration.ReadStrategy {
		case "", "full_reread", "time_window", "watermark":
		default:
			return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "read_strategy %q is not full_reread, time_window "+
				"or watermark", declaration.ReadStrategy)
		}
		info := ProviderInfo{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName,
			ReadStrategy: declaration.ReadStrategy, CadenceSeconds: declaration.CadenceSeconds,
			MinCadenceSeconds: declaration.MinCadenceSeconds, TriggeredBy: declaration.TriggeredBy,
			Endpoints: endpointInfos(plugin.Endpoints)}
		base := implementation{plugin: plugin.Name, contract: kindContract, endpoints: plugin.Endpoints,
			scriptProbe: scriptEndpoint(plugin.Name, plugin.Manifest.Script)}
		if (core && kindContract.Push && !provider.PolledRecordTables(kindContract)[kindContract.PushRecord]) ||
			(!core && own.Push && declaration.Source == "") {
			// The kind's records arrive only by push and its polled functions read something else -- the netflow
			// plugin's exporter configuration -- so a healthy read is not a healthy provider while its push entry
			// receives nothing (D1 of the live validation of 17 October 2026).
			base.pushFed, base.key = plugin, key
		}
		if !core {
			base.tables = map[string]plugindecl.PluginTable{}
			for _, table := range plugin.OwnTables {
				base.tables[table.Table] = table
			}
		}
		if (declaration.Probe != nil || declaration.Read != nil) && declaration.Live != nil {
			_, refusal := checkLive(plugin, declaration, exports)
			return nil, refusal
		}
		var serving provider.Implementation
		declarative := false
		switch {
		case declaration.Source == providerSourcePush:
			info.Form = FormPush
			serving = pushProviderImplementation{plugin: plugin, kind: declaration.Kind, key: key}
		case declaration.Probe != nil || declaration.Read != nil:
			info.Form = FormDeclarative
			form, refusal := checkDeclarative(plugin, declaration, kindContract, core)
			if refusal != nil {
				return nil, refusal
			}
			base.invoker = form
			serving, declarative = base, true
		default:
			info.Form = FormScript
			if plugin.script == nil {
				return nil, contract.Refuse(plugin.Name, contract.RefusalScriptMissing, "the %s provider declares no probe and no read, "+
					"so a script serves it, and the manifest names none", declaration.Kind)
			}
			if refusal := checkExports(plugin.Name, exports, declaration.Kind, kindContract, core); refusal != nil {
				return nil, refusal
			}
			live, refusal := checkLive(plugin, declaration, exports)
			if refusal != nil {
				return nil, refusal
			}
			base.invoker = scriptInvoker{script: plugin.script, namespace: declaration.Kind}
			serving = base
			if live != nil {
				if plugin.lives == nil {
					plugin.lives = map[string]*liveBinding{}
				}
				plugin.lives[key] = live
				info.Live = listedLive(declaration)
				serving = liveImplementation{implementation: base, script: plugin.script, live: live}
			}
		}
		values, err := plugin.exportedValues(kindContract, declarative)
		if err != nil {
			return nil, contract.Refuse(plugin.Name, contract.RefusalScriptMissingFunction, "the %s provider's exported values: %v",
				declaration.Kind, err)
		}
		for name, source := range declaration.ValueSources {
			if _, exported := values[name]; !exported || strings.TrimSpace(source.Source) == "" {
				return nil, contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "the %s provider cites the value %q, "+
					"which it does not export, or cites it with no source", declaration.Kind, name)
			}
		}
		registration := provider.Registration{Kind: declaration.Kind, Key: key, DisplayName: declaration.DisplayName,
			Plugin: plugin.Name, Cadence: cadenceOf(declaration),
			MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second,
			Triggers:   declaration.TriggeredBy, Values: values, Implementation: serving}
		isCore := core
		checked = append(checked, checkedProvider{info: info, add: func(set *provider.Set) error {
			if !isCore {
				if err := set.DeclareKind(own); err != nil {
					return err
				}
			}
			if plugin.implementations == nil {
				plugin.implementations = map[string]provider.Implementation{}
			}
			plugin.implementations[registration.Kind] = registration.Implementation
			return set.Add(registration)
		}})
	}
	return checked, nil
}

// checkCadence checks a provider's cadence: a core kind with a surveyed interval declares none,
// any other kind declares one, at or above its own lower bound.
func checkCadence(plugin string, declaration contract.ProviderDeclaration, surveyed bool) *contract.Refusal {
	if surveyed {
		if declaration.CadenceSeconds != 0 || declaration.MinCadenceSeconds != 0 {
			return contract.Refuse(plugin, contract.RefusalCadenceInvalid, "the %s kind runs at the interval the collection surface "+
				"sets, so its provider declares no cadence", declaration.Kind)
		}
		return nil
	}
	if declaration.CadenceSeconds <= 0 {
		return contract.Refuse(plugin, contract.RefusalCadenceInvalid, "the %s provider declares no cadence_seconds, which its kind "+
			"needs", declaration.Kind)
	}
	if declaration.MinCadenceSeconds < 0 || declaration.CadenceSeconds < declaration.MinCadenceSeconds {
		return contract.Refuse(plugin, contract.RefusalCadenceInvalid, "cadence_seconds %d is below min_cadence_seconds %d",
			declaration.CadenceSeconds, declaration.MinCadenceSeconds)
	}
	return nil
}

// cadenceOf is a provider's declared cadence.
func cadenceOf(declaration contract.ProviderDeclaration) time.Duration {
	return time.Duration(declaration.CadenceSeconds) * time.Second
}

// checkExports checks that a script exports every function and value its kind's contract names.
func checkExports(plugin string, exports map[string]map[string]string, kind string, kindContract provider.Contract,
	core bool) *contract.Refusal {
	members := exports[kind]
	required := []string{"probe", "collect"}
	if core {
		required = nil
		for _, function := range kindContract.Functions {
			required = append(required, function.Name)
		}
	}
	for _, function := range required {
		if members[function] != "function" {
			return contract.Refuse(plugin, contract.RefusalScriptMissingFunction, "the script does not export the function %s.%s",
				kind, function)
		}
	}
	for _, value := range kindContract.Values {
		// An optional value -- pf's state timeouts on the firewall_log contract -- may be left out: the core then
		// publishes none for the provider, and what reads it says what it does without one.
		if value.Required && members[value.Name] != "value" {
			return contract.Refuse(plugin, contract.RefusalScriptMissingFunction, "the script does not export the value %s.%s",
				kind, value.Name)
		}
	}
	return nil
}

// endpointInfos lists a plugin's endpoints, by name.
func endpointInfos(endpoints map[string]opnsense.Endpoint) []EndpointInfo {
	infos := make([]EndpointInfo, 0, len(endpoints))
	for _, name := range scripthost.SortedKeys(endpoints) {
		infos = append(infos, EndpointInfo{Name: name, Method: endpoints[name].Method, Path: endpoints[name].Path})
	}
	return infos
}

// loadWidgets checks that every widget a manifest names has its directory, widgets/<type>/, and
// keeps it for the widget service. The widget itself is validated by the widget service, which
// reports a refused widget with the widget's own code -- and, for a built-in, refuses to start.
func loadWidgets(plugin *Plugin, directory fs.FS, location, onDisk, origin string) *contract.Refusal {
	for _, name := range plugin.Manifest.Widgets {
		relative := path.Join("widgets", name)
		info, err := fs.Stat(directory, relative)
		if err != nil || !info.IsDir() {
			return contract.Refuse(plugin.Name, contract.RefusalWidgetMissing, "the widget %q has no directory widgets/%s", name, name)
		}
		files, err := fs.Sub(directory, relative)
		if err != nil {
			return contract.Refuse(plugin.Name, contract.RefusalWidgetMissing, "the widget %q: %v", name, err)
		}
		where := path.Join(location, relative)
		if onDisk != "" {
			where = filepath.Join(onDisk, "widgets", name)
		}
		plugin.WidgetDirectories = append(plugin.WidgetDirectories, where)
		plugin.Widgets = append(plugin.Widgets, widget.Source{Plugin: plugin.Name, Type: name,
			Builtin: origin == OriginBuiltin, Location: where, Files: files, Datasets: plugin.Manifest.Datasets})
	}
	return nil
}

// WidgetSources returns the widgets every loaded plugin carries, for the widget service: the
// built-ins' first, each plugin's in its manifest's order.
func (l *Loaded) WidgetSources() []widget.Source {
	sources := []widget.Source{}
	bindable := l.Set.BindableKinds()
	for _, plugin := range l.Members {
		for _, source := range plugin.Widgets {
			source.BindableKinds = bindable
			sources = append(sources, source)
		}
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
func loadStrings(plugin *Plugin, directory fs.FS, providers []checkedProvider) *contract.Refusal {
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
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "locale/%s cannot be read: %v", entry.Name(), err)
		}
		var catalogue struct {
			Language string            `json:"language"`
			Strings  map[string]string `json:"strings"`
		}
		if err := json.Unmarshal(raw, &catalogue); err != nil {
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "locale/%s is not a catalogue file: %v",
				entry.Name(), err)
		}
		language := strings2Language(entry.Name())
		if catalogue.Language != language {
			return contract.Refuse(plugin.Name, contract.RefusalManifestInvalid, "locale/%s declares the language %q",
				entry.Name(), catalogue.Language)
		}
		for key := range catalogue.Strings {
			if !owned[key] && !strings.HasPrefix(key, "plugin."+plugin.Name+".") {
				return contract.Refuse(plugin.Name, contract.RefusalStringOutsideNamespace, "locale/%s defines %q, which is not "+
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
func checkDestinations(manifest contract.Manifest) *contract.Refusal {
	for _, destination := range manifest.Destinations {
		parsed, err := url.Parse(destination)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.User != nil {
			return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the destination %q is not a scheme and a host, "+
				"such as https://example.org", destination)
		}
	}
	return nil
}

// extensions are what a plugin's script may reach beyond the firewall, given the options it was
// loaded with.
func (p *Plugin) extensions(options Options) *scripthost.Extensions {
	extra := &scripthost.Extensions{}
	extra.Datasets = p.datasetAccess(options)
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
func checkTriggers(plugin string, declaration contract.ProviderDeclaration, surveyed bool) *contract.Refusal {
	if len(declaration.TriggeredBy) == 0 {
		return nil
	}
	if surveyed {
		return contract.Refuse(plugin, contract.RefusalTriggerInvalid, "the %s kind runs at the interval the collection surface "+
			"sets, so its provider names no trigger", declaration.Kind)
	}
	for _, name := range declaration.TriggeredBy {
		channel, subject, hasSubject := strings.Cut(name, ":")
		if !slices.Contains(sqldb.Channels(), channel) || (hasSubject && !contract.NamePattern.MatchString(subject)) {
			return contract.Refuse(plugin, contract.RefusalTriggerInvalid, "triggered_by names %q, which is not a channel the core "+
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
func pluginDataset(plugin string, declaration contract.TableDeclaration, table plugindecl.PluginTable,
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
