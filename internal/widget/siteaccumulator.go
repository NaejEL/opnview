package widget

import (
	"sort"
	"strconv"
)

// siteTableRow is one site name, or one registrable domain grouping several.
type siteTableRow struct {
	// Kind is DestinationKindSite: a table ranking site rows and address rows together says which
	// each row is.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// SiteNames are the names a registrable-domain row groups.
	SiteNames []string     `json:"site_names,omitempty"`
	Flows     Count        `json:"flows"`
	Bytes     Count        `json:"bytes"`
	Clients   int64        `json:"clients"`
	ClientIDs []int64      `json:"client_ids"`
	Methods   MethodCounts `json:"methods"`
	// MeanDelaySeconds and MaxDelaySeconds describe the correlation delays of the flows
	// named inside the flow horizon; null when none was.
	MeanDelaySeconds *float64 `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64   `json:"max_delay_seconds"`
	// The country and operator that carried most of the row's bytes, null when none of its
	// destinations was placed.
	CountryCode *string `json:"country_code"`
	ASN         *int64  `json:"asn"`
	Operator    *string `json:"operator"`
}

// SiteAccumulator sums site rows into one table row.
type SiteAccumulator struct {
	row      siteTableRow
	clients  map[int64]bool
	delaySum int64
	timed    int64
	geoBytes map[string]int64
	geo      map[string][3]any
}

func NewSiteAccumulator(name string) *SiteAccumulator {
	return &SiteAccumulator{row: siteTableRow{Kind: DestinationKindSite, Name: name, Flows: LoggedCount(0), Bytes: LoggedCount(0)},
		clients: map[int64]bool{}, geoBytes: map[string]int64{}, geo: map[string][3]any{}}
}

func (a *SiteAccumulator) Add(item siteRow) {
	a.row.Flows.Value += item.flows
	a.row.Bytes.Value += item.bytes
	a.row.Methods.add(item.method, item.flows)
	if item.ClientID != nil {
		a.clients[*item.ClientID] = true
	}
	if item.delaySum != nil {
		a.delaySum += *item.delaySum
		a.timed += item.flows
		if a.row.MaxDelaySeconds == nil || *item.delayMax > *a.row.MaxDelaySeconds {
			value := *item.delayMax
			a.row.MaxDelaySeconds = &value
		}
	}
	if item.country != nil || item.asn != nil {
		key := ""
		if item.country != nil {
			key += *item.country
		}
		key += "|"
		if item.asn != nil {
			key += strconv.FormatInt(*item.asn, 10)
		}
		a.geoBytes[key] += item.bytes
		a.geo[key] = [3]any{item.country, item.asn, item.operator}
	}
}

func (a *SiteAccumulator) Finish() siteTableRow {
	row := a.row
	row.ClientIDs = []int64{}
	for id := range a.clients {
		row.ClientIDs = append(row.ClientIDs, id)
	}
	sort.Slice(row.ClientIDs, func(i, j int) bool { return row.ClientIDs[i] < row.ClientIDs[j] })
	row.Clients = int64(len(row.ClientIDs))
	if a.timed > 0 {
		mean := float64(a.delaySum) / float64(a.timed)
		row.MeanDelaySeconds = &mean
	}
	best, bestBytes := "", int64(-1)
	for _, key := range SortedKeys(a.geoBytes) {
		if a.geoBytes[key] > bestBytes {
			best, bestBytes = key, a.geoBytes[key]
		}
	}
	if geo, present := a.geo[best]; present {
		row.CountryCode, _ = geo[0].(*string)
		row.ASN, _ = geo[1].(*int64)
		row.Operator, _ = geo[2].(*string)
	}
	return row
}
