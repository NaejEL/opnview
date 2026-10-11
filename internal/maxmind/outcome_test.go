package maxmind

import (
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// TestEachRefreshProbeMapsToItsOwnOutcome is AD1 and AD2 for the MaxMind refresh
// (SPEC-step-5b-widget-api.md, D1): every probe the refresh records has its own code, a
// missing licence key and a missing account ID are two named absences, a refusal and a
// spent limit are two codes, and a current dataset that is not on disk is not healthy.
func TestEachRefreshProbeMapsToItsOwnOutcome(t *testing.T) {
	t.Parallel()
	seen := map[store.AvailabilityOutcome]string{}
	for _, want := range []struct {
		probe   string
		ready   bool
		outcome store.AvailabilityOutcome
	}{
		{ProbeCurrent, true, store.OutcomeHealthy},
		{ProbeNoLicenceKey, false, store.OutcomeNoLicenceKey},
		{ProbeLicenceKeyUndecryptable, false, store.OutcomeLicenceKeyUndecryptable},
		{ProbeNoAccountID, false, store.OutcomeNoAccountID},
		{ProbeRefused, false, store.OutcomeDownloadRefused},
		{ProbeLimited, true, store.OutcomeDownloadLimitReached},
		{ProbeFailed, true, store.OutcomeDownloadFailed},
	} {
		got := outcomeOf(want.probe, want.ready)
		if got != want.outcome {
			t.Errorf("%s: the outcome is %q, not %q", want.probe, got, want.outcome)
		}
		if other, duplicate := seen[got]; duplicate {
			t.Errorf("%s and %s share the outcome %q", want.probe, other, got)
		}
		seen[got] = want.probe
	}
	if got := outcomeOf(ProbeCurrent, false); got == store.OutcomeHealthy {
		t.Error("a current dataset that is not on disk was called healthy")
	}
}
