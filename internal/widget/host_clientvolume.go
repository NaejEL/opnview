package widget

import (
	"sort"

	"github.com/dop251/goja"
)

// host.clients: every client's logged volume over the request's rolling window (clientvolume.go),
// assembled as the interface family's window is -- whole hours from the client and peer families'
// 1 h slots, the partial hours at either edge from the flows. A client's flows are those whose
// inside end it is; this firewall is never a client, and a second leg is its first leg's
// connection. Reading it records the window's coverage and the freshness of the slots read, as
// host.volume.window does.
//
//	host.clients.volumes()  {requested, covered, clients}: covered is the covered part of the window
//	                        as recorded, null when the store holds none of it; clients is one entry
//	                        per client with traffic in the window, by ascending id:
//	                        {client_id, figures, by_direction, destinations} -- figures its summable
//	                        figures (host.figures), by_direction the same per traffic direction (a
//	                        map: outbound, inbound, inter_interface), destinations the distinct far
//	                        ends it reached, counted over the hours and never summed

// clientVolumeEntry is one client's volume as a script reads it.
type clientVolumeEntry struct {
	ClientID     int64                      `json:"client_id"`
	Figures      summableFigures            `json:"figures"`
	ByDirection  map[string]summableFigures `json:"by_direction"`
	Destinations int64                      `json:"destinations"`
}

// clientVolumes is the window's client volumes as a script reads them.
type clientVolumes struct {
	Requested Interval            `json:"requested"`
	Covered   *Interval           `json:"covered"`
	Clients   []clientVolumeEntry `json:"clients"`
}

func init() {
	registerHostArea("clients", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "volumes", func(goja.FunctionCall) goja.Value {
			if !r.Preset.Windowed {
				call.throw("clients.volumes reads a rolling window, and the widget %s is not windowed", r.Preset.Type)
			}
			volumes, err := r.ClientVolumes()
			if err != nil {
				return call.fail(err)
			}
			read := clientVolumes{Requested: Interval{From: r.Window.From, To: r.Window.To},
				Clients: make([]clientVolumeEntry, 0, len(volumes))}
			if report := r.Response.Window; report != nil && report.Covered != nil {
				covered := *report.Covered
				read.Covered = &covered
			}
			for id, volume := range volumes {
				entry := clientVolumeEntry{ClientID: id, Figures: summable(volume.Figures),
					ByDirection: map[string]summableFigures{}, Destinations: int64(len(volume.Peers))}
				for direction, figures := range volume.ByDirection {
					entry.ByDirection[direction] = summable(figures)
				}
				read.Clients = append(read.Clients, entry)
			}
			sort.Slice(read.Clients, func(i, j int) bool { return read.Clients[i].ClientID < read.Clients[j].ClientID })
			return call.value(read)
		})
	})
}
