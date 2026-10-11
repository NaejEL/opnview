package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// blocked_dns_lookups: the names the resolver refused, ranked by count, with the clients
// that asked, the interfaces they sit in, and the list that refused them; the purpose of a
// list is somebody's decision, never inferred from its name, and a refusal attributed to
// no list is its own state (docs/widget-catalogue.md, "Blocked DNS lookups"). A blocked
// lookup's domain is a domain name, withheld under aggregate_mode = no_domains (decision 6);
// its counts are not.

var blockedDNSLookups = Preset{
	Type: "blocked_dns_lookups", Shape: "table", Windowed: true,
	Providers: []string{"dns_lookup"}, Primary: "dns_lookup",
	Params: []Param{
		periodParam(),
		{Key: "purposes", Kind: KindEnumSet, Values: lookupPurposes, Default: stringsAsAny(lookupPurposes), Data: true},
		{Key: "blocklists", Kind: KindStringList, Data: true},
		interfacesParam(),
		clientsParam(),
		limitParam(20),
		{Key: "min_count", Kind: KindInteger, Min: 1, Default: float64(1), Data: true},
	},
	Serve: serveBlockedDNSLookups,
}

// lookupPurposes are the purposes a user assigns, plus the two states selectable so they
// cannot be quietly excluded.
var lookupPurposes = []string{"advertising", "tracking", "threat", "parental", "other", "unassigned", "not_recorded"}

// BlockedLookupRow is one refused name and the list that refused it.
type BlockedLookupRow struct {
	// Domain is null when the aggregate mode withholds domain names.
	Domain *string `json:"domain"`
	// List is the blocklist's name as the resolver reported it, null when not recorded.
	List *string `json:"list"`
	// Purpose is the assigned purpose, unassigned, or not_recorded.
	Purpose    string   `json:"purpose"`
	Lookups    Count    `json:"lookups"`
	Clients    []int64  `json:"clients"`
	Addresses  []string `json:"unmatched_client_addresses"`
	Interfaces []int64  `json:"interfaces"`
	LastSeenAt int64    `json:"last_seen_at"`
}

// BlockedLookupsData is the table.
type BlockedLookupsData struct {
	Rows          []BlockedLookupRow `json:"rows"`
	Remainder     Remainder          `json:"remainder"`
	BelowMinCount Remainder          `json:"below_min_count"`
	Total         Count              `json:"total"`
}

func serveBlockedDNSLookups(r *Request) (any, bool, error) {
	earliest, has, err := r.earliest("widget_earliest_lookup")
	if err != nil {
		return nil, false, err
	}
	r.coverFrom(earliest, has)
	interfaces, clients := r.idSet("interfaces"), r.idSet("clients")
	lists := r.params.set("blocklists")
	type accumulator struct {
		row        BlockedLookupRow
		clients    map[int64]bool
		addresses  map[string]bool
		interfaces map[int64]bool
	}
	rows := map[string]*accumulator{}
	var total int64
	err = r.db.WidgetRows(r.ctx, "widget_blocked_lookups", store.Period{}, r.windowParameters(),
		func(row store.RowScanner) error {
			var (
				domain, address       string
				listID, client, iface store.NullInt64
				name, purpose         store.NullString
				count, newest         int64
			)
			if err := row.Scan(&domain, &listID, &name, &purpose, &client, &address, &iface, &count,
				&newest); err != nil {
				return err
			}
			state := "not_recorded"
			switch {
			case listID.Valid && purpose.Valid:
				state = purpose.String
			case listID.Valid:
				state = "unassigned"
			}
			if !r.params.has("purposes", state) ||
				(len(lists) > 0 && (!name.Valid || !contains(lists, name.String))) ||
				(clients != nil && (!client.Valid || !clients[client.Int64])) ||
				(interfaces != nil && (!iface.Valid || !interfaces[iface.Int64])) {
				return nil
			}
			key := domain + "\x1f" + strconv.FormatInt(listID.Int64, 10) + strconv.FormatBool(listID.Valid)
			entry := rows[key]
			if entry == nil {
				copied := domain
				entry = &accumulator{row: BlockedLookupRow{Domain: &copied, List: nullableText(name), Purpose: state,
					Lookups: Count{Measure: "dns_lookups", LowerBound: true}},
					clients: map[int64]bool{}, addresses: map[string]bool{}, interfaces: map[int64]bool{}}
				rows[key] = entry
			}
			entry.row.Lookups.Value += count
			entry.row.LastSeenAt = max(entry.row.LastSeenAt, newest)
			if client.Valid {
				entry.clients[client.Int64] = true
			} else {
				entry.addresses[address] = true
			}
			if iface.Valid {
				entry.interfaces[iface.Int64] = true
			}
			total += count
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	var list []BlockedLookupRow
	for _, key := range sortedKeys(rows) {
		entry := rows[key]
		entry.row.Clients = interfaceIDs(entry.clients)
		entry.row.Addresses = sortedKeys(entry.addresses)
		entry.row.Interfaces = interfaceIDs(entry.interfaces)
		if r.noDomains() {
			entry.row.Domain = nil
			r.withhold("rows[].domain")
		}
		list = append(list, entry.row)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Lookups.Value > list[j].Lookups.Value })
	data := BlockedLookupsData{Rows: []BlockedLookupRow{}, Total: Count{Measure: "dns_lookups", LowerBound: true,
		Value: total}}
	limit, minimum := int(r.params.integer("limit")), r.params.integer("min_count")
	below := Count{Measure: "dns_lookups", LowerBound: true}
	rest := Count{Measure: "dns_lookups", LowerBound: true}
	for _, row := range list {
		switch {
		case row.Lookups.Value < minimum:
			data.BelowMinCount.Members++
			below.Value += row.Lookups.Value
		case len(data.Rows) < limit:
			data.Rows = append(data.Rows, row)
		default:
			data.Remainder.Members++
			rest.Value += row.Lookups.Value
		}
	}
	data.BelowMinCount.Count, data.Remainder.Count = &below, &rest
	return data, total > 0, nil
}
