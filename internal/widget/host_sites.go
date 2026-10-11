package widget

import (
	"github.com/dop251/goja"
)

// host.sites: the site names of the request's window (sites.go), and the table row a set of them
// sums to (siteaccumulator.go) -- the shared figures of every widget that ranks inferred site names.
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
//	                                   most of its bytes

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
}

func init() {
	registerHostArea("sites", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "rows", func(goja.FunctionCall) goja.Value {
			rows, err := r.SiteRows()
			if err != nil {
				return call.fail(err)
			}
			answer := make([]scriptSiteRow, 0, len(rows))
			for _, row := range rows {
				answer = append(answer, scriptSiteRow{Site: row.Site, ClientID: row.ClientID,
					InterfaceID: row.interfaceID, Method: row.method, CountryCode: row.country, ASN: row.asn,
					Operator: row.operator, Flows: row.flows, Bytes: row.bytes, DelaySum: row.delaySum,
					DelayMax: row.delayMax})
			}
			return call.value(answer)
		})
		call.function(area, "accumulate", func(arguments goja.FunctionCall) goja.Value {
			name := call.textOf(arguments.Argument(0), "the row's name")
			if absent(arguments.Argument(1)) {
				call.throw("the site rows are %s, not an array", describe(arguments.Argument(1)))
			}
			var rows []scriptSiteRow
			call.decodeArgument(arguments.Argument(1), "the site rows", &rows)
			accumulator := NewSiteAccumulator(name)
			for _, row := range rows {
				if row.DelaySum != nil && row.DelayMax == nil {
					call.throw("a site row carries delay_sum without delay_max")
				}
				accumulator.Add(siteRow{Site: row.Site, ClientID: row.ClientID, interfaceID: row.InterfaceID,
					method: row.Method, country: row.CountryCode, asn: row.ASN, operator: row.Operator,
					flows: row.Flows, bytes: row.Bytes, delaySum: row.DelaySum, delayMax: row.DelayMax})
			}
			return call.value(accumulator.Finish())
		})
	})
}
