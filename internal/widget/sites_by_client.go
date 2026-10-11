package widget

import (
	"sort"
)

// sites_by_client: clients down the side, their top inferred site names across, each cell
// carrying the flow count, the logged bytes and the named flows per method; a client with
// no attributed flow keeps its row and says no site name could be inferred, with its
// attribution rate beside it (docs/widget-catalogue.md, "Sites by client"). Withheld whole
// under aggregate_mode = no_domains (decision 6).

var sitesByClient = Preset{
	Type: "sites_by_client", Shape: "matrix", Windowed: true,
	Providers: []string{"dns_lookup", "resolver_cache", "firewall_log", "dhcp_lease"}, Primary: "dns_lookup",
	WithheldWholeUnderNoDomains: true,
	Params: []Param{
		periodParam(),
		interfacesParam(),
		clientsParam(),
		{Key: "sites_per_client", Kind: KindInteger, Min: 1, Default: float64(5), Data: true},
		{Key: "min_flows", Kind: KindInteger, Min: 1, Default: float64(1), Data: true},
		{Key: "show_attribution_rate", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveSitesByClient,
}

// ClientSites is one client's row.
type ClientSites struct {
	Client ClientInfo `json:"client"`
	// State is named, or no_site_name_inferred for a client none of whose flows could be
	// named.
	State         string    `json:"state"`
	Sites         []SiteRow `json:"sites"`
	Remainder     Remainder `json:"remainder"`
	BelowMinFlows Remainder `json:"below_min_flows"`
	// Attribution is the client's rate, when show_attribution_rate is set.
	Attribution *AttributionRow `json:"attribution,omitempty"`
}

// SitesByClientData is the matrix.
type SitesByClientData struct {
	Rows []ClientSites `json:"rows"`
}

func serveSitesByClient(r *Request) (any, bool, error) {
	rows, err := r.siteRows()
	if err != nil {
		return nil, false, err
	}
	counts, whole, err := r.attributionByClient()
	if err != nil {
		return nil, false, err
	}
	interfaces, clients := r.idSet("interfaces"), r.idSet("clients")
	perClient := map[int64]map[string]*siteAccumulator{}
	for _, row := range rows {
		if row.clientID == nil {
			continue
		}
		if perClient[*row.clientID] == nil {
			perClient[*row.clientID] = map[string]*siteAccumulator{}
		}
		if perClient[*row.clientID][row.site] == nil {
			perClient[*row.clientID][row.site] = newSiteAccumulator(row.site)
		}
		perClient[*row.clientID][row.site].add(row)
	}
	ids := map[int64]bool{}
	for id := range perClient {
		ids[id] = true
	}
	for id := range counts {
		ids[id] = true
	}
	data := SitesByClientData{Rows: []ClientSites{}}
	perRow := int(r.params.integer("sites_per_client"))
	minimum := r.params.integer("min_flows")
	for id := range ids {
		info, err := r.client(id)
		if err != nil {
			return nil, false, err
		}
		if info == nil || (clients != nil && !clients[id]) ||
			(interfaces != nil && (info.InterfaceID == nil || !interfaces[*info.InterfaceID])) {
			continue
		}
		entry := ClientSites{Client: *info, State: "named", Sites: []SiteRow{}}
		var sites []SiteRow
		for _, accumulator := range perClient[id] {
			sites = append(sites, accumulator.finish())
		}
		sort.SliceStable(sites, func(i, j int) bool {
			if sites[i].Flows.Value != sites[j].Flows.Value {
				return sites[i].Flows.Value > sites[j].Flows.Value
			}
			return sites[i].Name < sites[j].Name
		})
		below, rest := loggedCount(0), loggedCount(0)
		for _, site := range sites {
			switch {
			case site.Flows.Value < minimum:
				entry.BelowMinFlows.Members++
				below.Value += site.Flows.Value
			case len(entry.Sites) < perRow:
				entry.Sites = append(entry.Sites, site)
			default:
				entry.Remainder.Members++
				rest.Value += site.Flows.Value
			}
		}
		entry.BelowMinFlows.Count, entry.Remainder.Count = &below, &rest
		if len(sites) == 0 {
			entry.State = "no_site_name_inferred"
		}
		if r.params.flag("show_attribution_rate") {
			count := counts[id]
			rate, state := rateOf(count, whole.ResolverCovered)
			entry.Attribution = &AttributionRow{Client: *info, EligibleFlows: loggedCount(count.eligible),
				EligibleBytes: countPointer(count.bytes), NamedFlows: loggedCount(count.named),
				Methods: MethodCounts{ResolverCacheAnswer: count.cache, LookupTiming: count.timing},
				Rate:    rate, RateState: state, MeanDelaySeconds: count.mean, MaxDelaySeconds: count.maximum}
		}
		data.Rows = append(data.Rows, entry)
	}
	sort.SliceStable(data.Rows, func(i, j int) bool {
		a, b := totalFlows(data.Rows[i]), totalFlows(data.Rows[j])
		if a != b {
			return a > b
		}
		return data.Rows[i].Client.ID < data.Rows[j].Client.ID
	})
	return data, len(rows) > 0, nil
}

// totalFlows is a client row's named flows.
func totalFlows(row ClientSites) int64 {
	total := row.Remainder.Count.Value + row.BelowMinFlows.Count.Value
	for _, site := range row.Sites {
		total += site.Flows.Value
	}
	return total
}
