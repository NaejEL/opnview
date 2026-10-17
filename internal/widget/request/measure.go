package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// The measure a volume answer is given in (PA5 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md, 17 October 2026; PS22 of its NetFlow join amendment;
// docs/widget-api.md, "Volumes and the observation-point limit").
//
// THE CHOICE IS THE CORE'S, AND IS MADE ONCE, HERE. A widget whose manifest declares `"volumes": true`
// is given the parameter volume_measure -- logged, best_available, or the name of a measure a plugin
// declares (vocab.DeclaredMeasure), best_available by default -- without declaring it, and is answered
// under the measure that parameter chooses: the core decides whether a measure can answer the window,
// runs the widget's script under it, and judges the figures it built. A widget only reads its figures
// through the core (host.volume, host.clients, host.peers, host.sites, host.flows, host.series,
// host.attribution) or through statements that declare their measured twin (host.query), and builds
// them through host.figures; none of it names a measure. A third party's widget gets the parameter
// from its manifest alone, and a third party's measure reaches every such widget from its own manifest.
//
// ONE MEASURE PER ANSWER, NEVER A LOGGED FIGURE LABELLED WITH ANOTHER MEASURE. A run under a declared
// measure reads every figure in it, and every Volume and Count it builds says so. A run that builds a
// figure the core did not read in the measure -- a non-zero logged figure, a logged count -- or that
// fails is not served in it: the answer falls back to logged with the measure's named state, or, for
// an explicit measure, carries no figure. Two measures are never summed.
//
// best_available answers a declared measure only under that measure's strict rule: its window states
// recorded as covered throughout the window, and every connection the answer counts covered -- known at
// its start, and no direction in a state the measure declares not covered (reads.MeasureCoverage.Covered).
// Among several measures that best_available may choose, the first in name order that answers is
// answered (decided question 4 of the amendment); when none does, it answers logged, byte for byte what
// volume_measure = logged answers, with the first candidate's named state that says why and, for a state
// that carries one, its detail in state_details.

// VolumeMeasures are volume_measure's values: logged, every declared measure in name order, and
// best_available, its default.
func VolumeMeasures() []string {
	values := []string{envelope.MeasureLogged}
	for _, measure := range vocab.Measures() {
		values = append(values, measure.Name)
	}
	return append(values, params.VolumeMeasureBestAvailable)
}

// VolumeMeasureParam is the parameter the core gives every type whose manifest declares volumes.
func VolumeMeasureParam() params.Param {
	return params.Param{Key: params.VolumeMeasureKey, Kind: params.KindEnum, Values: VolumeMeasures(),
		Default: params.VolumeMeasureBestAvailable, Data: true}
}

// measureRun is the measure one run of a widget answers under, and what the run built.
type measureRun struct {
	// Name is the declared measure every figure of the run is read in; empty for a logged run.
	Name string
	// LoggedFigure says the run built a figure the core did not read in the measure.
	LoggedFigure bool
	// uncovered says a figure of the run counts a connection with a direction not covered.
	uncovered bool
}

// MeasureRead is the declared measure every core read of a volume figure reads in, empty when the
// request is answered in the logged measure.
func (r *Request) MeasureRead() string { return r.Measure.Name }

// MeasureInForce is the measure the request is being answered under: logged, or a declared measure.
func (r *Request) MeasureInForce() string {
	if r.Measure.Name != "" {
		return r.Measure.Name
	}
	return envelope.MeasureLogged
}

// VolumeOf is the Volume of some figures, in their own measure. Under a measured run it records a
// figure that is not of the measure -- a logged figure holding anything -- and one counting a connection
// with a direction not covered.
func (r *Request) VolumeOf(figures reads.VolumeFigures) envelope.Volume {
	if figures.Measured() || (r.Measure.Name != "" && figures == (reads.VolumeFigures{})) {
		if !figures.Measured() {
			figures.Measure = r.Measure.Name
		}
		r.judge(figures)
		return measuredVolume(figures)
	}
	if r.Measure.Name != "" {
		r.Measure.LoggedFigure = true
	}
	return envelope.Logged(figures)
}

