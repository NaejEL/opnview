package settings_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// TestChangingACredentialTakesEffectWithoutARestart is AC16, and it is the second of
// this cycle's named risks: the credentials reaching the database but not the running
// collectors.
func TestChangingACredentialTakesEffectWithoutARestart(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()
	_ = login
	_ = password

	apiKey := webtest.RandomHex(t, 12)
	firstSecret := webtest.RandomHex(t, 16)

	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, firstSecret), http.StatusSeeOther)

	// The pass the verification made is a real pass: it carried the credentials.
	first := harness.Fake.Last()
	if first.APIKey != apiKey || first.APISecret != firstSecret {
		t.Fatalf("the first pass carried key %q and secret %q rather than what was typed",
			first.APIKey, first.APISecret)
	}

	// A COLLECTION PASS, in this same process, through the same client the collectors
	// hold. Nothing was restarted and nothing was rebuilt.
	assertPassCarries(t, harness, apiKey, firstSecret)

	// Now change only the secret.
	secondSecret := webtest.RandomHex(t, 16)
	if secondSecret == firstSecret {
		t.Fatal("the two generated secrets collided, so this proves nothing")
	}
	generationBefore := harness.Creds.Generation()
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, secondSecret), http.StatusSeeOther)
	if harness.Creds.Generation() <= generationBefore {
		t.Error("the settings surface stored a credential without writing it into the live holder")
	}

	assertPassCarries(t, harness, apiKey, secondSecret)

	// AND NO PRIOR PLAINTEXT OR PRIOR CIPHERTEXT IS LEFT READABLE.
	if where := webtest.DatabaseContains(t, harness, firstSecret); where != "" {
		t.Errorf("the first secret is readable in the database as plaintext, in %s", where)
	}
	if where := webtest.DatabaseContains(t, harness, secondSecret); where != "" {
		t.Errorf("the current secret is readable in the database as plaintext, in %s", where)
	}
	if count := credentialRowCount(t, harness, accounts.CredentialOPNsenseAPISecret); count != 1 {
		t.Errorf("there are %d rows for the API secret rather than one, so a prior "+
			"ciphertext survives beside the current one", count)
	}
}

// assertPassCarries runs one call through the client the collectors hold and asserts
// the credentials it carried.
func assertPassCarries(t *testing.T, harness *webtest.Harness, apiKey, apiSecret string) {
	t.Helper()
	response, err := harness.Server.Client().Call(t.Context(), opnsense.InterfacesInfo,
		opnsense.RequestOptions{})
	if err != nil {
		t.Fatalf("a collection pass failed: %v", err)
	}
	if !response.OK() {
		t.Fatalf("a collection pass reported %q", response.Outcome)
	}
	last := harness.Fake.Last()
	if last.APIKey != apiKey {
		t.Errorf("the pass carried the API key %q rather than %q", last.APIKey, apiKey)
	}
	if last.APISecret != apiSecret {
		t.Error("the pass carried an API secret other than the one last entered")
	}
}

// credentialRowCount counts the rows stored for one credential name.
func credentialRowCount(t *testing.T, harness *webtest.Harness, name string) int {
	t.Helper()
	var count int
	if err := harness.Store.DB().QueryRow(
		"SELECT count(*) FROM encrypted_credential WHERE name = $1", name).Scan(&count); err != nil {
		t.Fatalf("counting the rows for %s: %v", name, err)
	}
	return count
}

