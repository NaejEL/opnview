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
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/shapes"
)

// Loading the widgets plugins carry (source.go).
//
// A PLUGIN WIDGET IS VALIDATED EXACTLY AS EVERY OTHER WIDGET IS -- the same manifest, the same
// rules, the same refusal codes -- and then served in one of two ways:
//
//   - with a script (manifest `script`): its statement files declare named statements, in the
//     marker format of every *.statements.sql file of the tree, and its script answers the request
//     through its host (internal/widget/script/script.go), running those statements by name;
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

// checkedStatement is what the load-time check found of one text.
type checkedStatement struct {
	domain  bool
	refusal *params.Refusal
}

var pluginStatements = struct {
	sync.Mutex
	// builtin holds the statements of every built-in plugin widget registered, by type.
	builtin map[string]map[string]script.WidgetStatement
	// checked holds what the check found of a built-in's statement texts, which depends on the
	// schema alone, so it is made once per process.
	checked map[string]checkedStatement
}{builtin: map[string]map[string]script.WidgetStatement{}, checked: map[string]checkedStatement{}}

// builtinRegistration serialises the registration of the built-in plugin widgets' statements.
var builtinRegistration sync.Mutex

// pluginSequence numbers the registrations of a third party's statements.
var pluginSequence atomic.Int64

// loadPluginWidget validates one widget a plugin carries and returns its preset.
func loadPluginWidget(ctx context.Context, database *store.Store, source Source, existing map[string]request.Preset) (
	request.Preset, *params.Refusal) {
	name := source.Type
	raw, err := fs.ReadFile(source.Files, manifestFile)
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalManifestUnreadable, "%s cannot be read: %v", manifestFile, err)
	}
	manifest, err := params.DecodeManifest(raw)
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalManifestInvalid, "%s is not a valid manifest: %v", manifestFile, err)
	}
	bindable := source.BindableKinds
	if len(bindable) == 0 {
		bindable = params.CoreBindableKinds()
	}
	if refusal := params.ValidateManifestWith(manifest, bindable); refusal != nil {
		refusal.Type = name
		return request.Preset{}, refusal
	}
	if manifest.Type != name {
		return request.Preset{}, params.Refuse(name, params.RefusalTypeMismatchWithDirectory,
			"the manifest declares type %q in the directory %q: they must be the same", manifest.Type, name)
	}
	if _, taken := existing[manifest.Type]; taken {
		return request.Preset{}, params.Refuse(name, params.RefusalTypeCollision,
			"the type %q is already served; a widget never shadows another widget", manifest.Type)
	}
	origin := request.OriginOperator
	if source.Builtin {
		origin = request.OriginBuiltin
	}
	if manifest.Script == "" {
		return loadPluginDeclarative(ctx, database, source, manifest, origin)
	}

	text, err := fs.ReadFile(source.Files, manifest.Script)
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalScriptMissing, "%s cannot be read: %v", manifest.Script, err)
	}
	program, err := script.CompileWidgetScript(path.Join(source.Location, manifest.Script), string(text))
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalScriptDoesNotCompile, "%v", err)
	}
	statements, refusal := pluginWidgetStatements(ctx, database, source, manifest)
	if refusal != nil {
		return request.Preset{}, refusal
	}
	preset := presetFromManifest(manifest, origin)
	script := &script.ScriptWidget{Plugin: source.Plugin, File: manifest.Script, Program: program, Statements: statements,
		Builtin: source.Builtin, Datasets: script.DatasetsAdmitted(source.Plugin, source.Datasets)}
	preset.Serve = script.Serve
	return preset, nil
}

// loadPluginDeclarative loads a plugin widget with no script: one query, served by the declarative
// engine.
func loadPluginDeclarative(ctx context.Context, database *store.Store, source Source, manifest params.Manifest,
	origin string) (request.Preset, *params.Refusal) {
	name := manifest.Type
	switch len(manifest.Queries) {
	case 0:
		return request.Preset{}, params.Refuse(name, params.RefusalQueryMissing, "a declarative widget declares its query file in `queries`")
	case 1:
	default:
		return request.Preset{}, params.Refuse(name, params.RefusalTooManyQueries,
			"a widget with no script runs exactly one query and this one declares %d", len(manifest.Queries))
	}
	text, err := fs.ReadFile(source.Files, manifest.Queries[0])
	if err != nil {
		code := params.RefusalQueryUnreadable
		if errors.Is(err, fs.ErrNotExist) {
			code = params.RefusalQueryMissing
		}
		return request.Preset{}, params.Refuse(name, code, "%s cannot be read: %v", manifest.Queries[0], err)
	}
	columns, refusal := checkDeclarative(ctx, database, manifest, string(text))
	if refusal != nil {
		return request.Preset{}, refusal
	}
	preset := presetFromManifest(manifest, origin)
	preset.Columns = columns
	preset.Serve = shapes.ServeDeclarative
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
	if sqldb.StatementOwner(statement) == owner {
		return preset, nil
	}
	if err := sqldb.RegisterQuery(owner, statement, string(text)); err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalStatementRegistrationFailed, "%v", err)
	}
	return preset, nil
}