// ByteCountOf is the Count of some figures' bytes, in their own measure, judged as VolumeOf judges.
// single says the figures are one connection's: a measured figure of one connection complete in every
// direction is exact, and is the only figure whose lower_bound is false.
func (r *Request) ByteCountOf(figures reads.VolumeFigures, single bool) envelope.Count {
	if figures.Measured() || (r.Measure.Name != "" && figures == (reads.VolumeFigures{})) {
		if !figures.Measured() {
			figures.Measure = r.Measure.Name
		}
		r.judge(figures)
		exact := single && figures.Connections() == 1 && figures.Coverage().Complete == 1
		return envelope.Count{Measure: figures.Measure, LowerBound: !exact, Value: figures.Bytes}
	}
	if r.Measure.Name != "" {
		r.Measure.LoggedFigure = true
	}
	return envelope.LoggedCount(figures.Bytes)
}

// CountOf is a count of the connections or records an answer's figures count, in the measure the
// request is answered under: the logged connections, whose measured figures a measured answer gives.
func (r *Request) CountOf(value int64) envelope.Count {
	return envelope.Count{Measure: r.MeasureInForce(), LowerBound: true, Value: value}
}

// LoggedCountOf is a count of logged records. Under a measured run one holding anything is a logged
// figure, which the run cannot serve in its measure.
func (r *Request) LoggedCountOf(value int64) envelope.Count {
	if r.Measure.Name != "" {
		if value != 0 {
			r.Measure.LoggedFigure = true
		}
		return r.CountOf(value)
	}
	return envelope.LoggedCount(value)
}

// CoverFigures records figures an answer counts whose numbers a widget uses directly rather than through a
// Volume or a Count: under a measured run, a logged figure or a connection with a direction not covered
// is recorded as VolumeOf records it.
func (r *Request) CoverFigures(figures reads.VolumeFigures) {
	if r.Measure.Name == "" {
		return
	}
	if !figures.Measured() && figures != (reads.VolumeFigures{}) {
		r.Measure.LoggedFigure = true
		return
	}
	if !figures.Measured() {
		figures.Measure = r.Measure.Name
	}
	r.judge(figures)
}

// judge records measured figures counting a connection with a direction not covered (PS22).
func (r *Request) judge(figures reads.VolumeFigures) {
	if r.Measure.Name != "" && !figures.Covered() {
		r.Measure.uncovered = true
	}
}

// measuredVolume is the Volume of a declared measure's figures, with its packets and coverage, whole and
// per part.
func measuredVolume(figures reads.VolumeFigures) envelope.Volume {
	volume := envelope.Logged(figures)
	volume.Measure = figures.Measure
	whole := figures.Parts.Whole()
	packets := func(value int64) *int64 { return &value }
	volume.Packets, volume.Coverage = packets(whole.Packets), envelope.CoverageOf(figures.Measure, whole.Coverage)
	volume.AllowedPackets = packets(figures.Parts.Allowed.Packets)
	volume.AllowedCoverage = envelope.CoverageOf(figures.Measure, figures.Parts.Allowed.Coverage)
	volume.BlockedPackets = packets(figures.Parts.Blocked.Packets)
	volume.BlockedCoverage = envelope.CoverageOf(figures.Measure, figures.Parts.Blocked.Coverage)
	volume.UnknownPackets = packets(figures.Parts.Unknown.Packets)
	volume.UnknownCoverage = envelope.CoverageOf(figures.Measure, figures.Parts.Unknown.Coverage)
	return volume
}

