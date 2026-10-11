package widget

import (
	"context"
	"sort"

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

// SuffixLists is what the API needs of the registrable_domain kind: each name's registrable
// domain, from the provider in use, and false when none is held. It never fetches: the active
// provider answers from the copy its own refresh downloaded.
type SuffixLists interface {
	RegistrableDomains(ctx context.Context, names []string) (map[string]string, bool)
}

// siteRow is one row of site figures.
type siteRow struct {
	Site        string
	ClientID    *int64
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

// SiteRows reads the window's site figures, filtered by interface and client.
func (r *Request) SiteRows() ([]siteRow, error) {
	interfaces, clients := r.IdSet("interfaces"), r.IdSet("clients")
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
	flowsFrom := r.Window.From
	var purged int64
	hasPurge, err := r.Db.WidgetRow(r.Ctx, "widget_purged_before", nil, &purged)
	if err != nil {
		return nil, err
	}
	earliest, hasFlow, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return nil, err
	}
	coveredFrom := r.Window.To
	if hasFlow {
		coveredFrom = max(r.Window.From, earliest)
	}
	if hasPurge && purged > r.Window.From {
		// The purged hours come from the slots, the rest from the flows.
		boundary := store.PeriodHour.SlotStart(purged)
		if boundary < purged {
			boundary = store.PeriodHour.SlotEnd(boundary)
		}
		firstHour := store.PeriodHour.SlotStart(r.Window.From)
		if firstHour < r.Window.From {
			firstHour = store.PeriodHour.SlotEnd(firstHour)
		}
		boundary = min(boundary, store.PeriodHour.SlotStart(r.Window.To))
		if firstHour < boundary {
			read := false
			err := r.Db.WidgetRows(r.Ctx, "widget_site_slots", store.Period{},
				map[string]any{"from": firstHour, "to": boundary}, func(row store.RowScanner) error {
					var (
						item   siteRow
						client store.NullInt64
					)
					if err := row.Scan(&item.Site, &client, &item.bytes, &item.flows); err != nil {
						return err
					}
					item.ClientID = NullableNumber(client)
					item.method = methodNotRecorded
					if item.ClientID != nil {
						info, err := r.Client(*item.ClientID)
						if err != nil {
							return err
						}
						if info != nil {
							item.interfaceID = info.InterfaceID
						}
					}
					read = true
					if keep(item.ClientID, item.interfaceID) {
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
				r.State("site_methods_not_recorded_beyond_flow_horizon")
			}
		}
	}
	err = r.Db.WidgetRows(r.Ctx, "widget_site_flows", store.Period{},
		map[string]any{"from": flowsFrom, "to": r.Window.To}, func(row store.RowScanner) error {
			var (
				item                                   siteRow
				client, iface, asn, delaySum, delayMax store.NullInt64
				country, operator                      store.NullString
			)
			if err := row.Scan(&item.Site, &client, &iface, &item.method, &country, &asn, &operator,
				&item.flows, &item.bytes, &delaySum, &delayMax); err != nil {
				return err
			}
			item.delaySum, item.delayMax = NullableNumber(delaySum), NullableNumber(delayMax)
			item.ClientID, item.interfaceID = NullableNumber(client), NullableNumber(iface)
			item.country, item.asn, item.operator = NullableText(country), NullableNumber(asn), NullableText(operator)
			if keep(item.ClientID, item.interfaceID) {
				rows = append(rows, item)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	if coveredFrom < min(r.Window.To, r.Now) {
		r.Cover(&Interval{From: coveredFrom, To: min(r.Window.To, r.Now)})
	} else {
		r.Cover(nil)
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

// RegistrableGrouping maps each site name to its registrable domain, or reports that no
// list has been downloaded yet.
func (r *Request) RegistrableGrouping(lists SuffixLists, names []string) (map[string]string, bool) {
	if lists == nil {
		return nil, false
	}
	domains, available := lists.RegistrableDomains(r.Ctx, names)
	if !available {
		return nil, false
	}
	// A name with no registrable domain -- one that is itself a public suffix -- stands for itself.
	result := make(map[string]string, len(names))
	for _, name := range names {
		if domain, found := domains[name]; found {
			result[name] = domain
		} else {
			result[name] = name
		}
	}
	return result, true
}

// SortedKeys returns a map's keys in order.
func SortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The named state of address rows over a window reaching past the flow horizon: no aggregate keeps
// the flows no name was inferred for per destination address, so the hours the retention purge
// removed have no address rows, and their figure is never estimated.
const StateUnnamedBeyondHorizon = "unnamed_destinations_beyond_flow_horizon"

// The kinds of a destination row: an inferred site name, or an address no name was inferred for.
const (
	DestinationKindSite    = "site"
	DestinationKindAddress = "address"
)

// addressRow is one row of address figures: the eligible flows of one source client and interface
// to one destination address that no site name was inferred for.
type addressRow struct {
	Address     string
	ClientID    *int64
	interfaceID *int64
	lookupState string
	country     *string
	asn         *int64
	operator    *string
	flows       int64
	bytes       int64
}

// AddressRows reads the window's address figures, filtered by interface and client as SiteRows is.
// A partly named address contributes its named flows to the site rows and only the rest here. The
// flows are read over the whole window; the hours before the retention horizon have none, which the
// named state StateUnnamedBeyondHorizon says.
func (r *Request) AddressRows() ([]addressRow, error) {
	interfaces, clients := r.IdSet("interfaces"), r.IdSet("clients")
	var rows []addressRow
	err := r.Db.WidgetRows(r.Ctx, "widget_address_flows", store.Period{}, r.WindowParameters(),
		func(row store.RowScanner) error {
			var (
				item              addressRow
				client, iface     store.NullInt64
				asn               store.NullInt64
				country, operator store.NullString
			)
			if err := row.Scan(&item.Address, &client, &iface, &item.lookupState, &country, &asn, &operator,
				&item.flows, &item.bytes); err != nil {
				return err
			}
			item.ClientID, item.interfaceID = NullableNumber(client), NullableNumber(iface)
			item.country, item.asn, item.operator = NullableText(country), NullableNumber(asn), NullableText(operator)
			if clients != nil && (item.ClientID == nil || !clients[*item.ClientID]) {
				return nil
			}
			if interfaces != nil && (item.interfaceID == nil || !interfaces[*item.interfaceID]) {
				return nil
			}
			rows = append(rows, item)
			return nil
		})
	if err != nil {
		return nil, err
	}
	if err := r.CoverFlowsState(StateUnnamedBeyondHorizon); err != nil {
		return nil, err
	}
	return rows, nil
}

// addressTableRow is one destination address no name was inferred for, as a table ranks it beside
// the site rows: the address, its operator and its country, or the state of its lookup.
type addressTableRow struct {
	Kind        string  `json:"kind"`
	Address     string  `json:"address"`
	Flows       Count   `json:"flows"`
	Bytes       Count   `json:"bytes"`
	Clients     int64   `json:"clients"`
	ClientIDs   []int64 `json:"client_ids"`
	LookupState string  `json:"lookup_state"`
	CountryCode *string `json:"country_code"`
	ASN         *int64  `json:"asn"`
	Operator    *string `json:"operator"`
}

// accumulateAddress sums the rows of one address into its table row.
func accumulateAddress(address string, rows []addressRow) addressTableRow {
	row := addressTableRow{Kind: DestinationKindAddress, Address: address, Flows: LoggedCount(0),
		Bytes: LoggedCount(0), ClientIDs: []int64{}, LookupState: GeoNoRow}
	clients := map[int64]bool{}
	for _, item := range rows {
		row.Flows.Value += item.flows
		row.Bytes.Value += item.bytes
		if item.ClientID != nil {
			clients[*item.ClientID] = true
		}
		// One address has one geolocation, so every row of it carries the same.
		row.LookupState, row.CountryCode, row.ASN, row.Operator = item.lookupState, item.country, item.asn,
			item.operator
	}
	for id := range clients {
		row.ClientIDs = append(row.ClientIDs, id)
	}
	sort.Slice(row.ClientIDs, func(i, j int) bool { return row.ClientIDs[i] < row.ClientIDs[j] })
	row.Clients = int64(len(row.ClientIDs))
	return row
}
