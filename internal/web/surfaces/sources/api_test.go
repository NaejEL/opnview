package sources_test

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/sources"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget"
)

// The Sources surface's JSON equivalent (C-AC9) and coverage row 39 (C-AC16): every figure of the
// page in the JSON answer, and when each provider's state began, bounded by opnview's own checks.

// TestTheSourcesJSONCarriesEveryFigureOfThePage is C-AC9's figures for the Sources page and its
// outage indicator, on an installation with an outage, a provider turned on and failing, one
// installed and not collecting, a detail and recent gaps.
func TestTheSourcesJSONCarriesEveryFigureOfThePage(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	webtest.Discard(t, h.StoreCredentials(webtest.RandomHex(t, 8), webtest.RandomHex(t, 16)))
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	detail := "the collector's own words"
	if err := h.Store.SetAvailability(ctx, h.ProviderID("dhcp_lease", "kea"), store.StatePresentButDisabled,
		store.AvailabilityOutcome("present_but_disabled"), "/api/kea/service/status", &detail, now-60); err != nil {
		t.Fatalf("recording an availability: %v", err)
	}
	h.SetAvailability("firewall_log", "pf", store.StateUnavailable)
	h.SetAvailability("dns_lookup", "unbound", store.StateReachable)
	h.SetAvailability("dns_lookup", "dnsmasq", store.StateUnavailable)
	h.SetSelection("dns_lookup", "dnsmasq", config.SelectionOn)
	h.RecordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-1800, now-1740)

	figures, err := sources.PageFigures(ctx, h.Server)
	if err != nil {
		t.Fatalf("reading the page's figures: %v", err)
	}
	answer := h.GetJSON(sources.PathAPISources)
	yesNo := func(value any) string {
		if value == true {
			return "state.yes"
		}
		return "state.no"
	}
	var expected []string
	for _, raw := range webtest.Path(answer, "providers").([]any) {
		kind, provider := webtest.Path(raw, "kind").(string), webtest.Path(raw, "provider_key").(string)
		checked := "state.source.never_checked"
		if at, ok := webtest.Path(raw, "checked_at").(float64); ok {
			checked = web.Instant(int64(at))
		}
		detailText := "state.source.no_detail"
		if text, ok := webtest.Path(raw, "detail").(string); ok {
			detailText = text
		}
		line := fmt.Sprintf("source %s %s state.availability.%s active %s selection state.sizing.selection_%s "+
			"outage %s checked %s probe %s detail %s", web.KindLabelKey(kind), web.ProviderLabelKey(kind, provider),
			webtest.Path(raw, "availability"), yesNo(webtest.Path(raw, "active")), webtest.Path(raw, "selection"),
			yesNo(webtest.Path(raw, "in_outage")), checked, webtest.Path(raw, "probe"), detailText)
		for _, page := range webtest.Path(raw, "opnsense_pages").([]any) {
			line += fmt.Sprintf(" page %s opnsense_page.%s", webtest.Path(page, "url"), webtest.Path(page, "page"))
		}
		expected = append(expected, line)
	}
	for _, raw := range webtest.Path(answer, "outages").([]any) {
		kind := webtest.Path(raw, "kind").(string)
		providerLabel, state := "", "state.outage.kind"
		if provider, ok := webtest.Path(raw, "provider_key").(string); ok {
			providerLabel, state = string(web.ProviderLabelKey(kind, provider)), "state.outage.provider"
		}
		expected = append(expected, fmt.Sprintf("outage %s %s %s", web.KindLabelKey(kind), providerLabel, state))
	}
	for _, raw := range webtest.Path(answer, "recent_gaps").([]any) {
		kind, provider := webtest.Path(raw, "kind").(string), webtest.Path(raw, "provider_key").(string)
		detailText := "state.source.no_detail"
		if text, ok := webtest.Path(raw, "detail").(string); ok {
			detailText = text
		}
		expected = append(expected, fmt.Sprintf("gap %s %s gap_reason.%s %s %s %v detail %s", web.KindLabelKey(kind),
			web.ProviderLabelKey(kind, provider), webtest.Path(raw, "reason"),
			web.Instant(int64(webtest.Path(raw, "start_at").(float64))),
			web.Instant(int64(webtest.Path(raw, "end_at").(float64))),
			web.SpelledDuration(int64(webtest.Path(raw, "missed_seconds").(float64))), detailText))
	}
	sort.Strings(figures)
	sort.Strings(expected)
	if fmt.Sprint(figures) != fmt.Sprint(expected) {
		t.Errorf("the page draws\n%s\nand the answer carries\n%s", fmt.Sprint(figures), fmt.Sprint(expected))
	}
	count, err := sources.CountOutages(ctx, h.Server)
	if err != nil {
		t.Fatalf("counting the outages: %v", err)
	}
	if webtest.Path(answer, "outage_count") != float64(count) || count == 0 {
		t.Errorf("the answer counts %v outages and the indicator %d", webtest.Path(answer, "outage_count"), count)
	}
}

// sinceOf is one provider's since bounds in an answer listing providers.
func sinceOf(providers any, kind, providerKey string) any {
	list, _ := providers.([]any)
	for _, provider := range list {
		if webtest.Path(provider, "kind") == kind && webtest.Path(provider, "provider_key") == providerKey {
			return webtest.Path(provider, "since")
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
	h := webtest.SignedInHarness(t)
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	pf := h.ProviderID("firewall_log", "pf")
	check := func(state store.AvailabilityState, outcome string, at int64) {
		t.Helper()
		if err := h.Store.SetAvailability(ctx, pf, state, store.AvailabilityOutcome(outcome), "/api/diagnostics/firewall/log",
			nil, at); err != nil {
			t.Fatalf("recording a check: %v", err)
		}
	}
	widgetProviders := func() any {
		parameters := url.QueryEscape(`{"kinds":["firewall_log","dns_lookup"]}`)
		return webtest.Path(h.GetJSON("/api/widget/source_availability?parameters="+parameters), "data", "providers")
	}
	absent := map[string]any{"after_at": nil, "by_at": nil, "state": widget.SinceNotObserved}

	// The first check after the row was created changes "not yet probed" into a state: nothing
	// was seen changing.
	check(store.StateReachable, "healthy", now-300)
	for _, providers := range []any{webtest.Path(h.GetJSON(sources.PathAPISources), "providers"), widgetProviders()} {
		if got := sinceOf(providers, "firewall_log", "pf"); fmt.Sprint(got) != fmt.Sprint(absent) {
			t.Errorf("a state never seen changing answers %v rather than the named absence", got)
		}
	}

	// The state changes between the check at now-300 and the one at now-120.
	check(store.StateUnavailable, "transport_failure", now-120)
	// A later check finding the same state keeps the bounds.
	check(store.StateUnavailable, "transport_failure", now-60)
	bounded := map[string]any{"after_at": float64(now - 300), "by_at": float64(now - 120), "state": nil}
	for name, providers := range map[string]any{"the Sources answer": webtest.Path(h.GetJSON(sources.PathAPISources), "providers"),
		"the source_availability widget": widgetProviders()} {
		if got := sinceOf(providers, "firewall_log", "pf"); fmt.Sprint(got) != fmt.Sprint(bounded) {
			t.Errorf("%s bounds the change by %v rather than by the two checks around it (%v)", name, got, bounded)
		}
		if got := sinceOf(providers, "dns_lookup", "unbound"); fmt.Sprint(got) != fmt.Sprint(absent) {
			t.Errorf("%s answers %v for a provider never checked rather than the named absence", name, got)
		}
	}
}