// ServeMeasured answers a type that declares volumes under the measure volume_measure chooses.
func (r *Request) ServeMeasured(serve ServeFunc) (any, bool, error) {
	requested := r.Params.Text(params.VolumeMeasureKey)
	if requested == envelope.MeasureLogged {
		return serve(r)
	}
	var candidates []vocab.DeclaredMeasure
	if requested == params.VolumeMeasureBestAvailable {
		for _, measure := range vocab.Measures() {
			if measure.BestAvailable {
				candidates = append(candidates, measure)
			}
		}
	} else if measure, declared := vocab.MeasureNamed(requested); declared {
		candidates = append(candidates, measure)
	}
	if len(candidates) == 0 {
		// No measure to try: what best_available answers with no declared measure is the logged one.
		return serve(r)
	}
	// The state answered when no candidate answers is the first candidate's.
	var fallback struct {
		measure  vocab.DeclaredMeasure
		state    string
		coverage bool
		set      bool
	}
	for _, measure := range candidates {
		state, err := r.MeasureWindowState(measure)
		if err != nil {
			return nil, false, err
		}
		// coverage says the state is the run's own judgment, not the window's: the window is covered
		// throughout, and a connection the answer counts is not.
		coverage := false
		if state == "" {
			before := r.snapshot()
			r.Measure = measureRun{Name: measure.Name}
			data, hasRows, err := serve(r)
			run := r.Measure
			var failed *ScriptError
			switch {
			case errors.As(err, &failed):
				// A script that cannot run in the measure is not answered in it.
				run.LoggedFigure = true
			case err != nil:
				r.Measure = measureRun{}
				return data, hasRows, err
			case holdsMeasure(data, envelope.MeasureLogged):
				// A figure the script wrote itself as logged: never served in an answer in another measure.
				run.LoggedFigure = true
			}
			switch {
			case !run.LoggedFigure && !run.uncovered:
				return data, hasRows, nil
			case !run.LoggedFigure && requested == measure.Name:
				// Asked for the measure, and it covered only some of the answer: its figures, each with
				// its coverage, and the state that says not every connection was covered.
				r.MeasureState(measure, measure.WindowStates.NotCovering, true)
				return data, hasRows, nil
			case run.LoggedFigure:
				state = measure.WindowStates.NotServed
			default:
				state, coverage = measure.WindowStates.NotCovering, true
			}
			r.restore(before)
			r.Measure = measureRun{}
		}
		if !fallback.set {
			fallback.measure, fallback.state, fallback.coverage, fallback.set = measure, state, coverage, true
		}
	}
	r.Measure = measureRun{}
	data, hasRows, err := serve(r)
	if err != nil {
		return data, hasRows, err
	}
	r.MeasureState(fallback.measure, fallback.state, fallback.coverage)
	if requested != params.VolumeMeasureBestAvailable {
		// An explicit measure that cannot be answered carries no figure, never logged figures labelled
		// with it (PS23).
		return nil, hasRows, nil
	}
	return data, hasRows, nil
}

// measureWindow is what the request's window says of one measure.
type measureWindow struct {
	// state is "" when the measure's window states are recorded as covered throughout the window, and
	// the named state otherwise.
	state string
	// intervalStates are the window states of the intervals the window meets, in name order.
	intervalStates []string
}

// MeasureWindowState says whether a measure can answer the request's window, read once per request and
// measure: "" when it can, and the named state otherwise.
func (r *Request) MeasureWindowState(measure vocab.DeclaredMeasure) (string, error) {
	window, err := r.measureWindowRead(measure)
	if err != nil {
		return "", err
	}
	return window.state, nil
}

// measureWindowRead is the window's reading of a measure, read once per request.
func (r *Request) measureWindowRead(measure vocab.DeclaredMeasure) (measureWindow, error) {
	if read, found := r.measureWindows[measure.Name]; found {
		return read, nil
	}
	read, err := r.readMeasureWindow(measure)
	if err != nil {
		return measureWindow{}, err
	}
	if r.measureWindows == nil {
		r.measureWindows = map[string]measureWindow{}
	}
	r.measureWindows[measure.Name] = read
	return read, nil
}

