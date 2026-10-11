package widget

import (
	"sort"
	"strconv"
)

// top_sites: inferred site names ranked by attributed flows, with the distinct clients
// that reached them, the logged bytes, the country and operator behind them, and the
// named flows per method (docs/widget-catalogue.md, "Top sites"). Withheld whole under
// aggregate_mode = no_domains (decision 6).

var topSites = Preset{
	Type: "top_sites", Shape: "table", Windowed: true,
	Providers: []string{"dns_lookup", "resolver_cache", "firewall_log", "geo_asn"}, Primary: "dns_lookup",
	WithheldWholeUnderNoDomains: true,
	Params: []Param{
		periodParam(),
		interfacesParam(),
		clientsParam(),
		limitParam(20),
		{Key: "min_flows", Kind: KindInteger, Min: 1, Default: float64(1), Data: true},
		{Key: "group_by_registrable_domain", Kind: KindBoolean, Default: false, Data: true},
	},
	Serve: serveTopSites,
}

// SiteRow is one site name, or one registrable domain grouping several.
type SiteRow struct {
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

// TopSitesData is the table.
type TopSitesData struct {
	// Grouping is site_name, or registrable_domain when the rows are grouped.
	Grouping  string    `json:"grouping"`
	Rows      []SiteRow `json:"rows"`
	Remainder Remainder `json:"remainder"`
	// BelowMinFlows is the names min_flows suppressed.
	BelowMinFlows Remainder `json:"below_min_flows"`
	Total         Remainder `json:"total"`
	// Attribution is how much of the window's outbound traffic could be named at all,
	// beside the list it qualifies.
	Attribution AttributionRow `json:"attribution"`
}

// siteAccumulator sums site rows into one table row.
type siteAccumulator struct {
	row      SiteRow
	clients  map[int64]bool
	delaySum int64
	timed    int64
	geoBytes map[string]int64
	geo      map[string][3]any
}

func newSiteAccumulator(name string) *siteAccumulator {
	return &siteAccumulator{row: SiteRow{Name: name, Flows: loggedCount(0), Bytes: loggedCount(0)},
		clients: map[int64]bool{}, geoBytes: map[string]int64{}, geo: map[string][3]any{}}
}

func (a *siteAccumulator) add(item siteRow) {
	a.row.Flows.Value += item.flows
	a.row.Bytes.Value += item.bytes
	a.row.Methods.add(item.method, item.flows)
	if item.clientID != nil {
		a.clients[*item.clientID] = true
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

func (a *siteAccumulator) finish() SiteRow {
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
	for _, key := range sortedKeys(a.geoBytes) {
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

func serveTopSites(r *Request) (any, bool, error) {
	rows, err := r.siteRows()
	if err != nil {
		return nil, false, err
	}
	data := TopSitesData{Grouping: "site_name", Rows: []SiteRow{}}
	nameOf := func(site string) string { return site }
	var grouped map[string]string
	if r.params.flag("group_by_registrable_domain") {
		names := map[string]bool{}
		for _, row := range rows {
			names[row.site] = true
		}
		mapping, available := r.registrableGrouping(r.lists, sortedKeys(names))
		if available {
			grouped = mapping
			data.Grouping = "registrable_domain"
			nameOf = func(site string) string { return grouped[site] }
		} else {
			r.state("registrable_domain_grouping_unavailable")
		}
	}
	accumulators := map[string]*siteAccumulator{}
	members := map[string]map[string]bool{}
	for _, row := range rows {
		name := nameOf(row.site)
		if accumulators[name] == nil {
			accumulators[name] = newSiteAccumulator(name)
			members[name] = map[string]bool{}
		}
		accumulators[name].add(row)
		members[name][row.site] = true
	}
	var list []SiteRow
	total := Remainder{Members: int64(len(accumulators))}
	totalFlows, totalBytes := loggedCount(0), loggedCount(0)
	for name, accumulator := range accumulators {
		row := accumulator.finish()
		if grouped != nil {
			row.SiteNames = sortedKeys(members[name])
		}
		totalFlows.Value += row.Flows.Value
		totalBytes.Value += row.Bytes.Value
		list = append(list, row)
	}
	total.Count, total.Volume = &totalFlows, nil
	data.Total = total
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Flows.Value != list[j].Flows.Value {
			return list[i].Flows.Value > list[j].Flows.Value
		}
		return list[i].Name < list[j].Name
	})
	minimum := r.params.integer("min_flows")
	limit := int(r.params.integer("limit"))
	below, rest := loggedCount(0), loggedCount(0)
	for _, row := range list {
		switch {
		case row.Flows.Value < minimum:
			data.BelowMinFlows.Members++
			below.Value += row.Flows.Value
		case len(data.Rows) < limit:
			data.Rows = append(data.Rows, row)
		default:
			data.Remainder.Members++
			rest.Value += row.Flows.Value
		}
	}
	data.BelowMinFlows.Count = &below
	data.Remainder.Count = &rest
	whole, err := r.db.ReadAttributionRate(r.ctx, r.window.From, r.window.To, nil)
	if err != nil {
		return nil, false, err
	}
	rate, state := rateOf(clientAttribution{eligible: whole.EligibleFlows, named: whole.NamedFlows},
		whole.ResolverCovered)
	data.Attribution = AttributionRow{EligibleFlows: loggedCount(whole.EligibleFlows),
		NamedFlows: loggedCount(whole.NamedFlows),
		Methods:    MethodCounts{ResolverCacheAnswer: whole.NamedByCacheAnswer, LookupTiming: whole.NamedByLookupTiming},
		Rate:       rate, RateState: state, MeanDelaySeconds: whole.MeanDelaySeconds, MaxDelaySeconds: whole.MaxDelaySeconds}
	return data, len(list) > 0, nil
}
