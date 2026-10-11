package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// blocked_by_firewall_rule: rules ranked by blocked connections, each naming its
// description, its pf label, the interfaces it fired on and the interfaces and clients it
// blocked (docs/widget-catalogue.md, "Blocked by firewall rule"). A rid that matches no
// known rule is a first-class row, rule_not_found: a rule can be removed after it logged.

var blockedByFirewallRule = Preset{
	Type: "blocked_by_firewall_rule", Shape: "table", Windowed: true,
	Providers: []string{"firewall_log"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		clientsParam(),
		limitParam(20),
		{Key: "include_automatic", Kind: KindBoolean, Default: true, Data: true},
		{Key: "sort", Kind: KindEnum, Values: []string{"blocked", "clients", "recency"}, Default: "blocked", Data: true},
	},
	Serve: serveBlockedByFirewallRule,
}

// FiredOn is a device a rule's records were logged on.
type FiredOn struct {
	Device string `json:"device"`
	// Description is the interface map's, null when the device matched no entry.
	Description *string `json:"description"`
	InterfaceID *int64  `json:"interface_id"`
}

// RuleRow is one rule, or one rid no rule carries.
type RuleRow struct {
	// State is resolved, or rule_not_found.
	State      string    `json:"state"`
	Rule       *RuleInfo `json:"rule"`
	Rid        *string   `json:"rid"`
	FiredOn    []FiredOn `json:"fired_on"`
	Interfaces []int64   `json:"interfaces"`
	Clients    []int64   `json:"clients"`
	Blocked    Count     `json:"blocked_connections"`
	LastSeenAt int64     `json:"last_seen_at"`
	// Timeline is the rule's blocked records per bucket of the window: a bucket with none
	// reads 0 where the window is covered and no gap meets it, and is a gap otherwise.
	Timeline []Bucket `json:"timeline"`
}

// RuleTableData is the table.
type RuleTableData struct {
	Rows      []RuleRow `json:"rows"`
	Remainder Remainder `json:"remainder"`
	// ExcludedAutomatic is the automatic rules include_automatic = false left out.
	ExcludedAutomatic Remainder `json:"excluded_automatic"`
	Total             Count     `json:"total"`
}

