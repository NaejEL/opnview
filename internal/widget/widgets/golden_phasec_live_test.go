package widgets

import "strings"

// The fields C3 of the backend-completion cycle changes by name: every current rate -- a chip rate,
// whatever widget carries it -- names the span one sample covers, the cadence it was taken at and
// that it is a lower bound (docs/widget-api.md, "Live figures"). Nothing else of these answers
// changed; a rate older than twice its cadence reads the new state stale_sample, which no recorded
// answer holds.
func init() {
	added := []string{"span_seconds", "cadence_seconds", "lower_bound"}
	rates := map[string][]string{
		"traffic_composition": {"data.current_rates.by_direction[].rate", "data.current_rates.by_member[].rate",
			"data.current_rates.outbound_unsplit"},
		"owner_activity":        {"data.cards[].chips[].current_rate", "data.unassigned.chips[].current_rate"},
		"client_traffic_detail": {"data.current_rate"},
	}
	for widgetType, prefixes := range rates {
		for _, prefix := range prefixes {
			for _, field := range added {
				registerPhaseCChange(widgetType, strings.Join([]string{prefix, field}, "."))
			}
		}
	}
}
