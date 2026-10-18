package widgets

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
)

// Carried item 2 of Lot 4 (specs/SPEC-backend-completion-plugins.md, the open-plugin-API amendment): the
// blocked map's security-engine half counts events, which are not connections of any volume measure. They
// were built as summable figures with no measure, which the core reads as a logged figure, so an answer in a
// declared measure was refused (the NotServed case of internal/widget/request/measure.go) and the map
// answered nothing at 30d with volume_measure netflow. They are now counts in their own measure,
// security_events, served beside the declared measure's figures in every measure.

func init() {
	// The two fields whose shape the fix changes: security_events is a count in its own measure rather than
	// in the measure the request is answered under, and each security_unplaced bin holds its events as such a
	// count rather than as a volume. The golden change is accepted by decision 1 of "Verification of the
	// plugin-API amendment, iteration 1: the orchestrator's decisions (18 October 2026)" in
	// specs/SPEC-backend-completion-plugins.md: security events are their own measure, security_events, and
	// security_unplaced[].volume becomes .events.
	registerPhaseCChange("blocked_traffic_world_map", "data.security_events", "data.security_unplaced")
}

// blockedSecurityEvent stores one blocked event towards an outside address, a minute before the fixture's now.
func (f *fixture) blockedSecurityEvent(t *testing.T, key, destination string) {
	t.Helper()
	ctx := context.Background()
	providerID, err := f.db.ProviderID(ctx, "security_event", "suricata")
	check(t, err)
	interfaceID := int64(1)
	check(t, f.db.InsertSecurityEvent(ctx, providerID, vocab.SecurityEvent{
		ProviderEventKey: key, OccurredAt: f.now - 60, IngestedAt: f.now - 60, RuleIdentity: "1",
		Signature: "example signature", EventAction: "blocked", SrcAddress: "192.0.2.10", DstAddress: destination,
		SrcInterfaceID: &interfaceID,
	}))
}

// TestTheBlockedMapServesSecurityEventsBesideAMeasuredAnswer: with every connection measured, the blocked map
// answers the netflow measure -- explicitly and under best_available -- with no logged figure and no state,
// and serves the security events beside it, counted in their own measure.
func TestTheBlockedMapServesSecurityEventsBesideAMeasuredAnswer(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a", "example_if_b")
	f.measureEverything(t)
	f.blockedSecurityEvent(t, "example-event-1", "203.0.113.9")
	f.blockedSecurityEvent(t, "example-event-2", "203.0.113.9")
	for _, measure := range []string{measureNetflow, "best_available"} {
		response := f.respond(t, "blocked_traffic_world_map", map[string]any{"period": "1h", "volume_measure": measure})
		answer := data(t, response)
		if answer == nil {
			t.Fatalf("%s: the map answers no data (states %v)", measure, response.States)
		}
		if len(response.States) != 0 {
			t.Errorf("%s: the answer carries the states %v", measure, response.States)
		}
		if measures, _ := measuresIn(t, response); measures[envelope.MeasureLogged] {
			t.Errorf("%s: the answer carries a logged figure: %s", measure, encoded(t, response))
		}
		if total, _ := answer["total"].(map[string]any); total["measure"] != measureNetflow {
			t.Errorf("%s: the total is %v, not in the netflow measure", measure, total)
		}
		events, _ := answer["security_events"].(map[string]any)
		if events["measure"] != "security_events" || events["value"] != float64(2) || events["lower_bound"] != true {
			t.Errorf("%s: the security events are %v", measure, events)
		}
		refusals, _ := answer["total_refusals"].(map[string]any)
		if refusals["value"] != float64(2) {
			t.Errorf("%s: the refusals are %v, not the two events", measure, refusals)
		}
		bins, _ := answer["security_unplaced"].([]any)
		if len(bins) != 4 {
			t.Fatalf("%s: the security bins are %v", measure, bins)
		}
		for _, entry := range bins {
			bin, _ := entry.(map[string]any)
			count, _ := bin["events"].(map[string]any)
			if _, volume := bin["volume"]; volume || count["measure"] != "security_events" {
				t.Errorf("%s: the security bin %v is not a count of events", measure, bin)
			}
		}
	}
}
