package widget

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NaejEL/opnview/internal/store"
)

// Loading the widgets plugins carry (source.go).
//
// A PLUGIN WIDGET IS VALIDATED EXACTLY AS EVERY OTHER WIDGET IS -- the same manifest, the same
// rules, the same refusal codes -- and then served in one of two ways:
//
//   - with a script (manifest `script`): its statement files declare named statements, in the
//     marker format of every *.statements.sql file of the tree, and its script answers the request
//     through its host (script.go), running those statements by name;
//   - without one: a single query file, served by the declarative engine as an operator widget is.
//
// EVERY STATEMENT IS CHECKED AT LOAD, before anything is served: one read-only statement that
// PostgreSQL plans, at each period for a period template, that reads no table holding a credential,
// a session or a setting row (RefusalQueryReadsCredential, the HTTP API's data-leak guard). Whether
// it reads a column carrying a domain name is recorded, and its host reports it.
//
// A BUILT-IN PLUGIN'S STATEMENTS KEEP THEIR NAMES. They are registered once per process under the
// owner widget:<type>, so sql/schema-checks.sh plans the same texts and the statement checks of the suite see them. A third
// party's are registered under names of their own each time a service loads them, as an operator
// query is, so two services over two data directories never meet in the registry.

// pluginStatementSeparator joins a third party's statement name to its registration's sequence.
const pluginStatementSeparator = "__p"

// widgetStatement is one statement a plugin widget declares.
type widgetStatement struct {
	// registered is the name it is registered under.
	registered string
	// template says it is a period template (@period@), run for the period a script names.
	template bool
	// domain says it reads a column carrying a domain name.
	domain bool
	// parameters are the names it binds, without the colon.
	parameters []string
}

// checkedStatement is what the load-time check found of one text.
type checkedStatement struct {
	domain  bool
	refusal *Refusal
}

var pluginStatements = struct {
	sync.Mutex
	// builtin holds the statements of every built-in plugin widget registered, by type.
	builtin map[string]map[string]widgetStatement
	// checked holds what the check found of a built-in's statement texts, which depends on the
	// schema alone, so it is made once per process.
	checked map[string]checkedStatement
}{builtin: map[string]map[string]widgetStatement{}, checked: map[string]checkedStatement{}}

// builtinRegistration serialises the registration of the built-in plugin widgets' statements.
var builtinRegistration sync.Mutex

// pluginSequence numbers the registrations of a third party's statements.
var pluginSequence atomic.Int64

