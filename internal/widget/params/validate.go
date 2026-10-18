package params

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Manifest validation, and the named reasons a widget is refused.
//
// A widget that fails any rule below is REFUSED: it is not served, the reason is named, and
// nothing else changes -- the other widgets load, the built-ins are untouched and the service
// starts. The code is a closed vocabulary, documented in docs/widget-authoring.md, so an
// operator reading the log knows which file to open.

// The refusal codes.
const (
	RefusalManifestUnreadable         = "manifest_unreadable"
	RefusalManifestInvalid            = "manifest_invalid"
	RefusalUnsupportedManifestVersion = "unsupported_manifest_version"
	RefusalUnknownShape               = "unknown_shape"
	RefusalUnknownProviderKind        = "unknown_provider_kind"
	RefusalUnknownParameterKind       = "unknown_parameter_kind"
	RefusalParameterVocabulary        = "parameter_vocabulary"
	RefusalBadDefault                 = "bad_default"
	RefusalReservedParameterKey       = "reserved_parameter_key"
	RefusalReferencesMismatch         = "references_mismatch"
	// RefusalRendererInvalid is a renderer slot that is not {"entry": "<file>.js"}, or a renderer
	// file that is not a classic script: it does not compile as one (an ES module's import or
	// export among the reasons).
	RefusalRendererInvalid = "renderer_invalid"
	// RefusalRendererMissing is a renderer the manifest names that the directory does not hold.
	RefusalRendererMissing           = "renderer_missing"
	RefusalTypeMismatchWithDirectory = "type_mismatch_with_directory"
	// RefusalTypeCollision is a widget whose type another widget already takes. It is the code
	// every part of the front's registry is refused with on a collision -- a theme's id, a
	// language's code, a widget's type -- so one rule reads the same everywhere
	// (specs/SPEC-step-7-frontend.md, A1: the built-in wins every collision).
	RefusalTypeCollision               = "name_collision"
	RefusalQueryMissing                = "query_missing"
	RefusalQueryUnreadable             = "query_unreadable"
	RefusalQueryMultipleStatements     = "query_multiple_statements"
	RefusalQueryNotReadOnly            = "query_not_read_only"
	RefusalQueryDoesNotPlan            = "query_does_not_plan"
	RefusalUnknownQueryParameter       = "unknown_query_parameter"
	RefusalMissingColumn               = "missing_column"
	RefusalTooManyQueries              = "too_many_queries"
	RefusalNotADirectory               = "not_a_directory"
	RefusalStatementRegistrationFailed = "statement_registration_failed"
	// RefusalScriptMissing is a script the manifest names that the directory does not hold.
	RefusalScriptMissing = "script_missing"
	// RefusalScriptDoesNotCompile is a script the interpreter cannot compile, that throws when it
	// is run to read what it exports, or that exports no widget.serve function.
	RefusalScriptDoesNotCompile = "script_does_not_compile"
	// RefusalQueryReadsCredential is a widget query reading a table that holds a credential, a
	// session or a setting row (store.CredentialTables): the HTTP API's data-leak guard.
	RefusalQueryReadsCredential = "query_reads_credential"
	// RefusalStatementNameInvalid is a statement of a widget's query files whose name is not a
	// snake_case word beginning with widget_.
	RefusalStatementNameInvalid = "statement_name_invalid"
	// RefusalMeasuredTwinMismatch is a statement's measured twin, <name>_measured, that binds other
	// parameters than <name>'s and :measure, or is a period template where <name> is not or the
	// reverse: the core runs the twin in its statement's place with the statement's parameters and the
	// measure's name (internal/widget/request/measure.go).
	RefusalMeasuredTwinMismatch = "measured_twin_mismatch"
)

// Refusal is one widget the loader would not serve.
type Refusal struct {
	// Type is the widget's type, or its directory name when the manifest could not be read.
	Type string
	// Code is one of the Refusal constants.
	Code string
	// Detail says what, precisely, was wrong.
	Detail string
	// Path is where the widget's directory was read from, for the listing of refused parts
	// (GET /api/widgets); empty when the loader had none to name.
	Path string
}

