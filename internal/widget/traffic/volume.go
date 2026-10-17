package traffic

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// What the volume presets share: the rolling window from store.ReadVolumeWindow, which
// assembles whole hours from the 1 h slots and the partial hours at either edge from the
// flows -- equal to a computation over the flows inside the flow horizon, and the slots'
// answer beyond it -- and the rules that say which interface a row belongs to.

// The traffic directions of classified_flow (docs/data-model.md, "Classification").
const (
	DirectionOutbound         = "outbound"
	DirectionInbound          = "inbound"
	DirectionInterInterface   = "inter_interface"
	DirectionToThisFirewall   = "to_this_firewall"
	DirectionFromThisFirewall = "from_this_firewall"
)

// The traffic scopes.
const (
	ScopeAll          = "all"
	ScopeEastWest     = "east_west"
	ScopeNorthSouth   = "north_south"
	ScopeThisFirewall = "this_firewall"
)

// VolumeWindow reads the rolling window, in the measure the request is answered under
// (internal/widget/request/measure.go), and records its coverage and the freshness of the slots it
// read. The outside peers of its rows are placed (request.Place), so their geolocations are read
// together at the first a script asks for.
func VolumeWindow(r *request.Request) (reads.VolumeWindow, error) {
	window, err := r.Db.ReadVolumeWindowOf(r.Ctx, r.Window.From, r.Window.To, r.Now, r.Netflow())
	if err != nil {
		return window, err
	}
	for _, row := range window.Rows {
		if row.PeerAddress != nil {
			r.Place(*row.PeerAddress)
		}
	}
	if window.Covered != nil {
		r.Cover(&envelope.Interval{From: window.Covered.From, To: window.Covered.To})
	} else {
		r.Cover(nil)
	}
	return window, r.HourFreshness(r.Window.From, r.Window.To)
}

// LocalInterface is the interface a row belongs to: the inside end that generated it --
// the source of an outbound, inter-interface or to-this-firewall row, the destination of an
// inbound or from-this-firewall row -- or nil when neither end is inside.
func LocalInterface(key reads.VolumeKey) *int64 {
	switch key.TrafficDirection {
	case DirectionInbound, DirectionFromThisFirewall:
		return key.DstInterfaceID
	}
	return key.SrcInterfaceID
}

// ScopeMatches reports whether a row is in the selected scope.
func ScopeMatches(scope string, key reads.VolumeKey) bool {
	return scope == ScopeAll || scope == "" || key.TrafficScope == scope
}

// InterfaceKey renders an interface id as a member key.
func InterfaceKey(id int64) string { return "interface:" + strconv.FormatInt(id, 10) }

// ClientKey renders a client id as a member key.
func ClientKey(id int64) string { return "client:" + strconv.FormatInt(id, 10) }

// FiguresValue picks the figure a `measure` names from figures of either measure: packets are a
// netflow figure's.
func FiguresValue(measure string, figures reads.VolumeFigures) float64 {
	switch measure {
	case "packets":
		return float64(figures.Packets())
	case "connections":
		return float64(figures.Connections())
	case "allowed":
		return float64(figures.AllowedConnections)
	case "blocked":
		return float64(figures.BlockedConnections)
	}
	return float64(figures.Bytes)
}

// SummableFigures is summable figures as a script reads and writes them. The members past the seven
// are a netflow figure's, and are absent from a logged one, which is written as it always was.
type SummableFigures struct {
	Bytes              int64 `json:"bytes"`
	AllowedBytes       int64 `json:"allowed_bytes"`
	BlockedBytes       int64 `json:"blocked_bytes"`
	UnknownBytes       int64 `json:"unknown_bytes"`
	AllowedConnections int64 `json:"allowed_connections"`
	BlockedConnections int64 `json:"blocked_connections"`
	UnknownConnections int64 `json:"unknown_connections"`
	NetflowMembers
}

