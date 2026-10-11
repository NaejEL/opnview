package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// traffic_sankey: who talks to whom at a glance -- interfaces or clients on the left;
// another interface, the destination operator or the country on the right
// (docs/widget-catalogue.md, "Traffic Sankey"). Ribbons past max_nodes or under min_share,
// and every ribbon whose right-hand name could not be resolved, fall into one explicit
// Other band, so the ribbons and the band make the total.

var trafficSankey = Preset{
	Type: "traffic_sankey", Shape: "sankey", Windowed: true,
	Providers: []string{"firewall_log", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "left", Kind: KindEnum, Values: []string{"interface", "client"}, Default: "interface", Data: true},
		{Key: "right", Kind: KindEnum, Values: []string{"interface", "operator", "country"}, Default: "operator",
			Data: true},
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth}, Default: scopeAll,
			Data: true},
		interfacesParam(),
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections"}, Default: "bytes", Data: true},
		{Key: "max_nodes", Kind: KindInteger, Min: 1, Default: float64(12), Data: true},
		{Key: "min_share", Kind: KindNumber, Min: 0, Max: 1, HasMax: true, Default: float64(0), Data: true},
		{Key: "show_blocked", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveTrafficSankey,
}

// Ribbon is one left-right pair.
type Ribbon struct {
	Left  string  `json:"left"`
	Right string  `json:"right"`
	Value float64 `json:"value"`
	Bytes Count   `json:"bytes"`
	// Connections is the logged records of the pair.
	Connections Count `json:"connections"`
	// BlockedShare is the share of the pair's connections that was blocked, when
	// show_blocked is set.
	BlockedShare *float64 `json:"blocked_share,omitempty"`
}

// SankeyNode names one end.
type SankeyNode struct {
	Key       string         `json:"key"`
	Interface *InterfaceInfo `json:"interface,omitempty"`
	Client    *ClientInfo    `json:"client,omitempty"`
	ASN       *int64         `json:"asn,omitempty"`
	Operator  *string        `json:"operator,omitempty"`
	Country   *string        `json:"country_code,omitempty"`
}

// OtherBand is the ribbons the Sankey did not draw, by why.
type OtherBand struct {
	Ribbons     int64 `json:"ribbons"`
	Bytes       Count `json:"bytes"`
	Connections Count `json:"connections"`
	// Unnamed is the part whose right-hand end could not be named, by reason.
	Unnamed []UnplacedBin `json:"unnamed"`
}

// SankeyData is the diagram.
type SankeyData struct {
	Left    string       `json:"left"`
	Right   string       `json:"right"`
	Measure string       `json:"measure"`
	Nodes   []SankeyNode `json:"nodes"`
	Ribbons []Ribbon     `json:"ribbons"`
	Other   OtherBand    `json:"other"`
	Total   Volume       `json:"total"`
}

// sankeyFlow is one grouped row of the left side.
type sankeyFlow struct {
	left       string
	direction  string
	src, dst   *int64
	peer       *string
	figures    store.VolumeFigures
	leftClient *int64
	leftIface  *int64
}

