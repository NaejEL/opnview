package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// unified_blocked_feed: every refusal of the window, newest first, over the four engines,
// each entry naming the engine, the client, the target and -- the load-bearing field --
// the rule, list or signature that produced the decision (docs/widget-catalogue.md,
// "Unified blocked feed"). It reads the blocked_decision view, which places each refusal
// once under one engine_kind vocabulary. Each contributing source carries its own
// condition, so an empty feed is never read as "nothing happened" unless every contributor
// is healthy.

var unifiedBlockedFeed = Preset{
	Type: "unified_blocked_feed", Shape: "feed", Windowed: true,
	Providers: []string{"firewall_log", "dns_lookup", "security_event"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "engines", Kind: KindEnumSet, Values: feedEngines, Default: stringsAsAny(feedEngines), Data: true},
		interfacesParam(),
		clientsParam(),
		limitParam(50),
		{Key: "group_by", Kind: KindEnum, Values: []string{"none", "engine", "client", "target"}, Default: "none",
			Data: true},
	},
	Serve: serveUnifiedBlockedFeed,
}

// feedEngines are the four engines a reader selects.
var feedEngines = []string{"firewall_rule", "dns_advertising_list", "dns_threat_list", "security_engine"}

// engineKinds maps a selected engine to the blocked_decision engine kinds it covers.
var engineKinds = map[string][]string{
	"firewall_rule":        {"firewall_rule", "firewall_no_rule", "firewall_reason_not_recorded"},
	"dns_advertising_list": {"dns_advertising_list"},
	"dns_threat_list":      {"dns_threat_list"},
	"security_engine":      {"security_engine"},
}

// engineProvider is the provider kind behind each engine kind.
func engineProvider(kind string) string {
	switch {
	case kind == "security_engine":
		return "security_event"
	case len(kind) > 4 && kind[:4] == "dns_":
		return "dns_lookup"
	}
	return "firewall_log"
}

// FeedEntry is one refusal.
type FeedEntry struct {
	SourceTable string `json:"source_table"`
	SourceID    int64  `json:"source_id"`
	OccurredAt  int64  `json:"occurred_at"`
	// EngineKind is the blocked_decision vocabulary.
	EngineKind string `json:"engine_kind"`
	// DecidedBy names what produced the decision: a rule, a list or a signature.
	DecidedBy FeedDecider    `json:"decided_by"`
	ClientID  *int64         `json:"client_id"`
	Interface *InterfaceInfo `json:"interface"`
	// Target is the address, or the domain a DNS engine refused -- null when the aggregate
	// mode withholds domains.
	Target               *string `json:"target"`
	TargetKind           string  `json:"target_kind"`
	TargetIsThisFirewall bool    `json:"target_is_this_firewall"`
}

// FeedDecider is the rule, list or signature behind a refusal.
type FeedDecider struct {
	// Kind is rule, rule_not_found, no_rule, reason_not_recorded, list,
	// list_not_recorded or signature.
	Kind      string    `json:"kind"`
	Reference *string   `json:"reference"`
	Rule      *RuleInfo `json:"rule,omitempty"`
}

// FeedGroup is one group of a grouped feed, over the whole window.
type FeedGroup struct {
	Key   string `json:"key"`
	Count Count  `json:"count"`
}

// FeedSource is one contributing source and its condition.
type FeedSource struct {
	ProviderKind string `json:"provider_kind"`
	Condition    string `json:"condition"`
	Count        Count  `json:"count"`
}

// FeedData is the feed.
type FeedData struct {
	GroupBy   string       `json:"group_by"`
	Entries   []FeedEntry  `json:"entries"`
	Groups    []FeedGroup  `json:"groups,omitempty"`
	Remainder Remainder    `json:"remainder"`
	Sources   []FeedSource `json:"sources"`
	// CountsByEngineKind are the refusals of the window per engine kind.
	CountsByEngineKind map[string]int64 `json:"counts_by_engine_kind"`
	// ExcludedEngineKinds are the refusals the engine selection left out, by kind, so a
	// narrowed feed says what it narrowed.
	ExcludedEngineKinds map[string]int64 `json:"excluded_engine_kinds"`
	Total               Count            `json:"total"`
}

