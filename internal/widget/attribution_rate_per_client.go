package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// attribution_rate_per_client: per client, how many of its outbound flows could be named
// at all, by which method, with what delay, and whether the resolver's cache was read over
// the period (docs/widget-catalogue.md, "Attribution rate per client"). The counts are
// store.ReadAttributionRate's, per client. A rate is UNDEFINED -- never 0 -- when no
// resolver source answered for the window or the client has no eligible flow.

var attributionRatePerClient = Preset{
	Type: "attribution_rate_per_client", Shape: "table", Windowed: true,
	Providers: []string{"dns_lookup"}, Primary: "dns_lookup",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		clientsParam(),
		limitParam(20),
		{Key: "sort", Kind: KindEnum, Values: []string{"worst_first", "best_first", "by_volume"},
			Default: "worst_first", Data: true},
	},
	Serve: serveAttributionRatePerClient,
}

// The states of a rate.
const (
	rateDefined             = "defined"
	rateUndefinedNoResolver = "undefined_no_resolver_evidence"
	rateUndefinedNoFlow     = "undefined_no_eligible_flow"
)

// AttributionRow is one client's attribution.
type AttributionRow struct {
	Client        ClientInfo   `json:"client"`
	EligibleFlows Count        `json:"eligible_flows"`
	EligibleBytes *Count       `json:"eligible_bytes,omitempty"`
	NamedFlows    Count        `json:"named_flows"`
	Methods       MethodCounts `json:"methods"`
	// Rate is NamedFlows / EligibleFlows, null when RateState says it is undefined.
	Rate             *float64 `json:"rate"`
	RateState        string   `json:"rate_state"`
	MeanDelaySeconds *float64 `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64   `json:"max_delay_seconds"`
}

// AttributionData is the table.
type AttributionData struct {
	// ResolverCovered says opnview holds evidence a resolver source answered for the
	// window; CacheCovered that the resolver's cache was read over it.
	ResolverCovered bool             `json:"resolver_covered"`
	CacheCovered    bool             `json:"cache_covered"`
	Rows            []AttributionRow `json:"rows"`
	Remainder       Remainder        `json:"remainder"`
}

// clientAttribution is one client's counts from widget_attribution_rate_by_client.
type clientAttribution struct {
	eligible, named, cache, timing, bytes int64
	mean                                  *float64
	maximum                               *int64
}

// attributionByClient reads the per-client counts of the window.
func (r *Request) attributionByClient() (map[int64]clientAttribution, store.AttributionRate, error) {
	whole, err := r.db.ReadAttributionRate(r.ctx, r.window.From, r.window.To, nil)
	if err != nil {
		return nil, whole, err
	}
	result := map[int64]clientAttribution{}
	err = r.db.WidgetRows(r.ctx, "widget_attribution_rate_by_client", store.Period{}, r.windowParameters(),
		func(row store.RowScanner) error {
			var (
				id      int64
				counts  clientAttribution
				mean    store.NullFloat64
				maximum store.NullInt64
			)
			if err := row.Scan(&id, &counts.eligible, &counts.named, &mean, &maximum, &counts.cache,
				&counts.timing, &counts.bytes); err != nil {
				return err
			}
			counts.mean, counts.maximum = nullableFloat(mean), nullableNumber(maximum)
			result[id] = counts
			return nil
		})
	return result, whole, err
}

// rateOf is a client's rate and its state.
func rateOf(counts clientAttribution, resolverCovered bool) (*float64, string) {
	switch {
	case !resolverCovered:
		return nil, rateUndefinedNoResolver
	case counts.eligible == 0:
		return nil, rateUndefinedNoFlow
	}
	rate := float64(counts.named) / float64(counts.eligible)
	return &rate, rateDefined
}

func serveAttributionRatePerClient(r *Request) (any, bool, error) {
	if err := r.coverFlows("attribution_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	counts, whole, err := r.attributionByClient()
	if err != nil {
		return nil, false, err
	}
	interfaces, clients := r.idSet("interfaces"), r.idSet("clients")
	data := AttributionData{ResolverCovered: whole.ResolverCovered, CacheCovered: whole.CacheCovered,
		Rows: []AttributionRow{}}
	var rows []AttributionRow
	for id, count := range counts {
		info, err := r.client(id)
		if err != nil {
			return nil, false, err
		}
		if info == nil || (clients != nil && !clients[id]) ||
			(interfaces != nil && (info.InterfaceID == nil || !interfaces[*info.InterfaceID])) {
			continue
		}
		rate, state := rateOf(count, whole.ResolverCovered)
		rows = append(rows, AttributionRow{Client: *info, EligibleFlows: loggedCount(count.eligible),
			EligibleBytes: countPointer(count.bytes), NamedFlows: loggedCount(count.named),
			Methods: MethodCounts{ResolverCacheAnswer: count.cache, LookupTiming: count.timing},
			Rate:    rate, RateState: state, MeanDelaySeconds: count.mean, MaxDelaySeconds: count.maximum})
	}
	order := r.params.text("sort")
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch order {
		case "by_volume":
			if a.EligibleBytes.Value != b.EligibleBytes.Value {
				return a.EligibleBytes.Value > b.EligibleBytes.Value
			}
		default:
			// An undefined rate sorts first under worst_first and last under best_first:
			// it is the least trustworthy of all.
			ra, rb := -1.0, -1.0
			if a.Rate != nil {
				ra = *a.Rate
			}
			if b.Rate != nil {
				rb = *b.Rate
			}
			if ra != rb {
				if order == "best_first" {
					return ra > rb
				}
				return ra < rb
			}
		}
		return a.Client.ID < b.Client.ID
	})
	limit := int(r.params.integer("limit"))
	rest := loggedCount(0)
	for index, row := range rows {
		if index < limit {
			data.Rows = append(data.Rows, row)
			continue
		}
		data.Remainder.Members++
		rest.Value += row.EligibleFlows.Value
	}
	data.Remainder.Count = &rest
	return data, len(rows) > 0, nil
}
