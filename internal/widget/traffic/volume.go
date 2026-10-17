package traffic

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
	window, err := r.Db.ReadVolumeWindowOf(r.Ctx, r.Window.From, r.Window.To, r.Now, r.MeasureRead())
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

// FiguresValue picks the figure a `measure` names from figures of any measure: packets are a declared
// measure's figure.
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
// are a declared measure's figures, and are absent from a logged one, which is written as it always
// was.
type SummableFigures struct {
	Bytes              int64 `json:"bytes"`
	AllowedBytes       int64 `json:"allowed_bytes"`
	BlockedBytes       int64 `json:"blocked_bytes"`
	UnknownBytes       int64 `json:"unknown_bytes"`
	AllowedConnections int64 `json:"allowed_connections"`
	BlockedConnections int64 `json:"blocked_connections"`
	UnknownConnections int64 `json:"unknown_connections"`
	MeasureMembers
}

// MeasureMembers are what a declared measure's figures carry beyond the seven summable members: the
// measure's name and its parts (reads.VolumeFigures.PartsArray), the column measure_parts a statement
// read in the measure answers with (docs/widget-api.md, "Volume figures and their measure").
type MeasureMembers struct {
	Measure      string  `json:"measure,omitempty"`
	MeasureParts []int64 `json:"measure_parts,omitempty"`
	// The packets and the coverage of each part are read from a Volume handed back as its figures,
	// which carries them beside its bytes; summable figures are never written with them.
	AllowedPackets  *int64                   `json:"allowed_packets,omitempty"`
	BlockedPackets  *int64                   `json:"blocked_packets,omitempty"`
	UnknownPackets  *int64                   `json:"unknown_packets,omitempty"`
	AllowedCoverage *envelope.VolumeCoverage `json:"allowed_coverage,omitempty"`
	BlockedCoverage *envelope.VolumeCoverage `json:"blocked_coverage,omitempty"`
	UnknownCoverage *envelope.VolumeCoverage `json:"unknown_coverage,omitempty"`
}

// Summable is store figures as a script reads them.
func Summable(figures reads.VolumeFigures) SummableFigures {
	read := SummableFigures{Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, AllowedConnections: figures.AllowedConnections,
		BlockedConnections: figures.BlockedConnections, UnknownConnections: figures.UnknownConnections}
	if !figures.Measured() {
		return read
	}
	read.Measure = figures.Measure
	read.MeasureParts = figures.PartsArray()
	return read
}

// ToStore is summable figures as the core reads them: a declared measure's figures with their parts,
// read from measure_parts, or from the packets and coverage objects of a Volume handed back.
func (f SummableFigures) ToStore() reads.VolumeFigures {
	figures := reads.VolumeFigures{Bytes: f.Bytes, AllowedBytes: f.AllowedBytes, BlockedBytes: f.BlockedBytes,
		UnknownBytes: f.UnknownBytes, AllowedConnections: f.AllowedConnections,
		BlockedConnections: f.BlockedConnections, UnknownConnections: f.UnknownConnections}
	if _, declared := vocab.MeasureNamed(f.Measure); !declared {
		return figures
	}
	if len(f.MeasureParts) > 0 {
		if figures.SetPartsArray(f.Measure, f.MeasureParts) == nil {
			return figures
		}
		figures.Parts = reads.MeasureParts{}
	}
	figures.Measure = f.Measure
	parts := figures.Parts.List()
	for index, packets := range []*int64{f.AllowedPackets, f.BlockedPackets, f.UnknownPackets} {
		if packets != nil {
			parts[index].Packets = *packets
		}
	}
	for index, coverage := range []*envelope.VolumeCoverage{f.AllowedCoverage, f.BlockedCoverage, f.UnknownCoverage} {
		if coverage != nil {
			parts[index].Coverage = coverage.StoreCoverage(f.Measure)
		}
	}
	return figures
}