func serveUnifiedBlockedFeed(r *Request) (any, bool, error) {
	if err := r.coverFlows("records_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	selectedEngines := r.params.set("engines")
	var kinds []string
	if len(selectedEngines) < len(feedEngines) {
		kinds = []string{}
		for _, engine := range selectedEngines {
			kinds = append(kinds, engineKinds[engine]...)
		}
	}
	parameters := r.windowParameters()
	parameters["engine_kinds"] = jsonTexts(kinds)
	parameters["client_ids"] = jsonIDs(r.ids("clients"))
	parameters["interface_ids"] = jsonIDs(r.ids("interfaces"))
	parameters["limit"] = r.params.integer("limit")
	data := FeedData{GroupBy: r.params.text("group_by"), Entries: []FeedEntry{}, Sources: []FeedSource{},
		CountsByEngineKind: map[string]int64{}, ExcludedEngineKinds: map[string]int64{}}
	err = r.db.WidgetRows(r.ctx, "widget_blocked_feed_counts", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				kind  string
				count int64
			)
			if err := row.Scan(&kind, &count); err != nil {
				return err
			}
			if kinds != nil && !contains(kinds, kind) {
				data.ExcludedEngineKinds[kind] = count
				return nil
			}
			data.CountsByEngineKind[kind] = count
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	var total int64
	perProvider := map[string]int64{}
	for kind, count := range data.CountsByEngineKind {
		total += count
		perProvider[engineProvider(kind)] += count
	}
	for _, kind := range []string{"firewall_log", "dns_lookup", "security_event"} {
		data.Sources = append(data.Sources, FeedSource{ProviderKind: kind,
			Condition: kindCondition(r.response.Availability, kind), Count: refusalCount(perProvider[kind])})
	}
	err = r.db.WidgetRows(r.ctx, "widget_blocked_feed", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				entry         FeedEntry
				reference     store.NullString
				client, iface store.NullInt64
				target        string
				thisFirewall  int64
			)
			if err := row.Scan(&entry.SourceTable, &entry.SourceID, &entry.OccurredAt, &entry.EngineKind,
				&reference, &client, &iface, &target, &entry.TargetKind, &thisFirewall); err != nil {
				return err
			}
			entry.ClientID = nullableNumber(client)
			if iface.Valid {
				if info, known := cache.byID[iface.Int64]; known {
					copied := info
					entry.Interface = &copied
				}
			}
			entry.TargetIsThisFirewall = thisFirewall == 1
			entry.Target = &target
			if entry.TargetKind == "domain" && r.noDomains() {
				entry.Target = nil
				r.withhold("entries[].target")
			}
			entry.DecidedBy = FeedDecider{Reference: nullableText(reference)}
			switch entry.EngineKind {
			case "firewall_rule":
				entry.DecidedBy.Kind = "rule_not_found"
				if reference.Valid {
					var id int64
					found, err := r.db.WidgetRow(r.ctx, "widget_ref_rule_pf_label",
						map[string]any{"value": reference.String}, &id)
					if err != nil {
						return err
					}
					if found {
						rule, err := r.rule(id)
						if err != nil {
							return err
						}
						entry.DecidedBy.Kind, entry.DecidedBy.Rule = "rule", rule
					}
				}
			case "firewall_no_rule":
				entry.DecidedBy.Kind = "no_rule"
			case "firewall_reason_not_recorded":
				entry.DecidedBy.Kind = "reason_not_recorded"
			case "dns_list_not_recorded":
				entry.DecidedBy.Kind = "list_not_recorded"
			case "security_engine":
				entry.DecidedBy.Kind = "signature"
			default:
				entry.DecidedBy.Kind = "list"
			}
			data.Entries = append(data.Entries, entry)
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	switch data.GroupBy {
	case "engine":
		for _, kind := range sortedKeys(data.CountsByEngineKind) {
			data.Groups = append(data.Groups, FeedGroup{Key: kind, Count: refusalCount(data.CountsByEngineKind[kind])})
		}
	case "client":
		err = r.db.WidgetRows(r.ctx, "widget_blocked_feed_by_client", store.Period{}, parameters,
			func(row store.RowScanner) error {
				var (
					client store.NullInt64
					count  int64
				)
				if err := row.Scan(&client, &count); err != nil {
					return err
				}
				key := "client:none"
				if client.Valid {
					key = clientKey(client.Int64)
				}
				data.Groups = append(data.Groups, FeedGroup{Key: key, Count: refusalCount(count)})
				return nil
			})
	case "target":
		err = r.db.WidgetRows(r.ctx, "widget_blocked_feed_by_target", store.Period{}, parameters,
			func(row store.RowScanner) error {
				var (
					target, kind string
					count        int64
				)
				if err := row.Scan(&target, &kind, &count); err != nil {
					return err
				}
				key := kind + ":" + target
				if kind == "domain" && r.noDomains() {
					// The groups of a withheld domain are kept apart without their names.
					key = "domain:withheld:" + strconv.Itoa(len(data.Groups))
					r.withhold("groups[].key")
				}
				data.Groups = append(data.Groups, FeedGroup{Key: key, Count: refusalCount(count)})
				return nil
			})
	}
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(data.Groups, func(i, j int) bool {
		if data.Groups[i].Count.Value != data.Groups[j].Count.Value {
			return data.Groups[i].Count.Value > data.Groups[j].Count.Value
		}
		return data.Groups[i].Key < data.Groups[j].Key
	})
	data.Total = refusalCount(total)
	rest := refusalCount(total - int64(len(data.Entries)))
	data.Remainder = Remainder{Members: total - int64(len(data.Entries)), Count: &rest}
	return data, total > 0, nil
}

// refusalCount is a count of refusals: logged records, lookups and events, each a lower
// bound for the observation-point reason.
func refusalCount(value int64) Count {
	return Count{Measure: "refusals", LowerBound: true, Value: value}
}
