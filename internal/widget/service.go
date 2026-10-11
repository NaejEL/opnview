package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// PathPrefix is the widget API's path: /api/widget/{type} (decision 3).
const PathPrefix = "/api/widget/"

// ParametersQueryKey is the one query parameter carrying the parameters object as JSON
// (decision 2).
const ParametersQueryKey = "parameters"

// Preset is one served widget type: its catalogue identity, what it binds, the keys it
// accepts, and the function that answers it. A preset is declared in a file of its own
// and named by one row of registry.go.
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
	Serve func(r *Request) (data any, hasRows bool, err error)
}

// Service serves the registered presets.
type Service struct {
	store   *store.Store
	now     func() time.Time
	lists   SuffixLists
	presets map[string]Preset
	types   []string
}

// Options are what a service needs beside the store.
type Options struct {
	// Now is the clock; nil takes time.Now.
	Now func() time.Time
	// SuffixLists is the Public Suffix List in use, for the registrable-domain grouping;
	// nil reports the grouping unavailable.
	SuffixLists SuffixLists
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
	service := &Service{store: database, now: now, lists: options.SuffixLists, presets: map[string]Preset{}}
	for _, preset := range registry {
		if _, duplicate := service.presets[preset.Type]; duplicate {
			return nil, fmt.Errorf("widget: the type %q is registered twice", preset.Type)
		}
		service.presets[preset.Type] = preset
		service.types = append(service.types, preset.Type)
	}
	sort.Strings(service.types)
	return service, nil
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
	var malformed errMalformed
	switch {
	case errors.As(err, &malformed):
		WriteJSON(writer, http.StatusBadRequest, Failure{Condition: ConditionMalformedParameters,
			Type: widgetType, Reason: malformed.reason})
	case errors.Is(err, errUnknownType):
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

var errUnknownType = errors.New("widget: unknown type")

// Respond computes one response. It is what ServeType writes, and what the response-time
// measurement of the pre-deployment command times.
func (s *Service) Respond(ctx context.Context, widgetType, rawParameters string) (Response, error) {
	preset, found := s.presets[widgetType]
	if !found {
		return Response{}, errUnknownType
	}
	parsed, err := parseParameters(rawParameters, preset.Params)
	if err != nil {
		return Response{}, err
	}
	request := &Request{
		ctx:            ctx,
		db:             s.store,
		lists:          s.lists,
		now:            s.now().Unix(),
		preset:         preset,
		params:         parsed,
		refs:           map[string][]resolved{},
		entries:        map[int]map[string]resolved{},
		failed:         map[int]bool{},
		withheldSeries: map[int]bool{},
		response: &Response{
			Type: widgetType, Parameters: parsed.effective, UnknownParameters: parsed.unknown,
			InvalidParameters: parsed.invalid, MissingParameters: parsed.missing,
			UnresolvedReferences: []UnresolvedReference{}, Gaps: []Gap{},
			Availability: []ProviderAvailability{}, Freshness: []Freshness{}, Withheld: []string{},
			States: []string{},
		},
	}
	if request.response.InvalidParameters == nil {
		request.response.InvalidParameters = []InvalidValue{}
	}
	if request.response.MissingParameters == nil {
		request.response.MissingParameters = []string{}
	}
	if err := request.prepare(); err != nil {
		return Response{}, err
	}
	response := request.response

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
	case preset.WithheldWholeUnderNoDomains && request.noDomains():
		condition = ConditionWithheldByAggregateMode
		response.Withheld = append(response.Withheld, "data")
	}
	if condition != "" {
		response.Condition = condition
		return *response, nil
	}

	data, hasRows, err := preset.Serve(request)
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
	if request.override != "" {
		response.Condition = request.override
		if request.override == ConditionNoOwnerExists {
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
	ctx      context.Context
	db       *store.Store
	lists    SuffixLists
	now      int64
	preset   Preset
	params   parsedParameters
	response *Response

	// refs are the resolved top-level references, by key; entries the resolved series
	// references, by entry index and key; failed the entries with an unresolved one.
	refs    map[string][]resolved
	entries map[int]map[string]resolved
	failed  map[int]bool
	// withheldSeries are the entries whose unresolved reference is a site reference the
	// aggregate mode withholds, a subset of failed.
	withheldSeries map[int]bool

	// window is the requested window of a windowed preset.
	window Interval
	// retention is the retention_seconds setting; 0 is unlimited.
	retention int64
	mode      string
	// override replaces the computed condition, for a preset with a condition of its own.
	override string

	installation *installation
}

// prepare reads what every response carries: the aggregate mode, the window, the
// references, the availability and the gaps.
func (r *Request) prepare() error {
	mode, err := r.setting(config.KeyAggregateMode, aggregateModeFull)
	if err != nil {
		return err
	}
	r.mode = mode
	r.response.AggregateMode = mode
	retention, err := r.setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
	if err != nil {
		return err
	}
	r.retention, _ = strconv.ParseInt(retention, 10, 64)

	for _, at := range r.params.references {
		value, reason, err := resolveReference(r.ctx, r.db, at, r.noDomains())
		if err != nil {
			return err
		}
		referenceValue, referenceLabel := at.reference.Value, at.reference.Label
		if r.noDomains() && at.reference.Kind == "site" {
			// The value of a site reference is a domain name, and under no_domains no
			// response carries one -- not even the one the request sent -- so it is removed
			// from the echo and from the unresolved entry, and listed as withheld.
			referenceValue, referenceLabel = nil, nil
			if at.echo != nil {
				at.echo["value"] = nil
				delete(at.echo, "label")
			}
			r.withhold(at.path + ".value")
		}
		if reason != "" {
			r.response.UnresolvedReferences = append(r.response.UnresolvedReferences, UnresolvedReference{
				Path: at.path, Kind: at.reference.Kind, By: at.reference.By, Value: referenceValue,
				Label: referenceLabel, Reason: reason,
			})
			if at.series >= 0 {
				r.failed[at.series] = true
				if reason == unresolvedWithheld {
					r.withheldSeries[at.series] = true
				}
			}
			continue
		}
		if at.series >= 0 {
			if r.entries[at.series] == nil {
				r.entries[at.series] = map[string]resolved{}
			}
			r.entries[at.series][at.key] = value
			continue
		}
		r.refs[at.key] = append(r.refs[at.key], value)
	}

	if r.preset.Windowed {
		duration := periodSeconds[r.params.text("period")]
		if duration == 0 {
			duration = periodSeconds["24h"]
		}
		r.window = Interval{From: r.now - duration, To: r.now}
		report := &WindowReport{Requested: r.window, Coverage: coverageNone}
		if r.retention > 0 {
			horizon := r.now - r.retention
			report.FlowHorizonAt = &horizon
		}
		r.response.Window = report
	}
	if err := r.readAvailability(r.preset.Providers); err != nil {
		return err
	}
	if r.preset.Windowed {
		gaps, err := r.readGaps(r.preset.Providers, r.window)
		if err != nil {
			return err
		}
		r.response.Gaps = gaps
		if contains(r.preset.Providers, "dns_lookup") {
			bounded := false
			for _, gap := range gaps {
				if gap.Reason == gapResolverWindowNotHonoured {
					bounded = true
				}
			}
			r.response.ResolverWindowBounded = &bounded
		}
	}
	return nil
}

// periodSeconds is each period's rolling-window duration.
var periodSeconds = map[string]int64{"1h": 3600, "24h": 86400, "7d": 7 * 86400, "30d": 30 * 86400}

// The two values of the aggregate_mode setting (docs/data-model.md, `setting`).
const (
	aggregateModeFull      = "full"
	aggregateModeNoDomains = "no_domains"
)

// gapResolverWindowNotHonoured is the collection-gap reason the resolver's ring buffer
// records (docs/data-model.md, collection_gap).
const gapResolverWindowNotHonoured = "resolver_window_not_honoured"

// setting reads one setting row, or the fallback when it is absent.
func (r *Request) setting(key, fallback string) (string, error) {
	var value string
	found, err := r.db.WidgetRow(r.ctx, "widget_setting", map[string]any{"key": key}, &value)
	if err != nil || !found {
		return fallback, err
	}
	return value, nil
}

// noDomains reports whether the aggregate mode withholds domain names.
func (r *Request) noDomains() bool { return r.mode == aggregateModeNoDomains }

// withhold records a withheld field or level.
func (r *Request) withhold(path string) {
	if !contains(r.response.Withheld, path) {
		r.response.Withheld = append(r.response.Withheld, path)
	}
}

// state records a named state.
func (r *Request) state(code string) {
	if !contains(r.response.States, code) {
		r.response.States = append(r.response.States, code)
	}
}

// ids returns the resolved ids of a reference-list key, or nil when the list is empty,
// which means "no restriction".
func (r *Request) ids(key string) []int64 {
	var result []int64
	for _, value := range r.refs[key] {
		result = append(result, value.id)
	}
	return result
}

// idSet is ids as a set, nil for no restriction.
func (r *Request) idSet(key string) map[int64]bool {
	values := r.ids(key)
	if values == nil {
		return nil
	}
	set := map[int64]bool{}
	for _, value := range values {
		set[value] = true
	}
	return set
}

// jsonIDs renders an id list as the JSON text a statement's set parameter takes, or nil
// for no restriction.
func jsonIDs(values []int64) any {
	if values == nil {
		return nil
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// jsonTexts renders a string list the same way.
func jsonTexts(values []string) any {
	if values == nil {
		return nil
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// windowParameters are the bounds every windowed statement takes.
func (r *Request) windowParameters() map[string]any {
	return map[string]any{"from": r.window.From, "to": r.window.To}
}

// cover records the covered part of the window.
func (r *Request) cover(covered *Interval) {
	report := r.response.Window
	if report == nil {
		return
	}
	switch {
	case covered == nil || covered.To <= covered.From:
		report.Covered = nil
		report.Coverage = coverageNone
	case covered.From <= report.Requested.From && covered.To >= report.Requested.To:
		report.Covered = covered
		report.Coverage = coverageFull
	default:
		report.Covered = covered
		report.Coverage = coveragePartial
	}
}

// The coverage values.
const (
	coverageFull    = "covered"
	coveragePartial = "partially_covered"
	coverageNone    = "not_covered"
)

// coverFrom records coverage from the earliest record a source holds: from that instant
// (or the window's start) to now, and nothing when the source holds no record at all.
func (r *Request) coverFrom(earliest int64, has bool) {
	if !has {
		r.cover(nil)
		return
	}
	covered := Interval{From: max(r.window.From, earliest), To: min(r.window.To, r.now)}
	r.cover(&covered)
}

// earliest reads one of the widget_earliest_* statements.
func (r *Request) earliest(name string) (int64, bool, error) {
	var value store.NullInt64
	found, err := r.db.WidgetRow(r.ctx, name, nil, &value)
	if err != nil || !found || !value.Valid {
		return 0, false, err
	}
	return value.Int64, true, nil
}

// coverFlows records the coverage of a reading of the flows alone. Before the earliest
// flow kept -- purged by the retention, or before opnview started -- the flows hold no
// answer; a figure the aggregates do not carry is then reported by name.
func (r *Request) coverFlows(beyondHorizonState string) error {
	earliest, has, err := r.earliest("widget_earliest_flow")
	if err != nil {
		return err
	}
	r.coverFrom(earliest, has)
	return r.coverFlowsState(beyondHorizonState)
}

// coverFlowsState records a named state when part of the window lies before the furthest
// horizon the retention purge has applied: the flows of that part are gone, so a figure
// read from the flows alone has no answer there, while the aggregates still carry theirs.
func (r *Request) coverFlowsState(beyondHorizonState string) error {
	if beyondHorizonState == "" {
		return nil
	}
	var purged int64
	has, err := r.db.WidgetRow(r.ctx, "widget_purged_before", nil, &purged)
	if err != nil {
		return err
	}
	if has && purged > r.window.From {
		r.state(beyondHorizonState)
	}
	return nil
}

// condition computes the widget's condition from the primary provider kind and whether
// the data holds a row.
func (r *Request) condition(hasRows bool) string {
	if r.preset.Primary == "" {
		if hasRows {
			return ConditionReachable
		}
		return ConditionReachableNoRows
	}
	condition := kindCondition(r.response.Availability, r.preset.Primary)
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

// kindCondition is the condition of a kind: its active providers' best condition, or,
// when none is active, the best of its registered providers'.
func kindCondition(availability []ProviderAvailability, kind string) string {
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
	providers, err := readProviders(r.ctx, r.db)
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if contains(kinds, provider.Kind) {
			r.response.Availability = append(r.response.Availability, provider)
		}
	}
	return nil
}

// readProviders reads every registered provider with its availability and condition.
func readProviders(ctx context.Context, database *store.Store) ([]ProviderAvailability, error) {
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
		provider.Outcome = nullableText(outcome)
		provider.State = nullableText(state)
		provider.Probe = nullableText(probe)
		provider.Detail = nullableText(detail)
		provider.CheckedAt = nullableNumber(checkedAt)
		provider.Condition = availabilityCondition(provider.State, provider.Probe, provider.Outcome)
		result = append(result, provider)
		return nil
	})
	return result, err
}

// probeNotYetRun is the probe value the schema seeds an availability row with
// (internal/store/schema.sql, the source_availability seed).
const probeNotYetRun = "not_yet_probed"

// availabilityCondition maps an availability row to the catalogue's conditions.
func availabilityCondition(state, probe, outcome *string) string {
	switch {
	case state == nil:
		return ConditionStateUnknown
	case *state == string(store.StateUnavailable) && ((probe != nil && *probe == probeNotYetRun) || (outcome != nil && *outcome == probeNotYetRun)):
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

// readGaps reads the collection gaps of the given kinds overlapping a window.
func (r *Request) readGaps(kinds []string, window Interval) ([]Gap, error) {
	gaps := []Gap{}
	err := r.db.WidgetRows(r.ctx, "widget_collection_gaps", store.Period{}, map[string]any{
		"kinds": jsonTexts(kinds), "from": window.From, "to": window.To,
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
	notRunning, err := r.db.ReadNotRunning(r.ctx, window.From, window.To)
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

// hourFreshness records the freshness of the 1 h slots of the whole hours of [from, to),
// which a rolling-window read assembled.
func (r *Request) hourFreshness(from, to int64) error {
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
		if _, err := r.db.WidgetRow(r.ctx, "widget_hour_slots_computed", parameters, &slots, &oldest,
			&newest); err != nil {
			return err
		}
		entry.SlotsRead = slots
		entry.LastComputedAt = nullableNumber(newest)
		if _, err := r.db.WidgetRow(r.ctx, "widget_stale_hours", parameters, &entry.StaleSlots); err != nil {
			return err
		}
		entry.Stale = entry.StaleSlots > 0
	}
	r.response.Freshness = append(r.response.Freshness, entry)
	return nil
}

// nullableText turns a nullable string into a pointer.
func nullableText(value store.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

// nullableNumber turns a nullable integer into a pointer.
func nullableNumber(value store.NullInt64) *int64 {
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
