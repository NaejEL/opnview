package settings_test

import (
	"context"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// publicSuffixCardOf returns the Public Suffix List card out of the settings page.
func publicSuffixCardOf(t *testing.T, page string) string {
	t.Helper()
	heading := `<h2 id="settings_public_suffix_name">` + webtest.CatalogueText(t, msgKindRegistrableDomain) + "</h2>"
	start := strings.Index(page, heading)
	if start < 0 {
		t.Fatal("the settings page has no Public Suffix List card")
	}
	return page[start : start+strings.Index(page[start:], "</section>")]
}

// TestEachRefreshOutcomeRendersItsOwnKey is AC14.
func TestEachRefreshOutcomeRendersItsOwnKey(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	if !strings.Contains(publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings)),
		webtest.CatalogueText(t, msgPublicSuffixNotYetRefreshed)) {
		t.Error("a list never refreshed does not say so")
	}
	for _, outcome := range []struct {
		probe string
		state store.AvailabilityState
		key   messageKey
	}{
		{registrabledomain.ProbeDownloaded, store.StateReachable, msgPublicSuffixDownloaded},
		{registrabledomain.ProbeNotModified, store.StateReachable, msgPublicSuffixNotModified},
		{registrabledomain.ProbeFailed, store.StateUnavailable, msgPublicSuffixFailed},
		{registrabledomain.ProbeMalformed, store.StateUnavailable, msgPublicSuffixMalformed},
		{store.ProbeNotYetProbed, store.StateUnavailable, msgPublicSuffixNotYetRefreshed},
		{"a_probe_nobody_wrote", store.StateReachable, msgPublicSuffixFailed},
	} {
		h.SetPublicSuffixProbe(outcome.state, outcome.probe, nil)
		card := publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings))
		if !strings.Contains(card, "<dd>"+webtest.CatalogueText(t, outcome.key)+"</dd>") {
			t.Errorf("the probe %q does not render %s", outcome.probe, outcome.key)
		}
	}
}

// TestTheCopysFiguresComeFromTheProcess is AC15.
func TestTheCopysFiguresComeFromTheProcess(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	// A detail that reads like figures, and no copy held: nothing of it renders.
	misleading := "VERSION 1999-12-31_00-00-00_UTC, 4321 rules"
	h.SetPublicSuffixProbe(store.StateReachable, registrabledomain.ProbeDownloaded, &misleading)
	card := publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings))
	if strings.Contains(card, "4321") || strings.Contains(card, "1999-12-31") {
		t.Error("the card read figures out of the availability detail")
	}
	if !strings.Contains(card, "<dd>"+webtest.CatalogueText(t, msgNo)+"</dd>") ||
		!strings.Contains(card, webtest.CatalogueText(t, msgNeverConfirmed)) {
		t.Error("with no copy held the card does not say so")
	}

	// A copy held: its version and rule count, from the process.
	confirmed := h.Clock.Now().Unix() - 600
	h.Suffix.Hold("2026-10-09_08-15-00_UTC", 9876, confirmed)
	card = publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings))
	for _, expected := range []string{
		`<span class="datum" translate="no">2026-10-09_08-15-00_UTC</span>`,
		`<span class="figure">9876</span>`,
		`<span class="figure">` + web.Instant(confirmed) + `</span>`,
		"<dd>" + webtest.CatalogueText(t, msgYes) + "</dd>",
	} {
		if !strings.Contains(card, expected) {
			t.Errorf("the card does not carry %q", expected)
		}
	}

	// A failed refresh over the copy still in use shows both facts.
	h.SetPublicSuffixProbe(store.StateReachable, registrabledomain.ProbeFailed, nil)
	card = publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings))
	if !strings.Contains(card, webtest.CatalogueText(t, msgPublicSuffixFailed)) ||
		!strings.Contains(card, `<span class="figure">9876</span>`) {
		t.Error("a failed refresh over a held copy does not show both the failure and the copy")
	}

	// THE GREP HALF: no code of the interface reads an availability detail except the
	// Sources surface, which draws it verbatim as data.
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, "_") || name == "webtest" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), ".Detail") && !strings.HasSuffix(filepath.ToSlash(path), "surfaces/sources/sources.go") {
			t.Errorf("%s reads an availability or gap detail", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("listing the interface: %v", err)
	}
}

