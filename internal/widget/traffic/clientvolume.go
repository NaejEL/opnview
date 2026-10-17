package traffic

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// Per-client volumes over a rolling window, assembled the way ReadVolumeWindow assembles
// the interface family: whole hours from the client and peer families' 1 h slots, the
// partial hours at either edge from the flows. A client's flows are those whose inside end
// it is; this firewall is never a client, and a second leg is its first leg's connection.

// clientVolume is one client's figures and distinct peers over a window.
type clientVolume struct {
	Figures     reads.VolumeFigures
	ByDirection map[string]reads.VolumeFigures
	Peers       map[string]bool
}

// ClientVolumes reads every client's figures over the request's window, in the measure it is answered
// under (internal/widget/request/measure.go), and records the window's coverage.
func ClientVolumes(r *request.Request) (map[int64]*clientVolume, error) {
	from, to := r.Window.From, r.Window.To
	firstHour := vocab.PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = vocab.PeriodHour.SlotEnd(firstHour)
	}
	lastHour := vocab.PeriodHour.SlotStart(to)
	result := map[int64]*clientVolume{}
	member := func(id int64) *clientVolume {
		if result[id] == nil {
			result[id] = &clientVolume{ByDirection: map[string]reads.VolumeFigures{}, Peers: map[string]bool{}}
		}
		return result[id]
	}
	figuresOf := func(name string, from, to int64) error {
		if to <= from {
			return nil
		}
		return r.Db.WidgetRows(r.Ctx, r.InMeasure(name), vocab.Period{}, r.WithMeasure(map[string]any{"from": from, "to": to}),
			func(row reads.RowScanner) error {
				var (
					id        int64
					direction string
					figures   reads.VolumeFigures
				)
				targets := []any{&id, &direction, &figures.Bytes, &figures.AllowedBytes, &figures.BlockedBytes,
					&figures.UnknownBytes, &figures.AllowedConnections, &figures.BlockedConnections,
					&figures.UnknownConnections}
				if measure := r.MeasureRead(); measure != "" {
					targets = append(targets, figures.MeasureTarget(measure))
				}
				if err := row.Scan(targets...); err != nil {
					return err
				}
				volume := member(id)
				envelope.AddFigures(&volume.Figures, figures)
				sum := volume.ByDirection[direction]
				envelope.AddFigures(&sum, figures)
				volume.ByDirection[direction] = sum
				return nil
			})
	}
	peersOf := func(name string, from, to int64) error {
		if to <= from {
			return nil
		}
		return r.Db.WidgetRows(r.Ctx, name, vocab.Period{}, map[string]any{"from": from, "to": to},
			func(row reads.RowScanner) error {
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
	if err := coverAssembled(r); err != nil {
		return nil, err
	}
	return result, r.HourFreshness(from, to)
}

// coverAssembled records the coverage of a window assembled from the 1 h slots and the
// flows: from the earliest flow or slot kept, whichever is older, to now.
func coverAssembled(r *request.Request) error {
	earliestFlow, hasFlow, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return err
	}
	var slot reads.NullInt64
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
