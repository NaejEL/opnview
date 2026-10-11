package widget

// The registry: one row per served preset. Adding a preset's endpoint is adding its file
// and its row here, and nothing else (ROADMAP.md, "Modularity"): the HTTP routes, the
// parameter handling and the envelope all follow from the row.
//
// The three alert presets -- security_alerts_over_time, alerts_by_signature and
// alerts_by_client_and_interface -- have no row: they belong to step 6 (severity
// resolution, alert aggregation, G7 and G8), and until then they answer
// unknown_widget_type like any other type with no endpoint (decision 1). No placeholder is
// built for them.
var registry = []Preset{
	interfaceTrafficMatrix,
	trafficOverTimeByScope,
	interfaceVolumeRanking,
	clientVolumeRanking,
	clientTrafficDetail,
	trafficComposition,
	connectionTree,
	ownerActivity,
	topSites,
	sitesByClient,
	attributionRatePerClient,
	passedTrafficWorldMap,
	blockedTrafficWorldMap,
	destinationCountries,
	destinationOperators,
	trafficSankey,
	unifiedBlockedFeed,
	blockedByFirewallRule,
	blockedDNSLookups,
	publicAddress,
	firewallHealthOverview,
	interfaceThroughput,
	customChart,
	sourceAvailability,
	aggregateFreshness,
	unresolvedJoins,
}
