package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
)

// The two surfaces whose cards share one form: the settings surface and the collection
// surface.
//
// PROVENANCE OF EVERY FIXTURE HERE: SYNTHESISED. Every value typed is chosen by the
// test; host names are RFC 2606 reserved names. Nothing is read off a network.

// followed reads the answer to a submission: a 303 back to a surface is followed, and
// the page it lands on returned with the 303; any other answer is read as it is.
func followed(t *testing.T, h *harness, answer *http.Response) (int, string) {
	t.Helper()
	status, body := statusAndBody(t, answer)
	if status != http.StatusSeeOther {
		return status, body
	}
	location := answer.Header.Get("Location")
	if cut := strings.Index(location, "#"); cut >= 0 {
		location = location[:cut]
	}
	if !strings.HasPrefix(location, "/") {
		t.Fatalf("a save redirected off the surface, to %q", location)
	}
	return status, pageSource(t, h, location)
}

// TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers: a firewall URL and a MaxMind
// account ID typed and not saved survive a save of the Public Suffix List card, drawn
// in place and marked edited and not saved, and nothing of them is stored.
func TestSavingOneSettingsCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	typedURL := "https://firewall.example.net"
	status, page := statusAndBody(t, h.post(PathSettings, url.Values{
		fieldSettingsCard:                        {settingsCardPublicSuffix},
		"firewall_url":                           {typedURL},
		"api_key":                                {""},
		"certificate_fingerprint":                {""},
		"maxmind_account_id":                     {"424242"},
		fieldPublicSuffixInterval:                {"12"},
		unitFieldName(fieldPublicSuffixInterval): {unitHours},
		"theme":                                  {string(ThemeSystem)},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with edits typed in other cards answered %d, not the page in place", status)
	}
	if got := h.setting(fieldPublicSuffixInterval); got != "43200" {
		t.Errorf("the card saved stored %q, want 43200", got)
	}
	for _, key := range []string{config.KeyOPNsenseBaseURL, config.KeyMaxMindAccountID} {
		if _, present, err := h.store.Setting(context.Background(), key); err != nil || present {
			t.Errorf("%s, typed in another card, was stored", key)
		}
	}
	edited := `<p class="notice">` + catalogueText(t, msgEditedNotSaved) + "</p>"
	firewall := settingsCardOf(t, page, settingsCardFirewall)
	geolocation := settingsCardOf(t, page, settingsCardGeolocation)
	if fieldValue(firewall, "firewall_url") != typedURL || !strings.Contains(firewall, edited) {
		t.Error("the firewall URL typed and not saved was not kept, marked edited")
	}
	if fieldValue(geolocation, "maxmind_account_id") != "424242" || !strings.Contains(geolocation, edited) {
		t.Error("the MaxMind account ID typed and not saved was not kept, marked edited")
	}
	suffix := settingsCardOf(t, page, settingsCardPublicSuffix)
	if !strings.Contains(suffix, catalogueText(t, msgSettingsSaved)) || strings.Contains(suffix, edited) {
		t.Error("the card saved does not confirm its save")
	}
	if strings.Contains(settingsCardOf(t, page, settingsCardAppearance), edited) {
		t.Error("a card whose fields were posted unchanged is marked edited")
	}

	// The firewall card's own save then stores what was kept.
	discard(t, h.post(PathSettings, url.Values{fieldSettingsCard: {settingsCardFirewall},
		"firewall_url": {typedURL}, "maxmind_account_id": {"424242"}}))
	if got := h.setting(config.KeyOPNsenseBaseURL); got != typedURL {
		t.Errorf("the firewall card's save stored %q", got)
	}
}

// TestASettingsSubmissionNamingNoCardStoresNothing: the form's default button, which
// the Enter key presses, names no card; nothing is stored, every value typed is kept,
// and the refusal is the page's.
func TestASettingsSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	page := pageSource(t, h, PathSettings)
	assertDefaultButtonNamesNoCard(t, page, PathSettings)

	before := databaseBytesExcept(t, h, "session")
	status, page := statusAndBody(t, h.post(PathSettings, url.Values{
		"firewall_url": {"https://firewall.example.net"}, "theme": {string(ThemeDark)},
		"maxmind_account_id": {"7"}}))
	if status != http.StatusBadRequest || !strings.Contains(page, catalogueText(t, msgCardNotNamed)) {
		t.Errorf("a submission naming no card answered %d without its refusal", status)
	}
	if databaseBytesExcept(t, h, "session") != before {
		t.Error("a submission naming no card stored something")
	}
	if fieldValue(settingsCardOf(t, page, settingsCardFirewall), "firewall_url") != "https://firewall.example.net" ||
		selectedIn(t, settingsCardOf(t, page, settingsCardAppearance), "theme") != string(ThemeDark) {
		t.Error("a submission naming no card discarded what was typed")
	}
}

