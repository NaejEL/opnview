package widget

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.peers: the outside peers of the rolling window and where the geolocation placed them
// (peers.go), for the map, country, operator and Sankey widgets. A peer the geolocation could not
// place is never dropped: it lands in a named unplaced bin, so placed plus unplaced is the total.
//
//	host.peers.outside(rows, scope, interfaces)  the window's rows grouped by outside peer, in a
//	                                    scope and for an interface filter (an id list, or null for
//	                                    none): {peers, others}. peers is [{address, figures,
//	                                    interfaces}], by bytes then address; others is every row of
//	                                    the scope and filter that has no outside peer, in order. A
//	                                    second leg (exit_leg_device set) is in neither
//	host.peers.sorted(peers)            peers [{address, figures, interfaces}] in the same order,
//	                                    each interface list sorted
//	host.peers.keepInterface(interfaces, row)  whether a row's inside interface passes an interface
//	                                    filter (an id list, or null for none)
//	host.peers.placement(geo)           "" for an address placed with coordinates, otherwise its
//	                                    unplaced bin: miss, pending, no_row or no_coordinates
//	host.peers.placementState(geo)      the bin of an address the geolocation did not resolve: miss,
//	                                    pending or no_row
//	host.peers.bins(reasons)            unplaced bins, each reason present even at zero: {add(reason,
//	                                    figures, addresses), list(), total()}; list() is [{reason,
//	                                    addresses, volume}] in the order the reasons were first named
//	host.peers.bins(reasons, true)      the same, listing each bin's addresses: addAddress(reason,
//	                                    address, figures) adds one address's figures, an address
//	                                    counting once however often it is added, and each bin of
//	                                    list() carries members: [{address, volume}], by bytes then
//	                                    address, summing to the bin
//	host.peers.reasons                  the unplaced bins, by name: miss, pending, no_row,
//	                                    no_coordinates, east_west, this_firewall, other_endpoint
//
// geo is what host.services.geolocation answers; figures are summable figures (host.figures).

// scriptPeer is one outside peer as a script reads it.
type scriptPeer struct {
	Address    string          `json:"address"`
	Figures    summableFigures `json:"figures"`
	Interfaces []int64         `json:"interfaces"`
}

// scriptPeers is the window's rows grouped by outside peer, as a script reads them.
type scriptPeers struct {
	Peers  []scriptPeer `json:"peers"`
	Others []volumeRow  `json:"others"`
}

