package widget

import (
	"encoding/json"

	"github.com/NaejEL/opnview/internal/store"
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
	InvalidParameters []InvalidValue `json:"invalid_parameters"`
	// MissingParameters are the required keys the request did not carry.
	MissingParameters []string `json:"missing_parameters"`
	// UnresolvedReferences are the references that matched nothing here.
	UnresolvedReferences []UnresolvedReference `json:"unresolved_references"`
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
	// ResolverWindowBounded is set on a response fed by the resolver's lookups: true when
	// the resolver's own ring buffer bounded the window (catalogue Part 3, fact 1), which
	// opnview records as a resolver_window_not_honoured collection gap.
	ResolverWindowBounded *bool `json:"resolver_window_bounded,omitempty"`
	// Data is the preset's data, or null when there is nothing honest to show.
	Data any `json:"data"`
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

// The measures a volume figure can be (A11, decision 7). Two measures are never summed
// together.
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

// Volume is one set of volume figures of one measure. lower_bound is always true:
// opnview sees only what crosses the firewall, and traffic between two clients behind
// one interface is in no figure.
type Volume struct {
	Measure            string `json:"measure"`
	LowerBound         bool   `json:"lower_bound"`
	Bytes              int64  `json:"bytes"`
	AllowedBytes       int64  `json:"allowed_bytes"`
	BlockedBytes       int64  `json:"blocked_bytes"`
	UnknownBytes       int64  `json:"unknown_bytes"`
	Connections        int64  `json:"connections"`
	AllowedConnections int64  `json:"allowed_connections"`
	BlockedConnections int64  `json:"blocked_connections"`
	UnknownConnections int64  `json:"unknown_connections"`
}

// Logged turns store figures into logged volume figures.
func Logged(figures store.VolumeFigures) Volume {
	return Volume{
		Measure: MeasureLogged, LowerBound: true,
		Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, Connections: figures.Connections(),
		AllowedConnections: figures.AllowedConnections, BlockedConnections: figures.BlockedConnections,
		UnknownConnections: figures.UnknownConnections,
	}
}

// add sums two logged figures.
func AddFigures(into *store.VolumeFigures, other store.VolumeFigures) {
	into.Bytes += other.Bytes
	into.AllowedBytes += other.AllowedBytes
	into.BlockedBytes += other.BlockedBytes
	into.UnknownBytes += other.UnknownBytes
	into.AllowedConnections += other.AllowedConnections
	into.BlockedConnections += other.BlockedConnections
	into.UnknownConnections += other.UnknownConnections
}

// Count is a connection or record count of one measure, carrying the same bound.
type Count struct {
	Measure    string `json:"measure"`
	LowerBound bool   `json:"lower_bound"`
	Value      int64  `json:"value"`
}

// CountPointer is a logged count, by address.
func CountPointer(value int64) *Count {
	count := LoggedCount(value)
	return &count
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