// assertDefaultButtonNamesNoCard holds a page's one form to its first button -- the
// one the Enter key presses -- being a hidden one that names no card.
func assertDefaultButtonNamesNoCard(t *testing.T, page, action string) {
	t.Helper()
	opening := `<form method="post" action="` + action + `"`
	if strings.Count(page, opening) != 1 {
		t.Fatalf("the page does not carry exactly one form posting to %s", action)
	}
	form := page[strings.Index(page, opening):]
	first := form[strings.Index(form, "<button"):]
	first = first[:strings.Index(first, ">")]
	if strings.Contains(first, "name=") || !strings.Contains(first, `class="visually-hidden"`) {
		t.Errorf("the form's first button, the Enter key's, is not a hidden one naming no card: %s", first)
	}
}

// TestACarriedVerificationIsOnlyAVerificationState: the redirect after a firewall save
// carries the verification's outcome, and the surface accepts nothing else in its
// place.
func TestACarriedVerificationIsOnlyAVerificationState(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	page := pageSource(t, h, PathSettings+"?saved=firewall&verified=notice.settings_saved")
	if !strings.Contains(settingsCardOf(t, page, settingsCardFirewall), catalogueText(t, msgVerifyNotAttempted)) {
		t.Error("a carried value that is no verification state was shown as one")
	}
	page = pageSource(t, h, PathSettings+"?saved=firewall&verified="+string(msgVerifyVerified))
	if !strings.Contains(settingsCardOf(t, page, settingsCardFirewall), catalogueText(t, msgVerifyVerified)) {
		t.Error("the carried verification outcome is not shown")
	}
}

// TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers: an interval typed in the
// filter log's card survives a save of the attribution card, drawn in place and marked
// edited and not saved, and is not stored; and the other way round.
func TestSavingOneCollectionCardKeepsWhatWasTypedInTheOthers(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	assertDefaultButtonNamesNoCard(t, pageSource(t, h, PathCollection), PathCollectionSettings)

	intervalName := cardFieldName("firewall_log", fieldInterval)
	status, page := statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard:                                 {"attribution"},
		config.KeyAttributionMaxDelay:             {"7"},
		config.KeyCacheAnswerDelay:                {"1"},
		unitFieldName(config.KeyCacheAnswerDelay): {unitHours},
		intervalName:                              {"45"},
		unitFieldName(intervalName):               {unitSeconds},
	}))
	if status != http.StatusOK {
		t.Fatalf("a save with an edit typed in another card answered %d, not the page in place", status)
	}
	if got := h.setting(config.KeyAttributionMaxDelay); got != "7" {
		t.Errorf("the card saved stored %q, want 7", got)
	}
	if _, present, err := h.store.Setting(context.Background(),
		"poll_interval_firewall_log_seconds"); err != nil || present {
		t.Error("the interval typed in another card was stored")
	}
	edited := `<p class="notice">` + catalogueText(t, msgEditedNotSaved) + "</p>"
	filterLog := cardOf(t, page, "firewall_log")
	if !durationFieldShows(t, filterLog, "interval_firewall_log", "45", unitSeconds) ||
		!strings.Contains(filterLog, edited) {
		t.Error("the interval typed and not saved was not kept, marked edited")
	}
	if !strings.Contains(cardOf(t, page, "attribution"), catalogueText(t, msgSettingsSaved)) {
		t.Error("the card saved does not confirm its save")
	}
	if strings.Contains(cardOf(t, page, "dhcp_lease"), edited) {
		t.Error("a card nothing was typed in is marked edited")
	}

	// And the other way round: a settings card's edit survives a sized card's save.
	dnsInterval := cardFieldName("dns_lookup", fieldInterval)
	status, page = statusAndBody(t, h.post(PathCollection, url.Values{
		fieldKind: {"dns_lookup"}, dnsInterval: {"2"}, unitFieldName(dnsInterval): {unitMinutes},
		config.KeyLeaseBackendFailurePassLimit: {"9"},
	}))
	if status != http.StatusOK || h.setting("poll_interval_dns_lookup_seconds") != "120" {
		t.Fatalf("a sized card's save answered %d", status)
	}
	leases := cardOf(t, page, "lease_backends")
	if fieldValue(leases, config.KeyLeaseBackendFailurePassLimit) != "9" || !strings.Contains(leases, edited) {
		t.Error("the lease failure limit typed and not saved was not kept, marked edited")
	}
}

