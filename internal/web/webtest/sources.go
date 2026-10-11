package webtest

import (
	"context"
	"github.com/NaejEL/opnview/internal/store"
	"regexp"
	"testing"
)

// setAvailabilityDetail records a probe outcome with a probe name and a detail.
func (h *Harness) SetAvailabilityDetail(kind, providerKey string, state store.AvailabilityState,
	probe string, detail *string) {
	h.T.Helper()
	if err := h.Store.SetAvailability(context.Background(), h.ProviderID(kind, providerKey),
		state, OutcomeOfState(state, probe), probe, detail, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// dropAvailability deletes one provider's availability row, which the schema
// prevents and a screen still has to name.
func (h *Harness) DropAvailability(kind, providerKey string) {
	h.T.Helper()
	if _, err := h.Store.DB().Exec("DELETE FROM source_availability WHERE provider_id = ?",
		h.ProviderID(kind, providerKey)); err != nil {
		h.T.Fatalf("deleting an availability row: %v", err)
	}
}

// gapReasonsOfSchema reads the reasons collection_gap's CHECK admits from the table's
// own definition, so a reason the schema gains is a reason this test asks a key of.
func GapReasonsOfSchema(t *testing.T, h *Harness) []string {
	t.Helper()
	var definition string
	if err := h.Store.DB().QueryRow(
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		 WHERE conrelid = 'collection_gap'::regclass AND contype = 'c'
		   AND pg_get_constraintdef(oid) LIKE '%reason%'`).
		Scan(&definition); err != nil {
		t.Fatalf("reading the collection_gap CHECK on reason: %v", err)
	}
	var reasons []string
	for _, quoted := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(definition, -1) {
		reasons = append(reasons, quoted[1])
	}
	if len(reasons) < 4 {
		t.Fatalf("read %d gap reasons out of the schema, which is fewer than it holds", len(reasons))
	}
	return reasons
}

// outcomeOfState is the outcome code a fixture records beside a state: the test sets
// the state and the probe, and the code follows from them as the collectors write it.
func OutcomeOfState(state store.AvailabilityState, probe string) store.AvailabilityOutcome {
	switch {
	case probe == store.ProbeNotYetProbed:
		return store.OutcomeNotYetProbed
	case state == store.StateReachable:
		return store.OutcomeHealthy
	case state == store.StatePresentButDisabled:
		return store.OutcomePresentButDisabled
	default:
		return store.OutcomeServerError
	}
}
