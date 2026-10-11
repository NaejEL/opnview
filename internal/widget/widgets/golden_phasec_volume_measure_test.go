package widgets

// PS22 of the NetFlow join amendment to specs/SPEC-backend-completion-plugins.md, with the maintainer's
// decision (b) of 11 October 2026: every type serving a Volume or a byte Count takes volume_measure,
// best_available by default, and a best_available answer that falls back to logged is byte-identical to
// the logged answer apart from two things, both admitted by name (B5 / X3 as amended):
//   - the echoed key, parameters.volume_measure;
//   - the named state in states that says why it fell back -- on the default fixture, which has no
//     active provider of flow_record, netflow_not_configured.
//
// Every other byte of each answer is still held to the record: the key is removed alone, and the
// state alone from the list, whatever else the list holds.

// volumeMeasureTypes are the built-in types whose manifest declares volumes.
var volumeMeasureTypes = []string{"attribution_rate_per_client", "blocked_traffic_world_map",
	"client_traffic_detail", "client_volume_ranking", "connection_tree", "custom_chart",
	"destination_countries", "destination_operators", "interface_traffic_matrix", "interface_volume_ranking",
	"owner_activity", "passed_traffic_world_map", "sites_by_client", "top_sites", "traffic_composition",
	"traffic_over_time_by_scope", "traffic_sankey"}

func init() {
	for _, widgetType := range volumeMeasureTypes {
		registerPhaseCChange(widgetType, "parameters.volume_measure")
		registerPhaseCValue(widgetType, "states", "netflow_not_configured")
	}
}
