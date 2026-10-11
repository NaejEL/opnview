package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// What the volume presets share: the rolling window from store.ReadVolumeWindow, which
// assembles whole hours from the 1 h slots and the partial hours at either edge from the
// flows -- equal to a computation over the flows inside the flow horizon, and the slots'
// answer beyond it -- and the rules that say which interface a row belongs to.

// The traffic directions of classified_flow (docs/data-model.md, "Classification").
const (
	directionOutbound         = "outbound"
	directionInbound          = "inbound"
	directionInterInterface   = "inter_interface"
	directionToThisFirewall   = "to_this_firewall"
	directionFromThisFirewall = "from_this_firewall"
)

// The traffic scopes.
const (
	scopeAll          = "all"
	scopeEastWest     = "east_west"
	scopeNorthSouth   = "north_south"
	scopeThisFirewall = "this_firewall"
)

// volumeWindow reads the rolling window, records its coverage and the freshness of the
// slots it read.
func (r *Request) volumeWindow() (store.VolumeWindow, error) {
	window, err := r.db.ReadVolumeWindow(r.ctx, r.window.From, r.window.To, r.now)
	if err != nil {
		return window, err
	}
	if window.Covered != nil {
		r.cover(&Interval{From: window.Covered.From, To: window.Covered.To})
	} else {
		r.cover(nil)
	}
	return window, r.hourFreshness(r.window.From, r.window.To)
}

// localInterface is the interface a row belongs to: the inside end that generated it --
// the source of an outbound, inter-interface or to-this-firewall row, the destination of an
// inbound or from-this-firewall row -- or nil when neither end is inside.
func localInterface(key store.VolumeKey) *int64 {
	switch key.TrafficDirection {
	case directionInbound, directionFromThisFirewall:
		return key.DstInterfaceID
	}
	return key.SrcInterfaceID
}

// scopeMatches reports whether a row is in the selected scope.
func scopeMatches(scope string, key store.VolumeKey) bool {
	return scope == scopeAll || scope == "" || key.TrafficScope == scope
}

// periodParam is the `period` key of every windowed preset.
func periodParam() Param {
	return Param{Key: "period", Kind: KindEnum, Values: []string{"1h", "24h", "7d", "30d"}, Default: "24h",
		Data: true}
}

// interfacesParam is the optional list of interface references most presets take.
func interfacesParam() Param {
	return Param{Key: "interfaces", Kind: KindReferenceList, Reference: "interface", Data: true}
}

// clientsParam is the optional list of client references.
func clientsParam() Param {
	return Param{Key: "clients", Kind: KindReferenceList, Reference: "client", Data: true}
}

// limitParam is a `limit`, whole and positive.
func limitParam(fallback float64) Param {
	return Param{Key: "limit", Kind: KindInteger, Min: 1, Default: fallback, Data: true}
}

// interfaceKey renders an interface id as a member key.
func interfaceKey(id int64) string { return "interface:" + strconv.FormatInt(id, 10) }

// clientKey renders a client id as a member key.
func clientKey(id int64) string { return "client:" + strconv.FormatInt(id, 10) }

// ranked is a member with a value to rank by.
type ranked struct {
	key   string
	value float64
	name  string
}

// rankDescending orders members by value, then by key, so equal values keep a stable
// order.
func rankDescending(members []ranked) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].value != members[j].value {
			return members[i].value > members[j].value
		}
		return members[i].key < members[j].key
	})
}

// figuresValue picks the figure a `measure` names from logged figures.
func figuresValue(measure string, figures store.VolumeFigures) float64 {
	switch measure {
	case "connections":
		return float64(figures.Connections())
	case "allowed":
		return float64(figures.AllowedConnections)
	case "blocked":
		return float64(figures.BlockedConnections)
	}
	return float64(figures.Bytes)
}
