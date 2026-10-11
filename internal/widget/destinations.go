package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// destination_countries and destination_operators: the outside peers of the window grouped
// by the country, or the autonomous system and operator, the geolocation placed them in,
// with the logged bytes, the allowed and blocked connections, the distinct peers, the
// contributing interfaces and the build of the dataset that answered. An address whose
// lookup is a miss, pending or was never made is carried in an explicit unresolved row.
// Neither reads a table holding a domain name, so both are served unchanged under
// aggregate_mode = no_domains.

var destinationCountries = Preset{
	Type: "destination_countries", Shape: "table", Windowed: true,
	Providers: []string{"firewall_log", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		limitParam(20),
		{Key: "series", Kind: KindEnumSet, Values: []string{"allowed", "blocked"},
			Default: []any{"allowed", "blocked"}, Data: true},
		{Key: "sort", Kind: KindEnum, Values: []string{"bytes", "blocked", "peers"}, Default: "bytes", Data: true},
	},
	Serve: func(r *Request) (any, bool, error) { return r.serveDestinations("country") },
}

var destinationOperators = Preset{
	Type: "destination_operators", Shape: "table", Windowed: true,
	Providers: []string{"firewall_log", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		limitParam(20),
		{Key: "series", Kind: KindEnumSet, Values: []string{"allowed", "blocked"},
			Default: []any{"allowed", "blocked"}, Data: true},
		{Key: "group", Kind: KindEnum, Values: []string{"asn", "operator"}, Default: "asn", Data: true},
	},
	Serve: func(r *Request) (any, bool, error) {
		return r.serveDestinations(r.params.text("group"))
	},
}

// DestinationRow is one country, AS number or operator.
type DestinationRow struct {
	Key         string  `json:"key"`
	CountryCode *string `json:"country_code,omitempty"`
	CountryName *string `json:"country_name,omitempty"`
	ASN         *int64  `json:"asn,omitempty"`
	Operator    *string `json:"operator,omitempty"`
	// Bytes is every logged byte to and from the row's peers.
	Bytes Count `json:"bytes"`
	// Allowed and Blocked are present when `series` selects them.
	Allowed *Volume `json:"allowed,omitempty"`
	Blocked *Volume `json:"blocked,omitempty"`
	// Peers is the distinct outside addresses, counted.
	Peers          int64   `json:"peers"`
	Interfaces     []int64 `json:"interfaces"`
	DatasetBuildAt *int64  `json:"dataset_build_at"`
}

// DestinationsData is the table.
type DestinationsData struct {
	Group     string           `json:"group"`
	Rows      []DestinationRow `json:"rows"`
	Remainder Remainder        `json:"remainder"`
	// Unresolved is the addresses the geolocation did not place, by reason, each present
	// even at zero.
	Unresolved []UnplacedBin `json:"unresolved"`
	Total      Volume        `json:"total"`
}

// serveDestinations groups the outside peers by country, AS number or operator.
func (r *Request) serveDestinations(group string) (any, bool, error) {
	window, err := r.volumeWindow()
	if err != nil {
		return nil, false, err
	}
	peers := outsidePeers(window, scopeAll, r.idSet("interfaces"), nil)
	bins := newUnplacedBins(unplacedMiss, unplacedPending, unplacedNoRow)
	type groupFigures struct {
		row        DestinationRow
		figures    store.VolumeFigures
		interfaces map[int64]bool
	}
	groups := map[string]*groupFigures{}
	var total store.VolumeFigures
	for _, peer := range sortedPeers(peers) {
		addFigures(&total, peer.figures)
		geo, err := r.geolocation(peer.address)
		if err != nil {
			return nil, false, err
		}
		if geo.LookupState != "resolved" {
			bins.add(placementState(geo), peer.figures, 1)
			continue
		}
		key, row := destinationKey(group, geo)
		entry := groups[key]
		if entry == nil {
			entry = &groupFigures{row: row, interfaces: map[int64]bool{}}
			groups[key] = entry
		}
		addFigures(&entry.figures, peer.figures)
		entry.row.Peers++
		for id := range peer.interfaces {
			entry.interfaces[id] = true
		}
		if geo.DatasetBuildAt != nil && (entry.row.DatasetBuildAt == nil || *geo.DatasetBuildAt > *entry.row.DatasetBuildAt) {
			build := *geo.DatasetBuildAt
			entry.row.DatasetBuildAt = &build
		}
	}
	allowed, blocked := r.params.has("series", "allowed"), r.params.has("series", "blocked")
	var rows []DestinationRow
	for _, entry := range groups {
		row := entry.row
		row.Bytes = loggedCount(entry.figures.Bytes)
		row.Interfaces = interfaceIDs(entry.interfaces)
		if allowed {
			volume := logged(allowedOnly(entry.figures))
			row.Allowed = &volume
		}
		if blocked {
			volume := logged(blockedOnly(entry.figures))
			row.Blocked = &volume
		}
		rows = append(rows, row)
	}
	order := "bytes"
	if r.preset.Type == "destination_countries" {
		order = r.params.text("sort")
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch order {
		case "blocked":
			if blockedConnections(a) != blockedConnections(b) {
				return blockedConnections(a) > blockedConnections(b)
			}
		case "peers":
			if a.Peers != b.Peers {
				return a.Peers > b.Peers
			}
		default:
			if a.Bytes.Value != b.Bytes.Value {
				return a.Bytes.Value > b.Bytes.Value
			}
		}
		return a.Key < b.Key
	})
	data := DestinationsData{Group: group, Rows: []DestinationRow{}, Unresolved: bins.list(), Total: logged(total)}
	limit := int(r.params.integer("limit"))
	var rest store.VolumeFigures
	for index, row := range rows {
		if index < limit {
			data.Rows = append(data.Rows, row)
			continue
		}
		data.Remainder.Members++
		addFigures(&rest, groups[row.Key].figures)
	}
	restVolume := logged(rest)
	data.Remainder.Volume = &restVolume
	return data, len(peers) > 0, nil
}

// blockedConnections is a row's blocked connections, 0 when the blocked series is off.
func blockedConnections(row DestinationRow) int64 {
	if row.Blocked == nil {
		return 0
	}
	return row.Blocked.BlockedConnections
}

// placementState is the unresolved bin of an address the geolocation did not resolve.
func placementState(geo *GeoInfo) string {
	switch geo.LookupState {
	case "miss":
		return unplacedMiss
	case "pending":
		return unplacedPending
	}
	return unplacedNoRow
}

// destinationKey is the group key and the named fields of a resolved address.
func destinationKey(group string, geo *GeoInfo) (string, DestinationRow) {
	switch group {
	case "asn":
		key := "asn:none"
		if geo.ASN != nil {
			key = "asn:" + strconv.FormatInt(*geo.ASN, 10)
		}
		return key, DestinationRow{Key: key, ASN: geo.ASN, Operator: geo.Operator}
	case "operator":
		key := "operator:none"
		if geo.Operator != nil {
			key = "operator:" + *geo.Operator
		}
		return key, DestinationRow{Key: key, Operator: geo.Operator}
	}
	key := "country:none"
	if geo.CountryCode != nil {
		key = "country:" + *geo.CountryCode
	}
	return key, DestinationRow{Key: key, CountryCode: geo.CountryCode, CountryName: geo.CountryName}
}
