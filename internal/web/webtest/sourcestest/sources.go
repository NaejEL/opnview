package sourcestest

import (
	"context"
	"regexp"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
)

// SetAvailabilityDetail records a probe outcome with a probe name and a detail.
func SetAvailabilityDetail(h *webtest.Harness, kind, providerKey string, state vocab.AvailabilityState,
	probe string, detail *string) {
	h.T.Helper()
	if err := h.Store.SetAvailability(context.Background(), collectiontest.ProviderID(h, kind, providerKey),
		state, OutcomeOfState(state, probe), probe, detail, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// DropAvailability deletes one provider's availability row, which the schema
// prevents and a screen still has to name.
func DropAvailability(h *webtest.Harness, kind, providerKey string) {
	h.T.Helper()
	if _, err := h.Store.DB().Exec("DELETE FROM source_availability WHERE provider_id = ?",
		collectiontest.ProviderID(h, kind, providerKey)); err != nil {
		h.T.Fatalf("deleting an availability row: %v", err)
	}
}

// GapReasonsOfSchema reads the reasons collection_gap's CHECK admits from the table's
// own definition, so a reason the schema gains is a reason this test asks a key of.
func GapReasonsOfSchema(t *testing.T, h *webtest.Harness) []string {
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

// OutcomeOfState is the outcome code a fixture records beside a state: the test sets
// the state and the probe, and the code follows from them as the collectors write it.
func OutcomeOfState(state vocab.AvailabilityState, probe string) vocab.AvailabilityOutcome {
	switch {
	case probe == reads.ProbeNotYetProbed:
		return vocab.OutcomeNotYetProbed
	case state == vocab.StateReachable:
		return vocab.OutcomeHealthy
	case state == vocab.StatePresentButDisabled:
		return vocab.OutcomePresentButDisabled
	default:
		return vocab.OutcomeServerError
	}
}
