package sources_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/surfaces/sources"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The Sources surface's JSON answer (C-AC9) and coverage row 39 (C-AC16): when each provider's
// state began, bounded by opnview's own checks. sources_test.go holds the answer's figures.

// sinceOf is one provider's since bounds in an answer listing providers.
func sinceOf(providers any, kind, providerKey string) any {
	list, _ := providers.([]any)
	for _, provider := range list {
		if settingstest.Path(provider, "kind") == kind && settingstest.Path(provider, "provider_key") == providerKey {
			return settingstest.Path(provider, "since")
		}
	}
	return nil
}

// TestAnAvailabilityResponseBoundsWhenAStateBeganByTheTwoChecksAroundIt is C-AC16's row 39: an
// availability response -- the Sources answer and the source_availability widget's -- carries per
// provider the current state's since bounds. A transition between two checks yields exactly those
// two instants, a later check finding the same state keeps them, and a state never seen changing
// yields a named absence rather than an instant.
func TestAnAvailabilityResponseBoundsWhenAStateBeganByTheTwoChecksAroundIt(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	pf := collectiontest.ProviderID(h, "firewall_log", "pf")
	check := func(state vocab.AvailabilityState, outcome string, at int64) {
		t.Helper()
		if err := h.Store.SetAvailability(ctx, pf, state, vocab.AvailabilityOutcome(outcome), "/api/diagnostics/firewall/log",
			nil, at); err != nil {
			t.Fatalf("recording a check: %v", err)
		}
	}
	widgetProviders := func() any {
		parameters := url.QueryEscape(`{"kinds":["firewall_log","dns_lookup"]}`)
		return settingstest.Path(settingstest.GetJSON(h, "/api/widget/source_availability?parameters="+parameters), "data", "providers")
	}
	absent := map[string]any{"after_at": nil, "by_at": nil, "state": request.SinceNotObserved}

	// The first check after the row was created changes "not yet probed" into a state: nothing
	// was seen changing.
	check(vocab.StateReachable, "healthy", now-300)
	for _, providers := range []any{settingstest.Path(settingstest.GetJSON(h, sources.PathAPISources), "providers"), widgetProviders()} {
		if got := sinceOf(providers, "firewall_log", "pf"); fmt.Sprint(got) != fmt.Sprint(absent) {
			t.Errorf("a state never seen changing answers %v rather than the named absence", got)
		}
	}

	// The state changes between the check at now-300 and the one at now-120.
	check(vocab.StateUnavailable, "transport_failure", now-120)
	// A later check finding the same state keeps the bounds.
	check(vocab.StateUnavailable, "transport_failure", now-60)
	bounded := map[string]any{"after_at": float64(now - 300), "by_at": float64(now - 120), "state": nil}
	for name, providers := range map[string]any{"the Sources answer": settingstest.Path(settingstest.GetJSON(h, sources.PathAPISources), "providers"),
		"the source_availability widget": widgetProviders()} {
		if got := sinceOf(providers, "firewall_log", "pf"); fmt.Sprint(got) != fmt.Sprint(bounded) {
			t.Errorf("%s bounds the change by %v rather than by the two checks around it (%v)", name, got, bounded)
		}
		if got := sinceOf(providers, "dns_lookup", "unbound"); fmt.Sprint(got) != fmt.Sprint(absent) {
			t.Errorf("%s answers %v for a provider never checked rather than the named absence", name, got)
		}
	}
}
