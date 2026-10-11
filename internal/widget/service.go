package widget

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
)

// PathPrefix is the widget API's path: /api/widget/{type} (decision 3).
const PathPrefix = "/api/widget/"

// ParametersQueryKey is the one query parameter carrying the parameters object as JSON
// (decision 2).
const ParametersQueryKey = "parameters"

// ServeFunc answers one request of a widget, returning the data and whether the data holds any row
// (the difference between reachable and reachable_no_rows, and between a degraded widget that still
// has history to show and one that has nothing). A script widget's is its script (script.go); a
// declarative widget's is the shape engine (serveDeclarative).
type ServeFunc = func(r *Request) (data any, hasRows bool, err error)

// Preset is one served widget type: its catalogue identity, what it binds, the keys it
// accepts, and the function that answers it. A preset is built from a widget directory's
// manifest by a loader (pluginwidget.go, loader.go); there is no list of them.
type Preset struct {
	// Type is the catalogue's `snake_case` identifier, which is also the last element of its path.
	Type string
	// Shape is the catalogue shape that draws it.
	Shape string
	// Providers are the provider kinds its Binding names. Each response carries the
	// availability of every registered provider of each.
	Providers []string
	// Primary is the provider kind whose condition is the widget's condition; empty for
	// a preset that reads only opnview's own records, whose condition is reachable or
	// reachable_no_rows.
	Primary string
	// Windowed says the preset takes a `period` and answers over a rolling window.
	Windowed bool
	// WithheldWholeUnderNoDomains says the aggregate mode no_domains withholds the whole
	// preset (decision 6).
	WithheldWholeUnderNoDomains bool
	// Params are the keys it accepts, with their defaults and classification.
	Params []Param
	// Serve answers a request. It returns the data and whether the data holds any row:
	// the difference between reachable and reachable_no_rows, and between a degraded
	// widget that still has history to show and one that has nothing.
	Serve ServeFunc
	// LowerBound and Measure come from the manifest: whether the figures of a declarative
	// widget are lower bounds, and what they measure.
	LowerBound bool
	Measure    string
	// Origin is OriginBuiltin or OriginOperator.
	Origin string
	// Statement is the registered read statement of a declarative preset, and Columns the
	// result columns it was checked to return.
	Statement string
	Columns   []string
	// Manifest is the manifest the preset was built from.
	Manifest Manifest
	// Volumes says the preset serves Volume or byte Count figures, answered under the measure
	// volume_measure chooses (measure.go).
	Volumes bool
}

// The origins of a preset.
const (
	// OriginBuiltin is a widget shipped in the binary.
	OriginBuiltin = "builtin"
	// OriginOperator is a widget read from the data directory at start.
	OriginOperator = "operator"
)

// Service serves the registered presets.
type Service struct {
	store    *store.Store
	Now      func() time.Time
	lists    SuffixLists
	presets  map[string]Preset
	types    []string
	refusals []Refusal
	operator []string
	// datasets is the data catalogue: the core's datasets and the loaded plugins' (datasets.go).
	datasets *dataset.Catalogue
	// liveInterest is the live-interest lease a request for a live figure renews (live.go).
	liveInterest *live.Interest
}

// Options are what a service needs beside the store.
type Options struct {
	// Now is the clock; nil takes time.Now.
	Now func() time.Time
	// SuffixLists is the Public Suffix List in use, for the registrable-domain grouping;
	// nil reports the grouping unavailable.
	SuffixLists SuffixLists
	// OperatorDir is the directory operator widgets are read from, <data-dir>/widgets; empty
	// loads none. A missing directory is not an error. Every widget the loader refuses is
	// reported by Service.Refusals and never stops the service.
	OperatorDir string
	// Datasets is the data catalogue the service answers from; nil is the core's datasets alone.
	Datasets *dataset.Catalogue
	// Plugins are the widgets the loaded plugins carry (source.go): the built-ins' and the data
	// directory's. Nil takes the built-in plugins' alone, as the plugin loader registered them. A
	// refused built-in is an error; a refused third party's widget is reported by Refusals.
	Plugins []Source
	// LiveInterest is the live-interest lease an answer carrying a live figure renews; nil
	// renews none (live.go).
	LiveInterest *live.Interest
}

