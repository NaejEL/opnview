package request

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// ServeFunc answers one request of a widget, returning the data and whether the data holds any row
// (the difference between reachable and reachable_no_rows, and between a degraded widget that still
// has history to show and one that has nothing). A script widget's is its script
// (internal/widget/script/script.go); a declarative widget's is the shape engine
// (serveDeclarative).
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
	Params []params.Param
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
	Manifest params.Manifest
	// Volumes says the preset serves Volume or byte Count figures, answered under the measure
	// volume_measure chooses (measure.go).
	Volumes bool
	// Plugin is the plugin that carries the widget, empty for an operator widget of the data
	// directory: a plugin widget's strings are its plugin's (plugin.<plugin>.widget.<type>.*).
	Plugin string
	// Location is where the widget's directory was read from.
	Location string
	// RendererEntry and RendererSource are the widget's own renderer, a classic script of its
	// directory checked at load (docs/front-plugins.md); both empty when its shape's renderer
	// draws it.
	RendererEntry  string
	RendererSource []byte
	// FrontOnly says the widget has a renderer and neither a query nor a script: nothing on the
	// server answers it, and it is not served (Service.FrontOnlyTypes).
	FrontOnly bool
}

// The origins of a preset.
const (
	// OriginBuiltin is a widget shipped in the binary.
	OriginBuiltin = "builtin"
	// OriginOperator is a widget read from the data directory at start.
	OriginOperator = "operator"
)

// IsSeriesPath reports whether a reference path is inside a series entry, whose failure
// concerns its own series only.
func IsSeriesPath(path string) bool {
	return len(path) > len("series[") && path[:len("series[")] == "series["
}

// Request is one request being answered.
type Request struct {
	Ctx      context.Context
	Db       *store.Store
	Lists    SuffixLists
	Now      int64
	Preset   Preset
	Params   params.ParsedParameters
	Response *envelope.Response

	// Refs are the resolved top-level references, by key; entries the resolved series
	// references, by entry index and key; failed the entries with an unresolved one.
	Refs    map[string][]params.Resolved
	Entries map[int]map[string]params.Resolved
	Failed  map[int]bool
	// WithheldSeries are the entries whose unresolved reference is a site reference the
	// aggregate mode withholds, a subset of failed.
	WithheldSeries map[int]bool

	// Window is the requested window of a windowed preset.
	Window envelope.Interval
	// Retention is the retention_seconds setting; 0 is unlimited.
	Retention int64
	Mode      string
	// Override replaces the computed condition, for a preset with a condition of its own.
	Override string

	// Datasets is the data catalogue, which a custom_chart series may read
	// (internal/widget/datasets/datasetseries.go).
	Datasets *dataset.Catalogue
	// LiveInterest is the lease a live figure renews (live.go).
	LiveInterest *live.Interest

	installation *installation
	// placed are the outside peers placed and whose geolocation is not read yet (installation.go,
	// Place).
	placed map[string]bool

	// Measure is the measure the request is being answered under, and what the run built (measure.go).
	Measure measureRun
	// netflowWindow is what the window says of NetFlow, once read (measure.go).
	netflowWindow *netflowWindow
}

