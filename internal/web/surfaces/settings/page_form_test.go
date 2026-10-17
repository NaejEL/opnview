package settings_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/settingfields"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers: a firewall URL typed and not saved
// survives a save of the appearance card, drawn in place and marked edited and not saved, and
// is not stored.
func TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	typedURL := "https://firewall.example.net"
	status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
		"firewall_url":            {typedURL},
		"api_key":                 {""},
		"certificate_fingerprint": {""},
		"theme":                   {string(web.ThemeDark)},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with edits typed in other cards answered %d, not the page in place", status)
	}
	if theme := h.Server.ReadTheme(context.Background()); theme != web.ThemeDark {
		t.Errorf("the card saved stored the theme %q, want %q", theme, web.ThemeDark)
	}
	if _, present, err := h.Store.Setting(context.Background(), config.KeyOPNsenseBaseURL); err != nil || present {
		t.Errorf("%s, typed in another card, was stored", config.KeyOPNsenseBaseURL)
	}
	edited := `<p class="notice">` + collectiontest.CatalogueText(t, msgEditedNotSaved) + "</p>"
	firewall := settingsCardOf(t, page, webtest.SettingsCardFirewall)
	if collectiontest.FieldValue(firewall, "firewall_url") != typedURL || !strings.Contains(firewall, edited) {
		t.Error("the firewall URL typed and not saved was not kept, marked edited")
	}
	appearance := settingsCardOf(t, page, webtest.SettingsCardAppearance)
	if !strings.Contains(appearance, collectiontest.CatalogueText(t, msgSettingsSaved)) || strings.Contains(appearance, edited) {
		t.Error("the card saved does not confirm its save")
	}

	// The firewall card's own save then stores what was kept.
	webtest.Discard(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardFirewall},
		"firewall_url": {typedURL}}))
	if got := collectiontest.Setting(h, config.KeyOPNsenseBaseURL); got != typedURL {
		t.Errorf("the firewall card's save stored %q", got)
	}
}

// TestASettingsSubmissionNamingNoCardStoresNothing: the form's default button, which
// the Enter key presses, names no card; nothing is stored, every value typed is kept,
// and the refusal is the page's.
func TestASettingsSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	page := webtest.PageSource(t, h, web.PathSettings)
	settingstest.AssertDefaultButtonNamesNoCard(t, page, web.PathSettings)

	before := webtest.DatabaseBytesExcept(t, h, "session")
	status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		"firewall_url": {"https://firewall.example.net"}, "theme": {string(web.ThemeDark)}}))
	if status != http.StatusBadRequest || !strings.Contains(page, collectiontest.CatalogueText(t, msgCardNotNamed)) {
		t.Errorf("a submission naming no card answered %d without its refusal", status)
	}
	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("a submission naming no card stored something")
	}
	if collectiontest.FieldValue(settingsCardOf(t, page, webtest.SettingsCardFirewall), "firewall_url") != "https://firewall.example.net" ||
		collectiontest.SelectedIn(t, settingsCardOf(t, page, webtest.SettingsCardAppearance), "theme") != string(web.ThemeDark) {
		t.Error("a submission naming no card discarded what was typed")
	}
}

// TestACarriedVerificationIsOnlyAVerificationState: the redirect after a firewall save
// carries the verification's outcome, and the surface accepts nothing else in its
// place.
func TestACarriedVerificationIsOnlyAVerificationState(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	page := webtest.PageSource(t, h, web.PathSettings+"?saved=firewall&verified=notice.settings_saved")
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardFirewall), collectiontest.CatalogueText(t, msgVerifyNotAttempted)) {
		t.Error("a carried value that is no verification state was shown as one")
	}
	page = webtest.PageSource(t, h, web.PathSettings+"?saved=firewall&verified="+string(msgVerifyVerified))
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardFirewall), collectiontest.CatalogueText(t, msgVerifyVerified)) {
		t.Error("the carried verification outcome is not shown")
	}
}

// TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers: an interval typed in the
// filter log's card survives a save of the attribution card, drawn in place and marked
// edited and not saved, and is not stored; and the other way round.
func TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	settingstest.AssertDefaultButtonNamesNoCard(t, webtest.PageSource(t, h, webtest.PathCollection), webtest.PathCollectionSettings)

	intervalName := webtest.CardFieldName("firewall_log", webtest.FieldInterval)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard:                                       {"attribution"},
		config.KeyAttributionMaxDelay:                           {"7"},
		config.KeyCacheAnswerDelay:                              {"1"},
		settingfields.UnitFieldName(config.KeyCacheAnswerDelay): {settingfields.UnitHours},
		intervalName:                                            {"45"},
		settingfields.UnitFieldName(intervalName):               {settingfields.UnitSeconds},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with an edit typed in another card answered %d, not the page in place", status)
	}
	if got := collectiontest.Setting(h, config.KeyAttributionMaxDelay); got != "7" {
		t.Errorf("the card saved stored %q, want 7", got)
	}
	if _, present, err := h.Store.Setting(context.Background(),
		"poll_interval_firewall_log_seconds"); err != nil || present {
		t.Error("the interval typed in another card was stored")
	}
	edited := `<p class="notice">` + collectiontest.CatalogueText(t, msgEditedNotSaved) + "</p>"
	filterLog := collectiontest.CardOf(t, page, "firewall_log")
	if !collectiontest.DurationFieldShows(t, filterLog, "interval_firewall_log", "45", settingfields.UnitSeconds) ||
		!strings.Contains(filterLog, edited) {
		t.Error("the interval typed and not saved was not kept, marked edited")
	}
	if !strings.Contains(collectiontest.CardOf(t, page, "attribution"), collectiontest.CatalogueText(t, msgSettingsSaved)) {
		t.Error("the card saved does not confirm its save")
	}
	if strings.Contains(collectiontest.CardOf(t, page, "dhcp_lease"), edited) {
		t.Error("a card nothing was typed in is marked edited")
	}

	// And the other way round: a settings card's edit survives a sized card's save.
	dnsInterval := webtest.CardFieldName("dns_lookup", webtest.FieldInterval)
	status, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollection, url.Values{
		webtest.FieldKind: {"dns_lookup"}, dnsInterval: {"2"}, settingfields.UnitFieldName(dnsInterval): {settingfields.UnitMinutes},
		config.KeyLeaseBackendFailurePassLimit: {"9"},
	}))
	if status != http.StatusOK || collectiontest.Setting(h, "poll_interval_dns_lookup_seconds") != "120" {
		t.Fatalf("a sized card's save answered %d", status)
	}
	leases := collectiontest.CardOf(t, page, "lease_backends")
	if collectiontest.FieldValue(leases, config.KeyLeaseBackendFailurePassLimit) != "9" || !strings.Contains(leases, edited) {
		t.Error("the lease failure limit typed and not saved was not kept, marked edited")
	}
}

// TestACollectionSubmissionNamingNoCardStoresNothing: the default button names no
// card; nothing is stored and every value typed is kept.
func TestACollectionSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	before := webtest.DatabaseBytesExcept(t, h, "session")
	for _, path := range []string{webtest.PathCollectionSettings, webtest.PathCollection} {
		status, page := webtest.StatusAndBody(t, h.Post(path, url.Values{
			config.KeyLeaseBackendFailurePassLimit: {"9"}}))
		if status != http.StatusBadRequest || !strings.Contains(page, collectiontest.CatalogueText(t, msgCardNotNamed)) {
			t.Errorf("%s: a submission naming no card answered %d without its refusal", path, status)
		}
		if collectiontest.FieldValue(collectiontest.CardOf(t, page, "lease_backends"), config.KeyLeaseBackendFailurePassLimit) != "9" {
			t.Errorf("%s: a submission naming no card discarded what was typed", path)
		}
	}
	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("a submission naming no card stored something")
	}
}

// TestASizedKindsIntervalIsTypedWithAUnit: the four sized kinds' intervals are a figure
// beside a unit, like every other interval on the page, and are stored in seconds.
func TestASizedKindsIntervalIsTypedWithAUnit(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	page := webtest.PageSource(t, h, webtest.PathCollection)
	for _, kind := range []string{"firewall_log", "security_event", "dhcp_lease", "dns_lookup"} {
		card := collectiontest.CardOf(t, page, kind)
		if !strings.Contains(card, `<select id="interval_`+kind+`_unit"`) {
			t.Errorf("the %s interval has no unit", kind)
		}
		if !strings.Contains(card, `<label for="interval_`+kind+`">`+collectiontest.CatalogueText(t, msgFieldPollInterval)+
			"</label>") {
			t.Errorf("the %s interval is not labelled as every other interval is", kind)
		}
	}
	intervalName := webtest.CardFieldName("security_event", webtest.FieldInterval)
	webtest.Discard(t, h.Post(webtest.PathCollection, url.Values{webtest.FieldKind: {"security_event"},
		webtest.CardFieldName("security_event", webtest.FieldPageSize): {"500"},
		intervalName: {"3"}, settingfields.UnitFieldName(intervalName): {settingfields.UnitMinutes}}))
	if got := collectiontest.Setting(h, "poll_interval_security_event_seconds"); got != "180" {
		t.Errorf("3 minutes was stored as %q, want 180", got)
	}
}