// NetflowMembers are what a netflow figure carries beyond the seven summable members: its measure and
// the fifty-four columns of netflow_parts (store.NetflowPartsColumns), the names a statement read in
// the netflow measure gives them with (netflow_sum(...)).*.
type NetflowMembers struct {
	Measure                                                string `json:"measure,omitempty"`
	AllowedPackets                                         *int64 `json:"allowed_packets,omitempty"`
	AllowedMeasuredConnections                             *int64 `json:"allowed_measured_connections,omitempty"`
	AllowedPartialConnections                              *int64 `json:"allowed_partial_connections,omitempty"`
	AllowedUnmeasuredConnections                           *int64 `json:"allowed_unmeasured_connections,omitempty"`
	AllowedCaptureNotKnownConnections                      *int64 `json:"allowed_capture_not_known_connections,omitempty"`
	AllowedDirectionMeasuredConnections                    *int64 `json:"allowed_direction_measured_connections,omitempty"`
	AllowedDirectionInProgressConnections                  *int64 `json:"allowed_direction_in_progress_connections,omitempty"`
	AllowedDirectionAwaitingExportConnections              *int64 `json:"allowed_direction_awaiting_export_connections,omitempty"`
	AllowedDirectionMissingConnections                     *int64 `json:"allowed_direction_missing_connections,omitempty"`
	AllowedDirectionExportGapConnections                   *int64 `json:"allowed_direction_export_gap_connections,omitempty"`
	AllowedDirectionNotExportedWhenRefusedConnections      *int64 `json:"allowed_direction_not_exported_when_refused_connections,omitempty"`
	AllowedDirectionForwardingExportUnsettledConnections   *int64 `json:"allowed_direction_forwarding_export_unsettled_connections,omitempty"`
	AllowedDirectionIngressNotCapturedConnections          *int64 `json:"allowed_direction_ingress_not_captured_connections,omitempty"`
	AllowedDirectionParentCopiesOnlyConnections            *int64 `json:"allowed_direction_parent_copies_only_connections,omitempty"`
	AllowedDirectionInterfaceNotExportedConnections        *int64 `json:"allowed_direction_interface_not_exported_connections,omitempty"`
	AllowedDirectionInterfaceNotCapturedConnections        *int64 `json:"allowed_direction_interface_not_captured_connections,omitempty"`
	AllowedDirectionCaptureConfigurationUnknownConnections *int64 `json:"allowed_direction_capture_configuration_unknown_connections,omitempty"`
	AllowedCountedCopyConnections                          *int64 `json:"allowed_counted_copy_connections,omitempty"`
	BlockedPackets                                         *int64 `json:"blocked_packets,omitempty"`
	BlockedMeasuredConnections                             *int64 `json:"blocked_measured_connections,omitempty"`
	BlockedPartialConnections                              *int64 `json:"blocked_partial_connections,omitempty"`
	BlockedUnmeasuredConnections                           *int64 `json:"blocked_unmeasured_connections,omitempty"`
	BlockedCaptureNotKnownConnections                      *int64 `json:"blocked_capture_not_known_connections,omitempty"`
	BlockedDirectionMeasuredConnections                    *int64 `json:"blocked_direction_measured_connections,omitempty"`
	BlockedDirectionInProgressConnections                  *int64 `json:"blocked_direction_in_progress_connections,omitempty"`
	BlockedDirectionAwaitingExportConnections              *int64 `json:"blocked_direction_awaiting_export_connections,omitempty"`
	BlockedDirectionMissingConnections                     *int64 `json:"blocked_direction_missing_connections,omitempty"`
	BlockedDirectionExportGapConnections                   *int64 `json:"blocked_direction_export_gap_connections,omitempty"`
	BlockedDirectionNotExportedWhenRefusedConnections      *int64 `json:"blocked_direction_not_exported_when_refused_connections,omitempty"`
	BlockedDirectionForwardingExportUnsettledConnections   *int64 `json:"blocked_direction_forwarding_export_unsettled_connections,omitempty"`
	BlockedDirectionIngressNotCapturedConnections          *int64 `json:"blocked_direction_ingress_not_captured_connections,omitempty"`
	BlockedDirectionParentCopiesOnlyConnections            *int64 `json:"blocked_direction_parent_copies_only_connections,omitempty"`
	BlockedDirectionInterfaceNotExportedConnections        *int64 `json:"blocked_direction_interface_not_exported_connections,omitempty"`
	BlockedDirectionInterfaceNotCapturedConnections        *int64 `json:"blocked_direction_interface_not_captured_connections,omitempty"`
	BlockedDirectionCaptureConfigurationUnknownConnections *int64 `json:"blocked_direction_capture_configuration_unknown_connections,omitempty"`
	BlockedCountedCopyConnections                          *int64 `json:"blocked_counted_copy_connections,omitempty"`
	UnknownPackets                                         *int64 `json:"unknown_packets,omitempty"`
	UnknownMeasuredConnections                             *int64 `json:"unknown_measured_connections,omitempty"`
	UnknownPartialConnections                              *int64 `json:"unknown_partial_connections,omitempty"`
	UnknownUnmeasuredConnections                           *int64 `json:"unknown_unmeasured_connections,omitempty"`
	UnknownCaptureNotKnownConnections                      *int64 `json:"unknown_capture_not_known_connections,omitempty"`
	UnknownDirectionMeasuredConnections                    *int64 `json:"unknown_direction_measured_connections,omitempty"`
	UnknownDirectionInProgressConnections                  *int64 `json:"unknown_direction_in_progress_connections,omitempty"`
	UnknownDirectionAwaitingExportConnections              *int64 `json:"unknown_direction_awaiting_export_connections,omitempty"`
	UnknownDirectionMissingConnections                     *int64 `json:"unknown_direction_missing_connections,omitempty"`
	UnknownDirectionExportGapConnections                   *int64 `json:"unknown_direction_export_gap_connections,omitempty"`
	UnknownDirectionNotExportedWhenRefusedConnections      *int64 `json:"unknown_direction_not_exported_when_refused_connections,omitempty"`
	UnknownDirectionForwardingExportUnsettledConnections   *int64 `json:"unknown_direction_forwarding_export_unsettled_connections,omitempty"`
	UnknownDirectionIngressNotCapturedConnections          *int64 `json:"unknown_direction_ingress_not_captured_connections,omitempty"`
	UnknownDirectionParentCopiesOnlyConnections            *int64 `json:"unknown_direction_parent_copies_only_connections,omitempty"`
	UnknownDirectionInterfaceNotExportedConnections        *int64 `json:"unknown_direction_interface_not_exported_connections,omitempty"`
	UnknownDirectionInterfaceNotCapturedConnections        *int64 `json:"unknown_direction_interface_not_captured_connections,omitempty"`
	UnknownDirectionCaptureConfigurationUnknownConnections *int64 `json:"unknown_direction_capture_configuration_unknown_connections,omitempty"`
	UnknownCountedCopyConnections                          *int64 `json:"unknown_counted_copy_connections,omitempty"`
	// AllowedCoverage, BlockedCoverage and UnknownCoverage are read from a netflow Volume handed back as
	// its figures, which carries each part's coverage as an object beside the part's packets; summable
	// figures are never written with them.
	AllowedCoverage *envelope.VolumeCoverage `json:"allowed_coverage,omitempty"`
	BlockedCoverage *envelope.VolumeCoverage `json:"blocked_coverage,omitempty"`
	UnknownCoverage *envelope.VolumeCoverage `json:"unknown_coverage,omitempty"`
}

