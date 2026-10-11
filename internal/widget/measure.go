package widget

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// The measure a volume answer is given in (PS22 of the NetFlow join amendment to
// specs/SPEC-backend-completion-plugins.md; docs/widget-api.md, "Volumes and the observation-point
// limit").
//
// THE CHOICE IS THE CORE'S, AND IS MADE ONCE, HERE. A widget whose manifest declares `"volumes": true`
// is given the parameter volume_measure -- logged, netflow or best_available, best_available by
// default -- without declaring it, and is answered under the measure that parameter chooses: the
// core decides whether NetFlow can answer the window, runs the widget's script under that measure,
// and judges the figures it built. A widget only reads its figures through the core (host.volume,
// host.clients, host.peers, host.sites, host.flows, host.series, host.attribution) or through
// statements that declare their netflow twin (host.query), and builds them through host.figures;
// none of it names a measure. A third party's widget gets the parameter from its manifest alone.
//
// ONE MEASURE PER ANSWER, NEVER A LOGGED FIGURE LABELLED NETFLOW. A run under netflow reads every
// figure of the netflow measure, and every Volume and Count it builds says so. A run that builds a
// figure the core did not read under netflow -- a non-zero logged figure, a logged count -- or that
// fails is not served as netflow: the answer falls back to logged with a named state, or, for an
// explicit netflow, carries no figure. Two measures are never summed.
//
// best_available answers netflow only when every connection the answer counts has every direction
// covered at the Observation Point -- measured, in_progress, awaiting_export or missing, never
// not_captured, export_gap or not_exported_when_refused -- under a single Observation Point throughout
// the window (store.NetflowCoverage.Covered); otherwise it answers logged, byte for byte what
// volume_measure = logged answers, with the named state that says why and, for a state that carries
// one, its detail in state_details.

// The volume_measure parameter, and its value beyond the measures.
const (
	VolumeMeasureKey           = "volume_measure"
	VolumeMeasureBestAvailable = "best_available"
)

// The named states of a volume answer that is not of the netflow measure, or carries no figure.
const (
	// StateNetflowNotConfigured: no active provider of flow_record, or the capture configuration
	// in force names no interface or has the export disabled.
	StateNetflowNotConfigured = store.CaptureNotConfigured
	// StateObservationPointNotExported: the configuration captures on two or more interfaces, so no
	// record says where it was observed.
	StateObservationPointNotExported = store.CaptureObservationPointHidden
	// StateCaptureConfigurationUnknown: the configuration could not be read, lies between two reads
	// that differ, or the stream contradicted it.
	StateCaptureConfigurationUnknown = store.CaptureUnknown
	// StateInterfaceIndexUnmapped: the configuration names one interface whose kernel index could
	// not be mapped.
	StateInterfaceIndexUnmapped = store.CaptureIndexUnmapped
	// StateNetflowNotCoveringWindow: the window mixes intervals in different states, or the answer
	// counts a connection with a direction the Observation Point does not cover.
	StateNetflowNotCoveringWindow = "netflow_not_covering_window"
	// StateNetflowNotServedByType: the widget built a figure the core did not read under netflow,
	// so its answer cannot be given in that measure. opnview's own term: no product names it.
	StateNetflowNotServedByType = "netflow_not_served_by_type"
)

// VolumeMeasures are volume_measure's values, best_available its default.
var VolumeMeasures = []string{MeasureLogged, MeasureNetflow, VolumeMeasureBestAvailable}

// volumeMeasureParam is the parameter the core gives every type whose manifest declares volumes.
func volumeMeasureParam() Param {
	return Param{Key: VolumeMeasureKey, Kind: KindEnum, Values: append([]string(nil), VolumeMeasures...),
		Default: VolumeMeasureBestAvailable, Data: true}
}

// measureRun is the measure one run of a widget answers under, and what the run built.
type measureRun struct {
	// netflow says the run reads every figure of the netflow measure.
	netflow bool
	// loggedFigure says the run built a figure the core did not read under netflow.
	loggedFigure bool
	// uncovered says a netflow figure of the run counts a connection with a direction not covered.
	uncovered bool
}

