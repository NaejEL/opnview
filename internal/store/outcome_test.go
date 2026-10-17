package store_test

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
	for _, outcome := range vocab.AvailabilityOutcomes {
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
		outcome != vocab.OutcomeNotYetProbed {
		t.Errorf("a fresh availability row reads %q (%v), not %q", outcome, err, vocab.OutcomeNotYetProbed)
	}
	if err := database.SetAvailability(ctx, providerID, vocab.StateReachable, "invented_code", "probe", nil, 1); err == nil {
		t.Error("SetAvailability accepted a code outside the set")
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE source_availability SET outcome = 'invented_code' WHERE provider_id = $1", providerID); !sqldb.IsConstraintViolation(err) {
		t.Errorf("the schema accepted a code outside the set: %v", err)
	}
	for _, outcome := range vocab.AvailabilityOutcomes {
		if err := database.SetAvailability(ctx, providerID, vocab.StateUnavailable, outcome, "probe", nil, 1); err != nil {
			t.Errorf("the code %q was refused: %v", outcome, err)
		}
	}
}