// slots are the fifty-four members, in the order of store.NetflowPartsColumns.
func (m *NetflowMembers) slots() []**int64 {
	return []**int64{
		&m.AllowedPackets,
		&m.AllowedMeasuredConnections,
		&m.AllowedPartialConnections,
		&m.AllowedUnmeasuredConnections,
		&m.AllowedCaptureNotKnownConnections,
		&m.AllowedDirectionMeasuredConnections,
		&m.AllowedDirectionInProgressConnections,
		&m.AllowedDirectionAwaitingExportConnections,
		&m.AllowedDirectionMissingConnections,
		&m.AllowedDirectionExportGapConnections,
		&m.AllowedDirectionNotExportedWhenRefusedConnections,
		&m.AllowedDirectionForwardingExportUnsettledConnections,
		&m.AllowedDirectionIngressNotCapturedConnections,
		&m.AllowedDirectionParentCopiesOnlyConnections,
		&m.AllowedDirectionInterfaceNotExportedConnections,
		&m.AllowedDirectionInterfaceNotCapturedConnections,
		&m.AllowedDirectionCaptureConfigurationUnknownConnections,
		&m.AllowedCountedCopyConnections,
		&m.BlockedPackets,
		&m.BlockedMeasuredConnections,
		&m.BlockedPartialConnections,
		&m.BlockedUnmeasuredConnections,
		&m.BlockedCaptureNotKnownConnections,
		&m.BlockedDirectionMeasuredConnections,
		&m.BlockedDirectionInProgressConnections,
		&m.BlockedDirectionAwaitingExportConnections,
		&m.BlockedDirectionMissingConnections,
		&m.BlockedDirectionExportGapConnections,
		&m.BlockedDirectionNotExportedWhenRefusedConnections,
		&m.BlockedDirectionForwardingExportUnsettledConnections,
		&m.BlockedDirectionIngressNotCapturedConnections,
		&m.BlockedDirectionParentCopiesOnlyConnections,
		&m.BlockedDirectionInterfaceNotExportedConnections,
		&m.BlockedDirectionInterfaceNotCapturedConnections,
		&m.BlockedDirectionCaptureConfigurationUnknownConnections,
		&m.BlockedCountedCopyConnections,
		&m.UnknownPackets,
		&m.UnknownMeasuredConnections,
		&m.UnknownPartialConnections,
		&m.UnknownUnmeasuredConnections,
		&m.UnknownCaptureNotKnownConnections,
		&m.UnknownDirectionMeasuredConnections,
		&m.UnknownDirectionInProgressConnections,
		&m.UnknownDirectionAwaitingExportConnections,
		&m.UnknownDirectionMissingConnections,
		&m.UnknownDirectionExportGapConnections,
		&m.UnknownDirectionNotExportedWhenRefusedConnections,
		&m.UnknownDirectionForwardingExportUnsettledConnections,
		&m.UnknownDirectionIngressNotCapturedConnections,
		&m.UnknownDirectionParentCopiesOnlyConnections,
		&m.UnknownDirectionInterfaceNotExportedConnections,
		&m.UnknownDirectionInterfaceNotCapturedConnections,
		&m.UnknownDirectionCaptureConfigurationUnknownConnections,
		&m.UnknownCountedCopyConnections,
	}
}

