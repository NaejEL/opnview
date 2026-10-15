package readers

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/hostareas/values"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.peers: the outside peers of the rolling window and where the geolocation placed them
// (internal/widget/figures/peers.go), for the map, country, operator and Sankey widgets. A peer the
// geolocation could not place is never dropped: it lands in a named unplaced bin, so placed plus
// unplaced is the total.
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
	Address    string                  `json:"address"`
	Figures    traffic.SummableFigures `json:"figures"`
	Interfaces []int64                 `json:"interfaces"`
}

// scriptPeers is the window's rows grouped by outside peer, as a script reads them.
type scriptPeers struct {
	Peers  []scriptPeer `json:"peers"`
	Others []volumeRow  `json:"others"`
}

func init() {
	script.RegisterHostArea("peers", func(call *script.ScriptCall, area *goja.Object) {
		call.Function(area, "outside", func(arguments goja.FunctionCall) goja.Value {
			var rows []volumeRow
			call.DecodeArgument(arguments.Argument(0), "the rows", &rows)
			scope := call.TextOf(arguments.Argument(1), "the scope")
			selected := peerInterfaceFilterOf(call, arguments.Argument(2))
			window := reads.VolumeWindow{}
			for _, row := range rows {
				window.Rows = append(window.Rows, reads.VolumeRow{VolumeKey: row.key(),
					VolumeFigures: values.FromScript(call, row.SummableFigures)})
			}
			answer := scriptPeers{Peers: []scriptPeer{}, Others: []volumeRow{}}
			grouped := figures.OutsidePeers(window, scope, selected, func(row reads.VolumeRow) {
				answer.Others = append(answer.Others, volumeRow{SrcInterfaceID: row.SrcInterfaceID,
					DstInterfaceID: row.DstInterfaceID, PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope,
					TrafficDirection: row.TrafficDirection, ExitLegDevice: row.ExitLegDevice,
					SummableFigures: traffic.Summable(row.VolumeFigures)})
			})
			for _, peer := range figures.SortedPeers(grouped) {
				answer.Peers = append(answer.Peers, scriptPeer{Address: peer.Address, Figures: traffic.Summable(peer.Figures),
					Interfaces: figures.InterfaceIDs(peer.Interfaces)})
			}
			return call.Value(answer)
		})
		call.Function(area, "sorted", func(arguments goja.FunctionCall) goja.Value {
			var listed []scriptPeer
			call.DecodeArgument(arguments.Argument(0), "the peers", &listed)
			peers := map[string]*figures.PeerFigures{}
			for _, peer := range listed {
				if _, duplicate := peers[peer.Address]; duplicate {
					call.Throw("peers.sorted is handed the address %s twice", peer.Address)
				}
				interfaces := map[int64]bool{}
				for _, id := range peer.Interfaces {
					interfaces[id] = true
				}
				peers[peer.Address] = &figures.PeerFigures{Address: peer.Address, Figures: values.FromScript(call, peer.Figures),
					Interfaces: interfaces}
			}
			answer := []scriptPeer{}
			for _, peer := range figures.SortedPeers(peers) {
				answer = append(answer, scriptPeer{Address: peer.Address, Figures: traffic.Summable(peer.Figures),
					Interfaces: figures.InterfaceIDs(peer.Interfaces)})
			}
			return call.Value(answer)
		})
		call.Function(area, "keepInterface", func(arguments goja.FunctionCall) goja.Value {
			selected := peerInterfaceFilterOf(call, arguments.Argument(0))
			var row volumeRow
			call.DecodeArgument(arguments.Argument(1), "the row", &row)
			return call.VM.ToValue(figures.KeepInterface(selected, row.key()))
		})
		call.Function(area, "placement", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(figures.Placement(peerGeoOf(call, arguments.Argument(0))))
		})
		call.Function(area, "placementState", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(figures.PlacementState(peerGeoOf(call, arguments.Argument(0))))
		})
		call.Function(area, "bins", func(arguments goja.FunctionCall) goja.Value {
			reasons := call.TextsOf(arguments.Argument(0), "the reasons")
			bins := figures.NewUnplacedBins(reasons...)
			if !script.Absent(arguments.Argument(1)) && arguments.Argument(1).ToBoolean() {
				bins = figures.NewListedUnplacedBins(reasons...)
			}
			object := call.VM.NewObject()
			call.Function(object, "addAddress", func(arguments goja.FunctionCall) goja.Value {
				reason := call.TextOf(arguments.Argument(0), "the reason")
				address := call.TextOf(arguments.Argument(1), "the address")
				bins.AddAddress(reason, address, values.FiguresOf(call, arguments.Argument(2), "the figures"))
				return goja.Undefined()
			})
			call.Function(object, "add", func(arguments goja.FunctionCall) goja.Value {
				reason := call.TextOf(arguments.Argument(0), "the reason")
				figures := values.FiguresOf(call, arguments.Argument(1), "the figures")
				bins.Add(reason, figures, call.IntegerOf(arguments.Argument(2), "the addresses"))
				return goja.Undefined()
			})
			call.Function(object, "list", func(goja.FunctionCall) goja.Value {
				return call.Value(bins.List(call.Request.VolumeOf))
			})
			call.Function(object, "total", func(goja.FunctionCall) goja.Value {
				return call.Value(traffic.Summable(bins.Total()))
			})
			return object
		})
		_ = area.Set("reasons", call.Value(map[string]string{
			"miss": figures.UnplacedMiss, "pending": figures.UnplacedPending, "no_row": figures.UnplacedNoRow,
			"no_coordinates": figures.UnplacedNoCoordinates, "east_west": figures.UnplacedEastWest,
			"this_firewall": figures.UnplacedThisFirewall, "other_endpoint": figures.UnplacedOtherEndpoint,
		}))
	})
}

// peerInterfaceFilterOf reads an interface filter: an id list, or null for no restriction.
func peerInterfaceFilterOf(c *script.ScriptCall, value goja.Value) map[int64]bool {
	if script.Absent(value) {
		return nil
	}
	selected := map[int64]bool{}
	for _, id := range c.IntegersOf(value, "the interface filter") {
		selected[id] = true
	}
	return selected
}

// peerGeoOf reads a geolocation a script hands back, as host.services.geolocation answered it.
func peerGeoOf(c *script.ScriptCall, value goja.Value) *request.GeoInfo {
	if script.Absent(value) {
		c.Throw("the geolocation is %s, not what host.services.geolocation answers", script.Describe(value))
	}
	var geo request.GeoInfo
	c.DecodeArgument(value, "the geolocation", &geo)
	return &geo
}
