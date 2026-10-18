package settings_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// TestChangingACredentialTakesEffectWithoutARestart is AC16, and it is the second of
// this cycle's named risks: the credentials reaching the database but not the running
// collectors.
func TestChangingACredentialTakesEffectWithoutARestart(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()

	apiKey := webtest.RandomHex(t, 12)
	firstSecret := webtest.RandomHex(t, 16)

	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, firstSecret), http.StatusOK)

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
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, secondSecret), http.StatusOK)
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
// TestVerificationTellsTheThreeAnswersApart is AC17's mapping half.
//
// NONE OF THE THREE IS WORKED AROUND OR REPORTED AS SUCCESS, and each is asserted by the code the
// save answers rather than by an internal value.
func TestVerificationTellsTheThreeAnswersApart(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)

	for _, answer := range []struct {
		what    string
		arrange func()
		want    messageKey
	}{
		{"the firewall answers", func() {
			harness.Fake.AnswerAll(http.StatusOK, webtest.MustJSON(t, map[string]any{"rows": []any{}}))
		}, msgVerifyVerified},
		{"401 is a credential failure", func() { harness.Fake.AnswerAll(http.StatusUnauthorized, []byte(`{}`)) },
			msgVerifyCredentialsBad},
		{"403 is a credential failure too", func() { harness.Fake.AnswerAll(http.StatusForbidden, []byte(`{}`)) },
			msgVerifyCredentialsBad},
		{"404 is an absent endpoint", func() {
			harness.Fake.AnswerAll(http.StatusNotFound, []byte(`{"errorMessage":"Not Found"}`))
		}, msgVerifyEndpointNotFound},
		{"no answer at all is unreachable", func() { harness.Fake.GoDown() }, msgVerifyUnreachable},
	} {
		t.Run(answer.what, func(t *testing.T) {
			answer.arrange()
			if got := saveAndVerify(t, harness, apiKey, apiSecret); got != web.ConditionOf(answer.want) {
				t.Errorf("the save's verification is %q, want %q", got, web.ConditionOf(answer.want))
			}
		})
	}
}

// saveAndVerify stores the credentials and returns the verification the save answered.
func saveAndVerify(t *testing.T, harness *webtest.Harness, apiKey, apiSecret string) string {
	t.Helper()
	status, body := webtest.StatusAndBody(t, settingstest.StoreCredentials(harness, apiKey, apiSecret))
	if status != http.StatusOK {
		t.Fatalf("the firewall save answered %d: %s", status, body)
	}
	verification, _ := settingstest.Path(settingstest.DecodeJSON(t, body), "firewall", "verification").(string)
	return verification
}

