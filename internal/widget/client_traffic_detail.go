package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// client_traffic_detail: one client's records over the period, newest first, and which
// server issued each of its leases (docs/widget-catalogue.md, "Client traffic detail").
// A record with no attribution is a first-class row, with its address, country and
// operator; an attributed one carries its site name, its method and its delay.

var clientTrafficDetail = Preset{
	Type: "client_traffic_detail", Shape: "table", Windowed: true,
	Providers: []string{"firewall_log", "dns_lookup", "resolver_cache", "geo_asn", "dhcp_lease"},
	Primary:   "firewall_log",
	Params: []Param{
		{Key: "client", Kind: KindReference, Reference: "client", Data: true, Required: true},
		periodParam(),
		{Key: "action", Kind: KindEnum, Values: []string{"all", "allowed", "blocked"}, Default: "all", Data: true},
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth}, Default: scopeAll,
			Data: true},
		{Key: "columns", Kind: KindEnumSet, Values: clientDetailColumns, Default: stringsAsAny(clientDetailColumns)},
		limitParam(100),
	},
	Serve: serveClientTrafficDetail,
}

// clientDetailColumns are the columns a reader may choose to show. The API returns every
// column; `columns` is rendering-only.
var clientDetailColumns = []string{"observed_at", "destination", "port", "protocol", "action", "scope",
	"bytes", "country", "operator", "site_name", "method", "correlation_delay", "interface"}

// stringsAsAny turns a vocabulary into a default set value.
func stringsAsAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

// ClientRecord is one record.
type ClientRecord struct {
	FlowID      int64   `json:"flow_id"`
	ObservedAt  int64   `json:"observed_at"`
	Destination string  `json:"destination"`
	Port        *int64  `json:"port"`
	Protocol    string  `json:"protocol"`
	Action      string  `json:"action"`
	Scope       string  `json:"scope"`
	Bytes       Count   `json:"bytes"`
	Geo         GeoInfo `json:"geo"`
	// SiteName is the inferred site name, null when no lookup could be correlated -- or
	// when the aggregate mode withholds it, which `withheld` lists.
	SiteName *string `json:"site_name"`
	// Method is resolver_cache_answer or lookup_timing, null without a site name.
	Method *string `json:"method"`
	// CorrelationDelaySeconds is how long before the record the naming lookup was made.
	CorrelationDelaySeconds *int64 `json:"correlation_delay_seconds"`
	// InterfaceDevice is the device the record was logged on, and InterfaceDescription
	// what the interface map calls it, null when the device matched no entry.
	InterfaceDevice      string  `json:"interface_device"`
	InterfaceDescription *string `json:"interface_description"`
}

// ClientLease is one lease of the client and the server that issued it.
type ClientLease struct {
	ProviderKind   string `json:"provider_kind"`
	ProviderKey    string `json:"provider_key"`
	Backend        string `json:"backend"`
	Address        string `json:"address"`
	LastObservedAt int64  `json:"last_observed_at"`
	ExpiresAt      *int64 `json:"expires_at"`
}

// ClientDetailData is the table.
type ClientDetailData struct {
	Client ClientInfo `json:"client"`
	// CurrentRate is the client's latest sampled rate, or its named absence.
	CurrentRate ChipRate       `json:"current_rate"`
	Leases      []ClientLease  `json:"leases"`
	Records     []ClientRecord `json:"records"`
	Remainder   Remainder      `json:"remainder"`
	Total       Remainder      `json:"total"`
}

func serveClientTrafficDetail(r *Request) (any, bool, error) {
	clientID := r.refs["client"][0].id
	info, err := r.client(clientID)
	if err != nil || info == nil {
		return nil, false, err
	}
	if err := r.coverFlows("records_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	parameters := r.windowParameters()
	parameters["client_id"] = clientID
	parameters["action"] = r.params.text("action")
	parameters["scope"] = r.params.text("scope")
	parameters["limit"] = r.params.integer("limit")
	data := ClientDetailData{Client: *info, Leases: []ClientLease{}, Records: []ClientRecord{}}
	rate, state, err := r.db.ReadClientCurrentRate(r.ctx, clientID, r.now)
	if err != nil {
		return nil, false, err
	}
	data.CurrentRate = sampledRate(rate, state == store.RateCurrent)
	err = r.db.WidgetRows(r.ctx, "widget_client_leases", store.Period{}, map[string]any{"client_id": clientID},
		func(row store.RowScanner) error {
			var (
				lease   ClientLease
				expires store.NullInt64
			)
			if err := row.Scan(&lease.ProviderKind, &lease.ProviderKey, &lease.Backend, &lease.Address,
				&lease.LastObservedAt, &expires); err != nil {
				return err
			}
			lease.ExpiresAt = nullableNumber(expires)
			data.Leases = append(data.Leases, lease)
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	var shownBytes int64
	err = r.db.WidgetRows(r.ctx, "widget_client_records", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				record                      ClientRecord
				port, mapInterface, asn     store.NullInt64
				build, delay                store.NullInt64
				description, state, country store.NullString
				operator, site, method      store.NullString
				bytes                       int64
			)
			if err := row.Scan(&record.FlowID, &record.ObservedAt, &record.Destination, &port, &record.Protocol,
				&record.Action, &record.Scope, &bytes, &record.InterfaceDevice, &description, &mapInterface,
				&state, &country, &asn, &operator, &build, &site, &method, &delay); err != nil {
				return err
			}
			record.Port = nullableNumber(port)
			record.Bytes = loggedCount(bytes)
			shownBytes += bytes
			record.InterfaceDescription = nullableText(description)
			record.Geo = GeoInfo{LookupState: geoNoRow, CountryCode: nullableText(country), ASN: nullableNumber(asn),
				Operator: nullableText(operator), DatasetBuildAt: nullableNumber(build)}
			if state.Valid {
				record.Geo.LookupState = state.String
			}
			record.SiteName = nullableText(site)
			record.Method = nullableText(method)
			record.CorrelationDelaySeconds = nullableNumber(delay)
			if r.noDomains() && record.SiteName != nil {
				record.SiteName = nil
				r.withhold("records[].site_name")
			}
			data.Records = append(data.Records, record)
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	var count, bytes int64
	if _, err := r.db.WidgetRow(r.ctx, "widget_client_record_totals", parameters, &count, &bytes); err != nil {
		return nil, false, err
	}
	totalBytes := loggedCount(bytes)
	data.Total = Remainder{Members: count, Count: &totalBytes}
	remainderBytes := loggedCount(bytes - shownBytes)
	data.Remainder = Remainder{Members: count - int64(len(data.Records)), Count: &remainderBytes}
	if r.noDomains() {
		r.withhold("records[].site_name")
	}
	return data, count > 0, nil
}
