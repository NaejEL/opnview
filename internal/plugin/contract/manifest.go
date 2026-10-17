package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/infer"
)

// ManifestFile is the manifest's file name in every plugin directory.
const ManifestFile = "plugin.json"

// The plugin API versions this build reads (docs/plugins.md, "The plugin API version"). A manifest declaring a
// version outside the range is refused, naming both, so a format change is never half understood. Version 2
// (the plugin-API amendment of 17 October 2026: kinds as data, datasets, derivations, measures, pages)
// is the only one: no version-1 plugin exists outside the repository, and none is read.
const (
	MinAPIVersion = 2
	MaxAPIVersion = 2
)

// Manifest is the content of one plugin.json: JSON, decoded strictly, so a misspelt key is a
// refusal rather than a setting silently ignored.
type Manifest struct {
	// APIVersion is the plugin API version the plugin targets.
	APIVersion int `json:"api_version"`
	// Name is the plugin's name: its directory's name, a snake_case word. It is the provider key
	// of every provider the plugin declares, unless a provider names its own key.
	Name string `json:"name"`
	// Version is the plugin's own version, which opnview reports and never interprets.
	Version string `json:"version"`
	// DisplayName is what the plugin is called, in English, for the listing.
	DisplayName string `json:"display_name"`
	// Description says what it does.
	Description string `json:"description,omitempty"`
	// Script is the JavaScript file, relative to the directory, holding the code the manifest
	// cannot express. Empty for a plugin that is declarative throughout.
	Script string `json:"script,omitempty"`
	// ScriptVersions is the range of OPNsense versions the script serves, and Scripts the script
	// files of other ranges (A19). Both optional.
	ScriptVersions *VersionRange   `json:"script_versions,omitempty"`
	Scripts        []ScriptVariant `json:"scripts,omitempty"`
	// Endpoints are the firewall endpoints the plugin reads, each by a name the script and the
	// mappings use.
	Endpoints []EndpointDeclaration `json:"endpoints,omitempty"`
	// Providers are the kinds the plugin implements.
	Providers []ProviderDeclaration `json:"providers,omitempty"`
	// Kinds are the attributes of the kinds the plugin declares for itself -- a kind one of its providers
	// names that is no core kind. A kind of its own listed nowhere here takes the defaults: one active
	// provider at a time, no push entry, bindable by a widget.
	Kinds []KindDeclaration `json:"kinds,omitempty"`
	// Tables are the tables the plugin owns.
	Tables []TableDeclaration `json:"tables,omitempty"`
	// Widgets are the widget directories the plugin carries, relative to its widgets/ directory.
	Widgets []string `json:"widgets,omitempty"`
	// Settings are the settings the plugin declares.
	Settings []SettingDeclaration `json:"settings,omitempty"`
	// Destinations are the hosts beyond the firewall the plugin contacts, each a scheme and a host
	// (such as https://publicsuffix.org). The fetch facility refuses any other.
	Destinations []string `json:"destinations,omitempty"`
	// Push are the push sources the plugin receives: data the firewall sends to opnview over syslog
	// or NetFlow, which the user configures on the firewall (push.go, docs/plugins.md "Push sources").
	Push []PushDeclaration `json:"push,omitempty"`
}

