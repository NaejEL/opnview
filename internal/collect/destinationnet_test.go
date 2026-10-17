package collect_test

import (
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/decode"
)

// TestRuleDestinationNetIsStoredVerbatimFromSearchRule is AC28 of
// specs/SPEC-step-5b-widget-api.md (B8a): each rule's destination_net from
// search_rule is stored on rule exactly as the endpoint returned it, and a rule that
// does not report the field stores NULL -- not reported -- rather than an empty value.
func TestRuleDestinationNetIsStoredVerbatimFromSearchRule(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)

	reported := 0
	for _, row := range fixtureRecords(t, "search_rule.json") {
		label, _ := decode.String(row, "uuid")
		raw, present := row["destination_net"]
		if !present {
			if stored := collecttest.ScalarCount(t, harness.Store,
				"SELECT count(*) FROM rule WHERE pf_label = $1 AND destination_net IS NULL", label); stored != 1 {
				t.Errorf("the rule %s reports no destination_net and stored one", label)
			}
			continue
		}
		reported++
		if stored := collecttest.ScalarCount(t, harness.Store,
			"SELECT count(*) FROM rule WHERE pf_label = $1 AND destination_net = $2", label, raw); stored != 1 {
			t.Errorf("the rule %s did not store its destination_net %q verbatim", label, raw)
		}
	}
	if reported < 2 {
		t.Fatalf("the fixture reports destination_net on %d rules, so the test is vacuous", reported)
	}
}
