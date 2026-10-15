package shapes

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// MapPoint is one placed address.
type MapPoint struct {
	Address string `json:"address"`
	// Engine is the engine whose records placed the point: firewall_rule or
	// security_engine.
	Engine     string           `json:"engine"`
	Geo        request.GeoInfo  `json:"geo"`
	Size       float64          `json:"size"`
	Volume     *envelope.Volume `json:"volume,omitempty"`
	Events     *envelope.Count  `json:"events,omitempty"`
	Interfaces []int64          `json:"interfaces"`
}

// MapData is the map.
type MapData struct {
	SizeBy string     `json:"size_by"`
	Points []MapPoint `json:"points"`
	// BelowMinimum is the placed addresses min_bytes or min_count left off the map.
	BelowMinimum envelope.Remainder `json:"below_minimum"`
	// Unplaced is every reason a part of the total has no point, each present at zero.
	Unplaced []figures.UnplacedBin `json:"unplaced"`
	Total    envelope.Volume       `json:"total"`
}

// AllowedOnly keeps the allowed part of figures, in their measure, with its own packets and coverage
// (store.VolumeFigures.AllowedPart).
func AllowedOnly(figures reads.VolumeFigures) reads.VolumeFigures {
	return figures.AllowedPart()
}