// MeasureState records a measure's named state of a volume answer and, for a state that carries one,
// its detail (state_details): the interval states met for the measure's not-covering state. coverage
// says the state is the run's judgment that a connection is not covered rather than the window's: the
// window is then covered throughout, and its interval state is the measure's covered state.
func (r *Request) MeasureState(measure vocab.DeclaredMeasure, state string, coverage bool) {
	r.State(state)
	window := r.measureWindows[measure.Name]
	if state == measure.WindowStates.NotCovering {
		states := window.intervalStates
		if coverage && len(states) == 0 {
			states = []string{measure.WindowStates.Covered}
		}
		r.StateDetail(state, envelope.StateDetail{IntervalStates: append([]string{}, states...)})
	}
}

// StateDetail records the detail of a named state the response carries.
func (r *Request) StateDetail(state string, detail envelope.StateDetail) {
	if r.Response.StateDetails == nil {
		r.Response.StateDetails = map[string]envelope.StateDetail{}
	}
	r.Response.StateDetails[state] = detail
}

// measureInterval is one recorded window interval of a measure (schema.sql, measure_interval).
type measureInterval struct {
	startsAt int64
	endsAt   *int64
	state    string
}

// readMeasureWindow reads whether a measure can answer the request's window: "" when its intervals say
// covered throughout it, and the named state otherwise -- the interval state when the window holds one,
// the not-covering state when it mixes several -- with the interval states met. With no active provider
// of the measure's kind, the window is in the measure's no-provider state; with several, which the
// exclusive kind's constraint refuses, in its not-covering state. An instant no interval holds is in
// its unrecorded state.
func (r *Request) readMeasureWindow(measure vocab.DeclaredMeasure) (measureWindow, error) {
	var providers []int64
	err := r.Db.WidgetRows(r.Ctx, "widget_measure_providers", vocab.Period{},
		map[string]any{"kind": measure.ProviderKind}, func(row reads.RowScanner) error {
			var id int64
			if err := row.Scan(&id); err != nil {
				return err
			}
			providers = append(providers, id)
			return nil
		})
	if err != nil {
		return measureWindow{}, err
	}
	noProvider := measure.WindowStates.NoProvider
	if len(providers) == 0 {
		return measureWindow{state: noProvider, intervalStates: []string{noProvider}}, nil
	}
	window := r.Window
	if window.To <= window.From {
		window = envelope.Interval{From: r.Now, To: r.Now + 1}
	}
	states := map[string]bool{}
	for _, provider := range providers {
		intervals, err := r.measureIntervals(measure.Name, provider)
		if err != nil {
			return measureWindow{}, err
		}
		for _, at := range intervalChangePoints(intervals, window.From, window.To) {
			states[intervalStateAt(intervals, at, measure.WindowStates.Unrecorded)] = true
		}
	}
	read := measureWindow{intervalStates: SortedNames(states)}
	switch {
	case len(providers) > 1:
		read.state = measure.WindowStates.NotCovering
	case len(states) == 1 && states[measure.WindowStates.Covered]:
		read.state = ""
	case len(states) == 1:
		read.state = read.intervalStates[0]
	default:
		read.state = measure.WindowStates.NotCovering
	}
	return read, nil
}

// measureIntervals reads one provider's recorded intervals of a measure, oldest first.
func (r *Request) measureIntervals(measure string, provider int64) ([]measureInterval, error) {
	var intervals []measureInterval
	err := r.Db.WidgetRows(r.Ctx, "widget_measure_intervals", vocab.Period{},
		map[string]any{"measure": measure, "provider_id": provider}, func(row reads.RowScanner) error {
			var interval measureInterval
			var ends sqlNullInt
			if err := row.Scan(&interval.startsAt, &ends, &interval.state); err != nil {
				return err
			}
			interval.endsAt = ends.pointer()
			intervals = append(intervals, interval)
			return nil
		})
	return intervals, err
}