// EndpointDeclaration is one firewall endpoint a plugin reads, in the fields an
// opnsense.Endpoint carries.
type EndpointDeclaration struct {
	// Name is how the script and the mappings name it: a snake_case word.
	Name string `json:"name"`
	// Path is the path on the firewall, /api/<module>/<controller>/<command>.
	Path string `json:"path"`
	// Method is GET or POST.
	Method string `json:"method"`
	// Body is how a request body is encoded: none, form or json.
	Body string `json:"body,omitempty"`
	// Argument is the kind of positional path argument it takes: none or interface_names.
	Argument string `json:"argument,omitempty"`
	// Stream marks a server-sent event stream, read only with a bound.
	Stream bool `json:"stream,omitempty"`
	// SurveySection and UpstreamURL say what establishes the endpoint: the section of
	// docs/opnsense-api-survey.md, and the documentation or source it cites. A built-in plugin
	// carries both; a third party is asked to.
	SurveySection string `json:"survey_section,omitempty"`
	UpstreamURL   string `json:"upstream_url,omitempty"`
	// Note records what a caller has to know about it.
	Note string `json:"note,omitempty"`
	// MaxAgeSeconds is how old an answer may be and still be good enough, by default, for every
	// call of the endpoint that states none (A17 of the broker amendment).
	MaxAgeSeconds float64 `json:"max_age_seconds,omitempty"`
	// FloorSeconds is the shortest refresh interval the endpoint is ever given: a call asking for
	// less is served at it (A12).
	FloorSeconds float64 `json:"floor_seconds,omitempty"`
	// Corrections correct what the broker infers of the endpoint's answers: nature, key, cursor,
	// parameter roles, field types, domain names, value-keyed objects (A10).
	Corrections *infer.Corrections `json:"corrections,omitempty"`
	// DiscardEvents is how many events a stream's connection begins with that are discarded.
	DiscardEvents int `json:"discard_events,omitempty"`
	// Replaces names another endpoint of the manifest that this stream replaces while it runs:
	// the plugin's demand for that endpoint is suspended until the stream drops (A14).
	Replaces string `json:"replaces,omitempty"`
	// Versions is the range of OPNsense versions this declaration serves, and Variants the routes
	// of other ranges (A19, docs/plugins.md "OPNsense versions"). Both optional: an endpoint with neither
	// is called the same way on every version.
	Versions *VersionRange     `json:"versions,omitempty"`
	Variants []EndpointVariant `json:"variants,omitempty"`
}

// ProviderDeclaration is one kind the plugin implements.
type ProviderDeclaration struct {
	// Kind is a core kind (docs/plugins.md, "Kinds") or a kind of the plugin's own, which then
	// writes into the plugin's tables.
	Kind string `json:"kind"`
	// Key is the provider key, when it is not the plugin's name.
	Key string `json:"key,omitempty"`
	// DisplayName is the provider's name in the registry row.
	DisplayName string `json:"display_name"`
	// CadenceSeconds is how often a pass runs, for a kind whose cadence is the plugin's to state
	// (reconciled_state and a kind of the plugin's own); MinCadenceSeconds is the lowest value it
	// may take. A core kind with a surveyed cadence runs at the interval the collection surface
	// sets, and declaring one for it is refused.
	CadenceSeconds    int `json:"cadence_seconds,omitempty"`
	MinCadenceSeconds int `json:"min_cadence_seconds,omitempty"`
	// TriggeredBy names core notifications (docs/plugins.md, "Notifications") that also run a pass,
	// never sooner than MinCadenceSeconds after the last one began: a channel, or a channel and its
	// subject ("opnview_collection_pass:dhcp_lease", "opnview_plugin_pass:<plugin>"). Only a
	// provider that states its own cadence may name one.
	TriggeredBy []string `json:"triggered_by,omitempty"`
	// ReadStrategy says how the source is read incrementally: full_reread (every pass reads the
	// whole set and the identity of a row makes a re-read idempotent), time_window (every pass
	// asks for a window) or watermark (every pass resumes from a durable position). It is
	// documentation the listing reports; what a kind does with what it is handed is its own.
	ReadStrategy string `json:"read_strategy,omitempty"`
	// Probe and Read are the declarative form: a probe and a read the manifest expresses
	// without a script. A provider declaring neither is served by the script.
	Probe *ProbeDeclaration `json:"probe,omitempty"`
	Read  *ReadDeclaration  `json:"read,omitempty"`
	// Live declares a live pass: a pass reading only live endpoints, run at a live cadence while
	// somebody looks at a live figure (live.go, docs/plugins.md "Live pass"). Optional.
	Live *LiveDeclaration `json:"live,omitempty"`
	// Source is "push" for a provider of a kind of the plugin's own that its push declarations feed:
	// it declares no probe, no read and no script function, and its availability is the push
	// listener's and the declarations' liveness (push.go). Empty for every other provider.
	Source string `json:"source,omitempty"`
}

