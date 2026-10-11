package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/publicsuffix"
	"github.com/NaejEL/opnview/internal/store"
)

// Site names over a window. Inside the flow horizon they are read from the attributions,
// which carry each name's method; for the whole hours the retention purge has already
// removed, from the domain family's 1 h slots, which keep the totals and not the method.
// Every record carrying a site name carries its method (A9).

// The method values of domain_attribution.method, and the state of a figure read from the
// slots, where the method was not kept.
const (
	methodCacheAnswer  = "resolver_cache_answer"
	methodLookupTiming = "lookup_timing"
	methodNotRecorded  = "not_recorded_beyond_flow_horizon"
)

// SuffixLists is what the API needs of the Public Suffix List: the copy in use, or nil
// before the first successful download. It never fetches: *publicsuffix.Refresher holds
// the list its own refresh downloaded, and reading it makes no request.
type SuffixLists interface {
	List() *publicsuffix.List
}

// siteRow is one row of site figures.
type siteRow struct {
	site        string
	clientID    *int64
	interfaceID *int64
	method      string
	country     *string
	asn         *int64
	operator    *string
	flows       int64
	bytes       int64
	// delaySum and delayMax are the correlation delays of the attributed flows, absent
	// for a row read from the slots.
	delaySum *int64
	delayMax *int64
}

// siteRows reads the window's site figures, filtered by interface and client.
func (r *Request) siteRows() ([]siteRow, error) {
	interfaces, clients := r.idSet("interfaces"), r.idSet("clients")
	keep := func(clientID, interfaceID *int64) bool {
		if clients != nil && (clientID == nil || !clients[*clientID]) {
			return false
		}
		if interfaces != nil && (interfaceID == nil || !interfaces[*interfaceID]) {
			return false
		}
		return true
	}
	var rows []siteRow
	flowsFrom := r.window.From
	var purged int64
	hasPurge, err := r.db.WidgetRow(r.ctx, "widget_purged_before", nil, &purged)
	if err != nil {
		return nil, err
	}
	earliest, hasFlow, err := r.earliest("widget_earliest_flow")
	if err != nil {
		return nil, err
	}
	coveredFrom := r.window.To
	if hasFlow {
		coveredFrom = max(r.window.From, earliest)
	}
	if hasPurge && purged > r.window.From {
		// The purged hours come from the slots, the rest from the flows.
		boundary := store.PeriodHour.SlotStart(purged)
		if boundary < purged {
			boundary = store.PeriodHour.SlotEnd(boundary)
		}
		firstHour := store.PeriodHour.SlotStart(r.window.From)
		if firstHour < r.window.From {
			firstHour = store.PeriodHour.SlotEnd(firstHour)
		}
		boundary = min(boundary, store.PeriodHour.SlotStart(r.window.To))
		if firstHour < boundary {
			read := false
			err := r.db.WidgetRows(r.ctx, "widget_site_slots", store.Period{},
				map[string]any{"from": firstHour, "to": boundary}, func(row store.RowScanner) error {
					var (
						item   siteRow
						client store.NullInt64
					)
					if err := row.Scan(&item.site, &client, &item.bytes, &item.flows); err != nil {
						return err
					}
					item.clientID = nullableNumber(client)
					item.method = methodNotRecorded
					if item.clientID != nil {
						info, err := r.client(*item.clientID)
						if err != nil {
							return err
						}
						if info != nil {
							item.interfaceID = info.InterfaceID
						}
					}
					read = true
					if keep(item.clientID, item.interfaceID) {
						rows = append(rows, item)
					}
					return nil
				})
			if err != nil {
				return nil, err
			}
			flowsFrom = boundary
			if read {
				coveredFrom = min(coveredFrom, firstHour)
				r.state("site_methods_not_recorded_beyond_flow_horizon")
			}
		}
	}
	err = r.db.WidgetRows(r.ctx, "widget_site_flows", store.Period{},
		map[string]any{"from": flowsFrom, "to": r.window.To}, func(row store.RowScanner) error {
			var (
				item                                   siteRow
				client, iface, asn, delaySum, delayMax store.NullInt64
				country, operator                      store.NullString
			)
			if err := row.Scan(&item.site, &client, &iface, &item.method, &country, &asn, &operator,
				&item.flows, &item.bytes, &delaySum, &delayMax); err != nil {
				return err
			}
			item.delaySum, item.delayMax = nullableNumber(delaySum), nullableNumber(delayMax)
			item.clientID, item.interfaceID = nullableNumber(client), nullableNumber(iface)
			item.country, item.asn, item.operator = nullableText(country), nullableNumber(asn), nullableText(operator)
			if keep(item.clientID, item.interfaceID) {
				rows = append(rows, item)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	if coveredFrom < min(r.window.To, r.now) {
		r.cover(&Interval{From: coveredFrom, To: min(r.window.To, r.now)})
	} else {
		r.cover(nil)
	}
	return rows, nil
}

// MethodCounts are the named flows per method; they sum to the named flows.
type MethodCounts struct {
	ResolverCacheAnswer int64 `json:"resolver_cache_answer"`
	LookupTiming        int64 `json:"lookup_timing"`
	// NotRecorded is the flows of hours read from the domain family beyond the flow
	// horizon, whose method was not kept.
	NotRecorded int64 `json:"not_recorded_beyond_flow_horizon"`
}

func (m *MethodCounts) add(method string, flows int64) {
	switch method {
	case methodCacheAnswer:
		m.ResolverCacheAnswer += flows
	case methodLookupTiming:
		m.LookupTiming += flows
	default:
		m.NotRecorded += flows
	}
}

// registrableGrouping maps each site name to its registrable domain, or reports that no
// list has been downloaded yet.
func (r *Request) registrableGrouping(lists SuffixLists, names []string) (map[string]string, bool) {
	if lists == nil {
		return nil, false
	}
	list := lists.List()
	if list == nil {
		return nil, false
	}
	totals := make([]store.SiteTotal, 0, len(names))
	for _, name := range names {
		totals = append(totals, store.SiteTotal{SiteName: name})
	}
	groups := publicsuffix.GroupSites(list, totals)
	result := map[string]string{}
	for _, group := range groups {
		for _, name := range group.SiteNames {
			result[name] = group.RegistrableDomain
		}
	}
	return result, true
}

// sortedKeys returns a map's keys in order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