// intervalChangePoints are the instants inside [from, to) at which a measure's state may change, from
// with them: the state is constant from one to the next.
func intervalChangePoints(intervals []measureInterval, from, to int64) []int64 {
	points := map[int64]bool{from: true}
	add := func(at int64) {
		if at > from && at < to {
			points[at] = true
		}
	}
	for _, interval := range intervals {
		add(interval.startsAt)
		if interval.endsAt != nil {
			add(*interval.endsAt)
		}
	}
	sorted := make([]int64, 0, len(points))
	for at := range points {
		sorted = append(sorted, at)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

// intervalStateAt is a measure's state at an instant: the latest interval holding it, or the unrecorded
// state.
func intervalStateAt(intervals []measureInterval, at int64, unrecorded string) string {
	state := unrecorded
	for _, interval := range intervals {
		if interval.startsAt <= at && (interval.endsAt == nil || at < *interval.endsAt) {
			state = interval.state
		}
	}
	return state
}

// sqlNullInt scans a nullable integer.
type sqlNullInt struct {
	value int64
	valid bool
}

// Scan implements sql.Scanner.
func (n *sqlNullInt) Scan(source any) error {
	switch value := source.(type) {
	case nil:
		n.valid = false
	case int64:
		n.value, n.valid = value, true
	default:
		return fmt.Errorf("request: an interval end is %T, not an integer", source)
	}
	return nil
}

func (n sqlNullInt) pointer() *int64 {
	if !n.valid {
		return nil
	}
	value := n.value
	return &value
}

// responseSnapshot is what a run of a widget may change of the response and the request: restored
// before a run under another measure, so the answer served is that run's alone.
type responseSnapshot struct {
	response envelope.Response
	window   *envelope.WindowReport
	override string
}

// cloneList copies a list, keeping a missing one missing and an empty one empty: the answer writes
// the first null and the second [].
func cloneList[T any](list []T) []T {
	if list == nil {
		return nil
	}
	return append(make([]T, 0, len(list)), list...)
}

// snapshot copies what a run may change.
func (r *Request) snapshot() responseSnapshot {
	copied := *r.Response
	copied.Gaps = cloneList(r.Response.Gaps)
	copied.Availability = cloneList(r.Response.Availability)
	copied.Freshness = cloneList(r.Response.Freshness)
	copied.Withheld = cloneList(r.Response.Withheld)
	copied.States = cloneList(r.Response.States)
	if r.Response.StateDetails != nil {
		copied.StateDetails = make(map[string]envelope.StateDetail, len(r.Response.StateDetails))
		for state, detail := range r.Response.StateDetails {
			copied.StateDetails[state] = detail
		}
	}
	copied.UnresolvedReferences = cloneList(r.Response.UnresolvedReferences)
	if r.Response.ResolverWindowBounded != nil {
		bounded := *r.Response.ResolverWindowBounded
		copied.ResolverWindowBounded = &bounded
	}
	snapshot := responseSnapshot{response: copied, override: r.Override}
	if r.Response.Window != nil {
		window := *r.Response.Window
		snapshot.window = &window
	}
	return snapshot
}

// restore puts back what snapshot copied.
func (r *Request) restore(snapshot responseSnapshot) {
	restored := snapshot.response
	if snapshot.window != nil {
		window := *snapshot.window
		restored.Window = &window
	}
	*r.Response = restored
	r.Override = snapshot.override
}

// MeasuredStatementSuffix names a statement's measured twin (PA5 of the plugin-API amendment): the same
// rows, with a declared measure's bytes in place of the logged bytes and, after the seven summable
// columns, the measure's parts as one column (reads.VolumeFigures.MeasureTarget; a widget's own statement
// names it measure_parts). One twin serves every declared measure: it binds the measure's name as
// :measure. A widget's own statement that has a twin is run as its twin under a declared measure
// (internal/widget/hostareas/installation/host_query.go); one that has none is run as it is.
const MeasuredStatementSuffix = "_measured"

// MeasureParameter is the parameter a measured twin binds the measure's name to.
const MeasureParameter = "measure"

// MeasuredTwinMismatch says what is wrong with a statement's measured twin, or "" when nothing is: texts
// are statements by name, and twin one of them. A twin, <name>_measured beside <name>, is run in its
// statement's place under a declared measure and bound with the statement's parameters and the measure,
// so it binds exactly its statement's parameters and :measure, and is a period template exactly when
// its statement is. A name ending in _measured with no statement beside it is a statement like any other.
func MeasuredTwinMismatch(texts map[string]string, twin string) string {
	base, isTwin := strings.CutSuffix(twin, MeasuredStatementSuffix)
	if !isTwin {
		return ""
	}
	statement, present := texts[base]
	if !present {
		return ""
	}
	expected := map[string]bool{MeasureParameter: true}
	for _, parameter := range parameterSet(statement) {
		expected[parameter] = true
	}
	twinParameters, wanted := parameterSet(texts[twin]), SortedNames(expected)
	if strings.Join(twinParameters, ",") != strings.Join(wanted, ",") {
		return fmt.Sprintf("the statement %s binds %s, so its measured twin %s binds %s; it binds %s: a twin binds "+
			"exactly its statement's parameters and :%s", base, describeParameters(parameterSet(statement)), twin,
			describeParameters(wanted), describeParameters(twinParameters), MeasureParameter)
	}
	if strings.Contains(texts[twin], PeriodToken) != strings.Contains(statement, PeriodToken) {
		return fmt.Sprintf("one of the statement %s and its measured twin %s is a period template (%s) and the "+
			"other is not: a twin runs at its statement's period", base, twin, PeriodToken)
	}
	return ""
}

// parameterSet is the parameters a statement binds, each once, in name order.
func parameterSet(text string) []string {
	seen := map[string]bool{}
	for _, parameter := range sqldb.QueryParameters(text) {
		seen[parameter] = true
	}
	return SortedNames(seen)
}

// describeParameters names parameters for a refusal's detail.
func describeParameters(parameters []string) string {
	if len(parameters) == 0 {
		return "no parameter"
	}
	return ":" + strings.Join(parameters, ", :")
}

// InMeasure is the name of a core statement in the measure the request is answered under: the
// statement itself, or its measured twin.
func (r *Request) InMeasure(statement string) string {
	if r.Measure.Name != "" {
		return statement + MeasuredStatementSuffix
	}
	return statement
}

// WithMeasure is a statement's parameters with the measure the request is answered under bound to
// :measure, which a measured twin reads; the parameters themselves under the logged measure.
func (r *Request) WithMeasure(parameters map[string]any) map[string]any {
	if r.Measure.Name == "" {
		return parameters
	}
	bound := make(map[string]any, len(parameters)+1)
	for name, value := range parameters {
		bound[name] = value
	}
	bound[MeasureParameter] = r.Measure.Name
	return bound
}

// holdsMeasure reports whether an answer's data holds a figure of a measure: an object whose member
// `measure` is that measure, wherever it is.
func holdsMeasure(data any, measure string) bool {
	if data == nil {
		return false
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return false
	}
	var tree any
	if json.Unmarshal(encoded, &tree) != nil {
		return false
	}
	var walk func(node any) bool
	walk = func(node any) bool {
		switch typed := node.(type) {
		case map[string]any:
			if typed["measure"] == measure {
				return true
			}
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(tree)
}

// PeriodToken is the store's period template token (store.PeriodStatement).
const PeriodToken = "@period@"

// SortedNames returns a map's keys, sorted.
func SortedNames[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