// KindDeclaration is a kind of the plugin's own, as data (PA2 of the plugin-API amendment): the same
// attributes a core kind's file carries.
type KindDeclaration struct {
	// Kind is the kind's name, which one of the plugin's providers implements.
	Kind string `json:"kind"`
	// Concurrent says several providers of the kind may be active at once: true only when every row of
	// its tables says which provider reported it.
	Concurrent bool `json:"concurrent,omitempty"`
	// Push says the kind's provider may be fed by the plugin's push entries (`source: push`).
	Push bool `json:"push,omitempty"`
	// Bindable says a widget manifest may bind the kind; absent is true.
	Bindable *bool `json:"bindable,omitempty"`
}

// ProbeDeclaration is a declarative probe.
type ProbeDeclaration struct {
	// Endpoint names the endpoint asked.
	Endpoint string `json:"endpoint"`
	// Check is what its answer must be for the provider to be reachable and separable:
	// "answers" (any usable answer) or "service_running" (an OPNsense service status reporting
	// running).
	Check string `json:"check"`
}

// ReadDeclaration is a declarative read: one endpoint, its rows, and each record field mapped
// from a field of a row.
type ReadDeclaration struct {
	// Endpoint names the endpoint read.
	Endpoint string `json:"endpoint"`
	// Paginate sends the standard grid-search form, current=1 and rowCount=<page size>.
	Paginate bool `json:"paginate,omitempty"`
	// Table is, for a kind of the plugin's own, the table the rows are written into.
	Table string `json:"table,omitempty"`
	// Fields maps each record field (or table column) to where its value comes from.
	Fields map[string]FieldMapping `json:"fields"`
}

// FieldMapping is where one record field's value comes from: a field of the row, read as the
// record field's type or as the reading named by As, or a constant Value.
type FieldMapping struct {
	// From is the row's field, a dotted path for a nested one.
	From string `json:"from,omitempty"`
	// As is how the value is read, when the record field's type is not enough: epoch,
	// iso_time, syslog_time, filter_log_time, port, mac or now.
	As string `json:"as,omitempty"`
	// Value is a constant.
	Value any `json:"value,omitempty"`
}

// UnmarshalJSON reads a mapping written either as a bare string, which is From, or as an
// object.
func (m *FieldMapping) UnmarshalJSON(raw []byte) error {
	var from string
	if err := json.Unmarshal(raw, &from); err == nil {
		*m = FieldMapping{From: from}
		return nil
	}
	type plain FieldMapping
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*m = FieldMapping(decoded)
	return nil
}

// TableDeclaration is one table the plugin owns.
type TableDeclaration struct {
	// Name is the table's own name; the database holds it as plugin_<plugin>__<name>.
	Name string `json:"name"`
	// Columns are its columns, in order.
	Columns []ColumnDeclaration `json:"columns"`
	// PrimaryKey names the primary key's columns; a row written again with the same key
	// replaces the first.
	PrimaryKey []string `json:"primary_key,omitempty"`
	// Indexes are its secondary indexes.
	Indexes []IndexDeclaration `json:"indexes,omitempty"`
	// Retention says how long a row is kept.
	Retention *RetentionDeclaration `json:"retention,omitempty"`
	// Nature says what a bucket of the data catalogue with no row of the table means: event (a
	// covered bucket with no row is a true zero) or sample (a gap); empty is sample, which never
	// draws a zero it did not read.
	Nature string `json:"nature,omitempty"`
}

