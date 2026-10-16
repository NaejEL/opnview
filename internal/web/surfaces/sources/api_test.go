package sources_test

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/settingfields"
	"github.com/NaejEL/opnview/internal/web/surfaces/sources"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The Sources surface's JSON equivalent (C-AC9) and coverage row 39 (C-AC16): every figure of the
// page in the JSON answer, and when each provider's state began, bounded by opnview's own checks.

// TestTheSourcesJSONCarriesEveryFigureOfThePage is C-AC9's figures for the Sources page and its
// outage indicator, on an installation with an outage, a provider turned on and failing, one
// installed and not collecting, a detail and recent gaps.
func TestTheSourcesJSONCarriesEveryFigureOfThePage(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	webtest.Discard(t, settingstest.StoreCredentials(h, webtest.RandomHex(t, 8), webtest.RandomHex(t, 16)))
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	detail := "the collector's own words"
	if err := h.Store.SetAvailability(ctx, collectiontest.ProviderID(h, "dhcp_lease", "kea"), vocab.StatePresentButDisabled,
		vocab.AvailabilityOutcome("present_but_disabled"), "/api/kea/service/status", &detail, now-60); err != nil {
		t.Fatalf("recording an availability: %v", err)
	}
	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateUnavailable)
	collectiontest.SetAvailability(h, "dns_lookup", "unbound", vocab.StateReachable)
	collectiontest.SetAvailability(h, "dns_lookup", "dnsmasq", vocab.StateUnavailable)
	collectiontest.SetSelection(h, "dns_lookup", "dnsmasq", config.SelectionOn)
	collectiontest.RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, now-1800, now-1740)

	figures, err := sources.PageFigures(ctx, h.Server)
	if err != nil {
		t.Fatalf("reading the page's figures: %v", err)
	}
	answer := settingstest.GetJSON(h, sources.PathAPISources)
	yesNo := func(value any) string {
		if value == true {
			return "state.yes"
		}
		return "state.no"
	}
	var expected []string
	for _, raw := range settingstest.Path(answer, "providers").([]any) {
		kind, provider := settingstest.Path(raw, "kind").(string), settingstest.Path(raw, "provider_key").(string)
		checked := "state.source.never_checked"
		if at, ok := settingstest.Path(raw, "checked_at").(float64); ok {
			checked = settingfields.Instant(int64(at))
		}
		detailText := "state.source.no_detail"
		if text, ok := settingstest.Path(raw, "detail").(string); ok {
			detailText = text
		}
		line := fmt.Sprintf("source %s %s state.availability.%s active %s selection state.sizing.selection_%s "+
			"outage %s checked %s probe %s detail %s", messages.KindLabelKey(kind), messages.ProviderLabelKey(kind, provider),
			settingstest.Path(raw, "availability"), yesNo(settingstest.Path(raw, "active")), settingstest.Path(raw, "selection"),
			yesNo(settingstest.Path(raw, "in_outage")), checked, settingstest.Path(raw, "probe"), detailText)
		for _, page := range settingstest.Path(raw, "opnsense_pages").([]any) {
			line += fmt.Sprintf(" page %s opnsense_page.%s", settingstest.Path(page, "url"), settingstest.Path(page, "page"))
		}
		expected = append(expected, line)
	}
	for _, raw := range settingstest.Path(answer, "outages").([]any) {
		kind := settingstest.Path(raw, "kind").(string)
		providerLabel, state := "", "state.outage.kind"
		if provider, ok := settingstest.Path(raw, "provider_key").(string); ok {
			providerLabel, state = string(messages.ProviderLabelKey(kind, provider)), "state.outage.provider"
		}
		expected = append(expected, fmt.Sprintf("outage %s %s %s", messages.KindLabelKey(kind), providerLabel, state))
	}
	for _, raw := range settingstest.Path(answer, "recent_gaps").([]any) {
		kind, provider := settingstest.Path(raw, "kind").(string), settingstest.Path(raw, "provider_key").(string)
		detailText := "state.source.no_detail"
		if text, ok := settingstest.Path(raw, "detail").(string); ok {
			detailText = text
		}
		expected = append(expected, fmt.Sprintf("gap %s %s gap_reason.%s %s %s %v detail %s", messages.KindLabelKey(kind),
			messages.ProviderLabelKey(kind, provider), settingstest.Path(raw, "reason"),
			settingfields.Instant(int64(settingstest.Path(raw, "start_at").(float64))),
			settingfields.Instant(int64(settingstest.Path(raw, "end_at").(float64))),
			settingfields.SpelledDuration(int64(settingstest.Path(raw, "missed_seconds").(float64))), detailText))
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
	if settingstest.Path(answer, "outage_count") != float64(count) || count == 0 {
		t.Errorf("the answer counts %v outages and the indicator %d", settingstest.Path(answer, "outage_count"), count)
	}
}

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