// TestTheRefreshIntervalIsSavedAndRefusedAgainstItsField: the settings save writes the
// interval and hands it to the running service; a refusal stores nothing.
func TestTheRefreshIntervalIsSavedAndRefusedAgainstItsField(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	webtest.AssertStatus(t, h.Post(web.PathSettings, map[string][]string{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix},
		webtest.FieldPublicSuffixInterval: {"43200"}}), http.StatusSeeOther)
	if got := h.Setting(webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("the stored interval is %q", got)
	}
	if h.Live.Get().PublicSuffixRefreshInterval.Seconds() != 43200 {
		t.Error("the live holder was not handed the saved interval")
	}
	status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, map[string][]string{
		webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix}, webtest.FieldPublicSuffixInterval: {"daily"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a refused interval answered %d", status)
	}
	card := publicSuffixCardOf(t, page)
	if !strings.Contains(card, `aria-describedby="public_suffix_interval_refusal"`) ||
		webtest.FieldValue(card, "public_suffix_interval") != "daily" {
		t.Error("the refused interval is not kept and marked in its field")
	}
	if got := h.Setting(webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("a refused interval was stored: %q", got)
	}
}

// TestEachSettingsCardSavesOnlyItsOwnFields: the page is one form and each card's Save
// names its card. A save stores that card's fields and nothing else, sends the browser
// back to the card, and the card says what it did, or what it refused, inside itself.
func TestEachSettingsCardSavesOnlyItsOwnFields(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	urlBefore, urlPresent, err := h.Store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil {
		t.Fatalf("reading the firewall URL: %v", err)
	}

	page := webtest.PageSource(t, h, web.PathSettings)
	if !strings.Contains(page, "<h1>"+webtest.CatalogueText(t, "settings.title")+"</h1>") {
		t.Error("the settings surface has no page heading")
	}
	if strings.Count(page, `<form method="post" action="`+web.PathSettings+`"`) != 1 {
		t.Error("the settings cards are not one form")
	}
	for _, card := range settings.SettingsCards() {
		save := `name="` + webtest.FieldSettingsCard + `" value="` + card + `" formaction="` + web.PathSettings +
			`#settings_` + card + `"`
		if !strings.Contains(settingsCardOf(t, page, card), save) {
			t.Errorf("the %s card has no Save naming it and posting to its anchor", card)
		}
	}

	answer := h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardAppearance}, "theme": {string(web.ThemeDark)}})
	if location := answer.Header.Get("Location"); location != web.PathSettings+"?saved=appearance#settings_appearance" {
		t.Errorf("the save sent the browser to %q, not back to its card", location)
	}
	status, page := webtest.Followed(t, h, answer)
	if status != http.StatusSeeOther || h.Setting(config.KeyTheme) != string(web.ThemeDark) {
		t.Fatalf("saving the appearance card answered %d", status)
	}
	urlAfter, urlStill, err := h.Store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil || urlStill != urlPresent || urlAfter != urlBefore {
		t.Error("saving the appearance card changed the firewall URL")
	}
	saved := `<p class="notice ok">` + webtest.CatalogueText(t, msgSettingsSaved) + "</p>"
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardAppearance), saved) ||
		strings.Count(page, saved) != 1 {
		t.Error("the save is not confirmed inside the card it was made in, and only there")
	}

	// A refused interval is refused inside its card, and the theme saved on the other
	// card is untouched by it.
	status, page = webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix}, webtest.FieldPublicSuffixInterval: {"daily"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a refused interval answered %d", status)
	}
	if h.Setting(config.KeyTheme) != string(web.ThemeDark) ||
		webtest.SelectedIn(t, settingsCardOf(t, page, webtest.SettingsCardAppearance), "theme") != string(web.ThemeDark) {
		t.Error("a refused interval discarded the theme")
	}

	// A card that does not exist is refused.
	status, page = webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {"no_such_card"}}))
	if status != http.StatusBadRequest || !strings.Contains(page, webtest.CatalogueText(t, msgSettingsCardUnknown)) {
		t.Errorf("a card that does not exist answered %d", status)
	}
}

// TestTheRefreshIntervalIsStoredOnlyWhenItChanges: saving the card with the interval
// in force -- the default -- leaves no row behind; a different interval is stored in
// seconds, whatever unit it was typed in.
func TestTheRefreshIntervalIsStoredOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	figure, unit := web.FieldDuration(int64(config.Defaults().PublicSuffixRefreshInterval / time.Second))
	webtest.AssertStatus(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix},
		webtest.FieldPublicSuffixInterval: {figure}, web.UnitFieldName(webtest.FieldPublicSuffixInterval): {unit}}), http.StatusSeeOther)
	if _, present, err := h.Store.Setting(context.Background(), webtest.FieldPublicSuffixInterval); err != nil || present {
		t.Error("saving the interval in force turned the default into a stored row")
	}
	webtest.AssertStatus(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix},
		webtest.FieldPublicSuffixInterval: {"12"}, web.UnitFieldName(webtest.FieldPublicSuffixInterval): {web.UnitHours}}), http.StatusSeeOther)
	if got := h.Setting(webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("12 hours was stored as %q, want 43200", got)
	}
	card := publicSuffixCardOf(t, webtest.PageSource(t, h, web.PathSettings))
	if !webtest.DurationFieldShows(t, card, "public_suffix_interval", "12", web.UnitHours) {
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
