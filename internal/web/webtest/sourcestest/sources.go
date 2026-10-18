package sourcestest

import (
	"context"
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
	if _, err := h.Store.DB().Exec("DELETE FROM source_availability WHERE provider_id = $1",
		collectiontest.ProviderID(h, kind, providerKey)); err != nil {
		h.T.Fatalf("deleting an availability row: %v", err)
	}
}

// GapReasonsRegistered reads the reasons the database's gap-reason registry holds -- every reason a
// collection gap may be recorded for, as the declarations wrote them -- so a reason a kind or a protocol
// gains is a reason this test asks a key of.
func GapReasonsRegistered(t *testing.T, h *webtest.Harness) []string {
	t.Helper()
	rows, err := h.Store.DB().Query(`SELECT reason FROM gap_reason ORDER BY reason`)
	if err != nil {
		t.Fatalf("reading the gap-reason registry: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var reasons []string
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatal(err)
		}
		reasons = append(reasons, reason)
	}
	if len(reasons) < 4 {
		t.Fatalf("read %d gap reasons out of the registry, which is fewer than the declarations name", len(reasons))
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
