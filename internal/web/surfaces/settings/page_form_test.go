package settings_test

import (
	"context"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers: a firewall URL and a MaxMind
// account ID typed and not saved survive a save of the Public Suffix List card, drawn
// in place and marked edited and not saved, and nothing of them is stored.
func TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	typedURL := "https://firewall.example.net"
	status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard:         {webtest.SettingsCardPublicSuffix},
		"firewall_url":                    {typedURL},
		"api_key":                         {""},
		"certificate_fingerprint":         {""},
		"maxmind_account_id":              {"424242"},
		webtest.FieldPublicSuffixInterval: {"12"},
		web.UnitFieldName(webtest.FieldPublicSuffixInterval): {web.UnitHours},
		"theme": {string(web.ThemeSystem)},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with edits typed in other cards answered %d, not the page in place", status)
	}
	if got := h.Setting(webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("the card saved stored %q, want 43200", got)
	}
	for _, key := range []string{config.KeyOPNsenseBaseURL, config.KeyMaxMindAccountID} {
		if _, present, err := h.Store.Setting(context.Background(), key); err != nil || present {
			t.Errorf("%s, typed in another card, was stored", key)
		}
	}
	edited := `<p class="notice">` + webtest.CatalogueText(t, msgEditedNotSaved) + "</p>"
	firewall := settingsCardOf(t, page, webtest.SettingsCardFirewall)
	geolocation := settingsCardOf(t, page, webtest.SettingsCardGeolocation)
	if webtest.FieldValue(firewall, "firewall_url") != typedURL || !strings.Contains(firewall, edited) {
		t.Error("the firewall URL typed and not saved was not kept, marked edited")
	}
	if webtest.FieldValue(geolocation, "maxmind_account_id") != "424242" || !strings.Contains(geolocation, edited) {
		t.Error("the MaxMind account ID typed and not saved was not kept, marked edited")
	}
	suffix := settingsCardOf(t, page, webtest.SettingsCardPublicSuffix)
	if !strings.Contains(suffix, webtest.CatalogueText(t, msgSettingsSaved)) || strings.Contains(suffix, edited) {
		t.Error("the card saved does not confirm its save")
	}
	if strings.Contains(settingsCardOf(t, page, webtest.SettingsCardAppearance), edited) {
		t.Error("a card whose fields were posted unchanged is marked edited")
	}

	// The firewall card's own save then stores what was kept.
	webtest.Discard(t, h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardFirewall},
		"firewall_url": {typedURL}, "maxmind_account_id": {"424242"}}))
	if got := h.Setting(config.KeyOPNsenseBaseURL); got != typedURL {
		t.Errorf("the firewall card's save stored %q", got)
	}
}

// TestASettingsSubmissionNamingNoCardStoresNothing: the form's default button, which
// the Enter key presses, names no card; nothing is stored, every value typed is kept,
// and the refusal is the page's.
func TestASettingsSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, web.PathSettings)
	webtest.AssertDefaultButtonNamesNoCard(t, page, web.PathSettings)

	before := webtest.DatabaseBytesExcept(t, h, "session")
	status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		"firewall_url": {"https://firewall.example.net"}, "theme": {string(web.ThemeDark)},
		"maxmind_account_id": {"7"}}))
	if status != http.StatusBadRequest || !strings.Contains(page, webtest.CatalogueText(t, msgCardNotNamed)) {
		t.Errorf("a submission naming no card answered %d without its refusal", status)
	}
	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("a submission naming no card stored something")
	}
	if webtest.FieldValue(settingsCardOf(t, page, webtest.SettingsCardFirewall), "firewall_url") != "https://firewall.example.net" ||
		webtest.SelectedIn(t, settingsCardOf(t, page, webtest.SettingsCardAppearance), "theme") != string(web.ThemeDark) {
		t.Error("a submission naming no card discarded what was typed")
	}
}

// TestACarriedVerificationIsOnlyAVerificationState: the redirect after a firewall save
// carries the verification's outcome, and the surface accepts nothing else in its
// place.
func TestACarriedVerificationIsOnlyAVerificationState(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, web.PathSettings+"?saved=firewall&verified=notice.settings_saved")
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardFirewall), webtest.CatalogueText(t, msgVerifyNotAttempted)) {
		t.Error("a carried value that is no verification state was shown as one")
	}
	page = webtest.PageSource(t, h, web.PathSettings+"?saved=firewall&verified="+string(msgVerifyVerified))
	if !strings.Contains(settingsCardOf(t, page, webtest.SettingsCardFirewall), webtest.CatalogueText(t, msgVerifyVerified)) {
		t.Error("the carried verification outcome is not shown")
	}
}

// TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers: an interval typed in the
// filter log's card survives a save of the attribution card, drawn in place and marked
// edited and not saved, and is not stored; and the other way round.
func TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	webtest.AssertDefaultButtonNamesNoCard(t, webtest.PageSource(t, h, webtest.PathCollection), webtest.PathCollectionSettings)

	intervalName := webtest.CardFieldName("firewall_log", webtest.FieldInterval)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard:                             {"attribution"},
		config.KeyAttributionMaxDelay:                 {"7"},
		config.KeyCacheAnswerDelay:                    {"1"},
		web.UnitFieldName(config.KeyCacheAnswerDelay): {web.UnitHours},
		intervalName:                                  {"45"},
		web.UnitFieldName(intervalName):               {web.UnitSeconds},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with an edit typed in another card answered %d, not the page in place", status)
	}
	if got := h.Setting(config.KeyAttributionMaxDelay); got != "7" {
		t.Errorf("the card saved stored %q, want 7", got)
	}
	if _, present, err := h.Store.Setting(context.Background(),
		"poll_interval_firewall_log_seconds"); err != nil || present {
		t.Error("the interval typed in another card was stored")
	}
	edited := `<p class="notice">` + webtest.CatalogueText(t, msgEditedNotSaved) + "</p>"
	filterLog := webtest.CardOf(t, page, "firewall_log")
	if !webtest.DurationFieldShows(t, filterLog, "interval_firewall_log", "45", web.UnitSeconds) ||
		!strings.Contains(filterLog, edited) {
		t.Error("the interval typed and not saved was not kept, marked edited")
	}
	if !strings.Contains(webtest.CardOf(t, page, "attribution"), webtest.CatalogueText(t, msgSettingsSaved)) {
		t.Error("the card saved does not confirm its save")
	}
	if strings.Contains(webtest.CardOf(t, page, "dhcp_lease"), edited) {
		t.Error("a card nothing was typed in is marked edited")
	}

	// And the other way round: a settings card's edit survives a sized card's save.
	dnsInterval := webtest.CardFieldName("dns_lookup", webtest.FieldInterval)
	status, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollection, url.Values{
		webtest.FieldKind: {"dns_lookup"}, dnsInterval: {"2"}, web.UnitFieldName(dnsInterval): {web.UnitMinutes},
		config.KeyLeaseBackendFailurePassLimit: {"9"},
	}))
	if status != http.StatusOK || h.Setting("poll_interval_dns_lookup_seconds") != "120" {
		t.Fatalf("a sized card's save answered %d", status)
	}
	leases := webtest.CardOf(t, page, "lease_backends")
	if webtest.FieldValue(leases, config.KeyLeaseBackendFailurePassLimit) != "9" || !strings.Contains(leases, edited) {
		t.Error("the lease failure limit typed and not saved was not kept, marked edited")
	}
}

// TestACollectionSubmissionNamingNoCardStoresNothing: the default button names no
// card; nothing is stored and every value typed is kept.
func TestACollectionSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	before := webtest.DatabaseBytesExcept(t, h, "session")
	for _, path := range []string{webtest.PathCollectionSettings, webtest.PathCollection} {
		status, page := webtest.StatusAndBody(t, h.Post(path, url.Values{
			config.KeyLeaseBackendFailurePassLimit: {"9"}}))
		if status != http.StatusBadRequest || !strings.Contains(page, webtest.CatalogueText(t, msgCardNotNamed)) {
			t.Errorf("%s: a submission naming no card answered %d without its refusal", path, status)
		}
		if webtest.FieldValue(webtest.CardOf(t, page, "lease_backends"), config.KeyLeaseBackendFailurePassLimit) != "9" {
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
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, webtest.PathCollection)
	for _, kind := range []string{"firewall_log", "security_event", "dhcp_lease", "dns_lookup"} {
		card := webtest.CardOf(t, page, kind)
		if !strings.Contains(card, `<select id="interval_`+kind+`_unit"`) {
			t.Errorf("the %s interval has no unit", kind)
		}
		if !strings.Contains(card, `<label for="interval_`+kind+`">`+webtest.CatalogueText(t, msgFieldPollInterval)+
			"</label>") {
			t.Errorf("the %s interval is not labelled as every other interval is", kind)
		}
	}
	intervalName := webtest.CardFieldName("security_event", webtest.FieldInterval)
	webtest.Discard(t, h.Post(webtest.PathCollection, url.Values{webtest.FieldKind: {"security_event"},
		webtest.CardFieldName("security_event", webtest.FieldPageSize): {"500"},
		intervalName: {"3"}, web.UnitFieldName(intervalName): {web.UnitMinutes}}))
	if got := h.Setting("poll_interval_security_event_seconds"); got != "180" {
		t.Errorf("3 minutes was stored as %q, want 180", got)
	}
}

// TestTheRetentionConfirmationShowsEveryChangeItApplies: a site-name mode changed in
// the same save as a shorter retention is listed in the confirmation, in force and
// asked for, rather than carried unseen; cancelling stores nothing.
func TestTheRetentionConfirmationShowsEveryChangeItApplies(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		web.UnitFieldName(config.KeyRetentionSeconds): {web.UnitDays},
		config.KeyAggregateMode:                       {webtest.AggregateModeNoDomains}}))
	if status != http.StatusOK {
		t.Fatalf("a shorter retention answered %d", status)
	}
	card := webtest.CardOf(t, page, "storage")
	change := "<dt>" + webtest.CatalogueText(t, msgLabelSiteNames) + "</dt>\n<dd>" +
		`<span class="state">` + webtest.CatalogueText(t, msgSiteNamesWithheld) + "</span>"
	if !strings.Contains(card, change) || !strings.Contains(card,
		`<span class="in-force-value"><span class="state">`+webtest.CatalogueText(t, msgSiteNamesShown)+"</span>") {
		t.Error("the confirmation does not show the site-name change it applies")
	}

	// Without a change beside it, only the horizons are listed.
	_, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		web.UnitFieldName(config.KeyRetentionSeconds): {web.UnitDays},
		config.KeyAggregateMode:                       {webtest.AggregateModeFull}}))
	if strings.Contains(webtest.CardOf(t, page, "storage"), webtest.CatalogueText(t, msgLabelSiteNames)) {
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
	if got := h.Setting(config.KeyRetentionSeconds); got != "7776000" {
		t.Errorf("cancelling stored a retention: %q", got)
	}
	if mode, _, err := h.Store.Setting(context.Background(), config.KeyAggregateMode); err != nil ||
		mode == webtest.AggregateModeNoDomains {
		t.Error("cancelling stored the site-name mode")
	}
}
