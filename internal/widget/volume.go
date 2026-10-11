package widget

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
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

// VolumeWindow reads the rolling window, in the measure the request is answered under (measure.go),
// and records its coverage and the freshness of the slots it read.
func (r *Request) VolumeWindow() (store.VolumeWindow, error) {
	window, err := r.Db.ReadVolumeWindowOf(r.Ctx, r.Window.From, r.Window.To, r.Now, r.Netflow())
	if err != nil {
		return window, err
	}
	if window.Covered != nil {
		r.Cover(&Interval{From: window.Covered.From, To: window.Covered.To})
	} else {
		r.Cover(nil)
	}
	return window, r.HourFreshness(r.Window.From, r.Window.To)
}

// LocalInterface is the interface a row belongs to: the inside end that generated it --
// the source of an outbound, inter-interface or to-this-firewall row, the destination of an
// inbound or from-this-firewall row -- or nil when neither end is inside.
func LocalInterface(key store.VolumeKey) *int64 {
	switch key.TrafficDirection {
	case DirectionInbound, DirectionFromThisFirewall:
		return key.DstInterfaceID
	}
	return key.SrcInterfaceID
}

// ScopeMatches reports whether a row is in the selected scope.
func ScopeMatches(scope string, key store.VolumeKey) bool {
	return scope == ScopeAll || scope == "" || key.TrafficScope == scope
}

// InterfaceKey renders an interface id as a member key.
func InterfaceKey(id int64) string { return "interface:" + strconv.FormatInt(id, 10) }

// ClientKey renders a client id as a member key.
func ClientKey(id int64) string { return "client:" + strconv.FormatInt(id, 10) }

// FiguresValue picks the figure a `measure` names from figures of either measure: packets are a
// netflow figure's.
func FiguresValue(measure string, figures store.VolumeFigures) float64 {
	switch measure {
	case "packets":
		return float64(figures.Packets)
	case "connections":
		return float64(figures.Connections())
	case "allowed":
		return float64(figures.AllowedConnections)
	case "blocked":
		return float64(figures.BlockedConnections)
	}
	return float64(figures.Bytes)
}
