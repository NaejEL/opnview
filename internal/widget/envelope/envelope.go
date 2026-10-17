package envelope

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// The response envelope (A5), one shape for every type, documented once in
// docs/widget-api.md. Every figure the store could not compute is ABSENT -- a null, a
// missing member, a named state -- and never a 0 that means "we could not look".

// Response is what every widget endpoint answers.
type Response struct {
	// Type is the widget type answered.
	Type string `json:"type"`
	// Parameters are the effective parameters, after defaults.
	Parameters map[string]any `json:"parameters"`
	// UnknownParameters are the keys the type does not declare, kept and reported, by
	// path; never applied, never an error.
	UnknownParameters map[string]json.RawMessage `json:"unknown_parameters"`
	// InvalidParameters are the values the API could not apply; each key took its default.
	InvalidParameters []params.InvalidValue `json:"invalid_parameters"`
	// MissingParameters are the required keys the request did not carry.
	MissingParameters []string `json:"missing_parameters"`
	// UnresolvedReferences are the references that matched nothing here.
	UnresolvedReferences []params.UnresolvedReference `json:"unresolved_references"`
	// Window is the requested window and the part of it the store covers; null for a
	// type that takes no period.
	Window *WindowReport `json:"window"`
	// Gaps are the intervals inside the window a provider the type binds is known not to
	// have covered.
	Gaps []Gap `json:"gaps"`
	// Availability is every registered provider of every kind the type binds.
	Availability []ProviderAvailability `json:"availability"`
	// Condition is the widget's condition code (docs/widget-api.md, "Condition codes").
	Condition string `json:"condition"`
	// Freshness is one entry per aggregate period the response read.
	Freshness []Freshness `json:"freshness"`
	// AggregateMode is the aggregate_mode setting in force: full or no_domains.
	AggregateMode string `json:"aggregate_mode"`
	// Withheld lists every field or level the aggregate mode withheld, by path.
	Withheld []string `json:"withheld"`
	// States are named states the response carries beside its data: a figure that has no
	// answer for a stated reason (docs/widget-api.md, "Named states").
	States []string `json:"states"`
	// StateDetails is the detail of a state of States that carries one, keyed by the state's name:
	// interval_states for netflow_not_covering_window (docs/widget-api.md, "Named states"). Absent when
	// no state of the
	// answer carries a detail, so an answer holding no such state is unchanged by it.
	StateDetails map[string]StateDetail `json:"state_details,omitempty"`
	// ResolverWindowBounded is set on a response fed by the resolver's lookups: true when
	// the resolver's own ring buffer bounded the window (catalogue Part 3, fact 1), which
	// opnview records as a resolver_window_not_honoured collection gap.
	ResolverWindowBounded *bool `json:"resolver_window_bounded,omitempty"`
	// Data is the preset's data, or null when there is nothing honest to show.
	Data any `json:"data"`
}

// StateDetail is what a named state says beyond its name. Each member belongs to one state, and a
// detail carries only its own state's.
type StateDetail struct {
	// IntervalStates is, for netflow_not_covering_window, the capture states of the intervals the
	// window meets, in name order: capture_configured for an interval with a readable, enabled capture
	// configuration, and netflow_not_configured or capture_configuration_unknown for the others.
	IntervalStates []string `json:"interval_states,omitempty"`
}

// Interval is a half-open span of UTC epoch seconds.
type Interval struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// WindowReport says what was asked for and what the store holds of it.
type WindowReport struct {
	Requested Interval `json:"requested"`
	// Covered is the part of the window the store holds data for, or null when it holds
	// none of it.
	Covered *Interval `json:"covered"`
	// Coverage is covered, partially_covered or not_covered.
	Coverage string `json:"coverage"`
	// FlowHorizonAt is the instant before which the retention purge may have removed the
	// flows, or null when the retention is unlimited. Before it, a figure the aggregates
	// do not hold has no answer, and says so by a named state.
	FlowHorizonAt *int64 `json:"flow_horizon_at"`
}

// Gap is one interval a provider is known not to have covered.
type Gap struct {
	ProviderKind string `json:"provider_kind"`
	ProviderKey  string `json:"provider_key"`
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	Reason       string `json:"reason"`
}

