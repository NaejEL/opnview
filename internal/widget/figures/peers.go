package figures

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// Outside peers and their geolocation, for the map, country, operator and Sankey presets.
// A peer the geolocation could not place is never dropped: it lands in a named unplaced
// bin -- miss, pending, never looked up, or placed without coordinates -- so placed plus
// unplaced is always the total (docs/widget-catalogue.md, Part 4, item 2: a LEFT join,
// never an inner one).

// The unplaced bins.
const (
	UnplacedMiss          = "miss"
	UnplacedPending       = "pending"
	UnplacedNoRow         = "no_row"
	UnplacedNoCoordinates = "no_coordinates"
	UnplacedEastWest      = "east_west"
	UnplacedThisFirewall  = "this_firewall_without_outside_end"
	UnplacedOtherEndpoint = "other_endpoint"
)

// PeerFigures is one peer's figures and the interfaces that reached it.
type PeerFigures struct {
	Address    string
	Figures    reads.VolumeFigures
	Interfaces map[int64]bool
}

// UnplacedBin is the volume one reason left unplaced.
type UnplacedBin struct {
	Reason    string          `json:"reason"`
	Addresses int64           `json:"addresses"`
	Volume    envelope.Volume `json:"volume"`
	// Members lists the bin's addresses with their figures, which sum to the bin, for bins that
	// list them (NewListedUnplacedBins): an unnamed destination is shown by its address, never only
	// folded into a count.
	Members *[]UnplacedMember `json:"members,omitempty"`
}

// UnplacedMember is one address of a listed bin.
type UnplacedMember struct {
	Address string          `json:"address"`
	Volume  envelope.Volume `json:"volume"`
}

// unplacedBins accumulates the bins in a fixed order, every bin present even at zero.
type unplacedBins struct {
	order   []string
	figures map[string]*reads.VolumeFigures
	members map[string]int64
	// listed says the bins list their addresses; addresses holds each bin's, by address.
	listed    bool
	addresses map[string]map[string]*reads.VolumeFigures
}

// NewListedUnplacedBins is NewUnplacedBins whose bins also list their addresses, each added with
// AddAddress, so each bin's addresses and their figures sum to it.
func NewListedUnplacedBins(reasons ...string) *unplacedBins {
	bins := NewUnplacedBins(reasons...)
	bins.listed = true
	bins.addresses = map[string]map[string]*reads.VolumeFigures{}
	return bins
}

// AddAddress adds one address's figures to a bin; an address added twice counts once.
func (b *unplacedBins) AddAddress(reason, address string, figures reads.VolumeFigures) {
	if b.addresses == nil {
		b.addresses = map[string]map[string]*reads.VolumeFigures{}
	}
	if b.addresses[reason] == nil {
		b.addresses[reason] = map[string]*reads.VolumeFigures{}
	}
	known := b.addresses[reason][address]
	if known == nil {
		known = &reads.VolumeFigures{}
		b.addresses[reason][address] = known
		b.Add(reason, reads.VolumeFigures{}, 1)
	}
	envelope.AddFigures(known, figures)
	envelope.AddFigures(b.figures[reason], figures)
}

func NewUnplacedBins(reasons ...string) *unplacedBins {
	bins := &unplacedBins{order: reasons, figures: map[string]*reads.VolumeFigures{},
		members: map[string]int64{}}
	for _, reason := range reasons {
		bins.figures[reason] = &reads.VolumeFigures{}
	}
	return bins
}

func (b *unplacedBins) Add(reason string, figures reads.VolumeFigures, addresses int64) {
	if b.figures[reason] == nil {
		b.figures[reason] = &reads.VolumeFigures{}
		b.order = append(b.order, reason)
	}
	envelope.AddFigures(b.figures[reason], figures)
	b.members[reason] += addresses
}

// List is every bin in order, each Volume built by volume -- the request's, in the figures' measure.
func (b *unplacedBins) List(volume func(reads.VolumeFigures) envelope.Volume) []UnplacedBin {
	result := []UnplacedBin{}
	for _, reason := range b.order {
		bin := UnplacedBin{Reason: reason, Addresses: b.members[reason], Volume: volume(*b.figures[reason])}
		if b.listed {
			members := []UnplacedMember{}
			for address, figures := range b.addresses[reason] {
				members = append(members, UnplacedMember{Address: address, Volume: volume(*figures)})
			}
			// The most bytes first, then the address in byte order, as every peer list.
			sort.Slice(members, func(i, j int) bool {
				if members[i].Volume.Bytes != members[j].Volume.Bytes {
					return members[i].Volume.Bytes > members[j].Volume.Bytes
				}
				return members[i].Address < members[j].Address
			})
			bin.Members = &members
		}
		result = append(result, bin)
	}
	return result
}

func (b *unplacedBins) Total() reads.VolumeFigures {
	var total reads.VolumeFigures
	for _, reason := range b.order {
		envelope.AddFigures(&total, *b.figures[reason])
	}
	return total
}

// Placement is where the geolocation puts an address, or the unplaced bin it lands in.
func Placement(geo *request.GeoInfo) string {
	switch geo.LookupState {
	case "resolved":
		if geo.Latitude == nil || geo.Longitude == nil {
			return UnplacedNoCoordinates
		}
		return ""
	case "miss":
		return UnplacedMiss
	case "pending":
		return UnplacedPending
	}
	return UnplacedNoRow
}

// SortedPeers returns peers in a stable order, by bytes then address.
func SortedPeers(peers map[string]*PeerFigures) []*PeerFigures {
	list := make([]*PeerFigures, 0, len(peers))
	for _, peer := range peers {
		list = append(list, peer)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Figures.Bytes != list[j].Figures.Bytes {
			return list[i].Figures.Bytes > list[j].Figures.Bytes
		}
		return list[i].Address < list[j].Address
	})
	return list
}

// InterfaceIDs renders an interface set as a sorted list.
func InterfaceIDs(set map[int64]bool) []int64 {
	result := []int64{}
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// KeepInterface reports whether a row's local interface passes an interface filter.
func KeepInterface(selected map[int64]bool, key reads.VolumeKey) bool {
	if selected == nil {
		return true
	}
	local := traffic.LocalInterface(key)
	return local != nil && selected[*local]
}

// OutsidePeers groups the window's rows that have an outside peer, in the scope given,
// and passes every other row of the scope to other.
func OutsidePeers(window reads.VolumeWindow, scope string, selected map[int64]bool,
	other func(row reads.VolumeRow)) map[string]*PeerFigures {
	peers := map[string]*PeerFigures{}
	for _, row := range window.Rows {
		if row.ExitLegDevice != nil || !traffic.ScopeMatches(scope, row.VolumeKey) ||
			!KeepInterface(selected, row.VolumeKey) {
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
			peer = &PeerFigures{Address: *row.PeerAddress, Interfaces: map[int64]bool{}}
			peers[*row.PeerAddress] = peer
		}
		envelope.AddFigures(&peer.Figures, row.VolumeFigures)
		if local := traffic.LocalInterface(row.VolumeKey); local != nil {
			peer.Interfaces[*local] = true
		}
	}
	return peers
}

// PlacementState is the unresolved bin of an address the geolocation did not resolve.
func PlacementState(geo *request.GeoInfo) string {
	switch geo.LookupState {
	case "miss":
		return UnplacedMiss
	case "pending":
		return UnplacedPending
	}
	return UnplacedNoRow
}