func serveBlockedByFirewallRule(r *Request) (any, bool, error) {
	if err := r.coverFlows("rules_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	parameters := r.windowParameters()
	parameters["client_ids"] = jsonIDs(r.ids("clients"))
	parameters["interface_ids"] = jsonIDs(r.ids("interfaces"))
	type accumulator struct {
		row        RuleRow
		automatic  bool
		devices    map[string]FiredOn
		interfaces map[int64]bool
		clients    map[int64]bool
	}
	rules := map[string]*accumulator{}
	var total int64
	err := r.db.WidgetRows(r.ctx, "widget_blocked_by_rule", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				ruleID, automatic, mapInterface, srcInterface, srcClient store.NullInt64
				rid, description, pfLabel, mapDescription                store.NullString
				device                                                   string
				count, newest                                            int64
			)
			if err := row.Scan(&ruleID, &rid, &description, &pfLabel, &automatic, &device, &mapDescription,
				&mapInterface, &srcInterface, &srcClient, &count, &newest); err != nil {
				return err
			}
			key := "rid:" + rid.String
			if ruleID.Valid {
				key = "rule:" + strconv.FormatInt(ruleID.Int64, 10)
			}
			entry := rules[key]
			if entry == nil {
				entry = &accumulator{row: RuleRow{State: "rule_not_found", Rid: nullableText(rid)},
					devices: map[string]FiredOn{}, interfaces: map[int64]bool{}, clients: map[int64]bool{}}
				if ruleID.Valid {
					entry.row.State = "resolved"
					entry.row.Rule = &RuleInfo{ID: ruleID.Int64, PfLabel: pfLabel.String,
						Description: description.String, Automatic: automatic.Int64 == 1}
					entry.automatic = automatic.Int64 == 1
				}
				rules[key] = entry
			}
			entry.row.Blocked.Value += count
			entry.row.LastSeenAt = max(entry.row.LastSeenAt, newest)
			entry.devices[device] = FiredOn{Device: device, Description: nullableText(mapDescription),
				InterfaceID: nullableNumber(mapInterface)}
			if srcInterface.Valid {
				entry.interfaces[srcInterface.Int64] = true
			}
			if srcClient.Valid {
				entry.clients[srcClient.Int64] = true
			}
			total += count
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	data := RuleTableData{Rows: []RuleRow{}, Total: loggedCount(total)}
	var rows []RuleRow
	excluded := loggedCount(0)
	for _, key := range sortedKeys(rules) {
		entry := rules[key]
		entry.row.Blocked.Measure, entry.row.Blocked.LowerBound = MeasureLogged, true
		if !r.params.flag("include_automatic") && entry.automatic {
			data.ExcludedAutomatic.Members++
			excluded.Value += entry.row.Blocked.Value
			continue
		}
		for _, device := range sortedKeys(entry.devices) {
			entry.row.FiredOn = append(entry.row.FiredOn, entry.devices[device])
		}
		entry.row.Interfaces = interfaceIDs(entry.interfaces)
		entry.row.Clients = interfaceIDs(entry.clients)
		rows = append(rows, entry.row)
	}
	data.ExcludedAutomatic.Count = &excluded
	order := r.params.text("sort")
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch order {
		case "clients":
			if len(a.Clients) != len(b.Clients) {
				return len(a.Clients) > len(b.Clients)
			}
		case "recency":
			if a.LastSeenAt != b.LastSeenAt {
				return a.LastSeenAt > b.LastSeenAt
			}
		default:
			if a.Blocked.Value != b.Blocked.Value {
				return a.Blocked.Value > b.Blocked.Value
			}
		}
		return false
	})
	limit := int(r.params.integer("limit"))
	rest := loggedCount(0)
	for index, row := range rows {
		if index < limit {
			data.Rows = append(data.Rows, row)
			continue
		}
		data.Remainder.Members++
		rest.Value += row.Blocked.Value
	}
	data.Remainder.Count = &rest
	if err := r.ruleTimelines(data.Rows, parameters); err != nil {
		return nil, false, err
	}
	return data, total > 0, nil
}

// ruleTimelines fills each shown rule's inline timeline.
func (r *Request) ruleTimelines(rows []RuleRow, parameters map[string]any) error {
	width := r.bucketWidth()
	parameters["width"] = width
	counts := map[string]map[int64]int64{}
	err := r.db.WidgetRows(r.ctx, "widget_blocked_rule_buckets", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				ruleID       store.NullInt64
				rid          store.NullString
				start, count int64
			)
			if err := row.Scan(&ruleID, &rid, &start, &count); err != nil {
				return err
			}
			key := "rid:" + rid.String
			if ruleID.Valid {
				key = "rule:" + strconv.FormatInt(ruleID.Int64, 10)
			}
			if counts[key] == nil {
				counts[key] = map[int64]int64{}
			}
			counts[key][start] += count
			return nil
		})
	if err != nil {
		return err
	}
	for index := range rows {
		key := "rid:"
		if rows[index].Rid != nil {
			key += *rows[index].Rid
		}
		if rows[index].Rule != nil {
			key = "rule:" + strconv.FormatInt(rows[index].Rule.ID, 10)
		}
		rows[index].Timeline = []Bucket{}
		for _, interval := range bucketIntervals(r.window, width) {
			start := interval.From - floorMod(interval.From, width)
			count, present := counts[key][start]
			bucket := Bucket{From: interval.From, To: interval.To,
				Gap: isGapBucket(interval, r.response.Window.Covered, r.response.Gaps)}
			if present || !bucket.Gap {
				value := float64(count)
				bucket.Value = &value
			}
			rows[index].Timeline = append(rows[index].Timeline, bucket)
		}
	}
	return nil
}
