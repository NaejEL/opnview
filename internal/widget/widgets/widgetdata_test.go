package widgets

import (
	"encoding/json"
	"testing"

	"github.com/NaejEL/opnview/internal/widget"
)

// The data of the built-in widgets, as a client reads it.
//
// A TEST READS A WIDGET'S DATA THROUGH ITS JSON: every built-in widget is a plugin whose script
// returns its data as JSON. The shapes below are the response shapes docs/widget-api.md documents,
// declared here once; every field they carry is a JSON field of the response.

// decodeData reads a response's data as T, through its JSON.
func decodeData[T any](t *testing.T, response widget.Response) T {
	t.Helper()
	encoded, err := json.Marshal(response.Data)
	check(t, err)
	var decoded T
	check(t, json.Unmarshal(encoded, &decoded))
	return decoded
}

// siteRow is one row of a site table (top_sites, sites_by_client): one site name, or one
// registrable domain grouping several.
type siteRow struct {
	Name             string              `json:"name"`
	SiteNames        []string            `json:"site_names,omitempty"`
	Flows            widget.Count        `json:"flows"`
	Bytes            widget.Count        `json:"bytes"`
	Clients          int64               `json:"clients"`
	ClientIDs        []int64             `json:"client_ids"`
	Methods          widget.MethodCounts `json:"methods"`
	MeanDelaySeconds *float64            `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64              `json:"max_delay_seconds"`
	CountryCode      *string             `json:"country_code"`
	ASN              *int64              `json:"asn"`
	Operator         *string             `json:"operator"`
}

// attributionRow is one client's attribution, or a whole table's.
type attributionRow struct {
	Client           widget.ClientInfo   `json:"client"`
	EligibleFlows    widget.Count        `json:"eligible_flows"`
	EligibleBytes    *widget.Count       `json:"eligible_bytes,omitempty"`
	NamedFlows       widget.Count        `json:"named_flows"`
	Methods          widget.MethodCounts `json:"methods"`
	Rate             *float64            `json:"rate"`
	RateState        string              `json:"rate_state"`
	MeanDelaySeconds *float64            `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64              `json:"max_delay_seconds"`
}

// destination_countries, destination_operators.
type destinationsData struct {
	Group string `json:"group"`
	Rows  []struct {
		Key            string         `json:"key"`
		CountryCode    *string        `json:"country_code,omitempty"`
		CountryName    *string        `json:"country_name,omitempty"`
		ASN            *int64         `json:"asn,omitempty"`
		Operator       *string        `json:"operator,omitempty"`
		Bytes          widget.Count   `json:"bytes"`
		Allowed        *widget.Volume `json:"allowed,omitempty"`
		Blocked        *widget.Volume `json:"blocked,omitempty"`
		Peers          int64          `json:"peers"`
		Interfaces     []int64        `json:"interfaces"`
		DatasetBuildAt *int64         `json:"dataset_build_at"`
	} `json:"rows"`
	Remainder  widget.Remainder     `json:"remainder"`
	Unresolved []widget.UnplacedBin `json:"unresolved"`
	Total      widget.Volume        `json:"total"`
}

// aggregate_freshness.
type freshnessData struct {
	Periods []struct {
		Period          string        `json:"period"`
		Slots           int64         `json:"slots"`
		EarliestStartAt *int64        `json:"earliest_period_start_at"`
		LatestEndAt     *int64        `json:"latest_period_end_at"`
		LastComputedAt  *int64        `json:"last_computed_at"`
		StaleSlots      int64         `json:"stale_slots"`
		Stale           bool          `json:"stale"`
		Bytes           *widget.Count `json:"bytes,omitempty"`
		ReadFrom        int64         `json:"read_from"`
	} `json:"periods"`
}

// attribution_rate_per_client.
type attributionData struct {
	ResolverCovered bool             `json:"resolver_covered"`
	CacheCovered    bool             `json:"cache_covered"`
	Rows            []attributionRow `json:"rows"`
	Remainder       widget.Remainder `json:"remainder"`
}

// blocked_by_firewall_rule.
type ruleTableData struct {
	Rows []struct {
		State   string           `json:"state"`
		Rule    *widget.RuleInfo `json:"rule"`
		Rid     *string          `json:"rid"`
		FiredOn []struct {
			Device      string  `json:"device"`
			Description *string `json:"description"`
			InterfaceID *int64  `json:"interface_id"`
		} `json:"fired_on"`
		Interfaces []int64         `json:"interfaces"`
		Clients    []int64         `json:"clients"`
		Blocked    widget.Count    `json:"blocked_connections"`
		LastSeenAt int64           `json:"last_seen_at"`
		Timeline   []widget.Bucket `json:"timeline"`
	} `json:"rows"`
	Remainder         widget.Remainder `json:"remainder"`
	ExcludedAutomatic widget.Remainder `json:"excluded_automatic"`
	Total             widget.Count     `json:"total"`
}