// loadPluginWidget validates one widget a plugin carries and returns its preset.
func loadPluginWidget(ctx context.Context, database *store.Store, source Source, existing map[string]Preset) (
	Preset, *Refusal) {
	name := source.Type
	raw, err := fs.ReadFile(source.Files, manifestFile)
	if err != nil {
		return Preset{}, refuse(name, RefusalManifestUnreadable, "%s cannot be read: %v", manifestFile, err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil {
		return Preset{}, refuse(name, RefusalManifestInvalid, "%s is not a valid manifest: %v", manifestFile, err)
	}
	if refusal := ValidateManifest(manifest); refusal != nil {
		refusal.Type = name
		return Preset{}, refusal
	}
	if manifest.Type != name {
		return Preset{}, refuse(name, RefusalTypeMismatchWithDirectory,
			"the manifest declares type %q in the directory %q: they must be the same", manifest.Type, name)
	}
	if _, taken := existing[manifest.Type]; taken {
		return Preset{}, refuse(name, RefusalTypeCollision,
			"the type %q is already served; a widget never shadows another widget", manifest.Type)
	}
	origin := OriginOperator
	if source.Builtin {
		origin = OriginBuiltin
	}
	if manifest.Script == "" {
		return loadPluginDeclarative(ctx, database, source, manifest, origin)
	}

	text, err := fs.ReadFile(source.Files, manifest.Script)
	if err != nil {
		return Preset{}, refuse(name, RefusalScriptMissing, "%s cannot be read: %v", manifest.Script, err)
	}
	program, err := compileWidgetScript(path.Join(source.Location, manifest.Script), string(text))
	if err != nil {
		return Preset{}, refuse(name, RefusalScriptDoesNotCompile, "%v", err)
	}
	statements, refusal := pluginWidgetStatements(ctx, database, source, manifest)
	if refusal != nil {
		return Preset{}, refusal
	}
	preset := presetFromManifest(manifest, origin)
	script := &scriptWidget{plugin: source.Plugin, file: manifest.Script, program: program, statements: statements,
		builtin: source.Builtin}
	preset.Serve = script.serve
	return preset, nil
}

// loadPluginDeclarative loads a plugin widget with no script: one query, served by the declarative
// engine.
func loadPluginDeclarative(ctx context.Context, database *store.Store, source Source, manifest Manifest,
	origin string) (Preset, *Refusal) {
	name := manifest.Type
	switch len(manifest.Queries) {
	case 0:
		return Preset{}, refuse(name, RefusalQueryMissing, "a declarative widget declares its query file in `queries`")
	case 1:
	default:
		return Preset{}, refuse(name, RefusalTooManyQueries,
			"a widget with no script runs exactly one query and this one declares %d", len(manifest.Queries))
	}
	text, err := fs.ReadFile(source.Files, manifest.Queries[0])
	if err != nil {
		code := RefusalQueryUnreadable
		if errors.Is(err, fs.ErrNotExist) {
			code = RefusalQueryMissing
		}
		return Preset{}, refuse(name, code, "%s cannot be read: %v", manifest.Queries[0], err)
	}
	columns, refusal := checkDeclarative(ctx, database, manifest, string(text))
	if refusal != nil {
		return Preset{}, refusal
	}
	preset := presetFromManifest(manifest, origin)
	preset.Columns = columns
	preset.Serve = serveDeclarative
	statement := declarativeStatementPrefix + manifest.Type
	owner := "widget:" + manifest.Type
	if !source.Builtin {
		sequence := pluginSequence.Add(1)
		statement = fmt.Sprintf("%s%s%d", statement, pluginStatementSeparator, sequence)
		owner = fmt.Sprintf("plugin:%s/%s#%d", source.Plugin, manifest.Type, sequence)
	}
	preset.Statement = statement
	if source.Builtin {
		builtinRegistration.Lock()
		defer builtinRegistration.Unlock()
	}
	if store.StatementOwner(statement) == owner {
		return preset, nil
	}
	if err := store.RegisterQuery(owner, statement, string(text)); err != nil {
		return Preset{}, refuse(name, RefusalStatementRegistrationFailed, "%v", err)
	}
	return preset, nil
}

// pluginWidgetStatements reads, checks and registers the statements a script widget's query files
// declare, and returns them by the name the files give them.
func pluginWidgetStatements(ctx context.Context, database *store.Store, source Source, manifest Manifest) (
	map[string]widgetStatement, *Refusal) {
	name := manifest.Type
	if source.Builtin {
		// Services are built concurrently by the tests: the first registers, the others wait for it
		// and take what it registered.
		builtinRegistration.Lock()
		defer builtinRegistration.Unlock()
		pluginStatements.Lock()
		held, registered := pluginStatements.builtin[name]
		pluginStatements.Unlock()
		if registered {
			return held, nil
		}
	}
	texts := map[string]string{}
	for _, file := range manifest.Queries {
		raw, err := fs.ReadFile(source.Files, file)
		if err != nil {
			code := RefusalQueryUnreadable
			if errors.Is(err, fs.ErrNotExist) {
				code = RefusalQueryMissing
			}
			return nil, refuse(name, code, "%s cannot be read: %v", file, err)
		}
		parsed, err := store.ParseStatements(string(raw))
		if err != nil {
			return nil, refuse(name, RefusalQueryMultipleStatements, "%s: %v", file, err)
		}
		if len(parsed) == 0 {
			return nil, refuse(name, RefusalQueryMissing, "%s declares no statement: each begins with a line %q",
				file, store.StatementMarker+"widget_<name>")
		}
		for statement, text := range parsed {
			if !statementNamePattern.MatchString(statement) {
				return nil, refuse(name, RefusalStatementNameInvalid,
					"%s declares the statement %q; a widget's statement is named widget_<snake_case>", file, statement)
			}
			if _, duplicate := texts[statement]; duplicate {
				return nil, refuse(name, RefusalStatementNameInvalid, "the statement %q is declared twice", statement)
			}
			texts[statement] = text
		}
	}
	statements := map[string]widgetStatement{}
	for _, statement := range sortedNames(texts) {
		text := texts[statement]
		checked, refusal := checkScriptStatement(ctx, database, name, statement, text, source.Builtin)
		if refusal != nil {
			return nil, refusal
		}
		statements[statement] = widgetStatement{registered: statement, template: strings.Contains(text, periodToken),
			domain: checked, parameters: store.QueryParameters(text)}
	}

	owner := "widget:" + name
	if !source.Builtin {
		sequence := pluginSequence.Add(1)
		owner = fmt.Sprintf("plugin:%s/%s#%d", source.Plugin, name, sequence)
		for statement, held := range statements {
			held.registered = fmt.Sprintf("%s%s%d", statement, pluginStatementSeparator, sequence)
			statements[statement] = held
		}
	}
	for _, statement := range sortedNames(texts) {
		if err := store.RegisterQuery(owner, statements[statement].registered, texts[statement]); err != nil {
			store.UnregisterOwner(owner)
			return nil, refuse(name, RefusalStatementRegistrationFailed, "%v", err)
		}
	}
	if source.Builtin {
		pluginStatements.Lock()
		pluginStatements.builtin[name] = statements
		pluginStatements.Unlock()
	}
	return statements, nil
}

// periodToken is the store's period template token (store.PeriodStatement).
const periodToken = "@period@"

// checkScriptStatement checks one statement of a script widget at every period it can run at, and
// returns whether it reads a column carrying a domain name.
func checkScriptStatement(ctx context.Context, database *store.Store, widgetType, statement, text string,
	builtin bool) (bool, *Refusal) {
	expansions := []string{text}
	if strings.Contains(text, periodToken) {
		expansions = expansions[:0]
		for _, period := range store.Periods() {
			expansions = append(expansions, strings.ReplaceAll(text, periodToken, period.Name))
		}
	}
	domain := false
	for _, expanded := range expansions {
		var found checkedStatement
		cached := false
		if builtin {
			pluginStatements.Lock()
			found, cached = pluginStatements.checked[expanded]
			pluginStatements.Unlock()
		}
		if !cached {
			found = checkReadStatement(ctx, database, widgetType, statement, expanded)
			if builtin && found.refusal == nil {
				pluginStatements.Lock()
				pluginStatements.checked[expanded] = found
				pluginStatements.Unlock()
			}
		}
		if found.refusal != nil {
			return false, found.refusal
		}
		domain = domain || found.domain
	}
	return domain, nil
}

// checkReadStatement is the load-time check of one statement text: one read that plans, reading no
// credential table.
func checkReadStatement(ctx context.Context, database *store.Store, widgetType, statement, text string) checkedStatement {
	parameters := store.QueryParameters(text)
	if _, err := database.CheckQuery(ctx, text, parameters); err != nil {
		return checkedStatement{refusal: queryRefusal(widgetType, fmt.Sprintf("the statement %s", statement), err)}
	}
	reads, err := database.Reads(ctx, text, parameters)
	if err != nil {
		return checkedStatement{refusal: refuse(widgetType, RefusalQueryDoesNotPlan, "the statement %s: %v",
			statement, err)}
	}
	if credential := store.CredentialTablesRead(reads); len(credential) > 0 {
		return checkedStatement{refusal: refuse(widgetType, RefusalQueryReadsCredential,
			"the statement %s reads %v, which hold a credential, a session or a setting row; no widget reads them",
			statement, credential)}
	}
	return checkedStatement{domain: database.CarriesDomain(reads)}
}

// queryRefusal names a refusal of the store's query check with the widget's code.
func queryRefusal(widgetType, what string, err error) *Refusal {
	var problem *store.QueryProblem
	if !errors.As(err, &problem) {
		return refuse(widgetType, RefusalQueryDoesNotPlan, "%s: %v", what, err)
	}
	code := map[string]string{
		store.QueryMultipleStatements: RefusalQueryMultipleStatements,
		store.QueryNotReadOnly:        RefusalQueryNotReadOnly,
		store.QueryDoesNotPlan:        RefusalQueryDoesNotPlan,
		store.QueryUnboundParameter:   RefusalUnknownQueryParameter,
		store.QueryEmpty:              RefusalQueryMissing,
	}[problem.Kind]
	return refuse(widgetType, code, "%s: %s", what, problem.Detail)
}

// sortedNames returns a map's keys, sorted.
func sortedNames[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loadPluginWidgets loads every source. A refused built-in is an error; a refused third party's
// widget is reported with its reason and the service carries on without it.
func loadPluginWidgets(ctx context.Context, database *store.Store, sources []Source, existing map[string]Preset) (
	[]Preset, []Refusal, error) {
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	var (
		loaded   []Preset
		refusals []Refusal
	)
	taken := make(map[string]Preset, len(existing))
	for widgetType, preset := range existing {
		taken[widgetType] = preset
	}
	ordered := append([]Source(nil), sources...)
	// The built-ins first, so a third party's widget never takes a type a built-in declares.
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Builtin && !ordered[j].Builtin })
	for _, source := range ordered {
		preset, refusal := loadPluginWidget(ctx, database, source, taken)
		if refusal != nil {
			if source.Builtin {
				return nil, nil, fmt.Errorf("widget: the built-in widget %q of the plugin %s is broken: %w",
					source.Type, source.Plugin, refusal)
			}
			refusals = append(refusals, *refusal)
			continue
		}
		taken[preset.Type] = preset
		loaded = append(loaded, preset)
	}
	sort.Slice(refusals, func(i, j int) bool { return refusals[i].Type < refusals[j].Type })
	return loaded, refusals, nil
}
