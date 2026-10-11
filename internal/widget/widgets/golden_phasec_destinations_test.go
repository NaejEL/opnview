package widgets

// The fields C2 (unnamed destinations by address, operator and country) and C4 (ports and
// protocols) of the backend-completion cycle change by name, for AC B5's byte-identity record
// (golden_phasec_test.go).
func init() {
	// C2: address rows rank beside the site rows, every row carries its kind, and the totals,
	// remainders and names below min_flows count the address rows too.
	registerPhaseCChange("top_sites", "data.rows", "data.remainder", "data.below_min_flows", "data.total")
	// C2: each client's cells hold its address rows beside its names, which reorders the clients by
	// their eligible flows.
	registerPhaseCChange("sites_by_client", "data.rows")
	// C2 and C4: below the operator, a site node or an address node (no site:none); below it, the
	// service port and protocol at depth 3.
	registerPhaseCChange("connection_tree", "data.outside[].children")
	// C2: each unnamed bin lists its addresses, counted once each.
	registerPhaseCChange("traffic_sankey", "data.other.unnamed")
	// C4: a service-port slice is a port and a protocol together (port:53/udp), and the adopted keys
	// split_by, window_seconds and line_interpolation are echoed at their defaults.
	registerPhaseCChange("traffic_composition", "data.slices", "data.other", "parameters.split_by",
		"parameters.window_seconds", "parameters.line_interpolation")
	// C4: an address target carries its port and protocol.
	registerPhaseCChange("unified_blocked_feed", "data.entries[].port", "data.entries[].protocol")
}
