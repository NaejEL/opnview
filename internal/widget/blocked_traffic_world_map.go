package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// blocked_traffic_world_map: where the traffic that was STOPPED was heading, or coming
// from, by the engine that stopped it -- the firewall, whose blocked records carry a real
// remote address, and the security engine, whose events carry a peer address
// (docs/widget-catalogue.md, "Blocked-traffic world map"). The DNS engines contribute
// nothing to a map, because a refused lookup resolves to no address: their refusals are a
// counter of their own beside it, so the map's total reconciles with the unified blocked
// feed's.

var blockedTrafficWorldMap = Preset{
	Type: "blocked_traffic_world_map", Shape: "map", Windowed: true,
	Providers: []string{"firewall_log", "security_event", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "engines", Kind: KindEnumSet, Values: []string{"firewall_rule", "security_engine"},
			Default: []any{"firewall_rule", "security_engine"}, Data: true},
		interfacesParam(),
		{Key: "endpoint", Kind: KindEnum, Values: []string{"destination", "source"}, Default: "destination",
			Data: true},
		{Key: "size_by", Kind: KindEnum, Values: []string{"connections", "bytes", "events"}, Default: "connections",
			Data: true},
		{Key: "min_count", Kind: KindInteger, Min: 0, Default: float64(0), Data: true},
		{Key: "show_unplaced", Kind: KindBoolean, Default: true},
		{Key: "cluster", Kind: KindBoolean, Default: true},
	},
	Serve: serveBlockedTrafficWorldMap,
}

// BlockedMapData is the map.
type BlockedMapData struct {
	MapData
	// SecurityEvents is the blocked security events, placed and unplaced.
	SecurityEvents Count `json:"security_events"`
	// SecurityUnplaced is the events whose peer has no point, by reason.
	SecurityUnplaced []UnplacedBin `json:"security_unplaced"`
	// DNSBlockedLookups is the DNS refusals of the window, which no map can show.
	DNSBlockedLookups Count `json:"dns_blocked_lookups"`
	// TotalRefusals is the firewall's blocked connections, the security engine's blocked
	// events and the DNS refusals together: the unified blocked feed's total.
	TotalRefusals Count `json:"total_refusals"`
}

// blockedOnly keeps the blocked half of figures.
func blockedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return store.VolumeFigures{Bytes: figures.BlockedBytes, BlockedBytes: figures.BlockedBytes,
		BlockedConnections: figures.BlockedConnections}
}