// Netflow reports whether the request is being answered under the netflow measure: every core read
// of a volume figure reads that measure.
func (r *Request) Netflow() bool { return r.measure.netflow }

// MeasureInForce is the measure the request is being answered under.
func (r *Request) MeasureInForce() string {
	if r.measure.netflow {
		return MeasureNetflow
	}
	return MeasureLogged
}

// VolumeOf is the Volume of some figures, in their own measure. Under a netflow run it records a
// figure that is not netflow -- a logged figure holding anything -- and one counting a connection with
// a direction not covered.
func (r *Request) VolumeOf(figures store.VolumeFigures) Volume {
	if figures.Netflow || (r.measure.netflow && figures == (store.VolumeFigures{})) {
		r.judge(figures)
		return netflowVolume(figures)
	}
	if r.measure.netflow {
		r.measure.loggedFigure = true
	}
	return Logged(figures)
}

// ByteCountOf is the Count of some figures' bytes, in their own measure, judged as VolumeOf judges.
// single says the figures are one connection's: a netflow figure of one connection measured in
// both directions is exact, and is the only figure whose lower_bound is false.
func (r *Request) ByteCountOf(figures store.VolumeFigures, single bool) Count {
	if figures.Netflow || (r.measure.netflow && figures == (store.VolumeFigures{})) {
		r.judge(figures)
		exact := single && figures.Netflow && figures.Connections() == 1 && figures.Coverage().Measured == 1
		return Count{Measure: MeasureNetflow, LowerBound: !exact, Value: figures.Bytes}
	}
	if r.measure.netflow {
		r.measure.loggedFigure = true
	}
	return LoggedCount(figures.Bytes)
}

// CountOf is a count of the connections or records an answer's figures count, in the measure the
// request is answered under: the logged connections, whose netflow figures a netflow answer gives.
func (r *Request) CountOf(value int64) Count {
	return Count{Measure: r.MeasureInForce(), LowerBound: true, Value: value}
}

// LoggedCountOf is a count of logged records. Under a netflow run one holding anything is a logged
// figure, which the run cannot serve as netflow.
func (r *Request) LoggedCountOf(value int64) Count {
	if r.measure.netflow {
		if value != 0 {
			r.measure.loggedFigure = true
		}
		return r.CountOf(value)
	}
	return LoggedCount(value)
}

// CoverFigures records figures an answer counts whose numbers a widget uses directly rather than through a
// Volume or a Count: under a netflow run, a logged figure or a connection with a direction not covered
// is recorded as VolumeOf records it.
func (r *Request) CoverFigures(figures store.VolumeFigures) {
	if !r.measure.netflow {
		return
	}
	if !figures.Netflow && figures != (store.VolumeFigures{}) {
		r.measure.loggedFigure = true
		return
	}
	r.judge(figures)
}

// judge records netflow figures counting a connection with a direction not covered (PS22).
func (r *Request) judge(figures store.VolumeFigures) {
	if r.measure.netflow && !figures.Coverage().Covered() {
		r.measure.uncovered = true
	}
}

// netflowVolume is the Volume of netflow figures, with its packets and coverage, whole and per part.
func netflowVolume(figures store.VolumeFigures) Volume {
	volume := Logged(figures)
	volume.Measure = MeasureNetflow
	whole := figures.Parts.Whole()
	packets := func(value int64) *int64 { return &value }
	volume.Packets, volume.Coverage = packets(whole.Packets), coverageOf(whole.Coverage)
	volume.AllowedPackets, volume.AllowedCoverage = packets(figures.Parts.Allowed.Packets),
		coverageOf(figures.Parts.Allowed.Coverage)
	volume.BlockedPackets, volume.BlockedCoverage = packets(figures.Parts.Blocked.Packets),
		coverageOf(figures.Parts.Blocked.Coverage)
	volume.UnknownPackets, volume.UnknownCoverage = packets(figures.Parts.Unknown.Packets),
		coverageOf(figures.Parts.Unknown.Coverage)
	return volume
}

