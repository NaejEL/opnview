package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// storeCredentials types a firewall URL and a key/secret pair into the settings
// surface. Every value is generated per run, and the URL is the loopback fake's,
// chosen by the operating system.
func (h *harness) storeCredentials(apiKey, apiSecret string) *http.Response {
	h.t.Helper()
	return h.post(PathSettings, url.Values{
		"firewall_url": {h.fake.baseURL()},
		"api_key":      {apiKey},
		"api_secret":   {apiSecret},
		"theme":        {string(ThemeSystem)},
	})
}

// TestChangingACredentialTakesEffectWithoutARestart is AC16, and it is the second of
// this cycle's named risks: the credentials reaching the database but not the running
// collectors.
func TestChangingACredentialTakesEffectWithoutARestart(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()
	_ = login
	_ = password

	apiKey := randomHex(t, 12)
	firstSecret := randomHex(t, 16)

	assertStatus(t, harness.storeCredentials(apiKey, firstSecret), http.StatusOK)

	// The pass the verification made is a real pass: it carried the credentials.
	first := harness.fake.last()
	if first.apiKey != apiKey || first.apiSecret != firstSecret {
		t.Fatalf("the first pass carried key %q and secret %q rather than what was typed",
			first.apiKey, first.apiSecret)
	}

	// A COLLECTION PASS, in this same process, through the same client the collectors
	// hold. Nothing was restarted and nothing was rebuilt.
	assertPassCarries(t, harness, apiKey, firstSecret)

	// Now change only the secret.
	secondSecret := randomHex(t, 16)
	if secondSecret == firstSecret {
		t.Fatal("the two generated secrets collided, so this proves nothing")
	}
	generationBefore := harness.creds.Generation()
	assertStatus(t, harness.storeCredentials(apiKey, secondSecret), http.StatusOK)
	if harness.creds.Generation() <= generationBefore {
		t.Error("the settings surface stored a credential without writing it into the live holder")
	}

	assertPassCarries(t, harness, apiKey, secondSecret)

	// AND NO PRIOR PLAINTEXT OR PRIOR CIPHERTEXT IS LEFT READABLE.
	if where := databaseContains(t, harness, firstSecret); where != "" {
		t.Errorf("the first secret is readable in the database as plaintext, in %s", where)
	}
	if where := databaseContains(t, harness, secondSecret); where != "" {
		t.Errorf("the current secret is readable in the database as plaintext, in %s", where)
	}
	if count := credentialRowCount(t, harness, store.CredentialOPNsenseAPISecret); count != 1 {
		t.Errorf("there are %d rows for the API secret rather than one, so a prior "+
			"ciphertext survives beside the current one", count)
	}
}

// assertPassCarries runs one call through the client the collectors hold and asserts
// the credentials it carried.
func assertPassCarries(t *testing.T, harness *harness, apiKey, apiSecret string) {
	t.Helper()
	response, err := harness.server.client.Call(t.Context(), opnsense.InterfacesInfo,
		opnsense.RequestOptions{})
	if err != nil {
		t.Fatalf("a collection pass failed: %v", err)
	}
	if !response.OK() {
		t.Fatalf("a collection pass reported %q", response.Outcome)
	}
	last := harness.fake.last()
	if last.apiKey != apiKey {
		t.Errorf("the pass carried the API key %q rather than %q", last.apiKey, apiKey)
	}
	if last.apiSecret != apiSecret {
		t.Error("the pass carried an API secret other than the one last entered")
	}
}