func serveBlockedTrafficWorldMap(r *Request) (any, bool, error) {
	window, err := r.volumeWindow()
	if err != nil {
		return nil, false, err
	}
	endpoint := r.params.text("endpoint")
	sizeBy := r.params.text("size_by")
	minimum := r.params.integer("min_count")
	selected := r.idSet("interfaces")
	data := BlockedMapData{MapData: MapData{SizeBy: sizeBy, Points: []MapPoint{}}}
	bins := newUnplacedBins(unplacedMiss, unplacedPending, unplacedNoRow, unplacedNoCoordinates,
		unplacedEastWest, unplacedOtherEndpoint)
	var total, below store.VolumeFigures
	hasRows := false
	wanted := map[string]bool{}
	if endpoint == "destination" {
		wanted[directionOutbound], wanted[directionFromThisFirewall] = true, true
	} else {
		wanted[directionInbound], wanted[directionToThisFirewall] = true, true
	}
	if r.params.has("engines", "firewall_rule") {
		peers := map[string]*peerFigures{}
		for _, row := range window.Rows {
			if row.ExitLegDevice != nil || !keepInterface(selected, row.VolumeKey) {
				continue
			}
			blocked := blockedOnly(row.VolumeFigures)
			if blocked.BlockedConnections == 0 {
				continue
			}
			hasRows = true
			switch {
			case row.PeerAddress == nil:
				bins.add(unplacedEastWest, blocked, 0)
				continue
			case !wanted[row.TrafficDirection]:
				bins.add(unplacedOtherEndpoint, blocked, 0)
				continue
			}
			peer := peers[*row.PeerAddress]
			if peer == nil {
				peer = &peerFigures{address: *row.PeerAddress, interfaces: map[int64]bool{}}
				peers[*row.PeerAddress] = peer
			}
			addFigures(&peer.figures, blocked)
			if local := localInterface(row.VolumeKey); local != nil {
				peer.interfaces[*local] = true
			}
		}
		for _, peer := range sortedPeers(peers) {
			geo, err := r.geolocation(peer.address)
			if err != nil {
				return nil, false, err
			}
			if reason := placement(geo); reason != "" {
				bins.add(reason, peer.figures, 1)
				continue
			}
			addFigures(&total, peer.figures)
			if peer.figures.BlockedConnections < minimum {
				data.BelowMinimum.Members++
				addFigures(&below, peer.figures)
				continue
			}
			volume := logged(peer.figures)
			size := float64(peer.figures.BlockedConnections)
			if sizeBy == "bytes" {
				size = float64(peer.figures.BlockedBytes)
			}
			data.Points = append(data.Points, MapPoint{Address: peer.address, Engine: "firewall_rule", Geo: *geo,
				Size: size, Volume: &volume, Interfaces: interfaceIDs(peer.interfaces)})
		}
	}
	belowVolume := logged(below)
	data.BelowMinimum.Volume = &belowVolume
	data.Unplaced = bins.list()
	addFigures(&total, bins.total())
	data.Total = logged(total)

	securityBins := newUnplacedBins(unplacedMiss, unplacedPending, unplacedNoRow, unplacedNoCoordinates)
	data.SecurityEvents = loggedCount(0)
	if r.params.has("engines", "security_engine") {
		parameters := r.windowParameters()
		parameters["endpoint"] = endpoint
		events := map[string]int64{}
		interfaces := map[string]map[int64]bool{}
		err := r.db.WidgetRows(r.ctx, "widget_security_blocked_peers", store.Period{}, parameters,
			func(row store.RowScanner) error {
				var (
					address string
					iface   store.NullInt64
					count   int64
				)
				if err := row.Scan(&address, &iface, &count); err != nil {
					return err
				}
				if selected != nil && (!iface.Valid || !selected[iface.Int64]) {
					return nil
				}
				events[address] += count
				if interfaces[address] == nil {
					interfaces[address] = map[int64]bool{}
				}
				if iface.Valid {
					interfaces[address][iface.Int64] = true
				}
				return nil
			})
		if err != nil {
			return nil, false, err
		}
		addresses := sortedKeys(events)
		sort.SliceStable(addresses, func(i, j int) bool { return events[addresses[i]] > events[addresses[j]] })
		for _, address := range addresses {
			hasRows = true
			count := events[address]
			data.SecurityEvents.Value += count
			geo, err := r.geolocation(address)
			if err != nil {
				return nil, false, err
			}
			if reason := placement(geo); reason != "" {
				securityBins.add(reason, store.VolumeFigures{BlockedConnections: count}, 1)
				continue
			}
			if count < minimum {
				data.BelowMinimum.Members++
				continue
			}
			eventCount := Count{Measure: "security_events", LowerBound: true, Value: count}
			data.Points = append(data.Points, MapPoint{Address: address, Engine: "security_engine", Geo: *geo,
				Size: float64(count), Events: &eventCount, Interfaces: interfaceIDs(interfaces[address])})
		}
	}
	data.SecurityUnplaced = securityBins.list()
	counts, err := r.db.ReadBlockedDecisionCounts(r.ctx, r.window.From, r.window.To)
	if err != nil {
		return nil, false, err
	}
	var dns int64
	for kind, count := range counts {
		if len(kind) > 4 && kind[:4] == "dns_" {
			dns += count
		}
	}
	data.DNSBlockedLookups = Count{Measure: "dns_lookups", LowerBound: true, Value: dns}
	refusals := dns + data.SecurityEvents.Value
	if r.params.has("engines", "firewall_rule") {
		refusals += data.Total.BlockedConnections
	}
	data.TotalRefusals = Count{Measure: "refusals", LowerBound: true, Value: refusals}
	return data, hasRows, nil
}
