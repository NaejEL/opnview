package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// Outside peers and their geolocation, for the map, country, operator and Sankey presets.
// A peer the geolocation could not place is never dropped: it lands in a named unplaced
// bin -- miss, pending, never looked up, or placed without coordinates -- so placed plus
// unplaced is always the total (docs/widget-catalogue.md, Part 4, item 2: a LEFT join,
// never an inner one).

// The unplaced bins.
const (
	unplacedMiss          = "miss"
	unplacedPending       = "pending"
	unplacedNoRow         = "no_row"
	unplacedNoCoordinates = "no_coordinates"
	unplacedEastWest      = "east_west"
	unplacedThisFirewall  = "this_firewall_without_outside_end"
	unplacedOtherEndpoint = "other_endpoint"
)

// peerFigures is one peer's figures and the interfaces that reached it.
type peerFigures struct {
	address    string
	figures    store.VolumeFigures
	interfaces map[int64]bool
}

// UnplacedBin is the volume one reason left unplaced.
type UnplacedBin struct {
	Reason    string `json:"reason"`
	Addresses int64  `json:"addresses"`
	Volume    Volume `json:"volume"`
}

// unplacedBins accumulates the bins in a fixed order, every bin present even at zero.
type unplacedBins struct {
	order   []string
	figures map[string]*store.VolumeFigures
	members map[string]int64
}

func newUnplacedBins(reasons ...string) *unplacedBins {
	bins := &unplacedBins{order: reasons, figures: map[string]*store.VolumeFigures{},
		members: map[string]int64{}}
	for _, reason := range reasons {
		bins.figures[reason] = &store.VolumeFigures{}
	}
	return bins
}

func (b *unplacedBins) add(reason string, figures store.VolumeFigures, addresses int64) {
	if b.figures[reason] == nil {
		b.figures[reason] = &store.VolumeFigures{}
		b.order = append(b.order, reason)
	}
	addFigures(b.figures[reason], figures)
	b.members[reason] += addresses
}

func (b *unplacedBins) list() []UnplacedBin {
	result := []UnplacedBin{}
	for _, reason := range b.order {
		result = append(result, UnplacedBin{Reason: reason, Addresses: b.members[reason],
			Volume: logged(*b.figures[reason])})
	}
	return result
}

func (b *unplacedBins) total() store.VolumeFigures {
	var total store.VolumeFigures
	for _, reason := range b.order {
		addFigures(&total, *b.figures[reason])
	}
	return total
}

// placement is where the geolocation puts an address, or the unplaced bin it lands in.
func placement(geo *GeoInfo) string {
	switch geo.LookupState {
	case "resolved":
		if geo.Latitude == nil || geo.Longitude == nil {
			return unplacedNoCoordinates
		}
		return ""
	case "miss":
		return unplacedMiss
	case "pending":
		return unplacedPending
	}
	return unplacedNoRow
}

// sortedPeers returns peers in a stable order, by bytes then address.
func sortedPeers(peers map[string]*peerFigures) []*peerFigures {
	list := make([]*peerFigures, 0, len(peers))
	for _, peer := range peers {
		list = append(list, peer)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].figures.Bytes != list[j].figures.Bytes {
			return list[i].figures.Bytes > list[j].figures.Bytes
		}
		return list[i].address < list[j].address
	})
	return list
}

// interfaceIDs renders an interface set as a sorted list.
func interfaceIDs(set map[int64]bool) []int64 {
	result := []int64{}
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// keepInterface reports whether a row's local interface passes an interface filter.
func keepInterface(selected map[int64]bool, key store.VolumeKey) bool {
	if selected == nil {
		return true
	}
	local := localInterface(key)
	return local != nil && selected[*local]
}

// outsidePeers groups the window's rows that have an outside peer, in the scope given,
// and passes every other row of the scope to other.
func outsidePeers(window store.VolumeWindow, scope string, selected map[int64]bool,
	other func(row store.VolumeRow)) map[string]*peerFigures {
	peers := map[string]*peerFigures{}
	for _, row := range window.Rows {
		if row.ExitLegDevice != nil || !scopeMatches(scope, row.VolumeKey) ||
			!keepInterface(selected, row.VolumeKey) {
			continue
		}
		if row.PeerAddress == nil {
			if other != nil {
				other(row)
			}
			continue
		}
		peer := peers[*row.PeerAddress]
		if peer == nil {
			peer = &peerFigures{address: *row.PeerAddress, interfaces: map[int64]bool{}}
			peers[*row.PeerAddress] = peer
		}
		addFigures(&peer.figures, row.VolumeFigures)
		if local := localInterface(row.VolumeKey); local != nil {
			peer.interfaces[*local] = true
		}
	}
	return peers
}
