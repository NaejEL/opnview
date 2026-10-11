package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// Per-client volumes over a rolling window, assembled the way ReadVolumeWindow assembles
// the interface family: whole hours from the client and peer families' 1 h slots, the
// partial hours at either edge from the flows. A client's flows are those whose inside end
// it is; this firewall is never a client, and a second leg is its first leg's connection.

// clientVolume is one client's figures and distinct peers over a window.
type clientVolume struct {
	Figures     store.VolumeFigures
	ByDirection map[string]store.VolumeFigures
	Peers       map[string]bool
}

// ClientVolumes reads every client's figures over the request's window, and records the
// window's coverage.
func (r *Request) ClientVolumes() (map[int64]*clientVolume, error) {
	from, to := r.Window.From, r.Window.To
	firstHour := store.PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = store.PeriodHour.SlotEnd(firstHour)
	}
	lastHour := store.PeriodHour.SlotStart(to)
	result := map[int64]*clientVolume{}
	member := func(id int64) *clientVolume {
		if result[id] == nil {
			result[id] = &clientVolume{ByDirection: map[string]store.VolumeFigures{}, Peers: map[string]bool{}}
		}
		return result[id]
	}
	figuresOf := func(name string, from, to int64) error {
		if to <= from {
			return nil
		}
		return r.Db.WidgetRows(r.Ctx, name, store.Period{}, map[string]any{"from": from, "to": to},
			func(row store.RowScanner) error {
				var (
					id        int64
					direction string
					figures   store.VolumeFigures
				)
				if err := row.Scan(&id, &direction, &figures.Bytes, &figures.AllowedBytes, &figures.BlockedBytes,
					&figures.UnknownBytes, &figures.AllowedConnections, &figures.BlockedConnections,
					&figures.UnknownConnections); err != nil {
					return err
				}
				volume := member(id)
				AddFigures(&volume.Figures, figures)
				sum := volume.ByDirection[direction]
				AddFigures(&sum, figures)
				volume.ByDirection[direction] = sum
				return nil
			})
	}
	peersOf := func(name string, from, to int64) error {
		if to <= from {
			return nil
		}
		return r.Db.WidgetRows(r.Ctx, name, store.Period{}, map[string]any{"from": from, "to": to},
			func(row store.RowScanner) error {
				var (
					id   int64
					peer string
				)
				if err := row.Scan(&id, &peer); err != nil {
					return err
				}
				member(id).Peers[peer] = true
				return nil
			})
	}
	type part struct {
		figures, peers string
		from, to       int64
	}
	parts := []part{{"widget_client_volume_flows", "widget_client_peer_flows", from, to}}
	if firstHour < lastHour {
		parts = []part{
			{"widget_client_volume_flows", "widget_client_peer_flows", from, firstHour},
			{"widget_client_volume_slots", "widget_client_peer_slots", firstHour, lastHour},
			{"widget_client_volume_flows", "widget_client_peer_flows", lastHour, to},
		}
	}
	for _, p := range parts {
		if err := figuresOf(p.figures, p.from, p.to); err != nil {
			return nil, err
		}
		if err := peersOf(p.peers, p.from, p.to); err != nil {
			return nil, err
		}
	}
	// The window's coverage is the interface family's: the same flows and the same hours
	// feed both, by the refresh contract.
	if err := r.coverAssembled(); err != nil {
		return nil, err
	}
	return result, r.HourFreshness(from, to)
}

// coverAssembled records the coverage of a window assembled from the 1 h slots and the
// flows: from the earliest flow or slot kept, whichever is older, to now.
func (r *Request) coverAssembled() error {
	earliestFlow, hasFlow, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return err
	}
	var slot store.NullInt64
	if _, err := r.Db.WidgetRow(r.Ctx, "widget_earliest_hour_slot", nil, &slot); err != nil {
		return err
	}
	switch {
	case hasFlow && slot.Valid:
		r.CoverFrom(min(earliestFlow, slot.Int64), true)
	case hasFlow:
		r.CoverFrom(earliestFlow, true)
	case slot.Valid:
		r.CoverFrom(slot.Int64, true)
	default:
		r.Cover(nil)
	}
	return nil
}