// ColumnDeclaration is one column.
type ColumnDeclaration struct {
	Name string `json:"name"`
	// Type is integer, real or text.
	Type string `json:"type"`
	// Nullable admits NULL.
	Nullable bool `json:"nullable,omitempty"`
	// CarriesDomain says the column holds a domain name, which aggregate_mode = no_domains
	// withholds from every widget reading the table.
	CarriesDomain bool `json:"carries_domain,omitempty"`
	// Role is time, dimension or measure, which the data catalogue serves; empty is dimension, or
	// time for the column the retention reads.
	Role string `json:"role,omitempty"`
	// Unit is a measure's unit: a lower-case word with no space, such as byte or second.
	Unit string `json:"unit,omitempty"`
	// Aggregations are the aggregations the data catalogue admits on the column; empty takes its
	// role's defaults (docs/plugins.md, "Tables").
	Aggregations []string `json:"aggregations,omitempty"`
}

// IndexDeclaration is one index.
type IndexDeclaration struct {
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

// RetentionDeclaration is a table's retention: the instant column the horizon reads, and how
// long a row is kept after it -- zero follows the installation's retention_seconds setting.
type RetentionDeclaration struct {
	Column  string `json:"column"`
	Seconds int64  `json:"seconds,omitempty"`
}

// NamePattern is what a plugin name, an endpoint name, a table name and a column name match: a
// lower-case snake_case word with no double underscore, so a table's database name
// (plugin_<plugin>__<table>) is never ambiguous.
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// scriptPattern is a script file name: a plain file of the plugin's own directory.
var scriptPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.js$`)

// errTrailingData is a manifest followed by another JSON value.
var errTrailingData = errors.New("data after the manifest object")

// DecodeManifest reads one plugin.json strictly.
func DecodeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if decoder.More() {
		return Manifest{}, errTrailingData
	}
	return manifest, nil
}

// ValidateShape checks what a manifest says about itself, before anything about the world --
// the directory, the kinds registered, the firewall's endpoints -- is consulted.
func ValidateShape(manifest Manifest, directory string) *Refusal {
	if manifest.APIVersion < MinAPIVersion || manifest.APIVersion > MaxAPIVersion {
		if MinAPIVersion == MaxAPIVersion {
			return Refuse(directory, RefusalUnsupportedAPIVersion,
				"the plugin targets plugin API version %d and this build reads version %d only",
				manifest.APIVersion, MaxAPIVersion)
		}
		return Refuse(directory, RefusalUnsupportedAPIVersion,
			"the plugin targets plugin API version %d and this build reads versions %d to %d",
			manifest.APIVersion, MinAPIVersion, MaxAPIVersion)
	}
	if !NamePattern.MatchString(manifest.Name) || len(manifest.Name) > 48 {
		return Refuse(directory, RefusalManifestInvalid,
			"name %q is not a snake_case word of at most 48 characters with no double underscore", manifest.Name)
	}
	if manifest.Name != directory {
		return Refuse(directory, RefusalNameMismatch,
			"the manifest declares the name %q in the directory %q: they must be the same", manifest.Name, directory)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Refuse(directory, RefusalManifestInvalid, "version is empty")
	}
	if strings.TrimSpace(manifest.DisplayName) == "" {
		return Refuse(directory, RefusalManifestInvalid, "display_name is empty")
	}
	if manifest.Script != "" && !scriptPattern.MatchString(manifest.Script) {
		return Refuse(directory, RefusalManifestInvalid,
			"script %q must be a plain file name ending in .js, in the plugin's own directory", manifest.Script)
	}
	if len(manifest.Providers) == 0 && len(manifest.Widgets) == 0 {
		return Refuse(directory, RefusalManifestInvalid, "the plugin declares no provider and no widget")
	}
	for _, widget := range manifest.Widgets {
		if !NamePattern.MatchString(widget) {
			return Refuse(directory, RefusalManifestInvalid, "widget %q is not a snake_case directory name", widget)
		}
	}
	return nil
}

// VersionRange is a range of OPNsense versions: from From, included, to Before, excluded. Either
// bound may be absent, never both.
type VersionRange struct {
	From   string `json:"from,omitempty"`
	Before string `json:"before,omitempty"`
	// Package names an OPNsense plugin's package whose version the range is about, instead of the
	// core's. Refused at load for now: see package_version_unavailable.
	Package string `json:"package,omitempty"`
}

// String renders a range for a refusal or an outcome's detail.
func (r VersionRange) String() string {
	switch {
	case r.From != "" && r.Before != "":
		return fmt.Sprintf("[%s, %s)", r.From, r.Before)
	case r.From != "":
		return fmt.Sprintf("[%s, ...)", r.From)
	default:
		return fmt.Sprintf("[..., %s)", r.Before)
	}
}

// EndpointVariant is one route of an endpoint for a range of versions.
type EndpointVariant struct {
	Versions VersionRange `json:"versions"`
	// Path, Method and Body are the route on those versions, as an endpoint declares them.
	Path   string `json:"path"`
	Method string `json:"method,omitempty"`
	Body   string `json:"body,omitempty"`
	// SurveySection and UpstreamURL cite what establishes the route.
	SurveySection string `json:"survey_section,omitempty"`
	UpstreamURL   string `json:"upstream_url,omitempty"`
	// Fields maps a name the script reads to the name the firewall writes on those versions, in
	// every object of the answer.
	Fields map[string]string `json:"fields,omitempty"`
	// Adapter names a function the script exports under `adapters`: it is handed the answer's value,
	// already renamed, and returns the value the script reads, where a rename is not enough.
	Adapter string `json:"adapter,omitempty"`
	// Corrections correct what the broker infers of the route's answers.
	Corrections *infer.Corrections `json:"corrections,omitempty"`
}

// ScriptVariant maps a range of versions to a whole script file.
type ScriptVariant struct {
	Versions VersionRange `json:"versions"`
	Script   string       `json:"script"`
}

// versionParts reads a version as numbers: 26.7.3 is 26, 7, 3; a hotfix suffix, 26.7.3_1, is one
// more part. It answers false for text that is not a version.
func versionParts(version string) ([]int, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, false
	}
	var parts []int
	for _, part := range strings.FieldsFunc(version, func(r rune) bool { return r == '.' || r == '_' }) {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, len(parts) > 0
}

// compareVersions orders two versions; a missing part is zero, so 26.7 is 26.7.0.
func compareVersions(a, b []int) int {
	for index := 0; index < len(a) || index < len(b); index++ {
		left, right := 0, 0
		if index < len(a) {
			left = a[index]
		}
		if index < len(b) {
			right = b[index]
		}
		if left != right {
			if left < right {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Valid reports what is wrong with a range, empty when nothing is.
func (r VersionRange) Valid() string {
	if r.From == "" && r.Before == "" {
		return "a range needs from, before, or both"
	}
	var from, before []int
	if r.From != "" {
		parts, ok := versionParts(r.From)
		if !ok {
			return fmt.Sprintf("%q is not a version", r.From)
		}
		from = parts
	}
	if r.Before != "" {
		parts, ok := versionParts(r.Before)
		if !ok {
			return fmt.Sprintf("%q is not a version", r.Before)
		}
		before = parts
	}
	if from != nil && before != nil && compareVersions(from, before) >= 0 {
		return fmt.Sprintf("from %s is not before %s", r.From, r.Before)
	}
	return ""
}

// Contains reports whether a version lies in the range.
func (r VersionRange) Contains(version string) bool {
	parts, ok := versionParts(version)
	if !ok {
		return false
	}
	if r.From != "" {
		from, _ := versionParts(r.From)
		if compareVersions(parts, from) < 0 {
			return false
		}
	}
	if r.Before != "" {
		before, _ := versionParts(r.Before)
		if compareVersions(parts, before) >= 0 {
			return false
		}
	}
	return true
}

// Overlaps reports whether two valid ranges share a version.
func (r VersionRange) Overlaps(other VersionRange) bool {
	// [a, b) and [c, d) overlap when a < d and c < b, an absent bound being infinite.
	below := func(from, before string) bool {
		if from == "" || before == "" {
			return true
		}
		low, _ := versionParts(from)
		high, _ := versionParts(before)
		return compareVersions(low, high) < 0
	}
	return below(r.From, other.Before) && below(other.From, r.Before)
}

// PushDeclaration is one push entry of a manifest.
type PushDeclaration struct {
	// Name is the entry's own name, a snake_case word: the parse function's name under the script's
	// push namespace, and the catalogue's.
	Name string `json:"name"`
	// Protocol is a registered push protocol: syslog or netflow.
	Protocol string `json:"protocol"`
	// Filter is the header filter, in the protocol's own form: {program, facility, severity} for
	// syslog, {require, where} for NetFlow.
	Filter json.RawMessage `json:"filter"`
	// Transports are the transports the entry accepts; empty is every one its protocol is received
	// over. An entry feeding a core kind is accepted only over the transports whose loss is detectable,
	// whatever it says -- TCP for syslog, UDP for NetFlow (Q9 of the push amendment).
	Transports []string `json:"transports,omitempty"`
	// Pattern is a regular expression in RE2 syntax whose named groups name output columns: the
	// fast path for a syslog message a regular expression can express.
	Pattern string `json:"pattern,omitempty"`
	// Parse names a function the script exports under `push`, called with a batch of decoded
	// messages, for a syslog message a regular expression cannot express.
	Parse string `json:"parse,omitempty"`
	// Fields maps output columns -- or, for an entry naming a kind, the fields of the kind's record -- to
	// fields every record carries: a syslog header field, or a NetFlow Information Element by registry
	// name -- one name, or a list whose first present one wins.
	Fields map[string]PushField `json:"fields,omitempty"`
	// OutputTable names one of the plugin's tables; Kind a core kind the plugin provides. One of the
	// two, never both.
	OutputTable string `json:"output_table,omitempty"`
	Kind        string `json:"kind,omitempty"`
	// Replaces names one of the plugin's endpoints, whose polling the entry replaces while it is alive.
	Replaces string `json:"replaces,omitempty"`
	// LivenessSeconds is how long the entry stays alive with no message; zero is push.DefaultLiveness.
	LivenessSeconds int `json:"liveness_seconds,omitempty"`
	// SurveySection names the section of docs/opnsense-api-survey.md establishing what the firewall
	// sends; a built-in carries it.
	SurveySection string `json:"survey_section,omitempty"`
}

// PushField is where one column's value comes from: field names, the first present one wins.
type PushField []string

// UnmarshalJSON reads one name or a list of names.
func (f *PushField) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*f = PushField{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return fmt.Errorf("a push field mapping is a field name or a list of field names")
	}
	*f = many
	return nil
}

// SettingDeclaration is one setting a plugin declares.
type SettingDeclaration struct {
	// Key is the setting's own key, a snake_case word; it is stored as <plugin>_<key>.
	Key string `json:"key"`
	// Type is integer, number, boolean, text, enum or secret.
	Type string `json:"type"`
	// Default is the value an unset setting takes. A secret has none.
	Default any `json:"default,omitempty"`
	// Min and Max bound an integer or a number.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Values is an enum's vocabulary.
	Values []string `json:"values,omitempty"`
	// Label is the catalogue key naming the setting, which the plugin's own locale/ carries.
	Label string `json:"label,omitempty"`
}

// LiveDeclaration is a provider's live pass.
type LiveDeclaration struct {
	// Function is the function of the kind's namespace the live pass calls. It is called as the
	// kind's read function is, and returns that function's answer.
	Function string `json:"function"`
	// CadenceSetting is the key of the plugin's declared integer setting holding the live cadence,
	// in seconds. Its default is the cadence with no row, and its min the lowest it may take.
	CadenceSetting string `json:"cadence_setting"`
	// Endpoints are the only endpoints the live pass may read, by their manifest names.
	Endpoints []string `json:"endpoints"`
}
