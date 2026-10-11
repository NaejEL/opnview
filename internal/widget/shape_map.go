package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// MapPoint is one placed address.
type MapPoint struct {
	Address string `json:"address"`
	// Engine is the engine whose records placed the point: firewall_rule or
	// security_engine.
	Engine     string  `json:"engine"`
	Geo        GeoInfo `json:"geo"`
	Size       float64 `json:"size"`
	Volume     *Volume `json:"volume,omitempty"`
	Events     *Count  `json:"events,omitempty"`
	Interfaces []int64 `json:"interfaces"`
}

// MapData is the map.
type MapData struct {
	SizeBy string     `json:"size_by"`
	Points []MapPoint `json:"points"`
	// BelowMinimum is the placed addresses min_bytes or min_count left off the map.
	BelowMinimum Remainder `json:"below_minimum"`
	// Unplaced is every reason a part of the total has no point, each present at zero.
	Unplaced []UnplacedBin `json:"unplaced"`
	Total    Volume        `json:"total"`
}

// AllowedOnly keeps the allowed half of figures.
func AllowedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return store.VolumeFigures{Bytes: figures.AllowedBytes, AllowedBytes: figures.AllowedBytes,
		AllowedConnections: figures.AllowedConnections}
}