// Error implements error.
func (r *Refusal) Error() string {
	return fmt.Sprintf("widget %q refused (%s): %s", r.Type, r.Code, r.Detail)
}

// Refuse builds a Refusal.
func Refuse(widgetType, code, format string, arguments ...any) *Refusal {
	return &Refusal{Type: widgetType, Code: code, Detail: fmt.Sprintf(format, arguments...)}
}

// The closed vocabularies a manifest is validated against.
var (
	// Shapes are the twelve catalogue shapes (docs/widget-catalogue.md, Part 1).
	Shapes = []string{"figure", "ranked_bars", "time_series", "table", "matrix", "map", "donut",
		"tree", "graph", "sankey", "feed", "cards"}
	// ParameterKinds are the value shapes of docs/dashboard-format.md, "Parameters".
	ParameterKinds = []string{string(KindEnum), string(KindEnumSet), string(KindBoolean), string(KindInteger),
		string(KindNumber), string(KindReference), string(KindReferenceList), string(KindStringList),
		string(KindSeries), string(KindObject)}
	// Periods are the rolling windows a windowed widget's `period` may offer.
	Periods = []string{"1h", "24h", "7d", "30d"}
	// reservedQueryParameters are the names the engine binds itself.
	reservedQueryParameters = []string{"from", "to", "now"}
)

var (
	typePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	keyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	measurePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	queryFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.sql$`)
	// scriptFilePattern is a widget's script file name: a plain file of its own directory.
	scriptFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.js$`)
	// StatementNamePattern is the name of a statement a widget's query files declare.
	StatementNamePattern = regexp.MustCompile(`^widget_[a-z0-9]+(_[a-z0-9]+)*$`)
)

// ValidateManifest checks a manifest against every rule that needs nothing but the manifest, binding the
// core's bindable kinds. Rules that need the world -- the directory, the script, the query -- are the loader's.
func ValidateManifest(manifest Manifest) *Refusal {
	return ValidateManifestWith(manifest, CoreBindableKinds())
}

