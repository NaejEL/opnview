package traffic

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/hostareas/values"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.volume: the rolling window of volume every volume widget reads
// (internal/widget/traffic/volume.go), in the measure the request is answered under
// (internal/widget/request/measure.go), assembled from the 1 h slots for the whole hours and from
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
	traffic.SummableFigures
}

// volumeWindow is the rolling window as a script reads it.
type volumeWindow struct {
	Requested envelope.Interval  `json:"requested"`
	Covered   *envelope.Interval `json:"covered"`
	Rows      []volumeRow        `json:"rows"`
}

func init() {
	script.RegisterHostArea("volume", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "window", func(goja.FunctionCall) goja.Value {
			if !r.Preset.Windowed {
				call.Throw("volume.window reads a rolling window, and the widget %s is not windowed", r.Preset.Type)
			}
			window, err := traffic.VolumeWindow(r)
			if err != nil {
				return call.Fail(err)
			}
			read := volumeWindow{Requested: envelope.Interval{From: window.Requested.From, To: window.Requested.To},
				Rows: make([]volumeRow, 0, len(window.Rows))}
			if window.Covered != nil {
				read.Covered = &envelope.Interval{From: window.Covered.From, To: window.Covered.To}
			}
			for _, row := range window.Rows {
				read.Rows = append(read.Rows, volumeRow{SrcInterfaceID: row.SrcInterfaceID,
					DstInterfaceID: row.DstInterfaceID, PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope,
					TrafficDirection: row.TrafficDirection, ExitLegDevice: row.ExitLegDevice,
					SummableFigures: traffic.Summable(row.VolumeFigures)})
			}
			return call.Value(read)
		})
		call.Function(area, "total", func(arguments goja.FunctionCall) goja.Value {
			var rows []volumeRow
			call.DecodeFlat(arguments.Argument(0), "the rows", &rows)
			window := reads.VolumeWindow{}
			for _, row := range rows {
				window.Rows = append(window.Rows, reads.VolumeRow{VolumeKey: row.key(),
					VolumeFigures: values.FromScript(call, row.SummableFigures)})
			}
			return call.Value(traffic.Summable(window.Total()))
		})
		call.Function(area, "localInterface", func(arguments goja.FunctionCall) goja.Value {
			var row volumeRow
			call.DecodeFlat(arguments.Argument(0), "the row", &row)
			local := traffic.LocalInterface(row.key())
			if local == nil {
				return goja.Null()
			}
			return call.VM.ToValue(*local)
		})
		call.Function(area, "scopeMatches", func(arguments goja.FunctionCall) goja.Value {
			scope := call.TextOf(arguments.Argument(0), "the scope")
			var row volumeRow
			call.DecodeFlat(arguments.Argument(1), "the row", &row)
			return call.VM.ToValue(traffic.ScopeMatches(scope, row.key()))
		})
		_ = area.Set("directions", call.Value(map[string]string{
			"outbound": traffic.DirectionOutbound, "inbound": traffic.DirectionInbound, "inter_interface": traffic.DirectionInterInterface,
			"to_this_firewall": traffic.DirectionToThisFirewall, "from_this_firewall": traffic.DirectionFromThisFirewall,
		}))
		_ = area.Set("scopes", call.Value(map[string]string{
			"all": traffic.ScopeAll, "east_west": traffic.ScopeEastWest, "north_south": traffic.ScopeNorthSouth,
			"this_firewall": traffic.ScopeThisFirewall,
		}))
	})
}

// key is a row's key as the core reads it.
func (row volumeRow) key() reads.VolumeKey {
	return reads.VolumeKey{SrcInterfaceID: row.SrcInterfaceID, DstInterfaceID: row.DstInterfaceID,
		PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope, TrafficDirection: row.TrafficDirection,
		ExitLegDevice: row.ExitLegDevice}
}