func init() {
	registerHostArea("peers", func(call *scriptCall, area *goja.Object) {
		call.function(area, "outside", func(arguments goja.FunctionCall) goja.Value {
			var rows []volumeRow
			call.decodeArgument(arguments.Argument(0), "the rows", &rows)
			scope := call.textOf(arguments.Argument(1), "the scope")
			selected := call.peerInterfaceFilterOf(arguments.Argument(2))
			window := store.VolumeWindow{}
			for _, row := range rows {
				window.Rows = append(window.Rows, store.VolumeRow{VolumeKey: row.key(), VolumeFigures: row.figures()})
			}
			answer := scriptPeers{Peers: []scriptPeer{}, Others: []volumeRow{}}
			grouped := OutsidePeers(window, scope, selected, func(row store.VolumeRow) {
				answer.Others = append(answer.Others, volumeRow{SrcInterfaceID: row.SrcInterfaceID,
					DstInterfaceID: row.DstInterfaceID, PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope,
					TrafficDirection: row.TrafficDirection, ExitLegDevice: row.ExitLegDevice,
					summableFigures: summable(row.VolumeFigures)})
			})
			for _, peer := range SortedPeers(grouped) {
				answer.Peers = append(answer.Peers, scriptPeer{Address: peer.Address, Figures: summable(peer.Figures),
					Interfaces: InterfaceIDs(peer.Interfaces)})
			}
			return call.value(answer)
		})
		call.function(area, "sorted", func(arguments goja.FunctionCall) goja.Value {
			var listed []scriptPeer
			call.decodeArgument(arguments.Argument(0), "the peers", &listed)
			peers := map[string]*PeerFigures{}
			for _, peer := range listed {
				if _, duplicate := peers[peer.Address]; duplicate {
					call.throw("peers.sorted is handed the address %s twice", peer.Address)
				}
				interfaces := map[int64]bool{}
				for _, id := range peer.Interfaces {
					interfaces[id] = true
				}
				peers[peer.Address] = &PeerFigures{Address: peer.Address, Figures: peer.figures(),
					Interfaces: interfaces}
			}
			answer := []scriptPeer{}
			for _, peer := range SortedPeers(peers) {
				answer = append(answer, scriptPeer{Address: peer.Address, Figures: summable(peer.Figures),
					Interfaces: InterfaceIDs(peer.Interfaces)})
			}
			return call.value(answer)
		})
		call.function(area, "keepInterface", func(arguments goja.FunctionCall) goja.Value {
			selected := call.peerInterfaceFilterOf(arguments.Argument(0))
			var row volumeRow
			call.decodeArgument(arguments.Argument(1), "the row", &row)
			return call.vm.ToValue(KeepInterface(selected, row.key()))
		})
		call.function(area, "placement", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(Placement(call.peerGeoOf(arguments.Argument(0))))
		})
		call.function(area, "placementState", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(PlacementState(call.peerGeoOf(arguments.Argument(0))))
		})
		call.function(area, "bins", func(arguments goja.FunctionCall) goja.Value {
			reasons := call.textsOf(arguments.Argument(0), "the reasons")
			bins := NewUnplacedBins(reasons...)
			if !absent(arguments.Argument(1)) && arguments.Argument(1).ToBoolean() {
				bins = NewListedUnplacedBins(reasons...)
			}
			object := call.vm.NewObject()
			call.function(object, "addAddress", func(arguments goja.FunctionCall) goja.Value {
				reason := call.textOf(arguments.Argument(0), "the reason")
				address := call.textOf(arguments.Argument(1), "the address")
				bins.AddAddress(reason, address, call.figuresOf(arguments.Argument(2), "the figures"))
				return goja.Undefined()
			})
			call.function(object, "add", func(arguments goja.FunctionCall) goja.Value {
				reason := call.textOf(arguments.Argument(0), "the reason")
				figures := call.figuresOf(arguments.Argument(1), "the figures")
				bins.Add(reason, figures, call.integerOf(arguments.Argument(2), "the addresses"))
				return goja.Undefined()
			})
			call.function(object, "list", func(goja.FunctionCall) goja.Value {
				return call.value(bins.List())
			})
			call.function(object, "total", func(goja.FunctionCall) goja.Value {
				return call.value(summable(bins.Total()))
			})
			return object
		})
		_ = area.Set("reasons", call.value(map[string]string{
			"miss": UnplacedMiss, "pending": UnplacedPending, "no_row": UnplacedNoRow,
			"no_coordinates": UnplacedNoCoordinates, "east_west": UnplacedEastWest,
			"this_firewall": UnplacedThisFirewall, "other_endpoint": UnplacedOtherEndpoint,
		}))
	})
}

// figures is a peer's summable figures as the core reads them.
func (peer scriptPeer) figures() store.VolumeFigures {
	return store.VolumeFigures{Bytes: peer.Figures.Bytes, AllowedBytes: peer.Figures.AllowedBytes,
		BlockedBytes: peer.Figures.BlockedBytes, UnknownBytes: peer.Figures.UnknownBytes,
		AllowedConnections: peer.Figures.AllowedConnections, BlockedConnections: peer.Figures.BlockedConnections,
		UnknownConnections: peer.Figures.UnknownConnections}
}

// peerInterfaceFilterOf reads an interface filter: an id list, or null for no restriction.
func (c *scriptCall) peerInterfaceFilterOf(value goja.Value) map[int64]bool {
	if absent(value) {
		return nil
	}
	selected := map[int64]bool{}
	for _, id := range c.integersOf(value, "the interface filter") {
		selected[id] = true
	}
	return selected
}

// peerGeoOf reads a geolocation a script hands back, as host.services.geolocation answered it.
func (c *scriptCall) peerGeoOf(value goja.Value) *GeoInfo {
	if absent(value) {
		c.throw("the geolocation is %s, not what host.services.geolocation answers", describe(value))
	}
	var geo GeoInfo
	c.decodeArgument(value, "the geolocation", &geo)
	return &geo
}