func serveTrafficSankey(r *Request) (any, bool, error) {
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	left, right := r.params.text("left"), r.params.text("right")
	scope := r.params.text("scope")
	selected := r.idSet("interfaces")
	var flows []sankeyFlow
	if left == "interface" {
		window, err := r.volumeWindow()
		if err != nil {
			return nil, false, err
		}
		for _, row := range window.Rows {
			if row.ExitLegDevice != nil || !scopeMatches(scope, row.VolumeKey) ||
				!keepInterface(selected, row.VolumeKey) {
				continue
			}
			local := localInterface(row.VolumeKey)
			key := "outside"
			if local != nil {
				key = interfaceKey(*local)
			}
			flows = append(flows, sankeyFlow{left: key, direction: row.TrafficDirection, src: row.SrcInterfaceID,
				dst: row.DstInterfaceID, peer: row.PeerAddress, figures: row.VolumeFigures, leftIface: local})
		}
	} else {
		if err := r.coverFlows("client_left_beyond_flow_horizon"); err != nil {
			return nil, false, err
		}
		err := r.db.WidgetRows(r.ctx, "widget_sankey_client_flows", store.Period{}, r.windowParameters(),
			func(row store.RowScanner) error {
				var (
					clientID, connections, blockedConnections, bytes, blockedBytes int64
					direction                                                      string
					src, dst                                                       store.NullInt64
					peer                                                           store.NullString
				)
				if err := row.Scan(&clientID, &direction, &src, &dst, &peer, &bytes, &blockedBytes, &connections,
					&blockedConnections); err != nil {
					return err
				}
				key := store.VolumeKey{SrcInterfaceID: nullableNumber(src), DstInterfaceID: nullableNumber(dst),
					TrafficDirection: direction}
				key.TrafficScope = scopeOf(key)
				if !scopeMatches(scope, key) || !keepInterface(selected, key) {
					return nil
				}
				id := clientID
				flows = append(flows, sankeyFlow{left: clientKey(clientID), direction: direction, src: key.SrcInterfaceID,
					dst: key.DstInterfaceID, peer: nullableText(peer), leftClient: &id,
					figures: store.VolumeFigures{Bytes: bytes, BlockedBytes: blockedBytes,
						AllowedBytes: bytes - blockedBytes, BlockedConnections: blockedConnections,
						AllowedConnections: connections - blockedConnections}})
				return nil
			})
		if err != nil {
			return nil, false, err
		}
	}

	measure := r.params.text("measure")
	unnamed := newUnplacedBins(unplacedMiss, unplacedPending, unplacedNoRow)
	type pair struct {
		ribbon  Ribbon
		figures store.VolumeFigures
	}
	pairs := map[[2]string]*pair{}
	nodes := map[string]SankeyNode{}
	var total, other store.VolumeFigures
	for _, flow := range flows {
		addFigures(&total, flow.figures)
		rightKey := ""
		switch {
		case flow.direction == directionToThisFirewall || flow.direction == directionFromThisFirewall:
			rightKey = matrixThisFirewall
		case flow.peer == nil:
			far := flow.dst
			if flow.leftIface != nil && flow.dst != nil && *flow.leftIface == *flow.dst {
				far = flow.src
			}
			if right == "interface" && far != nil {
				rightKey = interfaceKey(*far)
				if info, known := cache.byID[*far]; known {
					copied := info
					nodes[rightKey] = SankeyNode{Key: rightKey, Interface: &copied}
				}
			} else {
				rightKey = "inter_interface"
			}
		case right == "interface":
			rightKey = "outside"
		default:
			geo, err := r.geolocation(*flow.peer)
			if err != nil {
				return nil, false, err
			}
			if geo.LookupState != "resolved" {
				unnamed.add(placementState(geo), flow.figures, 1)
				addFigures(&other, flow.figures)
				continue
			}
			if right == "country" {
				code := "none"
				if geo.CountryCode != nil {
					code = *geo.CountryCode
				}
				rightKey = "country:" + code
				nodes[rightKey] = SankeyNode{Key: rightKey, Country: geo.CountryCode}
			} else {
				number := "none"
				if geo.ASN != nil {
					number = strconv.FormatInt(*geo.ASN, 10)
				}
				rightKey = "operator:" + number
				nodes[rightKey] = SankeyNode{Key: rightKey, ASN: geo.ASN, Operator: geo.Operator}
			}
		}
		key := [2]string{flow.left, rightKey}
		entry := pairs[key]
		if entry == nil {
			entry = &pair{ribbon: Ribbon{Left: flow.left, Right: rightKey}}
			pairs[key] = entry
		}
		addFigures(&entry.figures, flow.figures)
		if _, named := nodes[flow.left]; !named {
			node := SankeyNode{Key: flow.left}
			if flow.leftIface != nil {
				if info, known := cache.byID[*flow.leftIface]; known && left == "interface" {
					copied := info
					node.Interface = &copied
				}
			}
			if flow.leftClient != nil {
				info, err := r.client(*flow.leftClient)
				if err != nil {
					return nil, false, err
				}
				node.Client = info
			}
			nodes[flow.left] = node
		}
	}
	var ribbons []*pair
	for _, entry := range pairs {
		entry.ribbon.Value = figuresValue(measure, entry.figures)
		entry.ribbon.Bytes = loggedCount(entry.figures.Bytes)
		entry.ribbon.Connections = loggedCount(entry.figures.Connections())
		if r.params.flag("show_blocked") && entry.figures.Connections() > 0 {
			share := float64(entry.figures.BlockedConnections) / float64(entry.figures.Connections())
			entry.ribbon.BlockedShare = &share
		}
		ribbons = append(ribbons, entry)
	}
	sort.SliceStable(ribbons, func(i, j int) bool {
		if ribbons[i].ribbon.Value != ribbons[j].ribbon.Value {
			return ribbons[i].ribbon.Value > ribbons[j].ribbon.Value
		}
		if ribbons[i].ribbon.Left != ribbons[j].ribbon.Left {
			return ribbons[i].ribbon.Left < ribbons[j].ribbon.Left
		}
		return ribbons[i].ribbon.Right < ribbons[j].ribbon.Right
	})
	whole := figuresValue(measure, total)
	data := SankeyData{Left: left, Right: right, Measure: measure, Ribbons: []Ribbon{}, Nodes: []SankeyNode{}}
	maxNodes := int(r.params.integer("max_nodes"))
	minShare := r.params.number("min_share")
	drawn := map[string]bool{}
	for _, entry := range ribbons {
		share := 0.0
		if whole > 0 {
			share = entry.ribbon.Value / whole
		}
		if len(data.Ribbons) >= maxNodes || share < minShare {
			data.Other.Ribbons++
			addFigures(&other, entry.figures)
			continue
		}
		data.Ribbons = append(data.Ribbons, entry.ribbon)
		drawn[entry.ribbon.Left], drawn[entry.ribbon.Right] = true, true
	}
	for _, key := range sortedKeys(nodes) {
		if drawn[key] {
			data.Nodes = append(data.Nodes, nodes[key])
		}
	}
	for _, key := range []string{"outside", "inter_interface", matrixThisFirewall} {
		if drawn[key] {
			data.Nodes = append(data.Nodes, SankeyNode{Key: key})
		}
	}
	data.Other.Bytes = loggedCount(other.Bytes)
	data.Other.Connections = loggedCount(other.Connections())
	data.Other.Unnamed = unnamed.list()
	data.Total = logged(total)
	return data, len(flows) > 0, nil
}

// scopeOf derives a row's scope from its interfaces and direction, as the schema pins it.
func scopeOf(key store.VolumeKey) string {
	switch {
	case key.TrafficDirection == directionToThisFirewall || key.TrafficDirection == directionFromThisFirewall:
		return scopeThisFirewall
	case key.SrcInterfaceID != nil && key.DstInterfaceID != nil:
		return scopeEastWest
	}
	return scopeNorthSouth
}