// ValidateManifestWith is ValidateManifest binding the kinds an installation's loaded plugins make
// bindable: the core's and the plugins' own.
func ValidateManifestWith(manifest Manifest, bindable []string) *Refusal {
	name := manifest.Type
	if name == "" {
		name = "(no type)"
	}
	if manifest.ManifestVersion != ManifestVersion {
		return Refuse(name, RefusalUnsupportedManifestVersion,
			"manifest_version is %d and this build reads version %d", manifest.ManifestVersion, ManifestVersion)
	}
	if !typePattern.MatchString(manifest.Type) {
		return Refuse(name, RefusalManifestInvalid,
			"type %q is not snake_case (a lower-case letter, then letters, digits and underscores)", manifest.Type)
	}
	if !Contains(Shapes, manifest.Shape) {
		return Refuse(name, RefusalUnknownShape, "shape %q is not one of the twelve shapes %v", manifest.Shape, Shapes)
	}
	seenKind := map[string]bool{}
	for _, kind := range manifest.Providers {
		if !Contains(bindable, kind) {
			return Refuse(name, RefusalUnknownProviderKind, "provider kind %q is not one of %v", kind, bindable)
		}
		if seenKind[kind] {
			return Refuse(name, RefusalManifestInvalid, "provider kind %q is listed twice", kind)
		}
		seenKind[kind] = true
	}
	if manifest.Primary != "" && !seenKind[manifest.Primary] {
		return Refuse(name, RefusalManifestInvalid, "primary %q is not one of the bound providers %v",
			manifest.Primary, manifest.Providers)
	}
	if manifest.NoDomains != NoDomainsNone && manifest.NoDomains != NoDomainsWhole {
		return Refuse(name, RefusalManifestInvalid, "no_domains is %q, not %q or %q", manifest.NoDomains,
			NoDomainsNone, NoDomainsWhole)
	}
	if manifest.Measure != "" && !measurePattern.MatchString(manifest.Measure) {
		return Refuse(name, RefusalManifestInvalid, "measure %q is not snake_case", manifest.Measure)
	}
	if renderer := manifest.Renderer; renderer != nil {
		if renderer.Invalid() != "" {
			return Refuse(name, RefusalRendererInvalid,
				"renderer must be {\"entry\": \"<file>.js\"}, a file of the widget's own directory: %s",
				renderer.Invalid())
		}
		if !scriptFilePattern.MatchString(renderer.Entry) {
			return Refuse(name, RefusalRendererInvalid,
				"renderer entry %q must be a plain file name ending in .js, in the widget's own directory",
				renderer.Entry)
		}
		if renderer.Entry == manifest.Script {
			return Refuse(name, RefusalRendererInvalid,
				"renderer entry %q is the widget's script: the script answers on the server and the renderer "+
					"draws in the browser, so they are two files", renderer.Entry)
		}
	}
	if manifest.FrontOnly() && manifest.Volumes {
		return Refuse(name, RefusalManifestInvalid,
			"a front-only widget is answered by nothing on the server, so it serves no volume figure")
	}
	if refusal := validateParameters(manifest); refusal != nil {
		return refusal
	}
	if refusal := validateReferences(manifest); refusal != nil {
		return refusal
	}
	if manifest.Volumes && (manifest.Script == "" || !manifest.Windowed) {
		return Refuse(name, RefusalManifestInvalid,
			"volumes is declared by a widget with a script, over a window: the core answers it under the measure "+
				"volume_measure chooses, which is decided over the window")
	}
	if manifest.Shape == "time_series" && !manifest.Windowed {
		return Refuse(name, RefusalManifestInvalid, "a time_series is drawn over a window, so it must be windowed")
	}
	if manifest.Windowed {
		var period *ManifestParameter
		for index := range manifest.Parameters {
			if manifest.Parameters[index].Key == "period" {
				period = &manifest.Parameters[index]
			}
		}
		if period == nil || period.Kind != string(KindEnum) || len(period.Values) == 0 {
			return Refuse(name, RefusalManifestInvalid,
				"a windowed widget declares a `period` enum parameter, drawn from %v", Periods)
		}
		for _, value := range period.Values {
			if !Contains(Periods, value) {
				return Refuse(name, RefusalParameterVocabulary, "period offers %q, which is not one of %v", value, Periods)
			}
		}
	}
	if manifest.Script != "" {
		if !scriptFilePattern.MatchString(manifest.Script) {
			return Refuse(name, RefusalManifestInvalid,
				"script %q must be a plain file name ending in .js, in the widget's own directory", manifest.Script)
		}
	}
	for _, file := range manifest.Queries {
		if !queryFilePattern.MatchString(file) {
			return Refuse(name, RefusalManifestInvalid,
				"query file %q must be a plain file name ending in .sql, in the widget's own directory", file)
		}
	}
	return nil
}