// TestNothingButTheFirewallIsContactedDuringTheThreeSurfaces is AC17's destination half and
// AC18.
//
// The transport behind the client fails any host but the fake AND fails the test, so reaching the
// end of this with no error is the assertion. The explicit MaxMind check is here as well, because
// "no second destination" and "no MaxMind call" are two different mistakes.
func TestNothingButTheFirewallIsContactedDuringTheThreeSurfaces(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()

	webtest.Discard(t, harness.Get(webtest.PathAPISession))
	harness.SignIn(login, password)
	webtest.Discard(t, harness.Get(settings.PathAPISettings))

	licenceKey := webtest.RandomHex(t, 16)
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, webtest.RandomHex(t, 12),
		webtest.RandomHex(t, 16)), http.StatusOK)
	if status, answer := settingstest.SendJSON(harness, "/api/plugins/maxmind/settings",
		map[string]any{"licence_key": licenceKey}); status != http.StatusOK {
		t.Fatalf("saving the licence key answered %d %v", status, answer)
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

	// The endpoint is in the registry. A path invented here could not be issued at all, and
	// this is the recording half of that guarantee.
	registered := false
	for _, endpoint := range opnsense.Registry() {
		if endpoint.Path == settings.VerificationEndpoint().Path {
			registered = true
		}
	}
	if !registered {
		t.Error("the verification endpoint is not in opnsense.Registry()")
	}

	// AC18: the licence key was stored and NOTHING was downloaded for it. The transport would
	// have failed the test on a MaxMind host; this asserts the key reached the database all the
	// same, so the absence of a call is not the absence of the feature.
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

// credentialState is the firewall's credential state the settings answer reports.
func credentialState(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	state, _ := settingstest.Path(settingstest.GetJSON(harness, settings.PathAPISettings),
		"firewall", "credential_state").(string)
	return state
}

// TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence is AC14 as the settings answer reports
// it.
func TestAnUnreadableCredentialIsItsOwnStateAndNotAnAbsence(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	login, password := harness.CompleteSetup()
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)),
		http.StatusOK)
	if state := credentialState(t, harness); state != web.ConditionOf(msgCredentialReady) {
		t.Errorf("stored credentials answer %q", state)
	}

	// The database survives; the key file does not. THIS IS THE CASE THE CRITERION IS ABOUT: the
	// service comes up, and what it says is not "no firewall is configured".
	withoutKey := webtest.ReopenWithoutKeyFile(t, harness)
	withoutKey.SignIn(login, password)
	state := credentialState(t, withoutKey)
	if state != web.ConditionOf(msgCredentialUndecryptable) {
		t.Errorf("a lost key file answers %q, not undecryptable", state)
	}
	// AND IT IS NOT REPORTED AS AN ABSENCE. Reporting the second as the first sends the reader to
	// look for a setting that is already there.
	if state == web.ConditionOf(msgCredentialAbsent) {
		t.Error("a lost key file is answered as an absence")
	}
	// And re-entry is possible: the configuration is still answered, so the front can offer it.
	if url := settingstest.Path(settingstest.GetJSON(withoutKey, settings.PathAPISettings), "firewall", "url"); url == nil {
		t.Error("the firewall URL is no longer answered once the key file is lost")
	}
}

// TestTheTwoStatesAreDistinguishableAtTheSource is AC14 below the interface: the values
// themselves differ, so nothing downstream has to guess.
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
		http.StatusOK)
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

// TestAnEmptySecretDoesNotDestroyTheCredentials is the consequence of a secret field that is
// never filled: a save carrying no secret must leave the stored one alone.
func TestAnEmptySecretDoesNotDestroyTheCredentials(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, apiSecret), http.StatusOK)

	// The front sends the card's whole content back; the secret field is empty.
	accounttest.AssertStatus(t, settingstest.StoreCredentials(harness, apiKey, ""), http.StatusOK)
	assertPassCarries(t, harness, apiKey, apiSecret)
}

// TestASecretIsNeverAnsweredBack asserts what the surface may not say.
func TestASecretIsNeverAnsweredBack(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	_, password := harness.CompleteSetup()
	apiKey, apiSecret := webtest.RandomHex(t, 12), webtest.RandomHex(t, 16)
	licenceKey := webtest.RandomHex(t, 16)

	_, saved := webtest.StatusAndBody(t, settingstest.StoreCredentials(harness, apiKey, apiSecret))
	_, licence := webtest.StatusAndBody(t, harness.PostWithToken("/api/plugins/maxmind/settings",
		string(webtest.MustJSON(t, map[string]any{"licence_key": licenceKey}))))
	_, read := webtest.StatusAndBody(t, harness.Get(settings.PathAPISettings))

	account, found, err := harness.Store.AnyAccount(t.Context())
	if err != nil || !found {
		t.Fatalf("reading the account: found=%v err=%v", found, err)
	}
	for _, body := range []string{saved, licence, read} {
		for name, forbidden := range map[string]string{
			"the API secret":    apiSecret,
			"the password":      password,
			"the password salt": string(account.Password.Salt),
			"the password hash": string(account.Password.Digest),
			"the key file":      harness.Box.KeyID(),
		} {
			if forbidden != "" && strings.Contains(body, forbidden) {
				t.Errorf("a response body carries %s", name)
			}
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