// blocked_dns_lookups.
type blockedLookupsData struct {
	Rows []struct {
		Domain     *string      `json:"domain"`
		List       *string      `json:"list"`
		Purpose    string       `json:"purpose"`
		Lookups    widget.Count `json:"lookups"`
		Clients    []int64      `json:"clients"`
		Addresses  []string     `json:"unmatched_client_addresses"`
		Interfaces []int64      `json:"interfaces"`
		LastSeenAt int64        `json:"last_seen_at"`
	} `json:"rows"`
	Remainder     widget.Remainder `json:"remainder"`
	BelowMinCount widget.Remainder `json:"below_min_count"`
	Total         widget.Count     `json:"total"`
}

// client_traffic_detail.
type clientDetailData struct {
	Client      widget.ClientInfo `json:"client"`
	CurrentRate widget.ChipRate   `json:"current_rate"`
	Leases      []struct {
		ProviderKind   string `json:"provider_kind"`
		ProviderKey    string `json:"provider_key"`
		Backend        string `json:"backend"`
		Address        string `json:"address"`
		LastObservedAt int64  `json:"last_observed_at"`
		ExpiresAt      *int64 `json:"expires_at"`
	} `json:"leases"`
	Records []struct {
		FlowID                  int64          `json:"flow_id"`
		ObservedAt              int64          `json:"observed_at"`
		Destination             string         `json:"destination"`
		Port                    *int64         `json:"port"`
		Protocol                string         `json:"protocol"`
		Action                  string         `json:"action"`
		Scope                   string         `json:"scope"`
		Bytes                   widget.Count   `json:"bytes"`
		Geo                     widget.GeoInfo `json:"geo"`
		SiteName                *string        `json:"site_name"`
		Method                  *string        `json:"method"`
		CorrelationDelaySeconds *int64         `json:"correlation_delay_seconds"`
		InterfaceDevice         string         `json:"interface_device"`
		InterfaceDescription    *string        `json:"interface_description"`
	} `json:"records"`
	Remainder widget.Remainder `json:"remainder"`
	Total     widget.Remainder `json:"total"`
}

// client_volume_ranking.
type clientRankingData struct {
	Measure string `json:"measure"`
	Bars    []struct {
		Client       widget.ClientInfo     `json:"client"`
		Interface    *widget.InterfaceInfo `json:"interface"`
		Value        float64               `json:"value"`
		Volume       widget.Volume         `json:"volume"`
		Destinations int64                 `json:"destinations"`
	} `json:"bars"`
	Remainder widget.Remainder `json:"remainder"`
	Excluded  widget.Remainder `json:"excluded"`
	Total     widget.Volume    `json:"total"`
}

// connection_tree.
type treeNode struct {
	Key         string                `json:"key"`
	Value       float64               `json:"value"`
	Bytes       widget.Count          `json:"bytes"`
	Connections widget.Count          `json:"connections"`
	Blocked     widget.Count          `json:"blocked_connections"`
	LastSeenAt  *int64                `json:"last_seen_at,omitempty"`
	Remainder   *int64                `json:"remainder_members,omitempty"`
	Interface   *widget.InterfaceInfo `json:"interface,omitempty"`
	Client      *widget.ClientInfo    `json:"client,omitempty"`
	Owner       *widget.OwnerInfo     `json:"owner,omitempty"`
	Operator    *string               `json:"operator,omitempty"`
	SiteName    *string               `json:"site_name,omitempty"`
	Children    []*treeNode           `json:"children,omitempty"`
}

type treeData struct {
	Root    string      `json:"root"`
	Measure string      `json:"measure"`
	Depth   int64       `json:"depth"`
	Inside  []*treeNode `json:"inside"`
	Outside []*treeNode `json:"outside"`
	Total   treeNode    `json:"total"`
}

// custom_chart.
type customChartData struct {
	BucketSeconds int64           `json:"bucket_seconds"`
	Series        []widget.Series `json:"series"`
	Annotations   []struct {
		Kind    string          `json:"kind"`
		Buckets []widget.Bucket `json:"buckets"`
	} `json:"annotations"`
}

