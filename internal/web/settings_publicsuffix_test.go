package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/publicsuffix"
	"github.com/NaejEL/opnview/internal/store"
)

// The Public Suffix List card.
//
// PROVENANCE OF EVERY FIXTURE HERE: SYNTHESISED. The probes are the refresher's own
// constants, the copy "held" is the harness's stand-in answering a version and a rule
// count invented here, and the availability detail is a sentence written to look like
// the refresher's, so a card that read figures out of it would show them.

// publicSuffixCardOf returns the Public Suffix List card out of the settings page.
func publicSuffixCardOf(t *testing.T, page string) string {
	t.Helper()
	heading := `<h2 id="settings_public_suffix_name">` + catalogueText(t, msgKindPublicSuffix) + "</h2>"
	start := strings.Index(page, heading)
	if start < 0 {
		t.Fatal("the settings page has no Public Suffix List card")
	}
	return page[start : start+strings.Index(page[start:], "</section>")]
}

// setPublicSuffixProbe records a refresh outcome, with a detail.
func (h *harness) setPublicSuffixProbe(state store.AvailabilityState, probe string, detail *string) {
	h.t.Helper()
	h.setAvailabilityDetail(publicsuffix.Kind, publicsuffix.ProviderKey, state, probe, detail)
}

// TestEachRefreshOutcomeRendersItsOwnKey is AC14.
func TestEachRefreshOutcomeRendersItsOwnKey(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	if !strings.Contains(publicSuffixCardOf(t, pageSource(t, h, PathSettings)),
		catalogueText(t, msgPublicSuffixNotYetRefreshed)) {
		t.Error("a list never refreshed does not say so")
	}
	for _, outcome := range []struct {
		probe string
		state store.AvailabilityState
		key   messageKey
	}{
		{publicsuffix.ProbeDownloaded, store.StateReachable, msgPublicSuffixDownloaded},
		{publicsuffix.ProbeNotModified, store.StateReachable, msgPublicSuffixNotModified},
		{publicsuffix.ProbeFailed, store.StateUnavailable, msgPublicSuffixFailed},
		{publicsuffix.ProbeMalformed, store.StateUnavailable, msgPublicSuffixMalformed},
		{store.ProbeNotYetProbed, store.StateUnavailable, msgPublicSuffixNotYetRefreshed},
		{"a_probe_nobody_wrote", store.StateReachable, msgPublicSuffixFailed},
	} {
		h.setPublicSuffixProbe(outcome.state, outcome.probe, nil)
		card := publicSuffixCardOf(t, pageSource(t, h, PathSettings))
		if !strings.Contains(card, "<dd>"+catalogueText(t, outcome.key)+"</dd>") {
			t.Errorf("the probe %q does not render %s", outcome.probe, outcome.key)
		}
	}
}

// TestTheCopysFiguresComeFromTheProcess is AC15.
func TestTheCopysFiguresComeFromTheProcess(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	// A detail that reads like figures, and no copy held: nothing of it renders.
	misleading := "VERSION 1999-12-31_00-00-00_UTC, 4321 rules"
	h.setPublicSuffixProbe(store.StateReachable, publicsuffix.ProbeDownloaded, &misleading)
	card := publicSuffixCardOf(t, pageSource(t, h, PathSettings))
	if strings.Contains(card, "4321") || strings.Contains(card, "1999-12-31") {
		t.Error("the card read figures out of the availability detail")
	}
	if !strings.Contains(card, "<dd>"+catalogueText(t, msgNo)+"</dd>") ||
		!strings.Contains(card, catalogueText(t, msgNeverConfirmed)) {
		t.Error("with no copy held the card does not say so")
	}

	// A copy held: its version and rule count, from the process.
	confirmed := h.clock.Now().Unix() - 600
	h.suffix.hold("2026-10-09_08-15-00_UTC", 9876, confirmed)
	card = publicSuffixCardOf(t, pageSource(t, h, PathSettings))
	for _, expected := range []string{
		`<span class="datum" translate="no">2026-10-09_08-15-00_UTC</span>`,
		`<span class="figure">9876</span>`,
		`<span class="figure">` + instant(confirmed) + `</span>`,
		"<dd>" + catalogueText(t, msgYes) + "</dd>",
	} {
		if !strings.Contains(card, expected) {
			t.Errorf("the card does not carry %q", expected)
		}
	}

	// A failed refresh over the copy still in use shows both facts.
	h.setPublicSuffixProbe(store.StateReachable, publicsuffix.ProbeFailed, nil)
	card = publicSuffixCardOf(t, pageSource(t, h, PathSettings))
	if !strings.Contains(card, catalogueText(t, msgPublicSuffixFailed)) ||
		!strings.Contains(card, `<span class="figure">9876</span>`) {
		t.Error("a failed refresh over a held copy does not show both the failure and the copy")
	}

	// THE GREP HALF: no code in this package reads an availability detail except the
	// Sources surface, which draws it verbatim as data.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if strings.Contains(string(source), ".Detail") && name != "sources.go" {
			t.Errorf("%s reads an availability or gap detail", name)
		}
	}
}

