package web

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// TestCollectionDoesNotWaitForASignIn is AC5.
//
// IT IS THE PROPERTY THE WHOLE KEY-FILE TRADE WAS TAKEN FOR. The service starts and
// collects without a human, or collection stops at every reboot until somebody signs
// in. Signing out must therefore change nothing about the next pass.
func TestCollectionDoesNotWaitForASignIn(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	login, password := harness.completeSetup()
	apiKey, apiSecret := randomHex(t, 12), randomHex(t, 16)
	assertStatus(t, harness.storeCredentials(apiKey, apiSecret), http.StatusOK)

	// Sign out, and destroy every session there is, so nothing anywhere is signed in.
	assertStatus(t, harness.post(PathSignOut, url.Values{}), http.StatusSeeOther)
	if count := sessionCount(t, harness); count != 0 {
		t.Fatalf("%d sessions survive, so this proves nothing about collecting without one", count)
	}

	// A real collector, built the way cmd/opnview builds one, over the same client.
	collector := collect.New(harness.server.client, harness.store, collect.SystemClock{})
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("a collection pass with nobody signed in failed: %v", err)
	}

	last := harness.fake.last()
	if last.apiKey != apiKey || last.apiSecret != apiSecret {
		t.Error("a pass made with nobody signed in did not carry the stored credentials")
	}
	if count := sessionCount(t, harness); count != 0 {
		t.Error("collection created a session, which it has no business doing")
	}

	// And signing back in changes nothing about it either way.
	harness.signIn(login, password)
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("a collection pass with somebody signed in failed: %v", err)
	}
}

// sessionCount counts the live session rows.
func sessionCount(t *testing.T, harness *harness) int {
	t.Helper()
	var count int
	if err := harness.store.DB().QueryRow("SELECT count(*) FROM session").Scan(&count); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	return count
}

// TestTheServerStopsOnItsContextAndTheDatabaseStaysIntact is AC26 as far as this
// package can prove it: cmd/opnview cancels one context, and the HTTP server returning
// is what lets the database be closed and checked.
func TestTheServerStopsOnItsContextAndTheDatabaseStaysIntact(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	harness.completeSetup()

	// A port the operating system chooses, so no port of any network is written here.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("taking a port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- harness.server.ListenAndServe(ctx, address, time.Second) }()

	// Wait for it to answer, so the cancellation below stops something that is
	// genuinely running.
	client := &http.Client{Timeout: 5 * time.Second}
	reachable := false
	for range 100 {
		answer, err := client.Get("http://" + address + PathStylesheet)
		if err == nil {
			_ = answer.Body.Close()
			reachable = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reachable {
		cancel()
		<-stopped
		t.Fatal("the server never answered, so this proves nothing about stopping it")
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("the server did not stop cleanly: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop when its context was cancelled")
	}

	if result, err := harness.store.IntegrityCheck(context.Background()); err != nil {
		t.Fatalf("the integrity check did not run: %v", err)
	} else if result != "ok" {
		t.Fatalf("the database reported %q rather than ok", result)
	}
}

// TestTheStepFourValidationSequenceIsPerformable is AC27.
//
// IT IS THE DOCUMENTED SEQUENCE, IN ORDER, AGAINST THE FIREWALL FAKE, and it exists
// because step 4's own validation — real collection against a real OPNsense — becomes
// reachable for the first time at the end of this cycle. The operator's steps are:
//
//  1. start the service against an empty data directory;
//  2. read the one-time setup token from the console;
//  3. create the first account with it;
//  4. sign in;
//  5. enter the firewall URL and the API key and secret;
//  6. read the verification outcome;
//  7. watch a collection pass use what was entered.
//
// NO CREDENTIAL REACHES THE PRODUCT BY ANY OTHER MEANS. There is no environment
// variable, no configuration file and no fixture: everything below goes in through
// the settings surface, which is the point of the whole cycle.
func TestTheStepFourValidationSequenceIsPerformable(t *testing.T) {
	t.Parallel()
	// 1. An empty data directory. The schema is applied and the account table is
	// empty, and the service is up regardless.
	harness := newHarness(t)
	assertNoAccount(t, harness)

	// 2. The token, which cmd/opnview prints and which exists nowhere else.
	token := harness.setupToken.Token()
	if token == "" {
		t.Fatal("no setup token was printed")
	}

	// 3. The first account.
	login := "operator-" + randomHex(t, 4)
	password := randomHex(t, 12)
	created := harness.post(PathSetup, url.Values{
		"setup_token": {token}, "login": {login},
		"password": {password}, "password_again": {password},
	})
	assertStatus(t, created, http.StatusSeeOther)

	// 4. Sign in, from a browser that has never been signed in.
	browser := freshClient(t, harness)
	browser.signIn(login, password)

	// 5. The firewall URL and the key and secret, typed into the settings surface.
	apiKey, apiSecret := randomHex(t, 12), randomHex(t, 16)
	harness.fake.answer(http.StatusOK, mustJSON(t, map[string]any{"rows": []any{}, "total": 0}))
	answer := browser.post(PathSettings, url.Values{
		"firewall_url": {harness.fake.baseURL()},
		"api_key":      {apiKey},
		"api_secret":   {apiSecret},
		"theme":        {string(ThemeSystem)},
	})
	status, body := statusAndBody(t, answer)
	if status != http.StatusOK {
		t.Fatalf("the settings surface answered %d: %s", status, body)
	}

	// 6. The verification outcome is on the page.
	assertContains(t, body, text(t, harness.server.renderer.catalogue, msgVerifyVerified))

	// 7. A collection pass uses what was entered, in this same process.
	collector := collect.New(harness.server.client, harness.store, collect.SystemClock{})
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("the collection pass failed: %v", err)
	}
	last := harness.fake.last()
	if last.apiKey != apiKey || last.apiSecret != apiSecret {
		t.Error("the collection pass did not carry the credentials entered in the interface")
	}

	// And the credential is at rest under encryption, not in the clear.
	if where := databaseContains(t, harness, apiSecret); where != "" {
		t.Errorf("the API secret is readable in the database, in %s", where)
	}
	sealed, stored, err := harness.store.Credential(
		context.Background(), store.CredentialOPNsenseAPISecret)
	if err != nil || !stored {
		t.Fatalf("the API secret was not stored: stored=%v err=%v", stored, err)
	}
	if sealed.Algorithm == "" || sealed.KeyID == "" || len(sealed.Nonce) == 0 {
		t.Error("the stored ciphertext carries no algorithm, key identifier or nonce")
	}
}

// TestTheVerificationEndpointIsTheOneTheRegistryCarries pins the choice, so replacing
// it is a deliberate act rather than an edit nobody notices.
func TestTheVerificationEndpointIsTheOneTheRegistryCarries(t *testing.T) {
	t.Parallel()
	chosen := verificationEndpoint()
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
