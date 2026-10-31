package request

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// THE MEASURE IS A PARAMETER THE CORE RESERVES, AND NOTHING MORE (ADR 0021, decision 5). A widget
// whose manifest declares `"volumes": true` is given the parameter volume_measure -- logged,
// best_available, or the name of a measure a plugin declares (vocab.DeclaredMeasure), best_available by
// default -- without declaring it. Which measure can answer the window, the fall-back to logged, the
// twin statement run in the measure's place and the judgement of what the script built are the figures
// library's (plugins/figures/figures.js, exports.measure), which the widget's script hands its answer
// to; the core reads the parameter for the catalogue and the front and never a figure.

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

// ResponseSnapshot is what a run of a widget may change of the response and the request: a script
// takes one before a run it may set aside, and restores it so the answer served is one run's alone
// (host.envelope.snapshot and restore).
type ResponseSnapshot struct {
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

// Snapshot copies what a run may change.
func (r *Request) Snapshot() ResponseSnapshot {
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
	snapshot := ResponseSnapshot{response: copied, override: r.Override}
	if r.Response.Window != nil {
		window := *r.Response.Window
		snapshot.window = &window
	}
	return snapshot
}

// Restore puts back what Snapshot copied.
func (r *Request) Restore(snapshot ResponseSnapshot) {
	restored := snapshot.response
	if snapshot.window != nil {
		window := *snapshot.window
		restored.Window = &window
	}
	*r.Response = restored
	r.Override = snapshot.override
}

// StateDetail records the detail of a named state the response carries.
func (r *Request) StateDetail(state string, detail envelope.StateDetail) {
	if r.Response.StateDetails == nil {
		r.Response.StateDetails = map[string]envelope.StateDetail{}
	}
	r.Response.StateDetails[state] = detail
}
