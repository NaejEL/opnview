package widget

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/shapes"
)

// Loading the operator directory, and what every widget loader shares.
//
// EVERY LOADER RUNS THE SAME VALIDATION. A widget a plugin carries (pluginwidget.go) and an
// operator widget, a directory in <data-dir>/widgets, each hold manifest.json and its queries.
// What differs is the consequence of a refusal: a broken built-in is a defect of the build, so
// New returns an error naming it (and the test suite fails); a broken operator or third-party
// widget is somebody's file, so it is refused with its reason, logged by the caller, and the
// service starts without it.

// loadTimeout bounds the planning of the queries at load.
const loadTimeout = 30 * time.Second

// manifestFile is the manifest's file name in every widget directory.
const manifestFile = "manifest.json"

// operatorSequence numbers the registrations of operator queries.
var operatorSequence atomic.Int64

// declarativeStatementPrefix prefixes the registered name of a declarative widget's query.
const declarativeStatementPrefix = "widget_x_"

// presetFromManifest builds the preset of a validated manifest.
func presetFromManifest(manifest params.Manifest, origin string) request.Preset {
	parameters := make([]params.Param, 0, len(manifest.Parameters))
	for _, parameter := range manifest.Parameters {
		parameters = append(parameters, parameter.Param())
	}
	if manifest.Volumes {
		// The core's parameter, which the widget does not declare
		// (internal/widget/request/measure.go).
		parameters = append(parameters, request.VolumeMeasureParam())
	}
	return request.Preset{
		Type: manifest.Type, Shape: manifest.Shape, Providers: append([]string{}, manifest.Providers...),
		Primary: manifest.Primary, Windowed: manifest.Windowed,
		WithheldWholeUnderNoDomains: manifest.NoDomains == params.NoDomainsWhole,
		Params:                      parameters, LowerBound: manifest.LowerBound, Measure: manifest.Measure,
		Origin: origin, Manifest: manifest, Volumes: manifest.Volumes,
	}
}

// queryParameters are the named parameters the engine binds for a manifest.
func queryParameters(manifest params.Manifest) []string {
	names := []string{"now"}
	if manifest.Windowed {
		names = append(names, "from", "to")
	}
	for _, parameter := range manifest.Parameters {
		switch params.ParamKind(parameter.Kind) {
		case params.KindSeries, params.KindObject:
			continue
		}
		names = append(names, parameter.Key)
	}
	return names
}

// checkDeclarative validates a declarative widget's query text and returns its columns.
func checkDeclarative(ctx context.Context, database *store.Store, manifest params.Manifest, text string) (
	[]string, *params.Refusal) {
	info, err := database.CheckQuery(ctx, text, queryParameters(manifest))
	if err != nil {
		return nil, queryRefusal(manifest.Type, "the query", err)
	}
	// The data-leak guard every widget query passes (B3 of the backend-completion cycle).
	relations, err := database.Reads(ctx, text, info.Parameters)
	if err != nil {
		return nil, params.Refuse(manifest.Type, params.RefusalQueryDoesNotPlan, "the query: %v", err)
	}
	if credential := reads.CredentialTablesRead(relations); len(credential) > 0 {
		return nil, params.Refuse(manifest.Type, params.RefusalQueryReadsCredential,
			"the query reads %v, which hold a credential, a session or a setting row; no widget reads them", credential)
	}
	for _, required := range shapes.ShapeColumns[manifest.Shape] {
		if !params.Contains(info.Columns, required) {
			return nil, params.Refuse(manifest.Type, params.RefusalMissingColumn,
				"the %s shape needs a column named %q and the query returns %v", manifest.Shape, required, info.Columns)
		}
	}
	return info.Columns, nil
}

// loadOperatorWidgets reads <dir>/<type>/ for every directory. existing holds the types
// already served, which an operator widget may not shadow.
func loadOperatorWidgets(ctx context.Context, database *store.Store, dir string, existing map[string]request.Preset) (
	[]request.Preset, []params.Refusal) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []params.Refusal{*params.Refuse(dir, params.RefusalNotADirectory, "the widgets directory cannot be read: %v", err)}
	}
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	var (
		loaded   []request.Preset
		refusals []params.Refusal
		claimed  = map[string]bool{}
	)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		preset, refusal := loadOperatorWidget(ctx, database, filepath.Join(dir, entry.Name()), entry.Name(), existing, claimed)
		if refusal != nil {
			refusals = append(refusals, *refusal)
			continue
		}
		claimed[preset.Type] = true
		loaded = append(loaded, preset)
	}
	sort.Slice(refusals, func(i, j int) bool { return refusals[i].Type < refusals[j].Type })
	return loaded, refusals
}

// loadOperatorWidget validates one operator directory and registers its query.
func loadOperatorWidget(ctx context.Context, database *store.Store, directory, name string,
	existing map[string]request.Preset, claimed map[string]bool) (request.Preset, *params.Refusal) {
	raw, err := os.ReadFile(filepath.Join(directory, manifestFile))
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalManifestUnreadable, "%s cannot be read: %v", manifestFile, err)
	}
	manifest, err := params.DecodeManifest(raw)
	if err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalManifestInvalid, "%s is not a valid manifest: %v", manifestFile, err)
	}
	if refusal := params.ValidateManifest(manifest); refusal != nil {
		refusal.Type = name
		return request.Preset{}, refusal
	}
	if manifest.Type != name {
		return request.Preset{}, params.Refuse(name, params.RefusalTypeMismatchWithDirectory,
			"the manifest declares type %q in the directory %q: they must be the same", manifest.Type, name)
	}
	if _, taken := existing[manifest.Type]; taken || claimed[manifest.Type] {
		return request.Preset{}, params.Refuse(name, params.RefusalTypeCollision,
			"the type %q is already served; an operator widget never shadows another widget", manifest.Type)
	}
	switch len(manifest.Queries) {
	case 0:
		return request.Preset{}, params.Refuse(name, params.RefusalQueryMissing, "a declarative widget declares its query file in `queries`")
	case 1:
	default:
		return request.Preset{}, params.Refuse(name, params.RefusalTooManyQueries, "a widget runs exactly one query and this one declares %d",
			len(manifest.Queries))
	}
	text, err := os.ReadFile(filepath.Join(directory, manifest.Queries[0]))
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
	preset := presetFromManifest(manifest, request.OriginOperator)
	// The registry is process-wide and a name is registered once, so each load of an operator
	// widget gets a name of its own: two services over two data directories, as the tests build,
	// never meet in it.
	sequence := operatorSequence.Add(1)
	preset.Statement = fmt.Sprintf("%s%s_%d", declarativeStatementPrefix, manifest.Type, sequence)
	preset.Columns = columns
	preset.Serve = shapes.ServeDeclarative
	if err := sqldb.RegisterQuery(fmt.Sprintf("operator:%s#%d", manifest.Type, sequence), preset.Statement, string(text)); err != nil {
		return request.Preset{}, params.Refuse(name, params.RefusalStatementRegistrationFailed, "%v", err)
	}
	return preset, nil
}