// serveMeasured answers a type that declares volumes under the measure volume_measure chooses.
func (r *Request) serveMeasured(serve ServeFunc) (any, bool, error) {
	requested := r.Params.Text(VolumeMeasureKey)
	if requested == MeasureLogged {
		return serve(r)
	}
	state, err := r.NetflowWindowState()
	if err != nil {
		return nil, false, err
	}
	// coverageFallback says the state is the run's own judgment, not the window's: the window has one
	// Observation Point throughout, and a connection the answer counts is not covered.
	coverageFallback := false
	if state == "" {
		before := r.snapshot()
		r.measure = measureRun{netflow: true}
		data, hasRows, err := serve(r)
		run := r.measure
		var failed *ScriptError
		switch {
		case errors.As(err, &failed):
			// A script that cannot run under netflow is not answered in it.
			run.loggedFigure = true
		case err != nil:
			r.measure = measureRun{}
			return data, hasRows, err
		case holdsMeasure(data, MeasureLogged):
			// A figure the script wrote itself as logged: never served in an answer under netflow.
			run.loggedFigure = true
		}
		switch {
		case !run.loggedFigure && !run.uncovered:
			return data, hasRows, nil
		case !run.loggedFigure && requested == MeasureNetflow:
			// Asked for netflow, and NetFlow covered only some of it: the netflow figures, each with
			// its coverage, and the state that says not every connection was covered.
			r.netflowState(StateNetflowNotCoveringWindow, true)
			return data, hasRows, nil
		case run.loggedFigure:
			state = StateNetflowNotServedByType
		default:
			state, coverageFallback = StateNetflowNotCoveringWindow, true
		}
		r.restore(before)
	}
	r.measure = measureRun{}
	data, hasRows, err := serve(r)
	if err != nil {
		return data, hasRows, err
	}
	r.netflowState(state, coverageFallback)
	if requested == MeasureNetflow {
		// An explicit netflow that cannot be answered carries no figure, never logged figures
		// labelled netflow (PS23).
		return nil, hasRows, nil
	}
	return data, hasRows, nil
}

// netflowWindow is what the request's window says of NetFlow.
type netflowWindow struct {
	// state is "" when one Observation Point is established throughout the window, and the named
	// state otherwise.
	state string
	// intervalStates are the capture states of the intervals the window meets, in name order.
	intervalStates []string
	// capturedInterfaces is the most interfaces a configuration in force over the window captures on,
	// counted where the window's intervals are observation_point_not_exported.
	capturedInterfaces int64
}

// NetflowWindowState says whether NetFlow can answer the request's window, read once per request:
// "" when it can, and the named state otherwise.
func (r *Request) NetflowWindowState() (string, error) {
	window, err := r.netflowWindowRead()
	if err != nil {
		return "", err
	}
	return window.state, nil
}

// netflowWindowRead is the window's reading, read once per request.
func (r *Request) netflowWindowRead() (netflowWindow, error) {
	if r.netflowWindow != nil {
		return *r.netflowWindow, nil
	}
	window, err := r.readNetflowWindow()
	if err != nil {
		return netflowWindow{}, err
	}
	r.netflowWindow = &window
	return window, nil
}

// netflowState records a named state of a volume answer and, for a state that carries one, its
// detail (state_details): the interfaces captured for observation_point_not_exported, the interval
// states met for netflow_not_covering_window. coverage says the state is the run's judgment that a
// connection is not covered rather than the window's: the window then has one Observation Point
// throughout, and its interval state is observation_point.
func (r *Request) netflowState(state string, coverage bool) {
	r.State(state)
	window := netflowWindow{}
	if r.netflowWindow != nil {
		window = *r.netflowWindow
	}
	switch state {
	case StateObservationPointNotExported:
		captured := window.capturedInterfaces
		r.StateDetail(state, StateDetail{CapturedInterfaces: &captured})
	case StateNetflowNotCoveringWindow:
		states := window.intervalStates
		if coverage && len(states) == 0 {
			states = []string{store.CaptureObservationPoint}
		}
		r.StateDetail(state, StateDetail{IntervalStates: append([]string{}, states...)})
	}
}