// New returns the service over a store.
func New(database *store.Store, options Options) (*Service, error) {
	if database == nil {
		return nil, errors.New("widget: a store is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	service := &Service{store: database, Now: now, lists: options.SuffixLists, presets: map[string]Preset{},
		liveInterest: options.LiveInterest}
	service.datasets = options.Datasets
	if service.datasets == nil {
		catalogue, err := dataset.NewCatalogue(nil)
		if err != nil {
			return nil, err
		}
		service.datasets = catalogue
	}
	if err := hostAreaFailures(); err != nil {
		return nil, err
	}
	sources := options.Plugins
	if sources == nil {
		sources = defaultSources()
	}
	fromPlugins, refused, err := loadPluginWidgets(context.Background(), database, sources, service.presets)
	if err != nil {
		return nil, err
	}
	service.refusals = append(service.refusals, refused...)
	for _, preset := range fromPlugins {
		service.presets[preset.Type] = preset
		if preset.Origin == OriginOperator {
			service.operator = append(service.operator, preset.Type)
		}
	}
	if options.OperatorDir != "" {
		loaded, refusals := loadOperatorWidgets(context.Background(), database, options.OperatorDir, service.presets)
		service.refusals = append(service.refusals, refusals...)
		for _, preset := range loaded {
			service.presets[preset.Type] = preset
			service.operator = append(service.operator, preset.Type)
		}
	}
	sort.Strings(service.operator)
	sort.Slice(service.refusals, func(i, j int) bool { return service.refusals[i].Type < service.refusals[j].Type })
	for widgetType := range service.presets {
		service.types = append(service.types, widgetType)
	}
	sort.Strings(service.types)
	return service, nil
}

// Refusals returns the operator widgets the loader refused, each with its named reason. The
// service is running without them.
func (s *Service) Refusals() []Refusal {
	return append([]Refusal(nil), s.refusals...)
}

// OperatorTypes returns the operator widgets that loaded, sorted.
func (s *Service) OperatorTypes() []string {
	return append([]string(nil), s.operator...)
}

// Types returns every served type, sorted. The HTTP routes are built from it.
func (s *Service) Types() []string {
	return append([]string(nil), s.types...)
}

// Preset returns a served type's declaration.
func (s *Service) Preset(widgetType string) (Preset, bool) {
	preset, found := s.presets[widgetType]
	return preset, found
}

// Failure is the JSON a request is refused with: a condition code and, where one
// applies, the reason.
type Failure struct {
	Condition string `json:"condition"`
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ServeType answers one request for a served type: 200 with the envelope, or 400 with a
// JSON condition when the parameters cannot be parsed at all.
func (s *Service) ServeType(writer http.ResponseWriter, request *http.Request, widgetType string) {
	response, err := s.Respond(request.Context(), widgetType, request.URL.Query().Get(ParametersQueryKey))
	var (
		malformed ErrMalformed
		failed    *ScriptError
	)
	switch {
	case errors.As(err, &failed):
		// A widget script that threw, ran past its time bound or answered something that is not
		// an answer: its own closed condition, and the detail in the log, never in the answer.
		if request.Context().Err() != nil {
			return
		}
		scriptLogger(failed.Plugin, failed.Error())
		WriteJSON(writer, http.StatusInternalServerError, Failure{Condition: ConditionScriptFailed, Type: widgetType})
	case errors.As(err, &malformed):
		WriteJSON(writer, http.StatusBadRequest, Failure{Condition: ConditionMalformedParameters,
			Type: widgetType, Reason: malformed.reason})
	case errors.Is(err, ErrUnknownType):
		s.ServeUnknown(writer, widgetType)
	case err != nil:
		// The error is the store's: a cancelled request or a failed read. Its text names
		// statements and is not shown; the condition says what happened.
		if request.Context().Err() != nil {
			return
		}
		WriteJSON(writer, http.StatusInternalServerError, Failure{Condition: "read_failed", Type: widgetType})
	default:
		WriteJSON(writer, http.StatusOK, response)
	}
}

// ServeUnknown answers a type with no endpoint: 404, JSON, unknown_widget_type (A16).
func (s *Service) ServeUnknown(writer http.ResponseWriter, widgetType string) {
	WriteJSON(writer, http.StatusNotFound, Failure{Condition: ConditionUnknownWidgetType, Type: widgetType})
}

// WriteJSON writes a JSON body with its status. It sets no CORS header, deliberately: the
// API answers its own origin only (A13), and is not cached, because every answer is about
// a window ending now.
func WriteJSON(writer http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		encoded = []byte(`{"condition":"encoding_failed"}`)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
}

var ErrUnknownType = errors.New("widget: unknown type")

// Respond computes one response. It is what ServeType writes, and what the response-time
// measurement of the pre-deployment command times.
func (s *Service) Respond(ctx context.Context, widgetType, rawParameters string) (Response, error) {
	preset, found := s.presets[widgetType]
	if !found {
		return Response{}, ErrUnknownType
	}
	parsed, err := ParseParametersWith(rawParameters, preset.Params, s.datasets)
	if err != nil {
		return Response{}, err
	}
	request := &Request{
		Ctx:            ctx,
		Db:             s.store,
		Lists:          s.lists,
		Now:            s.Now().Unix(),
		Preset:         preset,
		Params:         parsed,
		Refs:           map[string][]Resolved{},
		Entries:        map[int]map[string]Resolved{},
		Failed:         map[int]bool{},
		WithheldSeries: map[int]bool{},
		datasets:       s.datasets,
		liveInterest:   s.liveInterest,
		Response: &Response{
			Type: widgetType, Parameters: parsed.Effective, UnknownParameters: parsed.Unknown,
			InvalidParameters: parsed.invalid, MissingParameters: parsed.missing,
			UnresolvedReferences: []UnresolvedReference{}, Gaps: []Gap{},
			Availability: []ProviderAvailability{}, Freshness: []Freshness{}, Withheld: []string{},
			States: []string{},
		},
	}
	if request.Response.InvalidParameters == nil {
		request.Response.InvalidParameters = []InvalidValue{}
	}
	if request.Response.MissingParameters == nil {
		request.Response.MissingParameters = []string{}
	}
	if err := request.prepare(); err != nil {
		return Response{}, err
	}
	response := request.Response

	topUnresolved, topWithheld := false, false
	for _, unresolvedReference := range response.UnresolvedReferences {
		if isSeriesPath(unresolvedReference.Path) {
			continue
		}
		if unresolvedReference.Reason == unresolvedWithheld {
			topWithheld = true
		} else {
			topUnresolved = true
		}
	}
	condition := ""
	switch {
	case topUnresolved:
		condition = ConditionUnresolvedReference
	case topWithheld:
		condition = ConditionWithheldByAggregateMode
	case len(response.MissingParameters) > 0:
		condition = ConditionMissingRequiredParameter
	case preset.WithheldWholeUnderNoDomains && request.NoDomains():
		condition = ConditionWithheldByAggregateMode
		response.Withheld = append(response.Withheld, "data")
	}
	if condition != "" {
		response.Condition = condition
		return *response, nil
	}

	serve := preset.Serve
	if preset.Volumes {
		serve = func(r *Request) (any, bool, error) { return r.serveMeasured(preset.Serve) }
	}
	data, hasRows, err := serve(request)
	if errors.Is(err, errWithheldUnderNoDomains) {
		response.Condition = ConditionWithheldByAggregateMode
		response.Data = nil
		response.Withheld = append(response.Withheld, "data")
		sort.Strings(response.States)
		sort.Strings(response.Withheld)
		return *response, nil
	}
	if err != nil {
		return Response{}, err
	}
	response.Condition = request.condition(hasRows)
	if response.Condition != ConditionReachable && response.Condition != ConditionReachableNoRows &&
		!hasRows {
		// A degraded source with nothing stored: no data at all, never a zero.
		response.Data = nil
	} else {
		response.Data = data
	}
	if request.Override != "" {
		response.Condition = request.Override
		if request.Override == ConditionNoOwnerExists {
			response.Data = nil
		}
	}
	sort.Strings(response.States)
	sort.Strings(response.Withheld)
	return *response, nil
}

// isSeriesPath reports whether a reference path is inside a series entry, whose failure
// concerns its own series only.
func isSeriesPath(path string) bool {
	return len(path) > len("series[") && path[:len("series[")] == "series["
}

// Request is one request being answered.
type Request struct {
	Ctx      context.Context
	Db       *store.Store
	Lists    SuffixLists
	Now      int64
	Preset   Preset
	Params   parsedParameters
	Response *Response

	// refs are the resolved top-level references, by key; entries the resolved series
	// references, by entry index and key; failed the entries with an unresolved one.
	Refs    map[string][]Resolved
	Entries map[int]map[string]Resolved
	Failed  map[int]bool
	// withheldSeries are the entries whose unresolved reference is a site reference the
	// aggregate mode withholds, a subset of failed.
	WithheldSeries map[int]bool

	// window is the requested window of a windowed preset.
	Window Interval
	// retention is the retention_seconds setting; 0 is unlimited.
	retention int64
	mode      string
	// override replaces the computed condition, for a preset with a condition of its own.
	Override string

	// datasets is the data catalogue, which a custom_chart series may read (datasetseries.go).
	datasets *dataset.Catalogue
	// liveInterest is the lease a live figure renews (live.go).
	liveInterest *live.Interest

	installation *installation

	// measure is the measure the request is being answered under, and what the run built (measure.go).
	measure measureRun
	// netflowState is whether NetFlow can answer the window, once read (measure.go).
	netflowState *string
}

// prepare reads what every response carries: the aggregate mode, the window, the
// references, the availability and the gaps.
func (r *Request) prepare() error {
	mode, err := r.setting(config.KeyAggregateMode, aggregateModeFull)
	if err != nil {
		return err
	}
	r.mode = mode
	r.Response.AggregateMode = mode
	retention, err := r.setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
	if err != nil {
		return err
	}
	r.retention, _ = strconv.ParseInt(retention, 10, 64)

	for _, at := range r.Params.references {
		value, reason, err := resolveReference(r.Ctx, r.Db, at, r.NoDomains())
		if err != nil {
			return err
		}
		referenceValue, referenceLabel := at.reference.Value, at.reference.Label
		if r.NoDomains() && at.reference.Kind == "site" {
			// The value of a site reference is a domain name, and under no_domains no
			// response carries one -- not even the one the request sent -- so it is removed
			// from the echo and from the unresolved entry, and listed as withheld.
			referenceValue, referenceLabel = nil, nil
			if at.echo != nil {
				at.echo["value"] = nil
				delete(at.echo, "label")
			}
			r.Withhold(at.path + ".value")
		}
		if reason != "" {
			r.Response.UnresolvedReferences = append(r.Response.UnresolvedReferences, UnresolvedReference{
				Path: at.path, Kind: at.reference.Kind, By: at.reference.By, Value: referenceValue,
				Label: referenceLabel, Reason: reason,
			})
			if at.series >= 0 {
				r.Failed[at.series] = true
				if reason == unresolvedWithheld {
					r.WithheldSeries[at.series] = true
				}
			}
			continue
		}
		if at.series >= 0 {
			if r.Entries[at.series] == nil {
				r.Entries[at.series] = map[string]Resolved{}
			}
			r.Entries[at.series][at.key] = value
			continue
		}
		r.Refs[at.key] = append(r.Refs[at.key], value)
	}

	if r.Preset.Windowed {
		duration := periodSeconds[r.Params.Text("period")]
		if duration == 0 {
			duration = periodSeconds["24h"]
		}
		r.Window = Interval{From: r.Now - duration, To: r.Now}
		report := &WindowReport{Requested: r.Window, Coverage: CoverageNone}
		if r.retention > 0 {
			horizon := r.Now - r.retention
			report.FlowHorizonAt = &horizon
		}
		r.Response.Window = report
	}
	if err := r.readAvailability(r.Preset.Providers); err != nil {
		return err
	}
	if r.Preset.Windowed {
		gaps, err := r.ReadGaps(r.Preset.Providers, r.Window)
		if err != nil {
			return err
		}
		r.Response.Gaps = gaps
		if Contains(r.Preset.Providers, "dns_lookup") {
			bounded := false
			for _, gap := range gaps {
				if gap.Reason == gapResolverWindowNotHonoured {
					bounded = true
				}
			}
			r.Response.ResolverWindowBounded = &bounded
		}
	}
	return nil
}

// periodSeconds is each period's rolling-window duration.
var periodSeconds = map[string]int64{"1h": 3600, "24h": 86400, "7d": 7 * 86400, "30d": 30 * 86400}

// The two values of the aggregate_mode setting (docs/data-model.md, `setting`).
const (
	aggregateModeFull      = "full"
	AggregateModeNoDomains = "no_domains"
)

// gapResolverWindowNotHonoured is the collection-gap reason the resolver's ring buffer
// records (docs/data-model.md, collection_gap).
const gapResolverWindowNotHonoured = "resolver_window_not_honoured"

// setting reads one setting row, or the fallback when it is absent.
func (r *Request) setting(key, fallback string) (string, error) {
	var value string
	found, err := r.Db.WidgetRow(r.Ctx, "widget_setting", map[string]any{"key": key}, &value)
	if err != nil || !found {
		return fallback, err
	}
	return value, nil
}

// NoDomains reports whether the aggregate mode withholds domain names.
func (r *Request) NoDomains() bool { return r.mode == AggregateModeNoDomains }

// Withhold records a withheld field or level.
func (r *Request) Withhold(path string) {
	if !Contains(r.Response.Withheld, path) {
		r.Response.Withheld = append(r.Response.Withheld, path)
	}
}

// State records a named state.
func (r *Request) State(code string) {
	if !Contains(r.Response.States, code) {
		r.Response.States = append(r.Response.States, code)
	}
}

// Ids returns the resolved ids of a reference-list key, or nil when the list is empty,
// which means "no restriction".
func (r *Request) Ids(key string) []int64 {
	var result []int64
	for _, value := range r.Refs[key] {
		result = append(result, value.Id)
	}
	return result
}

// IdSet is ids as a set, nil for no restriction.
func (r *Request) IdSet(key string) map[int64]bool {
	values := r.Ids(key)
	if values == nil {
		return nil
	}
	set := map[int64]bool{}
	for _, value := range values {
		set[value] = true
	}
	return set
}

// JsonIDs renders an id list as the JSON text a statement's set parameter takes, or nil
// for no restriction.
func JsonIDs(values []int64) any {
	if values == nil {
		return nil
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// JsonTexts renders a string list the same way.
func JsonTexts(values []string) any {
	if values == nil {
		return nil
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// WindowParameters are the bounds every windowed statement takes.
func (r *Request) WindowParameters() map[string]any {
	return map[string]any{"from": r.Window.From, "to": r.Window.To}
}

// Cover records the covered part of the window.
func (r *Request) Cover(covered *Interval) {
	report := r.Response.Window
	if report == nil {
		return
	}
	switch {
	case covered == nil || covered.To <= covered.From:
		report.Covered = nil
		report.Coverage = CoverageNone
	case covered.From <= report.Requested.From && covered.To >= report.Requested.To:
		report.Covered = covered
		report.Coverage = CoverageFull
	default:
		report.Covered = covered
		report.Coverage = CoveragePartial
	}
}

// The coverage values.
const (
	CoverageFull    = "covered"
	CoveragePartial = "partially_covered"
	CoverageNone    = "not_covered"
)

// CoverFrom records coverage from the earliest record a source holds: from that instant
// (or the window's start) to now, and nothing when the source holds no record at all.
func (r *Request) CoverFrom(earliest int64, has bool) {
	if !has {
		r.Cover(nil)
		return
	}
	covered := Interval{From: max(r.Window.From, earliest), To: min(r.Window.To, r.Now)}
	r.Cover(&covered)
}

// Earliest reads one of the widget_earliest_* statements.
func (r *Request) Earliest(name string) (int64, bool, error) {
	var value store.NullInt64
	found, err := r.Db.WidgetRow(r.Ctx, name, nil, &value)
	if err != nil || !found || !value.Valid {
		return 0, false, err
	}
	return value.Int64, true, nil
}

// CoverFlows records the coverage of a reading of the flows alone. Before the earliest
// flow kept -- purged by the retention, or before opnview started -- the flows hold no
// answer; a figure the aggregates do not carry is then reported by name.
func (r *Request) CoverFlows(beyondHorizonState string) error {
	earliest, has, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return err
	}
	r.CoverFrom(earliest, has)
	return r.CoverFlowsState(beyondHorizonState)
}

// CoverFlowsState records a named state when part of the window lies before the furthest
// horizon the retention purge has applied: the flows of that part are gone, so a figure
// read from the flows alone has no answer there, while the aggregates still carry theirs.
func (r *Request) CoverFlowsState(beyondHorizonState string) error {
	if beyondHorizonState == "" {
		return nil
	}
	var purged int64
	has, err := r.Db.WidgetRow(r.Ctx, "widget_purged_before", nil, &purged)
	if err != nil {
		return err
	}
	if has && purged > r.Window.From {
		r.State(beyondHorizonState)
	}
	return nil
}

// condition computes the widget's condition from the primary provider kind and whether
// the data holds a row.
func (r *Request) condition(hasRows bool) string {
	if r.Preset.Primary == "" {
		if hasRows {
			return ConditionReachable
		}
		return ConditionReachableNoRows
	}
	condition := KindCondition(r.Response.Availability, r.Preset.Primary)
	if condition == ConditionReachable && !hasRows {
		return ConditionReachableNoRows
	}
	return condition
}

// conditionRank orders the conditions from the most to the least healthy.
var conditionRank = map[string]int{
	ConditionReachable: 0, ConditionPresentButDisabled: 1, ConditionUnavailable: 2,
	ConditionNotYetProbed: 3, ConditionStateUnknown: 4,
}

// KindCondition is the condition of a kind: its active providers' best condition, or,
// when none is active, the best of its registered providers'.
func KindCondition(availability []ProviderAvailability, kind string) string {
	best := ""
	consider := func(activeOnly bool) {
		for _, provider := range availability {
			if provider.Kind != kind || (activeOnly && !provider.Active) {
				continue
			}
			if best == "" || conditionRank[provider.Condition] < conditionRank[best] {
				best = provider.Condition
			}
		}
	}
	consider(true)
	if best == "" {
		consider(false)
	}
	if best == "" {
		return ConditionStateUnknown
	}
	return best
}

// readAvailability reads every registered provider of the given kinds.
func (r *Request) readAvailability(kinds []string) error {
	providers, err := ReadProviders(r.Ctx, r.Db)
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if Contains(kinds, provider.Kind) {
			r.Response.Availability = append(r.Response.Availability, provider)
		}
	}
	return nil
}

// ReadProviders reads every registered provider with its availability and condition.
func ReadProviders(ctx context.Context, database *store.Store) ([]ProviderAvailability, error) {
	var result []ProviderAvailability
	err := database.WidgetRows(ctx, "widget_availability", store.Period{}, nil, func(row store.RowScanner) error {
		var (
			id                            int64
			provider                      ProviderAvailability
			active                        int64
			state, probe, detail, outcome store.NullString
			checkedAt                     store.NullInt64
		)
		if err := row.Scan(&id, &provider.Kind, &provider.ProviderKey, &provider.DisplayName, &active,
			&state, &probe, &detail, &checkedAt, &outcome); err != nil {
			return err
		}
		provider.Active = active == 1
		provider.Outcome = NullableText(outcome)
		provider.State = NullableText(state)
		provider.Probe = NullableText(probe)
		provider.Detail = NullableText(detail)
		provider.CheckedAt = NullableNumber(checkedAt)
		provider.Condition = availabilityCondition(provider.State, provider.Probe, provider.Outcome)
		result = append(result, provider)
		return nil
	})
	return result, err
}

// ProbeNotYetRun is the probe value the schema seeds an availability row with
// (internal/store/schema.sql, the source_availability seed).
const ProbeNotYetRun = "not_yet_probed"

// availabilityCondition maps an availability row to the catalogue's conditions.
func availabilityCondition(state, probe, outcome *string) string {
	switch {
	case state == nil:
		return ConditionStateUnknown
	case *state == string(store.StateUnavailable) && ((probe != nil && *probe == ProbeNotYetRun) || (outcome != nil && *outcome == ProbeNotYetRun)):
		return ConditionNotYetProbed
	case *state == string(store.StateUnavailable):
		return ConditionUnavailable
	case *state == string(store.StatePresentButDisabled):
		return ConditionPresentButDisabled
	case *state == string(store.StateReachable):
		return ConditionReachable
	}
	return ConditionStateUnknown
}

// ReadGaps reads the collection gaps of the given kinds overlapping a window.
func (r *Request) ReadGaps(kinds []string, window Interval) ([]Gap, error) {
	gaps := []Gap{}
	err := r.Db.WidgetRows(r.Ctx, "widget_collection_gaps", store.Period{}, map[string]any{
		"kinds": JsonTexts(kinds), "from": window.From, "to": window.To,
	}, func(row store.RowScanner) error {
		var gap Gap
		if err := row.Scan(&gap.ProviderKind, &gap.ProviderKey, &gap.From, &gap.To, &gap.Reason); err != nil {
			return err
		}
		gaps = append(gaps, gap)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// While opnview was not running no source was read at all: each such interval is a
	// gap of every provider the reading binds (Group D).
	notRunning, err := r.Db.ReadNotRunning(r.Ctx, window.From, window.To)
	if err != nil {
		return nil, err
	}
	for _, interval := range notRunning {
		gaps = append(gaps, Gap{ProviderKind: gapKindOpnview, From: interval.From, To: interval.To,
			Reason: gapOpnviewNotRunning})
	}
	return gaps, nil
}

// The gap opnview records for its own absence: no provider, and its own reason.
const (
	gapKindOpnview       = "opnview"
	gapOpnviewNotRunning = "opnview_not_running"
)

// HourFreshness records the freshness of the 1 h slots of the whole hours of [from, to),
// which a rolling-window read assembled.
func (r *Request) HourFreshness(from, to int64) error {
	firstHour := store.PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = store.PeriodHour.SlotEnd(firstHour)
	}
	lastHour := store.PeriodHour.SlotStart(to)
	entry := Freshness{Period: store.PeriodHour.Name}
	if firstHour < lastHour {
		parameters := map[string]any{"from": firstHour, "to": lastHour}
		var (
			slots          int64
			oldest, newest store.NullInt64
		)
		if _, err := r.Db.WidgetRow(r.Ctx, "widget_hour_slots_computed", parameters, &slots, &oldest,
			&newest); err != nil {
			return err
		}
		entry.SlotsRead = slots
		entry.LastComputedAt = NullableNumber(newest)
		if _, err := r.Db.WidgetRow(r.Ctx, "widget_stale_hours", parameters, &entry.StaleSlots); err != nil {
			return err
		}
		entry.Stale = entry.StaleSlots > 0
	}
	r.Response.Freshness = append(r.Response.Freshness, entry)
	return nil
}

// NullableText turns a nullable string into a pointer.
func NullableText(value store.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

// NullableNumber turns a nullable integer into a pointer.
func NullableNumber(value store.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

// nullableFloat turns a nullable real into a pointer.
func nullableFloat(value store.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	number := value.Float64
	return &number
}