// Prepare reads what every response carries: the aggregate mode, the window, the
// references, the availability and the gaps.
func (r *Request) Prepare() error {
	mode, err := r.Setting(config.KeyAggregateMode, AggregateModeFull)
	if err != nil {
		return err
	}
	r.Mode = mode
	r.Response.AggregateMode = mode
	retention, err := r.Setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
	if err != nil {
		return err
	}
	r.Retention, _ = strconv.ParseInt(retention, 10, 64)

	for _, at := range r.Params.References {
		value, reason, err := params.ResolveReference(r.Ctx, r.Db, at, r.NoDomains())
		if err != nil {
			return err
		}
		referenceValue, referenceLabel := at.Reference.Value, at.Reference.Label
		if r.NoDomains() && at.Reference.Kind == "site" {
			// The value of a site reference is a domain name, and under no_domains no
			// response carries one -- not even the one the request sent -- so it is removed
			// from the echo and from the unresolved entry, and listed as withheld.
			referenceValue, referenceLabel = nil, nil
			if at.Echo != nil {
				at.Echo["value"] = nil
				delete(at.Echo, "label")
			}
			r.Withhold(at.Path + ".value")
		}
		if reason != "" {
			r.Response.UnresolvedReferences = append(r.Response.UnresolvedReferences, params.UnresolvedReference{
				Path: at.Path, Kind: at.Reference.Kind, By: at.Reference.By, Value: referenceValue,
				Label: referenceLabel, Reason: reason,
			})
			if at.Series >= 0 {
				r.Failed[at.Series] = true
				if reason == params.UnresolvedWithheld {
					r.WithheldSeries[at.Series] = true
				}
			}
			continue
		}
		if at.Series >= 0 {
			if r.Entries[at.Series] == nil {
				r.Entries[at.Series] = map[string]params.Resolved{}
			}
			r.Entries[at.Series][at.Key] = value
			continue
		}
		r.Refs[at.Key] = append(r.Refs[at.Key], value)
	}

	if r.Preset.Windowed {
		duration := periodSeconds[r.Params.Text("period")]
		if duration == 0 {
			duration = periodSeconds["24h"]
		}
		r.Window = envelope.Interval{From: r.Now - duration, To: r.Now}
		report := &envelope.WindowReport{Requested: r.Window, Coverage: CoverageNone}
		if r.Retention > 0 {
			horizon := r.Now - r.Retention
			report.FlowHorizonAt = &horizon
		}
		r.Response.Window = report
	}
	if err := r.ReadAvailability(r.Preset.Providers); err != nil {
		return err
	}
	if r.Preset.Windowed {
		gaps, err := r.ReadGaps(r.Preset.Providers, r.Window)
		if err != nil {
			return err
		}
		r.Response.Gaps = gaps
		if params.Contains(r.Preset.Providers, "dns_lookup") {
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
	AggregateModeFull      = "full"
	AggregateModeNoDomains = "no_domains"
)

// gapResolverWindowNotHonoured is the collection-gap reason the resolver's ring buffer
// records (docs/data-model.md, collection_gap).
const gapResolverWindowNotHonoured = "resolver_window_not_honoured"

// Setting reads one setting row, or the fallback when it is absent.
func (r *Request) Setting(key, fallback string) (string, error) {
	var value string
	found, err := r.Db.WidgetRow(r.Ctx, "widget_setting", map[string]any{"key": key}, &value)
	if err != nil || !found {
		return fallback, err
	}
	return value, nil
}

// NoDomains reports whether the aggregate mode withholds domain names.
func (r *Request) NoDomains() bool { return r.Mode == AggregateModeNoDomains }

// Withhold records a withheld field or level.
func (r *Request) Withhold(path string) {
	if !params.Contains(r.Response.Withheld, path) {
		r.Response.Withheld = append(r.Response.Withheld, path)
	}
}

// State records a named state.
func (r *Request) State(code string) {
	if !params.Contains(r.Response.States, code) {
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
func (r *Request) Cover(covered *envelope.Interval) {
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
	covered := envelope.Interval{From: max(r.Window.From, earliest), To: min(r.Window.To, r.Now)}
	r.Cover(&covered)
}

// Earliest reads one of the widget_earliest_* statements.
func (r *Request) Earliest(name string) (int64, bool, error) {
	var value reads.NullInt64
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

// Condition computes the widget's condition from the primary provider kind and whether
// the data holds a row.
func (r *Request) Condition(hasRows bool) string {
	if r.Preset.Primary == "" {
		if hasRows {
			return envelope.ConditionReachable
		}
		return envelope.ConditionReachableNoRows
	}
	condition := KindCondition(r.Response.Availability, r.Preset.Primary)
	if condition == envelope.ConditionReachable && !hasRows {
		return envelope.ConditionReachableNoRows
	}
	return condition
}

// conditionRank orders the conditions from the most to the least healthy.
var conditionRank = map[string]int{
	envelope.ConditionReachable: 0, envelope.ConditionPresentButDisabled: 1, envelope.ConditionUnavailable: 2,
	envelope.ConditionNotYetProbed: 3, envelope.ConditionStateUnknown: 4,
}

// KindCondition is the condition of a kind: its active providers' best condition, or,
// when none is active, the best of its registered providers'.
func KindCondition(availability []envelope.ProviderAvailability, kind string) string {
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
		return envelope.ConditionStateUnknown
	}
	return best
}

// ReadAvailability reads every registered provider of the given kinds.
func (r *Request) ReadAvailability(kinds []string) error {
	providers, err := ReadProviders(r.Ctx, r.Db)
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if params.Contains(kinds, provider.Kind) {
			r.Response.Availability = append(r.Response.Availability, provider)
		}
	}
	return nil
}

// ReadProviders reads every registered provider with its availability and condition.
func ReadProviders(ctx context.Context, database *store.Store) ([]envelope.ProviderAvailability, error) {
	var result []envelope.ProviderAvailability
	err := database.WidgetRows(ctx, "widget_availability", vocab.Period{}, nil, func(row reads.RowScanner) error {
		var (
			id                            int64
			provider                      envelope.ProviderAvailability
			active                        int64
			state, probe, detail, outcome reads.NullString
			checkedAt                     reads.NullInt64
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
		return envelope.ConditionStateUnknown
	case *state == string(vocab.StateUnavailable) && ((probe != nil && *probe == ProbeNotYetRun) || (outcome != nil && *outcome == ProbeNotYetRun)):
		return envelope.ConditionNotYetProbed
	case *state == string(vocab.StateUnavailable):
		return envelope.ConditionUnavailable
	case *state == string(vocab.StatePresentButDisabled):
		return envelope.ConditionPresentButDisabled
	case *state == string(vocab.StateReachable):
		return envelope.ConditionReachable
	}
	return envelope.ConditionStateUnknown
}

// ReadGaps reads the collection gaps of the given kinds overlapping a window.
func (r *Request) ReadGaps(kinds []string, window envelope.Interval) ([]envelope.Gap, error) {
	gaps := []envelope.Gap{}
	err := r.Db.WidgetRows(r.Ctx, "widget_collection_gaps", vocab.Period{}, map[string]any{
		"kinds": JsonTexts(kinds), "from": window.From, "to": window.To,
	}, func(row reads.RowScanner) error {
		var gap envelope.Gap
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
		gaps = append(gaps, envelope.Gap{ProviderKind: gapKindOpnview, From: interval.From, To: interval.To,
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
	firstHour := vocab.PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = vocab.PeriodHour.SlotEnd(firstHour)
	}
	lastHour := vocab.PeriodHour.SlotStart(to)
	entry := envelope.Freshness{Period: vocab.PeriodHour.Name}
	if firstHour < lastHour {
		parameters := map[string]any{"from": firstHour, "to": lastHour}
		var (
			slots          int64
			oldest, newest reads.NullInt64
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
func NullableText(value reads.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

// NullableNumber turns a nullable integer into a pointer.
func NullableNumber(value reads.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

// NullableFloat turns a nullable real into a pointer.
func NullableFloat(value reads.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	number := value.Float64
	return &number
}

// SuffixLists is what the API needs of the registrable_domain kind: each name's registrable
// domain, from the provider in use, and false when none is held. It never fetches: the active
// provider answers from the copy its own refresh downloaded.
type SuffixLists interface {
	RegistrableDomains(ctx context.Context, names []string) (map[string]string, bool)
}

// ConditionScriptFailed is a request whose widget script failed.
const ConditionScriptFailed = "script_failed"

// ScriptError is a widget script that did not answer usably.
type ScriptError struct {
	Plugin string
	Type   string
	Detail string
}

// Error implements error.
func (e *ScriptError) Error() string {
	return fmt.Sprintf("the widget %s failed: %s", e.Type, e.Detail)
}