// ProviderAvailability is one registered provider and the condition it is in.
type ProviderAvailability struct {
	Kind        string `json:"kind"`
	ProviderKey string `json:"provider_key"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
	// State, Probe, CheckedAt and Detail are the provider's source_availability row, or
	// null when it has none. Detail is diagnostic text from the collector, passed through
	// verbatim (decision 18); the interface translates the condition, never the detail.
	State *string `json:"state"`
	// Outcome is the closed outcome code of the last probe or read
	// (source_availability.outcome, docs/data-model.md): what an interface translates.
	Outcome   *string `json:"outcome"`
	Probe     *string `json:"probe"`
	CheckedAt *int64  `json:"checked_at"`
	Detail    *string `json:"detail"`
	// Condition is one of the catalogue's four conditions, or state_unknown.
	Condition string `json:"condition"`
}

// Freshness is the state of one aggregate period a response read.
type Freshness struct {
	Period string `json:"period"`
	// SlotsRead is how many slot rows of the period the read covered.
	SlotsRead int64 `json:"slots_read"`
	// LastComputedAt is the newest computation among them, null when none was read.
	LastComputedAt *int64 `json:"last_computed_at"`
	// StaleSlots counts the slots whose computation is behind their flows, by the
	// refresh contract's recognition rule.
	StaleSlots int64 `json:"stale_slots"`
	// Stale is true when any slot read is stale.
	Stale bool `json:"stale"`
}

// The four conditions of the catalogue, Part 1, and the provider with no row.
const (
	ConditionNotYetProbed       = "not_yet_probed"
	ConditionUnavailable        = "unavailable"
	ConditionPresentButDisabled = "present_but_disabled"
	ConditionReachable          = "reachable"
	ConditionStateUnknown       = "state_unknown"
)

// The widget-level conditions beyond a provider's.
const (
	// ConditionReachableNoRows is the fourth condition: the source is healthy and the
	// period holds nothing. It is the only condition with measured zeros.
	ConditionReachableNoRows = "reachable_no_rows"
	// ConditionUnresolvedReference is a widget whose reference matched nothing.
	ConditionUnresolvedReference = "unresolved_reference"
	// ConditionMissingRequiredParameter is a widget missing a key it cannot answer
	// without, such as client_traffic_detail's client.
	ConditionMissingRequiredParameter = "missing_required_parameter"
	// ConditionWithheldByAggregateMode is a widget the aggregate mode withholds whole.
	ConditionWithheldByAggregateMode = "withheld_by_aggregate_mode"
	// ConditionNoOwnerExists is Per-person activity's fifth condition: nobody has been
	// created, so no card can be drawn and the reason is not an absence of traffic.
	ConditionNoOwnerExists = "no_owner_exists"
)

// The request-level conditions, answered instead of an envelope.
const (
	ConditionUnknownWidgetType   = "unknown_widget_type"
	ConditionMalformedParameters = "malformed_parameters"
)

// The measures a volume figure can be (A11, decision 7), beside every measure a plugin declares (PA5
// of the plugin-API amendment; vocab.DeclaredMeasure): the set is open, and a declared measure's name
// is its own value. Two measures are never summed together.
const (
	// MeasureLogged is the filter log: bytes are the sum of the logged packet lengths --
	// the packet that establishes a state, not the rest of the connection -- and
	// connections are the logged records.
	MeasureLogged = "logged"
	// MeasureSampled is the bytes seen in the traffic/top samples, a few seconds each.
	MeasureSampled = "sampled"
	// MeasureInterfaceCounter is the interface's own counters, which include traffic no
	// logging rule matched.
	MeasureInterfaceCounter = "interface_counter"
)

// Volume is one set of volume figures of one measure: logged or a declared measure, never both, and
// never a sum of two (internal/widget/request/measure.go). lower_bound is true on every Volume: opnview
// sees only what crosses the firewall, and traffic between two clients behind one interface is in
// no figure; a declared measure's Volume is an aggregate, and the only exact figure of it is one
// connection's byte Count (Request.ByteCountOf). A logged Volume gains no field. A declared measure's
// Volume also carries its packets and its coverage, whole and for each of its allowed, blocked and
// unknown parts, as its bytes and connections are split: each part carries its own packets and
// coverage, so the part a map keeps is served with its own (PS21, and the maintainer's decision of 11
// October 2026, "coverage per action").
type Volume struct {
	Measure            string          `json:"measure"`
	LowerBound         bool            `json:"lower_bound"`
	Bytes              int64           `json:"bytes"`
	AllowedBytes       int64           `json:"allowed_bytes"`
	BlockedBytes       int64           `json:"blocked_bytes"`
	UnknownBytes       int64           `json:"unknown_bytes"`
	Connections        int64           `json:"connections"`
	AllowedConnections int64           `json:"allowed_connections"`
	BlockedConnections int64           `json:"blocked_connections"`
	UnknownConnections int64           `json:"unknown_connections"`
	Packets            *int64          `json:"packets,omitempty"`
	AllowedPackets     *int64          `json:"allowed_packets,omitempty"`
	BlockedPackets     *int64          `json:"blocked_packets,omitempty"`
	UnknownPackets     *int64          `json:"unknown_packets,omitempty"`
	Coverage           *VolumeCoverage `json:"coverage,omitempty"`
	AllowedCoverage    *VolumeCoverage `json:"allowed_coverage,omitempty"`
	BlockedCoverage    *VolumeCoverage `json:"blocked_coverage,omitempty"`
	UnknownCoverage    *VolumeCoverage `json:"unknown_coverage,omitempty"`
}

// VolumeCoverage is how a declared measure covered a Volume's connections, written in the measure's own
// names (vocab.DeclaredMeasure): its four coverage classes, which sum to the connections; under
// direction_states, per declared direction state in its declared order, the connections with at least
// one direction in it, so that no direction the measure cannot count is silently zero -- they say which
// states were met, and do not sum to the connections; then, per declared marker, the connections with a
// direction carrying it. For the netflow measure that is NJ5's and NJ8's object of the amendment "the
// NetFlow join with every interface captured", byte for byte.
type VolumeCoverage struct {
	// measure names the measure whose names the coverage is written in.
	measure string
	// counts are the counts, in the store's layout.
	counts reads.MeasureCoverage
	// decoded is a coverage object a script handed back, read in its measure's names by StoreCoverage.
	decoded json.RawMessage
}

// CoverageOf is a store coverage of a measure as a Volume carries it.
func CoverageOf(measure string, coverage reads.MeasureCoverage) *VolumeCoverage {
	return &VolumeCoverage{measure: measure, counts: coverage}
}

// MarshalJSON writes the coverage in its measure's names and declared order.
func (c VolumeCoverage) MarshalJSON() ([]byte, error) {
	measure, found := vocab.MeasureNamed(c.measure)
	if !found {
		if c.decoded != nil {
			return c.decoded, nil
		}
		return nil, fmt.Errorf("envelope: the measure %q is not declared", c.measure)
	}
	var out bytes.Buffer
	field := func(name string, value int64) {
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, "%q:%d", name, value)
	}
	out.WriteByte('{')
	field(measure.Classes.Complete, c.counts.Complete)
	field(measure.Classes.Partial, c.counts.Partial)
	field(measure.Classes.Unmeasured, c.counts.Unmeasured)
	field(measure.Classes.NotKnown, c.counts.NotKnown)
	out.WriteString(`,"direction_states":{`)
	for index, state := range measure.States {
		if index > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, "%q:%d", state.Name, c.counts.States[index])
	}
	out.WriteByte('}')
	for index, marker := range measure.Markers {
		field(marker, c.counts.Markers[index])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// UnmarshalJSON keeps a coverage object a script handed back, for StoreCoverage to read in its measure.
func (c *VolumeCoverage) UnmarshalJSON(raw []byte) error {
	c.decoded = append(json.RawMessage(nil), raw...)
	return nil
}

// StoreCoverage is a Volume's coverage as the store holds it, read in a measure's names: the counts it
// was built from, or the object a script handed back. A name the measure does not declare is ignored.
func (c VolumeCoverage) StoreCoverage(measureName string) reads.MeasureCoverage {
	if c.decoded == nil {
		return c.counts
	}
	measure, found := vocab.MeasureNamed(measureName)
	var counts reads.MeasureCoverage
	if !found {
		return counts
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(c.decoded, &object) != nil {
		return counts
	}
	number := func(name string) int64 {
		var value int64
		_ = json.Unmarshal(object[name], &value)
		return value
	}
	counts.Complete, counts.Partial = number(measure.Classes.Complete), number(measure.Classes.Partial)
	counts.Unmeasured, counts.NotKnown = number(measure.Classes.Unmeasured), number(measure.Classes.NotKnown)
	var states map[string]int64
	_ = json.Unmarshal(object["direction_states"], &states)
	for index, state := range measure.States {
		counts.States[index] = states[state.Name]
	}
	for index, marker := range measure.Markers {
		counts.Markers[index] = number(marker)
	}
	return counts
}

// Logged turns store figures into logged volume figures.
func Logged(figures reads.VolumeFigures) Volume {
	return Volume{
		Measure: MeasureLogged, LowerBound: true,
		Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, Connections: figures.Connections(),
		AllowedConnections: figures.AllowedConnections, BlockedConnections: figures.BlockedConnections,
		UnknownConnections: figures.UnknownConnections,
	}
}

// AddFigures sums two figures of one measure.
func AddFigures(into *reads.VolumeFigures, other reads.VolumeFigures) {
	into.Add(other)
}

// Count is a connection or record count of one measure, carrying the same bound.
type Count struct {
	Measure    string `json:"measure"`
	LowerBound bool   `json:"lower_bound"`
	Value      int64  `json:"value"`
}

// LoggedCount is a count of logged records.
func LoggedCount(value int64) Count {
	return Count{Measure: MeasureLogged, LowerBound: true, Value: value}
}

// Remainder is what a truncating parameter dropped (A8): how many members, and their
// figures, so the members shown plus the remainder make the whole.
type Remainder struct {
	Members int64   `json:"members"`
	Volume  *Volume `json:"volume,omitempty"`
	Count   *Count  `json:"count,omitempty"`
}