// Summable is store figures as a script reads them.
func Summable(figures reads.VolumeFigures) SummableFigures {
	read := SummableFigures{Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, AllowedConnections: figures.AllowedConnections,
		BlockedConnections: figures.BlockedConnections, UnknownConnections: figures.UnknownConnections}
	if !figures.Netflow {
		return read
	}
	read.Measure = envelope.MeasureNetflow
	values := figures.Parts.Fields()
	for index, slot := range read.slots() {
		value := *values[index]
		*slot = &value
	}
	return read
}

// ToStore is summable figures as the core reads them.
func (f SummableFigures) ToStore() reads.VolumeFigures {
	figures := reads.VolumeFigures{Bytes: f.Bytes, AllowedBytes: f.AllowedBytes, BlockedBytes: f.BlockedBytes,
		UnknownBytes: f.UnknownBytes, AllowedConnections: f.AllowedConnections,
		BlockedConnections: f.BlockedConnections, UnknownConnections: f.UnknownConnections}
	if f.Measure != envelope.MeasureNetflow {
		return figures
	}
	figures.Netflow = true
	values := figures.Parts.Fields()
	for index, slot := range f.slots() {
		if *slot != nil {
			*values[index] = **slot
		}
	}
	// A Volume handed back carries each part's coverage as an object.
	for index, coverage := range []*envelope.VolumeCoverage{f.AllowedCoverage, f.BlockedCoverage, f.UnknownCoverage} {
		if coverage != nil {
			figures.Parts.List()[index].Coverage = coverage.StoreCoverage()
		}
	}
	return figures
}
