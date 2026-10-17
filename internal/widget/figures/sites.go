package figures

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
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

// SiteRow is one row of site figures.
type SiteRow struct {
	Site        string
	ClientID    *int64
	InterfaceID *int64
	Method      string
	Country     *string
	ASN         *int64
	Operator    *string
	Flows       int64
	Bytes       int64
	// Measured holds what a row read under netflow carries beyond its bytes: the measure, the packets
	// and the coverage classes of its flows (internal/widget/request/measure.go).
	Measured reads.VolumeFigures
	// DelaySum and delayMax are the correlation delays of the attributed flows, absent
	// for a row read from the slots.
	DelaySum *int64
	DelayMax *int64
}

// SiteRows reads the window's site figures, filtered by interface and client.
func SiteRows(r *request.Request) ([]SiteRow, error) {
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
	var rows []SiteRow
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
		boundary := vocab.PeriodHour.SlotStart(purged)
		if boundary < purged {
			boundary = vocab.PeriodHour.SlotEnd(boundary)
		}
		firstHour := vocab.PeriodHour.SlotStart(r.Window.From)
		if firstHour < r.Window.From {
			firstHour = vocab.PeriodHour.SlotEnd(firstHour)
		}
		boundary = min(boundary, vocab.PeriodHour.SlotStart(r.Window.To))
		if firstHour < boundary {
			read := false
			err := r.Db.WidgetRows(r.Ctx, r.InMeasure("widget_site_slots"), vocab.Period{},
				r.WithMeasure(map[string]any{"from": firstHour, "to": boundary}), func(row reads.RowScanner) error {
					var (
						item   SiteRow
						client reads.NullInt64
					)
					targets := []any{&item.Site, &client, &item.Bytes, &item.Flows}
					if measure := r.MeasureRead(); measure != "" {
						targets = append(targets, item.Measured.MeasureTarget(measure))
					}
					if err := row.Scan(targets...); err != nil {
						return err
					}
					item.ClientID = request.NullableNumber(client)
					item.Method = methodNotRecorded
					if item.ClientID != nil {
						info, err := r.Client(*item.ClientID)
						if err != nil {
							return err
						}
						if info != nil {
							item.InterfaceID = info.InterfaceID
						}
					}
					read = true
					if keep(item.ClientID, item.InterfaceID) {
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
	err = r.Db.WidgetRows(r.Ctx, r.InMeasure("widget_site_flows"), vocab.Period{},
		r.WithMeasure(map[string]any{"from": flowsFrom, "to": r.Window.To}), func(row reads.RowScanner) error {
			var (
				item                                   SiteRow
				client, iface, asn, delaySum, delayMax reads.NullInt64
				country, operator                      reads.NullString
			)
			targets := []any{&item.Site, &client, &iface, &item.Method, &country, &asn, &operator,
				&item.Flows, &item.Bytes, &delaySum, &delayMax}
			if measure := r.MeasureRead(); measure != "" {
				targets = append(targets, item.Measured.MeasureTarget(measure))
			}
			if err := row.Scan(targets...); err != nil {
				return err
			}
			item.DelaySum, item.DelayMax = request.NullableNumber(delaySum), request.NullableNumber(delayMax)
			item.ClientID, item.InterfaceID = request.NullableNumber(client), request.NullableNumber(iface)
			item.Country, item.ASN, item.Operator = request.NullableText(country), request.NullableNumber(asn), request.NullableText(operator)
			if keep(item.ClientID, item.InterfaceID) {
				rows = append(rows, item)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	if coveredFrom < min(r.Window.To, r.Now) {
		r.Cover(&envelope.Interval{From: coveredFrom, To: min(r.Window.To, r.Now)})
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
func RegistrableGrouping(r *request.Request, lists request.SuffixLists, names []string) (map[string]string, bool) {
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

// AddressRow is one row of address figures: the eligible flows of one source client and interface
// to one destination address that no site name was inferred for.
type AddressRow struct {
	Address     string
	ClientID    *int64
	InterfaceID *int64
	LookupState string
	Country     *string
	ASN         *int64
	Operator    *string
	Flows       int64
	Bytes       int64
	// Measured is as siteRow's.
	Measured reads.VolumeFigures
}

// AddressRows reads the window's address figures, filtered by interface and client as SiteRows is.
// A partly named address contributes its named flows to the site rows and only the rest here. The
// flows are read over the whole window; the hours before the retention horizon have none, which the
// named state StateUnnamedBeyondHorizon says.
func AddressRows(r *request.Request) ([]AddressRow, error) {
	interfaces, clients := r.IdSet("interfaces"), r.IdSet("clients")
	var rows []AddressRow
	err := r.Db.WidgetRows(r.Ctx, r.InMeasure("widget_address_flows"), vocab.Period{}, r.WithMeasure(r.WindowParameters()),
		func(row reads.RowScanner) error {
			var (
				item              AddressRow
				client, iface     reads.NullInt64
				asn               reads.NullInt64
				country, operator reads.NullString
			)
			targets := []any{&item.Address, &client, &iface, &item.LookupState, &country, &asn, &operator,
				&item.Flows, &item.Bytes}
			if measure := r.MeasureRead(); measure != "" {
				targets = append(targets, item.Measured.MeasureTarget(measure))
			}
			if err := row.Scan(targets...); err != nil {
				return err
			}
			item.ClientID, item.InterfaceID = request.NullableNumber(client), request.NullableNumber(iface)
			item.Country, item.ASN, item.Operator = request.NullableText(country), request.NullableNumber(asn), request.NullableText(operator)
			if clients != nil && (item.ClientID == nil || !clients[*item.ClientID]) {
				return nil
			}
			if interfaces != nil && (item.InterfaceID == nil || !interfaces[*item.InterfaceID]) {
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
	Kind        string         `json:"kind"`
	Address     string         `json:"address"`
	Flows       envelope.Count `json:"flows"`
	Bytes       envelope.Count `json:"bytes"`
	Clients     int64          `json:"clients"`
	ClientIDs   []int64        `json:"client_ids"`
	LookupState string         `json:"lookup_state"`
	CountryCode *string        `json:"country_code"`
	ASN         *int64         `json:"asn"`
	Operator    *string        `json:"operator"`
}

// AccumulateAddress sums the rows of one address into its table row, its counts in the measure of the
// rows (internal/widget/request/measure.go).
func AccumulateAddress(r *request.Request, address string, rows []AddressRow) addressTableRow {
	row := addressTableRow{Kind: DestinationKindAddress, Address: address, ClientIDs: []int64{},
		LookupState: request.GeoNoRow}
	clients := map[int64]bool{}
	var (
		flows   int64
		figures reads.VolumeFigures
	)
	for _, item := range rows {
		flows += item.Flows
		figures.Add(MeasuredFigures(item.Bytes, item.Measured))
		if item.ClientID != nil {
			clients[*item.ClientID] = true
		}
		// One address has one geolocation, so every row of it carries the same.
		row.LookupState, row.CountryCode, row.ASN, row.Operator = item.LookupState, item.Country, item.ASN,
			item.Operator
	}
	row.Flows, row.Bytes = r.CountOf(flows), r.ByteCountOf(figures, false)
	for id := range clients {
		row.ClientIDs = append(row.ClientIDs, id)
	}
	sort.Slice(row.ClientIDs, func(i, j int) bool { return row.ClientIDs[i] < row.ClientIDs[j] })
	row.Clients = int64(len(row.ClientIDs))
	return row
}

// figures are a site row's bytes in their measure.
func (item SiteRow) figures() reads.VolumeFigures { return MeasuredFigures(item.Bytes, item.Measured) }

// MeasuredFigures are bytes in the measure a row was read in: measured carries the netflow members of
// a row read under netflow, and nothing for a logged one.
func MeasuredFigures(bytes int64, measured reads.VolumeFigures) reads.VolumeFigures {
	figures := measured
	figures.Bytes = bytes
	return figures
}