// StateDetail records the detail of a named state the response carries.
func (r *Request) StateDetail(state string, detail StateDetail) {
	if r.Response.StateDetails == nil {
		r.Response.StateDetails = map[string]StateDetail{}
	}
	r.Response.StateDetails[state] = detail
}

// readNetflowWindow reads whether NetFlow can answer the request's window: "" when one Observation
// Point is established throughout it, and the named state otherwise -- the interval state when the
// window holds one, netflow_not_covering_window when it mixes several -- with the interval states met
// and the interfaces captured.
func (r *Request) readNetflowWindow() (netflowWindow, error) {
	var providers []int64
	err := r.Db.WidgetRows(r.Ctx, "widget_flow_record_providers", store.Period{}, nil,
		func(row store.RowScanner) error {
			var id int64
			if err := row.Scan(&id); err != nil {
				return err
			}
			providers = append(providers, id)
			return nil
		})
	if err != nil {
		return netflowWindow{}, err
	}
	if len(providers) == 0 {
		return netflowWindow{state: StateNetflowNotConfigured, intervalStates: []string{StateNetflowNotConfigured}}, nil
	}
	window := r.Window
	if window.To <= window.From {
		window = Interval{From: r.Now, To: r.Now + 1}
	}
	states := map[string]bool{}
	var captured int64
	for _, provider := range providers {
		configurations, conditions, err := r.captureHistory(provider, window)
		if err != nil {
			return netflowWindow{}, err
		}
		for _, at := range store.CaptureChangePoints(configurations, conditions, window.From, window.To) {
			state, interfaces, _ := store.CaptureStateAt(configurations, conditions, at)
			states[state] = true
			if state == store.CaptureObservationPointHidden && interfaces > captured {
				captured = interfaces
			}
		}
		if states[store.CaptureObservationPoint] {
			// The index mapping is the collector's, held in memory; what it decided is on the records
			// stored. One captured interface whose records were all stored unmapped is that state; some
			// mapped and some not mixes two states over the window.
			var unmapped, mapped int64
			if _, err := r.Db.WidgetRow(r.Ctx, "widget_flow_record_unmapped", map[string]any{
				"provider_id": provider, "from": window.From, "to": window.To}, &unmapped, &mapped); err != nil {
				return netflowWindow{}, err
			}
			if unmapped > 0 {
				states[store.CaptureIndexUnmapped] = true
				if mapped == 0 {
					delete(states, store.CaptureObservationPoint)
				}
			}
		}
	}
	read := netflowWindow{intervalStates: sortedNames(states), capturedInterfaces: captured}
	switch {
	case len(providers) > 1:
		// One active provider of an exclusive kind is the rule (store, kinds.go); two would make two
		// Observation Points, which the join never has.
		read.state = StateNetflowNotCoveringWindow
	case len(states) == 1 && states[store.CaptureObservationPoint]:
		read.state = ""
	case len(states) == 1:
		read.state = read.intervalStates[0]
	default:
		read.state = StateNetflowNotCoveringWindow
	}
	return read, nil
}