// interface_traffic_matrix.
type matrixAxis struct {
	Key       string                `json:"key"`
	Interface *widget.InterfaceInfo `json:"interface,omitempty"`
}

type matrixData struct {
	Measure string       `json:"measure"`
	Rows    []matrixAxis `json:"rows"`
	Columns []matrixAxis `json:"columns"`
	Cells   []struct {
		Row    string        `json:"row"`
		Column string        `json:"column"`
		Value  float64       `json:"value"`
		Volume widget.Volume `json:"volume"`
		Rules  *[]struct {
			RuleID      *int64       `json:"rule_id"`
			Description *string      `json:"description"`
			Rid         *string      `json:"rid"`
			State       string       `json:"state"`
			Connections widget.Count `json:"connections"`
		} `json:"rules,omitempty"`
	} `json:"cells"`
	Total widget.Volume `json:"total"`
}

// interface_volume_ranking.
type interfaceRankingData struct {
	Measure string `json:"measure"`
	Bars    []struct {
		Interface       widget.InterfaceInfo `json:"interface"`
		Value           float64              `json:"value"`
		Volume          widget.Volume        `json:"volume"`
		EastWest        widget.Volume        `json:"east_west"`
		NorthSouth      widget.Volume        `json:"north_south"`
		ThisFirewall    widget.Volume        `json:"this_firewall"`
		ExitLegs        widget.Volume        `json:"exit_legs"`
		ClientsSeen     int64                `json:"clients_seen"`
		ClientsKnown    int64                `json:"clients_known"`
		RulesNotLogging int64                `json:"rules_not_logging"`
	} `json:"bars"`
	Remainder                 widget.Remainder  `json:"remainder"`
	ExcludedUnlabelled        *widget.Remainder `json:"excluded_unlabelled,omitempty"`
	Unattributed              widget.Volume     `json:"unattributed"`
	Total                     widget.Volume     `json:"total"`
	FloatingRulesNotLogging   int64             `json:"floating_rules_not_logging"`
	UnresolvedRulesNotLogging int64             `json:"unresolved_rules_not_logging"`
	RulesLoggingNotReported   int64             `json:"rules_logging_not_reported"`
}

// owner_activity.
type ownerCard struct {
	Key      string            `json:"key"`
	Owner    *widget.OwnerInfo `json:"owner"`
	Value    float64           `json:"value"`
	Volume   widget.Volume     `json:"volume"`
	Machines int64             `json:"machines"`
	Chips    []struct {
		Client      widget.ClientInfo `json:"client"`
		State       string            `json:"state"`
		Volume      *widget.Volume    `json:"volume"`
		CurrentRate widget.ChipRate   `json:"current_rate"`
	} `json:"chips"`
	ChipRemainder widget.Remainder `json:"chip_remainder"`
	Timeline      []widget.Bucket  `json:"timeline,omitempty"`
}

type ownerActivityData struct {
	Measure                 string        `json:"measure"`
	Cards                   []ownerCard   `json:"cards"`
	Unassigned              *ownerCard    `json:"unassigned"`
	TotalsExcludeUnassigned bool          `json:"totals_exclude_unassigned"`
	Total                   widget.Volume `json:"total"`
}

// public_address.
type heldAddress struct {
	Address        string `json:"address"`
	Family         string `json:"family"`
	PrefixLength   *int64 `json:"prefix_length"`
	SourceField    string `json:"source_field"`
	SpecialPurpose *struct {
		Block             string `json:"block"`
		Name              string `json:"name"`
		Reference         string `json:"reference"`
		GloballyReachable *bool  `json:"globally_reachable"`
	} `json:"special_purpose"`
	Public      bool   `json:"public"`
	FirstSeenAt int64  `json:"first_seen_at"`
	LastSeenAt  int64  `json:"last_seen_at"`
	ChangedAt   *int64 `json:"changed_at,omitempty"`
	ChangeState string `json:"change_state,omitempty"`
}

type publicAddressData struct {
	Upstreams []struct {
		Interface         widget.InterfaceInfo `json:"interface"`
		Current           []heldAddress        `json:"current"`
		History           []heldAddress        `json:"history,omitempty"`
		HistoryRemainder  *widget.Remainder    `json:"history_remainder,omitempty"`
		Gateways          []heldAddress        `json:"gateways"`
		BehindUpstreamNAT *bool                `json:"behind_upstream_nat"`
	} `json:"upstreams"`
	Registries []string `json:"registries"`
}

