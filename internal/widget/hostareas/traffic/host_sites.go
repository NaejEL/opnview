package traffic

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/hostareas/values"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.sites: the site names of the request's window (internal/widget/figures/sites.go), and the
// table row a set of them sums to (internal/widget/figures/siteaccumulator.go) -- the shared
// figures of every widget that ranks inferred site names.
//
//	host.sites.rows()                  the window's site figures, filtered by the request's
//	                                   `interfaces` and `clients` references: [{site, client_id,
//	                                   interface_id, method, country_code, asn, operator, flows, bytes,
//	                                   delay_sum, delay_max}], inside the flow horizon from the
//	                                   attributions and, for the whole hours the retention purge
//	                                   removed, from the domain family's 1 h slots, whose method is
//	                                   not_recorded_beyond_flow_horizon. Reading them records the
//	                                   window's coverage, and the named state
//	                                   site_methods_not_recorded_beyond_flow_horizon when slots were read.
//	host.sites.accumulate(name, rows)  the table row a list of those rows sums to, named `name`:
//	                                   {name, flows, bytes, clients, client_ids, methods,
//	                                   mean_delay_seconds, max_delay_seconds, country_code, asn,
//	                                   operator} -- the country and operator being those that carried
//	                                   most of its bytes -- and kind "site"
//	host.sites.addresses()             the window's eligible flows no site name was inferred for, per
//	                                   destination address, filtered as rows() is: [{address,
//	                                   client_id, interface_id, lookup_state, country_code, asn,
//	                                   operator, flows, bytes}]; lookup_state is resolved, miss,
//	                                   pending or no_row. With the site rows read from the flows they
//	                                   make the window's eligible flows (host.attribution). A window
//	                                   reaching past the flow horizon records the named state
//	                                   unnamed_destinations_beyond_flow_horizon.
//	host.sites.accumulateAddress(address, rows)  the table row of one address: {kind: "address",
//	                                   address, flows, bytes, clients, client_ids, lookup_state,
//	                                   country_code, asn, operator}

// scriptSiteRow is one row of site figures as a script reads it, and hands it back.
type scriptSiteRow struct {
	Site        string  `json:"site"`
	ClientID    *int64  `json:"client_id"`
	InterfaceID *int64  `json:"interface_id"`
	Method      string  `json:"method"`
	CountryCode *string `json:"country_code"`
	ASN         *int64  `json:"asn"`
	Operator    *string `json:"operator"`
	Flows       int64   `json:"flows"`
	Bytes       int64   `json:"bytes"`
	DelaySum    *int64  `json:"delay_sum"`
	DelayMax    *int64  `json:"delay_max"`
	// The members a row read under netflow carries beyond its bytes (host.figures).
	traffic.NetflowMembers
}

// scriptAddressRow is one row of address figures as a script reads it, and hands it back.
type scriptAddressRow struct {
	Address     string  `json:"address"`
	ClientID    *int64  `json:"client_id"`
	InterfaceID *int64  `json:"interface_id"`
	LookupState string  `json:"lookup_state"`
	CountryCode *string `json:"country_code"`
	ASN         *int64  `json:"asn"`
	Operator    *string `json:"operator"`
	Flows       int64   `json:"flows"`
	Bytes       int64   `json:"bytes"`
	traffic.NetflowMembers
}

func init() {
	script.RegisterHostArea("sites", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "rows", func(goja.FunctionCall) goja.Value {
			rows, err := figures.SiteRows(r)
			if err != nil {
				return call.Fail(err)
			}
			answer := make([]scriptSiteRow, 0, len(rows))
			for _, row := range rows {
				answer = append(answer, scriptSiteRow{Site: row.Site, ClientID: row.ClientID,
					InterfaceID: row.InterfaceID, Method: row.Method, CountryCode: row.Country, ASN: row.ASN,
					Operator: row.Operator, Flows: row.Flows, Bytes: row.Bytes, DelaySum: row.DelaySum,
					DelayMax: row.DelayMax, NetflowMembers: traffic.Summable(row.Measured).NetflowMembers})
			}
			return call.Value(answer)
		})
		call.Function(area, "accumulate", func(arguments goja.FunctionCall) goja.Value {
			name := call.TextOf(arguments.Argument(0), "the row's name")
			if script.Absent(arguments.Argument(1)) {
				call.Throw("the site rows are %s, not an array", script.Describe(arguments.Argument(1)))
			}
			var rows []scriptSiteRow
			call.DecodeFlat(arguments.Argument(1), "the site rows", &rows)
			accumulator := figures.NewSiteAccumulator(name)
			for _, row := range rows {
				if row.DelaySum != nil && row.DelayMax == nil {
					call.Throw("a site row carries delay_sum without delay_max")
				}
				accumulator.Add(figures.SiteRow{Site: row.Site, ClientID: row.ClientID, InterfaceID: row.InterfaceID,
					Method: row.Method, Country: row.CountryCode, ASN: row.ASN, Operator: row.Operator,
					Flows: row.Flows, Bytes: row.Bytes, DelaySum: row.DelaySum, DelayMax: row.DelayMax,
					Measured: values.MeasuredOf(call, row.NetflowMembers, row.Bytes)})
			}
			return call.Value(accumulator.Finish(r))
		})
		call.Function(area, "addresses", func(goja.FunctionCall) goja.Value {
			rows, err := figures.AddressRows(r)
			if err != nil {
				return call.Fail(err)
			}
			answer := make([]scriptAddressRow, 0, len(rows))
			for _, row := range rows {
				answer = append(answer, scriptAddressRow{Address: row.Address, ClientID: row.ClientID,
					InterfaceID: row.InterfaceID, LookupState: row.LookupState, CountryCode: row.Country,
					ASN: row.ASN, Operator: row.Operator, Flows: row.Flows, Bytes: row.Bytes,
					NetflowMembers: traffic.Summable(row.Measured).NetflowMembers})
			}
			return call.Value(answer)
		})
		call.Function(area, "accumulateAddress", func(arguments goja.FunctionCall) goja.Value {
			address := call.TextOf(arguments.Argument(0), "the address")
			if script.Absent(arguments.Argument(1)) {
				call.Throw("the address rows are %s, not an array", script.Describe(arguments.Argument(1)))
			}
			var listed []scriptAddressRow
			call.DecodeFlat(arguments.Argument(1), "the address rows", &listed)
			rows := make([]figures.AddressRow, 0, len(listed))
			for _, row := range listed {
				if row.Address != address {
					call.Throw("accumulateAddress(%s) is handed a row of %s", address, row.Address)
				}
				rows = append(rows, figures.AddressRow{Address: row.Address, ClientID: row.ClientID,
					InterfaceID: row.InterfaceID, LookupState: row.LookupState, Country: row.CountryCode,
					ASN: row.ASN, Operator: row.Operator, Flows: row.Flows, Bytes: row.Bytes,
					Measured: values.MeasuredOf(call, row.NetflowMembers, row.Bytes)})
			}
			return call.Value(figures.AccumulateAddress(r, address, rows))
		})
	})
}