// TestVerificationTellsTheThreeAnswersApart is AC17's mapping half.
//
// NONE OF THE THREE IS WORKED AROUND OR REPORTED AS SUCCESS, and each is asserted by
// the string the surface actually renders rather than by an internal value.
func TestVerificationTellsTheThreeAnswersApart(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)

	verified := settingstest.Text(t, harness.Server.Catalogue(), msgVerifyVerified)
	rejected := settingstest.Text(t, harness.Server.Catalogue(), msgVerifyCredentialsBad)
	notFound := settingstest.Text(t, harness.Server.Catalogue(), msgVerifyEndpointNotFound)
	unreachable := settingstest.Text(t, harness.Server.Catalogue(), msgVerifyUnreachable)

	t.Run("the firewall answers", func(t *testing.T) {
		harness.Fake.AnswerAll(http.StatusOK, webtest.MustJSON(t, map[string]any{"rows": []any{}}))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		settingstest.AssertContains(t, body, verified)
		assertLacks(t, body, rejected, notFound, unreachable)
	})

	t.Run("401 is a credential failure", func(t *testing.T) {
		harness.Fake.AnswerAll(http.StatusUnauthorized, []byte(`{}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		settingstest.AssertContains(t, body, rejected)
		assertLacks(t, body, verified, notFound, unreachable)
	})

	t.Run("403 is a credential failure too", func(t *testing.T) {
		harness.Fake.AnswerAll(http.StatusForbidden, []byte(`{}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		settingstest.AssertContains(t, body, rejected)
		assertLacks(t, body, verified)
	})

	t.Run("404 is an absent endpoint", func(t *testing.T) {
		harness.Fake.AnswerAll(http.StatusNotFound, []byte(`{"errorMessage":"Not Found"}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		settingstest.AssertContains(t, body, notFound)
		assertLacks(t, body, verified, rejected, unreachable)
	})

	t.Run("no answer at all is unreachable", func(t *testing.T) {
		harness.Fake.GoDown()
		body := submitAndRead(t, harness, apiKey, apiSecret)
		settingstest.AssertContains(t, body, unreachable)
		assertLacks(t, body, verified, rejected, notFound)
	})
}

// submitAndRead stores the credentials and returns the page that came back.
func submitAndRead(t *testing.T, harness *webtest.Harness, apiKey, apiSecret string) string {
	t.Helper()
	answer := settingstest.StoreCredentials(harness, apiKey, apiSecret)
	status, body := webtest.Followed(t, harness, answer)
	if status != http.StatusSeeOther {
		t.Fatalf("the settings surface answered %d: %s", status, body)
	}
	return body
}

// TestNothingButTheFirewallIsContactedDuringTheThreeSurfaces is AC17's destination
// half and AC18.
//
// The transport behind the client fails any host but the fake AND fails the test, so
// reaching the end of this with no error is the assertion. The explicit MaxMind check
// is here as well, because "no second destination" and "no MaxMind call" are two
// different mistakes: the licence key is stored through the maxmind plugin's settings,
// and nothing is downloaded for it.
func TestNothingButTheFirewallIsContactedDuringTheThreeSurfaces(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()

	_ = harness.Get(web.PathSetup)
	_ = harness.Get(web.PathSignIn)
	_ = harness.Get(web.PathRecover)
	_ = harness.Get(web.PathStylesheet)
	harness.SignIn(login, password)
	_ = harness.Get(web.PathSettings)

	licenceKey := webtest.RandomHex(t, 16)
	accounttest.AssertStatus(t, harness.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardFirewall},
		"firewall_url":            {harness.Fake.BaseURL()},
		"api_key":                 {webtest.RandomHex(t, 12)},
		"api_secret":              {webtest.RandomHex(t, 16)},
	}), http.StatusSeeOther)
	if status, answer := settingstest.SendJSON(harness, "/api/plugins/maxmind/settings",
		map[string]any{"licence_key": licenceKey}); status != http.StatusOK {
		t.Fatalf("storing the MaxMind licence key answered %d %v", status, answer)
	}

	received := harness.Fake.Requests()
	if len(received) == 0 {
		t.Fatal("the firewall was never contacted, so this assertion is vacuous")
	}
	for _, request := range received {
		if request.Path != opnsense.InterfacesInfo.Path {
			t.Errorf("verification called %s rather than the one registry endpoint it declares",
				request.Path)
		}
		if request.Method != http.MethodGet {
			t.Errorf("verification used the method %s", request.Method)
		}
		if command := opnsense.MutatingCommand(request.Path); command != "" {
			t.Errorf("verification named the mutating command %q", command)
		}
	}

	// The endpoint is in the registry. A path invented here could not be issued at
	// all, and this is the recording half of that guarantee.
	registered := false
	for _, endpoint := range opnsense.Registry() {
		if endpoint.Path == settings.VerificationEndpoint().Path {
			registered = true
		}
	}
	if !registered {
		t.Error("the verification endpoint is not in opnsense.Registry()")
	}

	// AC18: the licence key was stored and NOTHING was downloaded for it. The
	// transport would have failed the test on a MaxMind host; this asserts the key
	// reached the database all the same, so the absence of a call is not the absence
	// of the feature.
	if _, stored, err := harness.Store.Credential(
		t.Context(), "maxmind_licence_key"); err != nil {
		t.Fatalf("reading the stored licence key: %v", err)
	} else if !stored {
		t.Error("the MaxMind licence key was not stored")
	}
	if where := webtest.DatabaseContains(t, harness, licenceKey); where != "" {
		t.Errorf("the MaxMind licence key is readable in the database as plaintext, in %s", where)
	}
}

// TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence is AC14 as the settings
// surface reports it.
func TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)),
		http.StatusSeeOther)

	absent := settingstest.Text(t, harness.Server.Catalogue(), msgCredentialAbsent)
	ready := settingstest.Text(t, harness.Server.Catalogue(), msgCredentialReady)
	unreadable := settingstest.Text(t, harness.Server.Catalogue(), msgCredentialUndecryptable)

	readable := pageText(t, harness, web.PathSettings)
	settingstest.AssertContains(t, readable, ready)

	// The database survives; the key file does not. THIS IS THE CASE THE CRITERION IS
	// ABOUT: the service comes up, and what it says is not "no firewall is configured".
	withoutKey := webtest.ReopenWithoutKeyFile(t, harness)
	withoutKey.SignIn(login, password)
	page := pageText(t, withoutKey, web.PathSettings)

	settingstest.AssertContains(t, page, unreadable)
	assertLacks(t, page, ready)
	// AND IT IS NOT REPORTED AS AN ABSENCE. The two are different sentences, and
	// reporting the second as the first sends the reader to look for a setting that is
	// already there.
	if absent == unreadable {
		t.Fatal("the two states share a string, so they cannot be told apart at all")
	}
	assertLacks(t, page, absent)

	// And re-entry is offered: the fields are there to type into.
	for _, field := range []string{`name="firewall_url"`, `name="api_key"`, `name="api_secret"`} {
		if !strings.Contains(webtest.PageSource(t, withoutKey, web.PathSettings), field) {
			t.Errorf("the settings surface offers no %s field, so re-entry is not offered", field)
		}
	}
}

// TestTheTwoStatesAreDistinguishableAtTheSource is AC14 below the interface: the
// values themselves differ, so nothing downstream has to guess.
func TestTheTwoStatesAreDistinguishableAtTheSource(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()

	_, absent, err := config.LoadFirewallCredentials(t.Context(), harness.Store, harness.Box)
	if err != nil {
		t.Fatalf("loading the credentials of an unconfigured installation: %v", err)
	}
	if absent != config.CredentialStateAbsent {
		t.Fatalf("an unconfigured installation reports %q", absent)
	}

	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)),
		http.StatusSeeOther)
	_, ready, err := config.LoadFirewallCredentials(t.Context(), harness.Store, harness.Box)
	if err != nil {
		t.Fatalf("loading the stored credentials: %v", err)
	}
	if ready != config.CredentialStateReady {
		t.Fatalf("a configured installation reports %q", ready)
	}

	// With no key at all, which is the key file having been lost.
	_, lost, err := config.LoadFirewallCredentials(t.Context(), harness.Store, nil)
	if err != nil {
		t.Fatalf("loading credentials with no key: %v", err)
	}
	if lost != config.CredentialStateUndecryptable {
		t.Fatalf("a lost key file reports %q", lost)
	}
	if lost == absent {
		t.Error("a lost key file and an unconfigured installation report the same state")
	}
}

// TestSavingTheThemeDoesNotDestroyTheCredentials is the consequence of a secret field
// that renders empty: an empty submission must leave what is stored alone.
func TestSavingTheThemeDoesNotDestroyTheCredentials(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, apiSecret), http.StatusSeeOther)

	// The page posts every card's fields with every save; the firewall's come back
	// as they are stored, the secret field empty.
	answer := harness.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
		"firewall_url":            {harness.Fake.BaseURL()},
		"api_key":                 {apiKey},
		"api_secret":              {""},
		"theme":                   {string(web.ThemeDark)},
	})
	accounttest.AssertStatus(t, answer, http.StatusSeeOther)

	assertPassCarries(t, harness, apiKey, apiSecret)
	if theme := harness.Server.ReadTheme(t.Context()); theme != web.ThemeDark {
		t.Errorf("the theme was not saved: %q", theme)
	}
}

// TestASecretIsNeverRenderedBackIntoThePage asserts what the surface may not say.
func TestASecretIsNeverRenderedBackIntoThePage(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)
	// The firewall card is saved: the page is drawn again, and the secret is never put back.
	answer := harness.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardFirewall},
		"firewall_url":            {harness.Fake.BaseURL()},
		"api_key":                 {apiKey},
		"api_secret":              {apiSecret},
		"theme":                   {string(web.ThemeSystem)},
	})
	status, submitted := webtest.StatusAndBody(t, answer)
	if status != http.StatusOK && status != http.StatusSeeOther {
		t.Fatalf("the settings surface answered %d", status)
	}
	// The page as the appearance card's save draws it, with an API secret typed in the
	// firewall card and not saved: drawn in place, the secret not put back and reported as
	// not kept.
	answer = harness.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardAppearance},
		"api_secret":              {apiSecret},
		"theme":                   {string(web.ThemeSystem)},
	})
	status, typedElsewhere := webtest.StatusAndBody(t, answer)
	if status != http.StatusOK || !strings.Contains(typedElsewhere,
		`<p class="field-state" id="api_secret_state">`+collectiontest.CatalogueText(t, msgSecretNotKept)+"</p>") {
		t.Errorf("an API secret typed and not saved answered %d and was not reported as not kept", status)
	}
	submitted += typedElsewhere

	account, found, err := harness.Store.AnyAccount(t.Context())
	if err != nil || !found {
		t.Fatalf("reading the account: found=%v err=%v", found, err)
	}

	pages := []string{submitted, webtest.PageSource(t, harness, web.PathSettings)}
	for _, page := range pages {
		for name, forbidden := range map[string]string{
			"the API secret":    apiSecret,
			"the password":      password,
			"the password salt": string(account.Password.Salt),
			"the password hash": string(account.Password.Digest),
			"the key file":      harness.Box.KeyID(),
		} {
			if forbidden != "" && strings.Contains(page, forbidden) {
				t.Errorf("a response body carries %s", name)
			}
		}
	}
	_ = login
}

// pageText fetches one page and returns only what a reader sees.
func pageText(t *testing.T, harness *webtest.Harness, path string) string {
	t.Helper()
	return webtest.VisibleText(webtest.PageSource(t, harness, path))
}

// assertLacks fails if any value appears.
func assertLacks(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(body, value) {
			t.Errorf("the page says %q, which it should not", value)
		}
	}
}

// TestTheVerificationEndpointIsTheOneTheRegistryCarries pins the choice, so replacing
// it is a deliberate act rather than an edit nobody notices.
func TestTheVerificationEndpointIsTheOneTheRegistryCarries(t *testing.T) {
	t.Parallel()
	chosen := settings.VerificationEndpoint()
	if chosen.Path != opnsense.InterfacesInfo.Path {
		t.Errorf("verification calls %s rather than the endpoint documented beside it",
			chosen.Path)
	}
	if chosen.Method != http.MethodGet {
		t.Errorf("verification uses %s rather than a read", chosen.Method)
	}
	if chosen.SurveySection == "" || chosen.UpstreamURL == "" {
		t.Error("the verification endpoint carries no survey citation")
	}
}