// captureHistory reads a provider's capture configurations and the stream conditions meeting a
// window.
func (r *Request) captureHistory(provider int64, window Interval) ([]store.CaptureConfiguration,
	[]store.FlowRecordCondition, error) {
	var configurations []store.CaptureConfiguration
	byID := map[int64]int{}
	parameters := map[string]any{"provider_id": provider}
	err := r.Db.WidgetRows(r.Ctx, "widget_capture_configurations", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				configuration     store.CaptureConfiguration
				readable, enabled int64
			)
			if err := row.Scan(&configuration.ID, &configuration.FirstReadAt, &configuration.LastReadAt, &readable,
				&enabled); err != nil {
				return err
			}
			configuration.ProviderID = provider
			configuration.Readable, configuration.Enabled = readable == 1, enabled == 1
			byID[configuration.ID] = len(configurations)
			configurations = append(configurations, configuration)
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	err = r.Db.WidgetRows(r.Ctx, "widget_capture_interfaces", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				id, ingress, egress int64
				identifier          string
			)
			if err := row.Scan(&id, &identifier, &ingress, &egress); err != nil {
				return err
			}
			if index, known := byID[id]; known {
				configurations[index].Interfaces = append(configurations[index].Interfaces,
					store.CaptureInterface{Identifier: identifier, Ingress: ingress == 1, Egress: egress == 1})
			}
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	var conditions []store.FlowRecordCondition
	err = r.Db.WidgetRows(r.Ctx, "widget_flow_record_conditions", store.Period{},
		map[string]any{"provider_id": provider, "from": window.From, "to": window.To},
		func(row store.RowScanner) error {
			var (
				condition store.FlowRecordCondition
				resolved  store.NullInt64
			)
			if err := row.Scan(&condition.ObservedAt, &resolved); err != nil {
				return err
			}
			condition.ResolvedAt = NullableNumber(resolved)
			conditions = append(conditions, condition)
			return nil
		})
	return configurations, conditions, err
}

// responseSnapshot is what a run of a widget may change of the response and the request: restored
// before a run under another measure, so the answer served is that run's alone.
type responseSnapshot struct {
	response Response
	window   *WindowReport
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
		copied.StateDetails = make(map[string]StateDetail, len(r.Response.StateDetails))
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

// netflowStatementSuffix names a statement's twin of the netflow measure: the same parameters and the
// same rows, with the bytes the NetFlow export reported in place of the logged bytes and, after the
// seven summable columns, the five netflow ones (store.VolumeFigures.NetflowTargets). A widget's own
// statement that has a twin is run as its twin under netflow (host_query.go); one that has none is run
// as it is.
const netflowStatementSuffix = "_netflow"

// netflowTwinMismatch says what is wrong with a statement's netflow twin, or "" when nothing is:
// texts are statements by name, and twin one of them. A twin, <name>_netflow beside <name>, is run in
// its statement's place under the netflow measure and bound with the statement's parameters, so it
// binds exactly the same parameters and is a period template exactly when its statement is. A name
// ending in _netflow with no statement beside it is a statement like any other.
func netflowTwinMismatch(texts map[string]string, twin string) string {
	base, isTwin := strings.CutSuffix(twin, netflowStatementSuffix)
	if !isTwin {
		return ""
	}
	statement, present := texts[base]
	if !present {
		return ""
	}
	twinParameters, baseParameters := parameterSet(texts[twin]), parameterSet(statement)
	if strings.Join(twinParameters, ",") != strings.Join(baseParameters, ",") {
		return fmt.Sprintf("the statement %s binds %s and its netflow twin %s binds %s: a twin binds exactly "+
			"its statement's parameters", base, describeParameters(baseParameters), twin,
			describeParameters(twinParameters))
	}
	if strings.Contains(texts[twin], periodToken) != strings.Contains(statement, periodToken) {
		return fmt.Sprintf("one of the statement %s and its netflow twin %s is a period template (%s) and the "+
			"other is not: a twin runs at its statement's period", base, twin, periodToken)
	}
	return ""
}

// parameterSet is the parameters a statement binds, each once, in name order.
func parameterSet(text string) []string {
	seen := map[string]bool{}
	for _, parameter := range store.QueryParameters(text) {
		seen[parameter] = true
	}
	return sortedNames(seen)
}

// describeParameters names parameters for a refusal's detail.
func describeParameters(parameters []string) string {
	if len(parameters) == 0 {
		return "no parameter"
	}
	return ":" + strings.Join(parameters, ", :")
}

// measured is the name of a core statement in the measure the request is answered under: the
// statement itself, or its netflow twin.
func (r *Request) measured(statement string) string {
	if r.measure.netflow {
		return statement + netflowStatementSuffix
	}
	return statement
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
