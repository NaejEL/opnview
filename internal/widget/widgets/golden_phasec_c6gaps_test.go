package widgets

// The fields the gaps closed after the C6 audit add by name (rows 4, 18, 24, E7 and E8 of
// docs/widget-api.md, "Coverage of the mockup"). Each is a new member; every other byte of these
// answers, the logged figures included, is still held to the record.
//
//   - owner_activity: a card's rate now and interface list over every machine, and the machines
//     folded past clients_per_card, listed with their rate (rows 18 and E7);
//   - traffic_composition: the members folded into Other, the whole's and each ring's (row E8), and,
//     for the protocol and service-port groupings, where the rate by member is read from (row 4):
//     by_member_source. by_member itself is still held to the record: the fixture has no pf_states;
//   - client_traffic_detail: the client's current rate per peer (row 24).
func init() {
	for _, card := range []string{"data.cards[]", "data.unassigned"} {
		for _, field := range []string{"current_rate", "current_rate_clients", "interfaces",
			"chip_remainder.current_rate", "chip_remainder.chips"} {
			registerPhaseCChange("owner_activity", card+"."+field)
		}
	}
	registerPhaseCChange("traffic_composition", "data.other.slices", "data.rings[].other.slices",
		"data.current_rates.by_member_source")
	registerPhaseCChange("client_traffic_detail", "data.current_rate_by_peer")
}