// credentialRowCount counts the rows stored for one credential name.
func credentialRowCount(t *testing.T, harness *harness, name string) int {
	t.Helper()
	var count int
	if err := harness.store.DB().QueryRow(
		"SELECT count(*) FROM encrypted_credential WHERE name = ?", name).Scan(&count); err != nil {
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
	harness := newHarness(t)
	harness.completeSetup()
	apiKey, apiSecret := randomHex(t, 12), randomHex(t, 16)

	verified := text(t, harness.server.renderer.catalogue, msgVerifyVerified)
	rejected := text(t, harness.server.renderer.catalogue, msgVerifyCredentialsBad)
	notFound := text(t, harness.server.renderer.catalogue, msgVerifyEndpointNotFound)
	unreachable := text(t, harness.server.renderer.catalogue, msgVerifyUnreachable)

	t.Run("the firewall answers", func(t *testing.T) {
		harness.fake.answer(http.StatusOK, mustJSON(t, map[string]any{"rows": []any{}}))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		assertContains(t, body, verified)
		assertLacks(t, body, rejected, notFound, unreachable)
	})

	t.Run("401 is a credential failure", func(t *testing.T) {
		harness.fake.answer(http.StatusUnauthorized, []byte(`{}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		assertContains(t, body, rejected)
		assertLacks(t, body, verified, notFound, unreachable)
	})

	t.Run("403 is a credential failure too", func(t *testing.T) {
		harness.fake.answer(http.StatusForbidden, []byte(`{}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		assertContains(t, body, rejected)
		assertLacks(t, body, verified)
	})

	t.Run("404 is an absent endpoint", func(t *testing.T) {
		harness.fake.answer(http.StatusNotFound, []byte(`{"errorMessage":"Not Found"}`))
		body := submitAndRead(t, harness, apiKey, apiSecret)
		assertContains(t, body, notFound)
		assertLacks(t, body, verified, rejected, unreachable)
	})

	t.Run("no answer at all is unreachable", func(t *testing.T) {
		harness.fake.goDown()
		body := submitAndRead(t, harness, apiKey, apiSecret)
		assertContains(t, body, unreachable)
		assertLacks(t, body, verified, rejected, notFound)
	})
}

// submitAndRead stores the credentials and returns the page that came back.
func submitAndRead(t *testing.T, harness *harness, apiKey, apiSecret string) string {
	t.Helper()
	answer := harness.storeCredentials(apiKey, apiSecret)
	status, body := statusAndBody(t, answer)
	if status != http.StatusOK {
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
// different mistakes and the second one is the one this cycle could plausibly make.
func TestNothingButTheFirewallIsContactedDuringTheThreeSurfaces(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()

	_ = harness.get(PathSetup)
	_ = harness.get(PathSignIn)
	_ = harness.get(PathRecover)
	_ = harness.get(PathStylesheet)
	harness.signIn(login, password)
	_ = harness.get(PathSettings)

	licenceKey := randomHex(t, 16)
	answer := harness.post(PathSettings, url.Values{
		"firewall_url":        {harness.fake.baseURL()},
		"api_key":             {randomHex(t, 12)},
		"api_secret":          {randomHex(t, 16)},
		"maxmind_licence_key": {licenceKey},
		"theme":               {string(ThemeSystem)},
	})
	assertStatus(t, answer, http.StatusOK)

	received := harness.fake.received()
	if len(received) == 0 {
		t.Fatal("the firewall was never contacted, so this assertion is vacuous")
	}
	for _, request := range received {
		if request.path != opnsense.InterfacesInfo.Path {
			t.Errorf("verification called %s rather than the one registry endpoint it declares",
				request.path)
		}
		if request.method != http.MethodGet {
			t.Errorf("verification used the method %s", request.method)
		}
		if command := opnsense.MutatingCommand(request.path); command != "" {
			t.Errorf("verification named the mutating command %q", command)
		}
	}

	// The endpoint is in the registry. A path invented here could not be issued at
	// all, and this is the recording half of that guarantee.
	registered := false
	for _, endpoint := range opnsense.Registry() {
		if endpoint.Path == verificationEndpoint().Path {
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
	if _, stored, err := harness.store.Credential(
		t.Context(), store.CredentialMaxMindLicenceKey); err != nil {
		t.Fatalf("reading the stored licence key: %v", err)
	} else if !stored {
		t.Error("the MaxMind licence key was not stored")
	}
	if where := databaseContains(t, harness, licenceKey); where != "" {
		t.Errorf("the MaxMind licence key is readable in the database as plaintext, in %s", where)
	}
}

// TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence is AC14 as the settings
// surface reports it.
func TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()
	assertStatus(t, harness.storeCredentials(randomHex(t, 12), randomHex(t, 16)),
		http.StatusOK)

	absent := text(t, harness.server.renderer.catalogue, msgCredentialAbsent)
	ready := text(t, harness.server.renderer.catalogue, msgCredentialReady)
	unreadable := text(t, harness.server.renderer.catalogue, msgCredentialUndecryptable)

	readable := pageText(t, harness, PathSettings)
	assertContains(t, readable, ready)

	// The database survives; the key file does not. THIS IS THE CASE THE CRITERION IS
	// ABOUT: the service comes up, and what it says is not "no firewall is configured".
	withoutKey := reopenWithoutKeyFile(t, harness)
	withoutKey.signIn(login, password)
	page := pageText(t, withoutKey, PathSettings)

	assertContains(t, page, unreadable)
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
		if !strings.Contains(pageSource(t, withoutKey, PathSettings), field) {
			t.Errorf("the settings surface offers no %s field, so re-entry is not offered", field)
		}
	}
}

// TestTheTwoStatesAreDistinguishableAtTheSource is AC14 below the interface: the
// values themselves differ, so nothing downstream has to guess.
func TestTheTwoStatesAreDistinguishableAtTheSource(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	harness.completeSetup()

	_, absent, err := config.LoadFirewallCredentials(t.Context(), harness.store, harness.box)
	if err != nil {
		t.Fatalf("loading the credentials of an unconfigured installation: %v", err)
	}
	if absent != config.CredentialStateAbsent {
		t.Fatalf("an unconfigured installation reports %q", absent)
	}

	assertStatus(t, harness.storeCredentials(randomHex(t, 12), randomHex(t, 16)),
		http.StatusOK)
	_, ready, err := config.LoadFirewallCredentials(t.Context(), harness.store, harness.box)
	if err != nil {
		t.Fatalf("loading the stored credentials: %v", err)
	}
	if ready != config.CredentialStateReady {
		t.Fatalf("a configured installation reports %q", ready)
	}

	// With no key at all, which is the key file having been lost.
	_, lost, err := config.LoadFirewallCredentials(t.Context(), harness.store, nil)
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
	harness := newHarness(t)
	harness.completeSetup()
	apiKey, apiSecret := randomHex(t, 12), randomHex(t, 16)
	assertStatus(t, harness.storeCredentials(apiKey, apiSecret), http.StatusOK)

	answer := harness.post(PathSettings, url.Values{
		"firewall_url": {harness.fake.baseURL()},
		"api_key":      {apiKey},
		"api_secret":   {""},
		"theme":        {string(ThemeDark)},
	})
	assertStatus(t, answer, http.StatusOK)

	assertPassCarries(t, harness, apiKey, apiSecret)
	if theme := harness.server.readTheme(t.Context()); theme != ThemeDark {
		t.Errorf("the theme was not saved: %q", theme)
	}
}

// TestASecretIsNeverRenderedBackIntoThePage asserts what the surface may not say.
func TestASecretIsNeverRenderedBackIntoThePage(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()
	apiKey, apiSecret := randomHex(t, 12), randomHex(t, 16)
	licenceKey := randomHex(t, 16)

	answer := harness.post(PathSettings, url.Values{
		"firewall_url":        {harness.fake.baseURL()},
		"api_key":             {apiKey},
		"api_secret":          {apiSecret},
		"maxmind_licence_key": {licenceKey},
		"theme":               {string(ThemeSystem)},
	})
	status, submitted := statusAndBody(t, answer)
	if status != http.StatusOK {
		t.Fatalf("the settings surface answered %d", status)
	}

	account, found, err := harness.store.AnyAccount(t.Context())
	if err != nil || !found {
		t.Fatalf("reading the account: found=%v err=%v", found, err)
	}

	pages := []string{submitted, pageSource(t, harness, PathSettings)}
	for _, page := range pages {
		for name, forbidden := range map[string]string{
			"the API secret":    apiSecret,
			"the licence key":   licenceKey,
			"the password":      password,
			"the password salt": string(account.Password.Salt),
			"the password hash": string(account.Password.Digest),
			"the key file":      harness.box.KeyID(),
		} {
			if forbidden != "" && strings.Contains(page, forbidden) {
				t.Errorf("a response body carries %s", name)
			}
		}
	}
	_ = login
}

// text resolves one catalogue key, so an assertion compares against the string the
// reader actually sees rather than a copy of it written in a test.
func text(t *testing.T, catalogue Catalogue, key messageKey) string {
	t.Helper()
	value, err := catalogue.translate(string(key))
	if err != nil {
		t.Fatalf("%v", err)
	}
	return value
}

// pageSource fetches one page's HTML.
func pageSource(t *testing.T, harness *harness, path string) string {
	t.Helper()
	answer := harness.get(path)
	body := readBody(t, answer)
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", path, answer.StatusCode)
	}
	return body
}

// pageText fetches one page and returns only what a reader sees.
func pageText(t *testing.T, harness *harness, path string) string {
	t.Helper()
	return visibleText(pageSource(t, harness, path))
}

// assertContains fails unless every value appears.
func assertContains(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Errorf("the page does not say %q", value)
		}
	}
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
