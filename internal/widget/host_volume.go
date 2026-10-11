package widget

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.volume: the rolling window of volume every volume widget reads (volume.go), in the measure the
// request is answered under (measure.go), assembled from the 1 h slots for the whole hours and from
// the flows at either edge; reading it records the window's coverage and the freshness of the slots
// read, as for every volume widget.
//
//	host.volume.window()                  {requested, covered, rows}: covered is null when the store
//	                                      holds none of the window; each row is {src_interface_id,
//	                                      dst_interface_id, peer_address, traffic_scope, traffic_direction,
//	                                      exit_leg_device} and its summable figures (host.figures)
//	host.volume.total(rows)               the summable figures of every row that counts a connection: every
//	                                      row but the second legs (exit_leg_device set), so a connection
//	                                      logged on two interfaces is counted once
//	host.volume.localInterface(row)       the inside interface a row belongs to, or null
//	host.volume.scopeMatches(scope, row)  whether a row is in a traffic scope ("all" holds every row)
//	host.volume.directions, host.volume.scopes  the traffic directions and scopes, by name

// volumeRow is one row of the rolling window as a script reads it.
type volumeRow struct {
	SrcInterfaceID   *int64  `json:"src_interface_id"`
	DstInterfaceID   *int64  `json:"dst_interface_id"`
	PeerAddress      *string `json:"peer_address"`
	TrafficScope     string  `json:"traffic_scope"`
	TrafficDirection string  `json:"traffic_direction"`
	ExitLegDevice    *string `json:"exit_leg_device"`
	summableFigures
}

// volumeWindow is the rolling window as a script reads it.
type volumeWindow struct {
	Requested Interval    `json:"requested"`
	Covered   *Interval   `json:"covered"`
	Rows      []volumeRow `json:"rows"`
}

func init() {
	registerHostArea("volume", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "window", func(goja.FunctionCall) goja.Value {
			if !r.Preset.Windowed {
				call.throw("volume.window reads a rolling window, and the widget %s is not windowed", r.Preset.Type)
			}
			window, err := r.VolumeWindow()
			if err != nil {
				return call.fail(err)
			}
			read := volumeWindow{Requested: Interval{From: window.Requested.From, To: window.Requested.To},
				Rows: make([]volumeRow, 0, len(window.Rows))}
			if window.Covered != nil {
				read.Covered = &Interval{From: window.Covered.From, To: window.Covered.To}
			}
			for _, row := range window.Rows {
				read.Rows = append(read.Rows, volumeRow{SrcInterfaceID: row.SrcInterfaceID,
					DstInterfaceID: row.DstInterfaceID, PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope,
					TrafficDirection: row.TrafficDirection, ExitLegDevice: row.ExitLegDevice,
					summableFigures: summable(row.VolumeFigures)})
			}
			return call.value(read)
		})
		call.function(area, "total", func(arguments goja.FunctionCall) goja.Value {
			var rows []volumeRow
			call.decodeArgument(arguments.Argument(0), "the rows", &rows)
			window := store.VolumeWindow{}
			for _, row := range rows {
				window.Rows = append(window.Rows, store.VolumeRow{VolumeKey: row.key(),
					VolumeFigures: call.fromScript(row.summableFigures)})
			}
			return call.value(summable(window.Total()))
		})
		call.function(area, "localInterface", func(arguments goja.FunctionCall) goja.Value {
			var row volumeRow
			call.decodeArgument(arguments.Argument(0), "the row", &row)
			local := LocalInterface(row.key())
			if local == nil {
				return goja.Null()
			}
			return call.vm.ToValue(*local)
		})
		call.function(area, "scopeMatches", func(arguments goja.FunctionCall) goja.Value {
			scope := call.textOf(arguments.Argument(0), "the scope")
			var row volumeRow
			call.decodeArgument(arguments.Argument(1), "the row", &row)
			return call.vm.ToValue(ScopeMatches(scope, row.key()))
		})
		_ = area.Set("directions", call.value(map[string]string{
			"outbound": DirectionOutbound, "inbound": DirectionInbound, "inter_interface": DirectionInterInterface,
			"to_this_firewall": DirectionToThisFirewall, "from_this_firewall": DirectionFromThisFirewall,
		}))
		_ = area.Set("scopes", call.value(map[string]string{
			"all": ScopeAll, "east_west": ScopeEastWest, "north_south": ScopeNorthSouth,
			"this_firewall": ScopeThisFirewall,
		}))
	})
}

// key is a row's key as the core reads it.
func (row volumeRow) key() store.VolumeKey {
	return store.VolumeKey{SrcInterfaceID: row.SrcInterfaceID, DstInterfaceID: row.DstInterfaceID,
		PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope, TrafficDirection: row.TrafficDirection,
		ExitLegDevice: row.ExitLegDevice}
}
