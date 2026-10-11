package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// passed_traffic_world_map: where the ALLOWED traffic went, as point marks sized by a
// measure, with the mandatory unplaced counter beside them (docs/widget-catalogue.md,
// "Passed-traffic world map"). Placed volume plus unplaced volume plus the points below
// min_bytes is the window's allowed total for the scope; the unplaced counter is present
// even at zero and is split by reason.

var passedTrafficWorldMap = Preset{
	Type: "passed_traffic_world_map", Shape: "map", Windowed: true,
	Providers: []string{"firewall_log", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		{Key: "scope", Kind: KindEnum, Values: []string{scopeNorthSouth, scopeAll}, Default: scopeNorthSouth,
			Data: true},
		{Key: "size_by", Kind: KindEnum, Values: []string{"bytes", "connections"}, Default: "bytes", Data: true},
		{Key: "min_bytes", Kind: KindInteger, Min: 0, Default: float64(0), Data: true},
		{Key: "show_unplaced", Kind: KindBoolean, Default: true},
		{Key: "cluster", Kind: KindBoolean, Default: true},
	},
	Serve: servePassedTrafficWorldMap,
}

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

// allowedOnly keeps the allowed half of figures.
func allowedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return store.VolumeFigures{Bytes: figures.AllowedBytes, AllowedBytes: figures.AllowedBytes,
		AllowedConnections: figures.AllowedConnections}
}

func servePassedTrafficWorldMap(r *Request) (any, bool, error) {
	window, err := r.volumeWindow()
	if err != nil {
		return nil, false, err
	}
	scope := r.params.text("scope")
	bins := newUnplacedBins(unplacedMiss, unplacedPending, unplacedNoRow, unplacedNoCoordinates)
	if scope == scopeAll {
		bins.add(unplacedEastWest, store.VolumeFigures{}, 0)
		bins.add(unplacedThisFirewall, store.VolumeFigures{}, 0)
	}
	peers := outsidePeers(window, scope, r.idSet("interfaces"), func(row store.VolumeRow) {
		reason := unplacedEastWest
		if row.TrafficScope == scopeThisFirewall {
			reason = unplacedThisFirewall
		}
		bins.add(reason, allowedOnly(row.VolumeFigures), 0)
	})
	data := MapData{SizeBy: r.params.text("size_by"), Points: []MapPoint{}}
	var total, below store.VolumeFigures
	minimum := r.params.integer("min_bytes")
	hasRows := false
	for _, peer := range sortedPeers(peers) {
		allowed := allowedOnly(peer.figures)
		if allowed.AllowedConnections == 0 && allowed.Bytes == 0 {
			continue
		}
		hasRows = true
		geo, err := r.geolocation(peer.address)
		if err != nil {
			return nil, false, err
		}
		if reason := placement(geo); reason != "" {
			bins.add(reason, allowed, 1)
			continue
		}
		addFigures(&total, allowed)
		if allowed.Bytes < minimum {
			data.BelowMinimum.Members++
			addFigures(&below, allowed)
			continue
		}
		volume := logged(allowed)
		size := float64(allowed.Bytes)
		if data.SizeBy == "connections" {
			size = float64(allowed.AllowedConnections)
		}
		data.Points = append(data.Points, MapPoint{Address: peer.address, Engine: "firewall_rule", Geo: *geo,
			Size: size, Volume: &volume, Interfaces: interfaceIDs(peer.interfaces)})
	}
	belowVolume := logged(below)
	data.BelowMinimum.Volume = &belowVolume
	data.Unplaced = bins.list()
	addFigures(&total, bins.total())
	data.Total = logged(total)
	if bins.total().Bytes > 0 || bins.total().AllowedConnections > 0 {
		hasRows = true
	}
	return data, hasRows, nil
}