// TestACollectionSubmissionNamingNoCardStoresNothing: the default button names no
// card; nothing is stored and every value typed is kept.
func TestACollectionSubmissionNamingNoCardStoresNothing(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	before := databaseBytesExcept(t, h, "session")
	for _, path := range []string{PathCollectionSettings, PathCollection} {
		status, page := statusAndBody(t, h.post(path, url.Values{
			config.KeyLeaseBackendFailurePassLimit: {"9"}}))
		if status != http.StatusBadRequest || !strings.Contains(page, catalogueText(t, msgCardNotNamed)) {
			t.Errorf("%s: a submission naming no card answered %d without its refusal", path, status)
		}
		if fieldValue(cardOf(t, page, "lease_backends"), config.KeyLeaseBackendFailurePassLimit) != "9" {
			t.Errorf("%s: a submission naming no card discarded what was typed", path)
		}
	}
	if databaseBytesExcept(t, h, "session") != before {
		t.Error("a submission naming no card stored something")
	}
}

// TestASizedKindsIntervalIsTypedWithAUnit: the four sized kinds' intervals are a figure
// beside a unit, like every other interval on the page, and are stored in seconds.
func TestASizedKindsIntervalIsTypedWithAUnit(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	page := pageSource(t, h, PathCollection)
	for _, kind := range []string{"firewall_log", "security_event", "dhcp_lease", "dns_lookup"} {
		card := cardOf(t, page, kind)
		if !strings.Contains(card, `<select id="interval_`+kind+`_unit"`) {
			t.Errorf("the %s interval has no unit", kind)
		}
		if !strings.Contains(card, `<label for="interval_`+kind+`">`+catalogueText(t, msgFieldPollInterval)+
			"</label>") {
			t.Errorf("the %s interval is not labelled as every other interval is", kind)
		}
	}
	intervalName := cardFieldName("security_event", fieldInterval)
	discard(t, h.post(PathCollection, url.Values{fieldKind: {"security_event"},
		cardFieldName("security_event", fieldPageSize): {"500"},
		intervalName: {"3"}, unitFieldName(intervalName): {unitMinutes}}))
	if got := h.setting("poll_interval_security_event_seconds"); got != "180" {
		t.Errorf("3 minutes was stored as %q, want 180", got)
	}
}

// TestTheRetentionConfirmationShowsEveryChangeItApplies: a site-name mode changed in
// the same save as a shorter retention is listed in the confirmation, in force and
// asked for, rather than carried unseen; cancelling stores nothing.
func TestTheRetentionConfirmationShowsEveryChangeItApplies(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	status, page := statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		unitFieldName(config.KeyRetentionSeconds): {unitDays},
		config.KeyAggregateMode:                   {aggregateModeNoDomains}}))
	if status != http.StatusOK {
		t.Fatalf("a shorter retention answered %d", status)
	}
	card := cardOf(t, page, "storage")
	change := "<dt>" + catalogueText(t, msgLabelSiteNames) + "</dt>\n<dd>" +
		`<span class="state">` + catalogueText(t, msgSiteNamesWithheld) + "</span>"
	if !strings.Contains(card, change) || !strings.Contains(card,
		`<span class="in-force-value"><span class="state">`+catalogueText(t, msgSiteNamesShown)+"</span>") {
		t.Error("the confirmation does not show the site-name change it applies")
	}

	// Without a change beside it, only the horizons are listed.
	_, page = statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		unitFieldName(config.KeyRetentionSeconds): {unitDays},
		config.KeyAggregateMode:                   {aggregateModeFull}}))
	if strings.Contains(cardOf(t, page, "storage"), catalogueText(t, msgLabelSiteNames)) {
		t.Error("the confirmation lists a setting the save does not change")
	}

	// Cancel stores nothing and returns to the card.
	answer := h.post(PathCollectionSettings, url.Values{fieldCancel: {"storage"},
		config.KeyRetentionSeconds: {"86400"}, config.KeyAggregateMode: {aggregateModeNoDomains},
		fieldConfirm: {"86400"}})
	if status, _ := statusAndBody(t, answer); status != http.StatusSeeOther ||
		answer.Header.Get("Location") != PathCollection+"#card_storage" {
		t.Errorf("cancelling answered %d to %q", status, answer.Header.Get("Location"))
	}
	if got := h.setting(config.KeyRetentionSeconds); got != "7776000" {
		t.Errorf("cancelling stored a retention: %q", got)
	}
	if mode, _, err := h.store.Setting(context.Background(), config.KeyAggregateMode); err != nil ||
		mode == aggregateModeNoDomains {
		t.Error("cancelling stored the site-name mode")
	}
}
