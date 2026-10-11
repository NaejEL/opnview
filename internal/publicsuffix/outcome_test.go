package publicsuffix

import (
	"context"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// outcome reads the provider's availability outcome code.
func (h *harness) outcome(t *testing.T) store.AvailabilityOutcome {
	t.Helper()
	providerID, err := h.database.ProviderID(context.Background(), Kind, ProviderKey)
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	outcome, err := h.database.AvailabilityOutcomeOf(context.Background(), providerID)
	if err != nil {
		t.Fatalf("reading the outcome: %v", err)
	}
	return outcome
}

// TestEachRefreshOutcomeWritesItsCode is AD1 for the Public Suffix List refresh
// (SPEC-step-5b-widget-api.md, D1): a failed download, a download in use, an unchanged list
// and a malformed download each write their own code beside the probe.
func TestEachRefreshOutcomeWritesItsCode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.server.set(http.StatusServiceUnavailable, nil, "")
	_ = h.refresher.Refresh(ctx)
	if got := h.outcome(t); got != store.OutcomeDownloadFailed {
		t.Errorf("a failed first download wrote %q", got)
	}
	h.server.set(http.StatusOK, fullEnough(t), `"example-etag-outcome"`)
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("downloading: %v", err)
	}
	if got := h.outcome(t); got != store.OutcomeHealthy {
		t.Errorf("a download in use wrote %q", got)
	}
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if got := h.outcome(t); got != store.OutcomeHealthy {
		t.Errorf("an unchanged list wrote %q", got)
	}
	h.server.set(http.StatusOK, []byte("<html>not the list</html>"), `"example-etag-outcome-2"`)
	_ = h.refresher.Refresh(ctx)
	if got := h.outcome(t); got != store.OutcomeDownloadMalformed {
		t.Errorf("a malformed download wrote %q", got)
	}
}