// pluginWidgetStatements reads, checks and registers the statements a script widget's query files
// declare, and returns them by the name the files give them.
func pluginWidgetStatements(ctx context.Context, database *store.Store, source Source, manifest params.Manifest) (
	map[string]script.WidgetStatement, *params.Refusal) {
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
			code := params.RefusalQueryUnreadable
			if errors.Is(err, fs.ErrNotExist) {
				code = params.RefusalQueryMissing
			}
			return nil, params.Refuse(name, code, "%s cannot be read: %v", file, err)
		}
		parsed, err := sqldb.ParseStatements(string(raw))
		if err != nil {
			return nil, params.Refuse(name, params.RefusalQueryMultipleStatements, "%s: %v", file, err)
		}
		if len(parsed) == 0 {
			return nil, params.Refuse(name, params.RefusalQueryMissing, "%s declares no statement: each begins with a line %q",
				file, sqldb.StatementMarker+"widget_<name>")
		}
		for statement, text := range parsed {
			if !params.StatementNamePattern.MatchString(statement) {
				return nil, params.Refuse(name, params.RefusalStatementNameInvalid,
					"%s declares the statement %q; a widget's statement is named widget_<snake_case>", file, statement)
			}
			if _, duplicate := texts[statement]; duplicate {
				return nil, params.Refuse(name, params.RefusalStatementNameInvalid, "the statement %q is declared twice", statement)
			}
			texts[statement] = text
		}
	}
	for _, statement := range request.SortedNames(texts) {
		if mismatch := request.NetflowTwinMismatch(texts, statement); mismatch != "" {
			return nil, params.Refuse(name, params.RefusalNetflowTwinMismatch, "%s", mismatch)
		}
	}
	statements := map[string]script.WidgetStatement{}
	for _, statement := range request.SortedNames(texts) {
		text := texts[statement]
		checked, refusal := checkScriptStatement(ctx, database, name, statement, text, source.Builtin)
		if refusal != nil {
			return nil, refusal
		}
		statements[statement] = script.WidgetStatement{Registered: statement, Template: strings.Contains(text, request.PeriodToken),
			Domain: checked, Parameters: sqldb.QueryParameters(text)}
	}

	owner := "widget:" + name
	if !source.Builtin {
		sequence := pluginSequence.Add(1)
		owner = fmt.Sprintf("plugin:%s/%s#%d", source.Plugin, name, sequence)
		for statement, held := range statements {
			held.Registered = fmt.Sprintf("%s%s%d", statement, pluginStatementSeparator, sequence)
			statements[statement] = held
		}
	}
	for _, statement := range request.SortedNames(texts) {
		if err := sqldb.RegisterQuery(owner, statements[statement].Registered, texts[statement]); err != nil {
			sqldb.UnregisterOwner(owner)
			return nil, params.Refuse(name, params.RefusalStatementRegistrationFailed, "%v", err)
		}
	}
	if source.Builtin {
		pluginStatements.Lock()
		pluginStatements.builtin[name] = statements
		pluginStatements.Unlock()
	}
	return statements, nil
}

// checkScriptStatement checks one statement of a script widget at every period it can run at, and
// returns whether it reads a column carrying a domain name.
func checkScriptStatement(ctx context.Context, database *store.Store, widgetType, statement, text string,
	builtin bool) (bool, *params.Refusal) {
	expansions := []string{text}
	if strings.Contains(text, request.PeriodToken) {
		expansions = expansions[:0]
		for _, period := range vocab.Periods() {
			expansions = append(expansions, strings.ReplaceAll(text, request.PeriodToken, period.Name))
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
	parameters := sqldb.QueryParameters(text)
	if _, err := database.CheckQuery(ctx, text, parameters); err != nil {
		return checkedStatement{refusal: queryRefusal(widgetType, fmt.Sprintf("the statement %s", statement), err)}
	}
	relations, err := database.Reads(ctx, text, parameters)
	if err != nil {
		return checkedStatement{refusal: params.Refuse(widgetType, params.RefusalQueryDoesNotPlan, "the statement %s: %v",
			statement, err)}
	}
	if credential := reads.CredentialTablesRead(relations); len(credential) > 0 {
		return checkedStatement{refusal: params.Refuse(widgetType, params.RefusalQueryReadsCredential,
			"the statement %s reads %v, which hold a credential, a session or a setting row; no widget reads them",
			statement, credential)}
	}
	return checkedStatement{domain: database.CarriesDomain(relations)}
}

// queryRefusal names a refusal of the store's query check with the widget's code.
func queryRefusal(widgetType, what string, err error) *params.Refusal {
	var problem *sqldb.QueryProblem
	if !errors.As(err, &problem) {
		return params.Refuse(widgetType, params.RefusalQueryDoesNotPlan, "%s: %v", what, err)
	}
	code := map[string]string{
		sqldb.QueryMultipleStatements: params.RefusalQueryMultipleStatements,
		sqldb.QueryNotReadOnly:        params.RefusalQueryNotReadOnly,
		sqldb.QueryDoesNotPlan:        params.RefusalQueryDoesNotPlan,
		sqldb.QueryUnboundParameter:   params.RefusalUnknownQueryParameter,
		sqldb.QueryEmpty:              params.RefusalQueryMissing,
	}[problem.Kind]
	return params.Refuse(widgetType, code, "%s: %s", what, problem.Detail)
}

// loadPluginWidgets loads every source. A refused built-in is an error; a refused third party's
// widget is reported with its reason and the service carries on without it.
func loadPluginWidgets(ctx context.Context, database *store.Store, sources []Source, existing map[string]request.Preset) (
	[]request.Preset, []params.Refusal, error) {
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	var (
		loaded   []request.Preset
		refusals []params.Refusal
	)
	taken := make(map[string]request.Preset, len(existing))
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
