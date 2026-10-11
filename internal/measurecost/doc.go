// Package measurecost is the LV-C measuring tool (C3.0 of the backend-completion cycle, and LV-C of
// its broker amendment): it measures what polling one firewall endpoint at candidate cadences costs
// the firewall's processor, so the live cadence and the freshness floors are set from a measurement
// rather than a guess.
//
// It samples the firewall's own processor stream (/api/diagnostics/cpu_usage/stream) for an idle
// baseline, then polls the endpoint at each candidate cadence for a set duration while sampling it,
// and reports per cadence the processor percentage points added over the baseline, the call latency
// (median and 95th percentile) and the response size, flagging the finest cadence adding at most two
// points as the endpoint's freshness floor. Every request goes through the broker, under its
// read-only rule; nothing is written to the firewall, and the report holds proportions and timings
// only, never a field the firewall answered. `opnview measure-cost` runs it
// (docs/opnsense-api-survey.md, "LV-C: measuring the firewall-side cost").
package measurecost
