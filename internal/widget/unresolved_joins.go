package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// unresolved_joins: four counters -- flows whose interface device matched no interface-map
// entry, flows whose rid matched no known rule, clients known only by an address behind an
// interface, and addresses whose geolocation is a miss, pending or was never made -- so the
// quality of every other widget is visible rather than assumed (docs/widget-catalogue.md,
// "Unresolved joins"). Reachable and no rows is the one condition that may read 0 here,
// because there it genuinely is one.

var unresolvedJoins = Preset{
	Type: "unresolved_joins", Shape: "figure", Windowed: true,
	Providers: []string{"firewall_log", "geo_asn", "dhcp_lease"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "counters", Kind: KindEnumSet, Values: []string{"interface", "rule", "client_identity", "geo"},
			Default: []any{"interface", "rule", "client_identity", "geo"}, Data: true},
		interfacesParam(),
		{Key: "show_records", Kind: KindBoolean, Default: false, Data: true},
	},
	Serve: serveUnresolvedJoins,
}

// unresolvedRecordLimit is how many records a counter lists when show_records is set: the
// newest, the rest being reachable from the counter's own figure.
const unresolvedRecordLimit = 20

// JoinCounter is one counter.
type JoinCounter struct {
	Counter string `json:"counter"`
	// Unresolved is how many failed the join, out of Of examined.
	Unresolved Count `json:"unresolved"`
	Of         Count `json:"of"`
	// Records are the newest records behind the counter, when show_records is set.
	Records []UnresolvedRecord `json:"records,omitempty"`
}

// UnresolvedRecord is one record behind a counter.
type UnresolvedRecord struct {
	FlowID          *int64  `json:"flow_id,omitempty"`
	ObservedAt      *int64  `json:"observed_at,omitempty"`
	InterfaceDevice *string `json:"interface_device,omitempty"`
	Rid             *string `json:"rid,omitempty"`
	State           string  `json:"state"`
	ClientID        *int64  `json:"client_id,omitempty"`
	Address         *string `json:"address,omitempty"`
}

// JoinsData is the counters.
type JoinsData struct {
	Counters []JoinCounter `json:"counters"`
}

func serveUnresolvedJoins(r *Request) (any, bool, error) {
	if err := r.coverFlows("joins_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	parameters := r.windowParameters()
	parameters["interface_ids"] = jsonIDs(r.ids("interfaces"))
	parameters["limit"] = unresolvedRecordLimit
	var interfaceMisses, ruleMisses, flows int64
	if _, err := r.db.WidgetRow(r.ctx, "widget_unresolved_flow_joins", parameters, &interfaceMisses, &ruleMisses,
		&flows); err != nil {
		return nil, false, err
	}
	data := JoinsData{Counters: []JoinCounter{}}
	records := func(counter string) ([]UnresolvedRecord, error) {
		list := []UnresolvedRecord{}
		parameters["counter"] = counter
		err := r.db.WidgetRows(r.ctx, "widget_unresolved_flow_records", store.Period{}, parameters,
			func(row store.RowScanner) error {
				var (
					id, at                  int64
					device, iface, ruleText string
					rid                     store.NullString
				)
				if err := row.Scan(&id, &at, &device, &iface, &rid, &ruleText); err != nil {
					return err
				}
				record := UnresolvedRecord{FlowID: &id, ObservedAt: &at, InterfaceDevice: &device,
					Rid: nullableText(rid), State: iface}
				if counter == "rule" {
					record.State = ruleText
				}
				list = append(list, record)
				return nil
			})
		return list, err
	}
	show := r.params.flag("show_records")
	for _, counter := range r.params.set("counters") {
		entry := JoinCounter{Counter: counter}
		switch counter {
		case "interface", "rule":
			value := interfaceMisses
			if counter == "rule" {
				value = ruleMisses
			}
			entry.Unresolved, entry.Of = loggedCount(value), loggedCount(flows)
			if show {
				list, err := records(counter)
				if err != nil {
					return nil, false, err
				}
				entry.Records = list
			}
		case "client_identity":
			var total, addressOnly int64
			list := []UnresolvedRecord{}
			err := r.db.WidgetRows(r.ctx, "widget_window_clients", store.Period{}, parameters,
				func(row store.RowScanner) error {
					var (
						id          int64
						kind        string
						address     store.NullString
						interfaceID store.NullInt64
					)
					if err := row.Scan(&id, &kind, &address, &interfaceID); err != nil {
						return err
					}
					total++
					if kind == "address_in_interface" {
						addressOnly++
						if show && len(list) < unresolvedRecordLimit {
							copied := id
							list = append(list, UnresolvedRecord{ClientID: &copied, Address: nullableText(address),
								State: kind})
						}
					}
					return nil
				})
			if err != nil {
				return nil, false, err
			}
			entry.Unresolved = Count{Measure: "clients", LowerBound: true, Value: addressOnly}
			entry.Of = Count{Measure: "clients", LowerBound: true, Value: total}
			if show {
				entry.Records = list
			}
		case "geo":
			var total, unplaced int64
			list := []UnresolvedRecord{}
			err := r.db.WidgetRows(r.ctx, "widget_window_peers", store.Period{}, parameters,
				func(row store.RowScanner) error {
					var address, state string
					if err := row.Scan(&address, &state); err != nil {
						return err
					}
					total++
					if state != "resolved" {
						unplaced++
						if show && len(list) < unresolvedRecordLimit {
							copied := address
							list = append(list, UnresolvedRecord{Address: &copied, State: state})
						}
					}
					return nil
				})
			if err != nil {
				return nil, false, err
			}
			entry.Unresolved = Count{Measure: "addresses", LowerBound: true, Value: unplaced}
			entry.Of = Count{Measure: "addresses", LowerBound: true, Value: total}
			if show {
				entry.Records = list
			}
		}
		data.Counters = append(data.Counters, entry)
	}
	return data, flows > 0, nil
}