// validateParameters checks every declared key.
func validateParameters(manifest Manifest) *Refusal {
	name := manifest.Type
	seen := map[string]bool{}
	for _, parameter := range manifest.Parameters {
		if !keyPattern.MatchString(parameter.Key) {
			return Refuse(name, RefusalManifestInvalid, "parameter key %q is not snake_case", parameter.Key)
		}
		if seen[parameter.Key] {
			return Refuse(name, RefusalManifestInvalid, "parameter %q is declared twice", parameter.Key)
		}
		seen[parameter.Key] = true
		if parameter.Key == VolumeMeasureKey {
			return Refuse(name, RefusalReservedParameterKey,
				"parameter %q is the core's: a widget declaring `\"volumes\": true` is given it", parameter.Key)
		}
		if manifest.Script == "" && Contains(reservedQueryParameters, parameter.Key) {
			return Refuse(name, RefusalReservedParameterKey,
				"parameter %q is bound by the engine itself (%v); choose another key", parameter.Key,
				reservedQueryParameters)
		}
		if !Contains(ParameterKinds, parameter.Kind) {
			return Refuse(name, RefusalUnknownParameterKind, "parameter %q has kind %q; the kinds are %v",
				parameter.Key, parameter.Kind, ParameterKinds)
		}
		if parameter.Class != ClassData && parameter.Class != ClassRendering {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q has class %q, not %q or %q",
				parameter.Key, parameter.Class, ClassData, ClassRendering)
		}
		kind := ParamKind(parameter.Kind)
		isEnum := kind == KindEnum || kind == KindEnumSet
		if isEnum && len(parameter.Values) == 0 {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q is an %s and lists no values",
				parameter.Key, parameter.Kind)
		}
		if !isEnum && len(parameter.Values) > 0 {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q lists values but is a %s, not an enum",
				parameter.Key, parameter.Kind)
		}
		seenValue := map[string]bool{}
		for _, value := range parameter.Values {
			if seenValue[value] {
				return Refuse(name, RefusalParameterVocabulary, "parameter %q lists the value %q twice", parameter.Key, value)
			}
			seenValue[value] = true
		}
		isReference := kind == KindReference || kind == KindReferenceList
		if isReference {
			if _, known := ReferenceByKind[parameter.Reference]; !known {
				return Refuse(name, RefusalParameterVocabulary,
					"parameter %q accepts reference kind %q, which is not one of the reference kinds", parameter.Key,
					parameter.Reference)
			}
		} else if parameter.Reference != "" {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q names a reference kind but is a %s",
				parameter.Key, parameter.Kind)
		}
		isNumeric := kind == KindInteger || kind == KindNumber
		if !isNumeric && (parameter.Min != nil || parameter.Max != nil) {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q has a min or max but is a %s",
				parameter.Key, parameter.Kind)
		}
		if parameter.Min != nil && parameter.Max != nil && *parameter.Min > *parameter.Max {
			return Refuse(name, RefusalParameterVocabulary, "parameter %q has min above max", parameter.Key)
		}
		if refusal := validateDefault(name, parameter); refusal != nil {
			return refusal
		}
	}
	return nil
}

// validateDefault checks that a default is a value its own parameter would accept.
func validateDefault(widgetType string, parameter ManifestParameter) *Refusal {
	if parameter.Default == nil {
		return nil
	}
	raw, err := json.Marshal(parameter.Default)
	if err != nil {
		return Refuse(widgetType, RefusalBadDefault, "the default of %q cannot be encoded: %v", parameter.Key, err)
	}
	switch ParamKind(parameter.Kind) {
	case KindReference, KindReferenceList:
		return Refuse(widgetType, RefusalBadDefault,
			"the default of reference parameter %q must be null: a reference names a row of one installation",
			parameter.Key)
	case KindSeries:
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) != nil || len(entries) != 0 {
			return Refuse(widgetType, RefusalBadDefault, "the default of series parameter %q must be [] or null",
				parameter.Key)
		}
		return nil
	}
	value, problems, _ := parseValue(parameter.Param(), parameter.Key, raw, -1)
	if value == nil && len(problems) > 0 {
		return Refuse(widgetType, RefusalBadDefault, "the default of %q (%s) is %s for a %s parameter", parameter.Key,
			string(raw), strings.ReplaceAll(problems[0].Reason, "_", " "), parameter.Kind)
	}
	return nil
}

// validateReferences checks the reference kinds against the reference parameters.
func validateReferences(manifest Manifest) *Refusal {
	name := manifest.Type
	declared := map[string]bool{}
	for _, kind := range manifest.References {
		if _, known := ReferenceByKind[kind]; !known {
			return Refuse(name, RefusalReferencesMismatch, "references lists %q, which is not a reference kind", kind)
		}
		declared[kind] = true
	}
	hasSeries := false
	used := map[string]bool{}
	for _, parameter := range manifest.Parameters {
		if parameter.Kind == string(KindSeries) {
			hasSeries = true
		}
		if parameter.Reference != "" {
			used[parameter.Reference] = true
			if !declared[parameter.Reference] {
				return Refuse(name, RefusalReferencesMismatch,
					"parameter %q accepts %q references but the references list does not name that kind",
					parameter.Key, parameter.Reference)
			}
		}
	}
	if !hasSeries {
		var unused []string
		for kind := range declared {
			if !used[kind] {
				unused = append(unused, kind)
			}
		}
		sort.Strings(unused)
		if len(unused) > 0 {
			return Refuse(name, RefusalReferencesMismatch,
				"references lists %v, which no reference parameter of the widget accepts", unused)
		}
	}
	return nil
}