// sites_by_client.
type sitesByClientData struct {
	Rows []struct {
		Client        widget.ClientInfo `json:"client"`
		State         string            `json:"state"`
		Sites         []siteRow         `json:"sites"`
		Remainder     widget.Remainder  `json:"remainder"`
		BelowMinFlows widget.Remainder  `json:"below_min_flows"`
		Attribution   *attributionRow   `json:"attribution,omitempty"`
	} `json:"rows"`
}

// top_sites.
type topSitesData struct {
	Grouping      string           `json:"grouping"`
	Rows          []siteRow        `json:"rows"`
	Remainder     widget.Remainder `json:"remainder"`
	BelowMinFlows widget.Remainder `json:"below_min_flows"`
	Total         widget.Remainder `json:"total"`
	Attribution   attributionRow   `json:"attribution"`
}

// traffic_composition.
type compositionData struct {
	GroupBy string `json:"group_by"`
	Measure string `json:"measure"`
	Slices  []struct {
		Key       string                `json:"key"`
		Interface *widget.InterfaceInfo `json:"interface,omitempty"`
		Client    *widget.ClientInfo    `json:"client,omitempty"`
		Protocol  *string               `json:"protocol,omitempty"`
		Port      *int64                `json:"port,omitempty"`
		Value     float64               `json:"value"`
		Volume    widget.Volume         `json:"volume"`
	} `json:"slices"`
	Other        widget.Remainder     `json:"other"`
	Whole        string               `json:"whole"`
	Total        widget.Volume        `json:"total"`
	CurrentRates *widget.CurrentRates `json:"current_rates"`
}

// traffic_over_time_by_scope.
type timeSeriesData struct {
	BucketSeconds int64           `json:"bucket_seconds"`
	Series        []widget.Series `json:"series"`
}

// traffic_sankey.
type sankeyData struct {
	Left    string `json:"left"`
	Right   string `json:"right"`
	Measure string `json:"measure"`
	Nodes   []struct {
		Key       string                `json:"key"`
		Interface *widget.InterfaceInfo `json:"interface,omitempty"`
		Client    *widget.ClientInfo    `json:"client,omitempty"`
		ASN       *int64                `json:"asn,omitempty"`
		Operator  *string               `json:"operator,omitempty"`
		Country   *string               `json:"country_code,omitempty"`
	} `json:"nodes"`
	Ribbons []struct {
		Left         string       `json:"left"`
		Right        string       `json:"right"`
		Value        float64      `json:"value"`
		Bytes        widget.Count `json:"bytes"`
		Connections  widget.Count `json:"connections"`
		BlockedShare *float64     `json:"blocked_share,omitempty"`
	} `json:"ribbons"`
	Other struct {
		Ribbons     int64                `json:"ribbons"`
		Bytes       widget.Count         `json:"bytes"`
		Connections widget.Count         `json:"connections"`
		Unnamed     []widget.UnplacedBin `json:"unnamed"`
	} `json:"other"`
	Total widget.Volume `json:"total"`
}

// unified_blocked_feed.
type feedData struct {
	GroupBy string `json:"group_by"`
	Entries []struct {
		SourceTable string `json:"source_table"`
		SourceID    int64  `json:"source_id"`
		OccurredAt  int64  `json:"occurred_at"`
		EngineKind  string `json:"engine_kind"`
		DecidedBy   struct {
			Kind      string           `json:"kind"`
			Reference *string          `json:"reference"`
			Rule      *widget.RuleInfo `json:"rule,omitempty"`
		} `json:"decided_by"`
		ClientID             *int64                `json:"client_id"`
		Interface            *widget.InterfaceInfo `json:"interface"`
		Target               *string               `json:"target"`
		TargetKind           string                `json:"target_kind"`
		TargetIsThisFirewall bool                  `json:"target_is_this_firewall"`
	} `json:"entries"`
	Groups []struct {
		Key   string       `json:"key"`
		Count widget.Count `json:"count"`
	} `json:"groups,omitempty"`
	Remainder widget.Remainder `json:"remainder"`
	Sources   []struct {
		ProviderKind string       `json:"provider_kind"`
		Condition    string       `json:"condition"`
		Count        widget.Count `json:"count"`
	} `json:"sources"`
	CountsByEngineKind  map[string]int64 `json:"counts_by_engine_kind"`
	ExcludedEngineKinds map[string]int64 `json:"excluded_engine_kinds"`
	Total               widget.Count     `json:"total"`
}
