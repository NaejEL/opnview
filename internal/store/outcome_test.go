package store

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTheOutcomeCheckHoldsExactlyTheClosedSet is AD1's schema half: the CHECK on
// source_availability.outcome names exactly store.AvailabilityOutcomes, a code outside the
// set is refused by the schema as well as by SetAvailability, and every code in it is
// accepted.
func TestTheOutcomeCheckHoldsExactlyTheClosedSet(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	check := regexp.MustCompile(`(?s)outcome\s+TEXT NOT NULL DEFAULT 'not_yet_probed'\s+CHECK \(outcome IN \(([^)]*)\)\)`).
		FindSubmatch(schema)
	if check == nil {
		t.Fatal("the schema carries no CHECK on source_availability.outcome")
	}
	var inSchema []string
	for _, quoted := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(check[1], -1) {
		inSchema = append(inSchema, string(quoted[1]))
	}
	var inCode []string
	for _, outcome := range AvailabilityOutcomes {
		inCode = append(inCode, string(outcome))
	}
	sort.Strings(inSchema)
	sort.Strings(inCode)
	if strings.Join(inSchema, ",") != strings.Join(inCode, ",") {
		t.Errorf("the schema's set\n%v\ndiffers from AvailabilityOutcomes\n%v", inSchema, inCode)
	}

	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := database.AvailabilityOutcomeOf(ctx, providerID); err != nil ||
		outcome != OutcomeNotYetProbed {
		t.Errorf("a fresh availability row reads %q (%v), not %q", outcome, err, OutcomeNotYetProbed)
	}
	if err := database.SetAvailability(ctx, providerID, StateReachable, "invented_code", "probe", nil, 1); err == nil {
		t.Error("SetAvailability accepted a code outside the set")
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE source_availability SET outcome = 'invented_code' WHERE provider_id = ?", providerID); err == nil ||
		!strings.Contains(err.Error(), "constraint failed") {
		t.Errorf("the schema accepted a code outside the set: %v", err)
	}
	for _, outcome := range AvailabilityOutcomes {
		if err := database.SetAvailability(ctx, providerID, StateUnavailable, outcome, "probe", nil, 1); err != nil {
			t.Errorf("the code %q was refused: %v", outcome, err)
		}
	}
}