// TestTheRefreshIntervalIsSavedAndRefusedAgainstItsField: the settings save writes the
// interval and hands it to the running service; a refusal stores nothing.
func TestTheRefreshIntervalIsSavedAndRefusedAgainstItsField(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	assertStatus(t, h.post(PathSettings, map[string][]string{fieldSettingsCard: {settingsCardPublicSuffix},
		fieldPublicSuffixInterval: {"43200"}}), http.StatusSeeOther)
	if got := h.setting(fieldPublicSuffixInterval); got != "43200" {
		t.Errorf("the stored interval is %q", got)
	}
	if h.live.Get().PublicSuffixRefreshInterval.Seconds() != 43200 {
		t.Error("the live holder was not handed the saved interval")
	}
	status, page := statusAndBody(t, h.post(PathSettings, map[string][]string{
		fieldSettingsCard: {settingsCardPublicSuffix}, fieldPublicSuffixInterval: {"daily"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a refused interval answered %d", status)
	}
	card := publicSuffixCardOf(t, page)
	if !strings.Contains(card, `aria-describedby="public_suffix_interval_refusal"`) ||
		fieldValue(card, "public_suffix_interval") != "daily" {
		t.Error("the refused interval is not kept and marked in its field")
	}
	if got := h.setting(fieldPublicSuffixInterval); got != "43200" {
		t.Errorf("a refused interval was stored: %q", got)
	}
}

// TestEachSettingsCardSavesOnlyItsOwnFields: the page is one form and each card's Save
// names its card. A save stores that card's fields and nothing else, sends the browser
// back to the card, and the card says what it did, or what it refused, inside itself.
func TestEachSettingsCardSavesOnlyItsOwnFields(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	urlBefore, urlPresent, err := h.store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil {
		t.Fatalf("reading the firewall URL: %v", err)
	}

	page := pageSource(t, h, PathSettings)
	if !strings.Contains(page, "<h1>"+catalogueText(t, "settings.title")+"</h1>") {
		t.Error("the settings surface has no page heading")
	}
	if strings.Count(page, `<form method="post" action="`+PathSettings+`"`) != 1 {
		t.Error("the settings cards are not one form")
	}
	for _, card := range settingsCards() {
		save := `name="` + fieldSettingsCard + `" value="` + card + `" formaction="` + PathSettings +
			`#settings_` + card + `"`
		if !strings.Contains(settingsCardOf(t, page, card), save) {
			t.Errorf("the %s card has no Save naming it and posting to its anchor", card)
		}
	}

	answer := h.post(PathSettings, url.Values{
		fieldSettingsCard: {settingsCardAppearance}, "theme": {string(ThemeDark)}})
	if location := answer.Header.Get("Location"); location != PathSettings+"?saved=appearance#settings_appearance" {
		t.Errorf("the save sent the browser to %q, not back to its card", location)
	}
	status, page := followed(t, h, answer)
	if status != http.StatusSeeOther || h.setting(config.KeyTheme) != string(ThemeDark) {
		t.Fatalf("saving the appearance card answered %d", status)
	}
	urlAfter, urlStill, err := h.store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil || urlStill != urlPresent || urlAfter != urlBefore {
		t.Error("saving the appearance card changed the firewall URL")
	}
	saved := `<p class="notice ok">` + catalogueText(t, msgSettingsSaved) + "</p>"
	if !strings.Contains(settingsCardOf(t, page, settingsCardAppearance), saved) ||
		strings.Count(page, saved) != 1 {
		t.Error("the save is not confirmed inside the card it was made in, and only there")
	}

	// A refused interval is refused inside its card, and the theme saved on the other
	// card is untouched by it.
	status, page = statusAndBody(t, h.post(PathSettings, url.Values{
		fieldSettingsCard: {settingsCardPublicSuffix}, fieldPublicSuffixInterval: {"daily"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a refused interval answered %d", status)
	}
	if h.setting(config.KeyTheme) != string(ThemeDark) ||
		selectedIn(t, settingsCardOf(t, page, settingsCardAppearance), "theme") != string(ThemeDark) {
		t.Error("a refused interval discarded the theme")
	}

	// A card that does not exist is refused.
	status, page = statusAndBody(t, h.post(PathSettings, url.Values{fieldSettingsCard: {"no_such_card"}}))
	if status != http.StatusBadRequest || !strings.Contains(page, catalogueText(t, msgSettingsCardUnknown)) {
		t.Errorf("a card that does not exist answered %d", status)
	}
}

// TestTheRefreshIntervalIsStoredOnlyWhenItChanges: saving the card with the interval
// in force -- the default -- leaves no row behind; a different interval is stored in
// seconds, whatever unit it was typed in.
func TestTheRefreshIntervalIsStoredOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	figure, unit := fieldDuration(int64(config.Defaults().PublicSuffixRefreshInterval / time.Second))
	assertStatus(t, h.post(PathSettings, url.Values{fieldSettingsCard: {settingsCardPublicSuffix},
		fieldPublicSuffixInterval: {figure}, unitFieldName(fieldPublicSuffixInterval): {unit}}), http.StatusSeeOther)
	if _, present, err := h.store.Setting(context.Background(), fieldPublicSuffixInterval); err != nil || present {
		t.Error("saving the interval in force turned the default into a stored row")
	}
	assertStatus(t, h.post(PathSettings, url.Values{fieldSettingsCard: {settingsCardPublicSuffix},
		fieldPublicSuffixInterval: {"12"}, unitFieldName(fieldPublicSuffixInterval): {unitHours}}), http.StatusSeeOther)
	if got := h.setting(fieldPublicSuffixInterval); got != "43200" {
		t.Errorf("12 hours was stored as %q, want 43200", got)
	}
	card := publicSuffixCardOf(t, pageSource(t, h, PathSettings))
	if !durationFieldShows(t, card, "public_suffix_interval", "12", unitHours) {
		t.Error("the card does not show the interval now in force in its field")
	}
}

// settingsCardOf returns one card of the settings surface.
func settingsCardOf(t *testing.T, page, card string) string {
	t.Helper()
	start := strings.Index(page, `<section class="card" id="settings_`+card+`"`)
	if start < 0 {
		t.Fatalf("the settings page has no %s card", card)
	}
	return page[start : start+strings.Index(page[start:], "</section>")]
}