// TestTheRetentionConfirmationShowsEveryChangeItApplies: a site-name mode changed in
// the same save as a shorter retention is listed in the confirmation, in force and
// asked for, rather than carried unseen; cancelling stores nothing.
func TestTheRetentionConfirmationShowsEveryChangeItApplies(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		settingfields.UnitFieldName(config.KeyRetentionSeconds): {settingfields.UnitDays},
		config.KeyAggregateMode:                                 {webtest.AggregateModeNoDomains}}))
	if status != http.StatusOK {
		t.Fatalf("a shorter retention answered %d", status)
	}
	card := collectiontest.CardOf(t, page, "storage")
	change := "<dt>" + collectiontest.CatalogueText(t, msgLabelSiteNames) + "</dt>\n<dd>" +
		`<span class="state">` + collectiontest.CatalogueText(t, msgSiteNamesWithheld) + "</span>"
	if !strings.Contains(card, change) || !strings.Contains(card,
		`<span class="in-force-value"><span class="state">`+collectiontest.CatalogueText(t, msgSiteNamesShown)+"</span>") {
		t.Error("the confirmation does not show the site-name change it applies")
	}

	// Without a change beside it, only the horizons are listed.
	_, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		settingfields.UnitFieldName(config.KeyRetentionSeconds): {settingfields.UnitDays},
		config.KeyAggregateMode:                                 {webtest.AggregateModeFull}}))
	if strings.Contains(collectiontest.CardOf(t, page, "storage"), collectiontest.CatalogueText(t, msgLabelSiteNames)) {
		t.Error("the confirmation lists a setting the save does not change")
	}

	// Cancel stores nothing and returns to the card.
	answer := h.Post(webtest.PathCollectionSettings, url.Values{webtest.FieldCancel: {"storage"},
		config.KeyRetentionSeconds: {"86400"}, config.KeyAggregateMode: {webtest.AggregateModeNoDomains},
		webtest.FieldConfirm: {"86400"}})
	if status, _ := webtest.StatusAndBody(t, answer); status != http.StatusSeeOther ||
		answer.Header.Get("Location") != webtest.PathCollection+"#card_storage" {
		t.Errorf("cancelling answered %d to %q", status, answer.Header.Get("Location"))
	}
	if got := collectiontest.Setting(h, config.KeyRetentionSeconds); got != "7776000" {
		t.Errorf("cancelling stored a retention: %q", got)
	}
	if mode, _, err := h.Store.Setting(context.Background(), config.KeyAggregateMode); err != nil ||
		mode == webtest.AggregateModeNoDomains {
		t.Error("cancelling stored the site-name mode")
	}
}

// TestEachSettingsCardSavesOnlyItsOwnFields: the page is one form and each card's Save
// names its card. A save stores that card's fields and nothing else, sends the browser
// back to the card, and the card says what it did, or what it refused, inside itself.
func TestEachSettingsCardSavesOnlyItsOwnFields(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	urlBefore, urlPresent, err := h.Store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil {
		t.Fatalf("reading the firewall URL: %v", err)
	}

	page := webtest.PageSource(t, h, web.PathSettings)
	if !strings.Contains(page, "<h1>"+collectiontest.CatalogueText(t, "settings.title")+"</h1>") {
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
	if status != http.StatusSeeOther || collectiontest.Setting(h, config.KeyTheme) != string(web.ThemeDark) {
		t.Fatalf("saving the appearance card answered %d", status)
	}
	urlAfter, urlStill, err := h.Store.Setting(context.Background(), config.KeyOPNsenseBaseURL)
	if err != nil || urlStill != urlPresent || urlAfter != urlBefore {
		t.Error("saving the appearance card changed the firewall URL")
	}
	saved := `<p class="notice ok">` + collectiontest.CatalogueText(t, msgSettingsSaved) + "</p>"
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardAppearance), saved) ||
		strings.Count(page, saved) != 1 {
		t.Error("the save is not confirmed inside the card it was made in, and only there")
	}

	// A refused firewall URL is refused inside its card, and the theme saved on the other
	// card is untouched by it.
	status, page = webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardFirewall}, "firewall_url": {"not-a-url"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a refused firewall URL answered %d", status)
	}
	if collectiontest.Setting(h, config.KeyTheme) != string(web.ThemeDark) ||
		collectiontest.SelectedIn(t, settingsCardOf(t, page, webtest.SettingsCardAppearance), "theme") != string(web.ThemeDark) {
		t.Error("a refused firewall URL discarded the theme")
	}

	// A card that does not exist is refused.
	status, page = webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {"no_such_card"}}))
	if status != http.StatusBadRequest || !strings.Contains(page, collectiontest.CatalogueText(t, msgSettingsCardUnknown)) {
		t.Errorf("a card that does not exist answered %d", status)
	}
}

// settingsCardOf is one card of the settings page, from its section to its end.
func settingsCardOf(t *testing.T, page, card string) string {
	t.Helper()
	start := strings.Index(page, `<section class="card" id="settings_`+card+`"`)
	if start < 0 {
		t.Fatalf("the settings page has no %s card", card)
	}
	return page[start : start+strings.Index(page[start:], "</section>")]
}
