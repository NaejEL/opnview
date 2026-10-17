package account_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/account"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// setupBody is a setup's JSON body.
func setupBody(t *testing.T, fields map[string]string) string {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encoding a setup body: %v", err)
	}
	return string(encoded)
}

// postSetup issues a setup carrying this reader's own form token.
func postSetup(t *testing.T, h *webtest.Harness, fields map[string]string) *http.Response {
	t.Helper()
	return h.PostWithToken(account.PathAPISetup, setupBody(t, fields))
}

// TestAFreshInstallationHasNoAccountAndRunsAnyway is AC3's half that belongs to this
// package: the service comes up against a database with no account, and the setup is what is
// open.
func TestAFreshInstallationHasNoAccountAndRunsAnyway(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)

	count, err := harness.Store.CountAccounts(context.Background())
	if err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != 0 {
		t.Fatalf("a freshly applied schema seeded %d accounts", count)
	}

	// The token exists and has not been used, which is what cmd/opnview prints.
	if harness.SetupToken.Token() == "" {
		t.Fatal("no setup token was generated")
	}
	if harness.SetupToken.Spent() {
		t.Fatal("the setup token was spent before setup ran")
	}

	// The session state sends an unconfigured installation's reader to the setup rather than to
	// the sign-in: no account exists, and nobody is signed in.
	if state := settingstest.GetJSON(harness, account.PathAPISession); state["account_exists"] != false ||
		state["signed_in"] != false {
		t.Errorf("a fresh installation's session state is %v", state)
	}
}

// TestTheSetupSurfaceDemandsTheTokenAndSpendsItOnce is AC4.
func TestTheSetupSurfaceDemandsTheTokenAndSpendsItOnce(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	password := webtest.RandomHex(t, 12)

	// Without a token: refused, and nothing created.
	withoutToken := postSetup(t, harness, map[string]string{
		"login": "first", "password": password, "password_again": password,
	})
	accounttest.AssertStatus(t, withoutToken, http.StatusForbidden)
	accounttest.AssertNoAccount(t, harness)

	// With a wrong one: refused, and nothing created.
	wrongToken := postSetup(t, harness, map[string]string{
		"setup_token": webtest.RandomHex(t, 32),
		"login":       "first", "password": password, "password_again": password,
	})
	accounttest.AssertStatus(t, wrongToken, http.StatusForbidden)
	accounttest.AssertNoAccount(t, harness)

	// With the right one: the account exists and the token is spent.
	login, _ := harness.CompleteSetup()
	if !harness.SetupToken.Spent() {
		t.Error("the setup token was not spent by a successful setup")
	}
	if _, found, err := harness.Store.AccountByLogin(context.Background(), login); err != nil {
		t.Fatalf("reading the account: %v", err)
	} else if !found {
		t.Fatal("setup reported success and created no account")
	}

	// A replay of the same token is refused. It is refused here because the account exists,
	// which is the permanent closure; the token being spent is the second, independent refusal,
	// and TestASpentTokenIsRefusedOnItsOwn asserts that one without the account in the way.
	replay := postSetup(t, webtest.FreshClient(t, harness), map[string]string{
		"setup_token": harness.SetupToken.Token(),
		"login":       "second", "password": password, "password_again": password,
	})
	accounttest.AssertStatus(t, replay, http.StatusForbidden)
	accounttest.AssertAccountCount(t, harness, 1)

	// And the token appears in no answer of the session routes, ever.
	answer := harness.Get(account.PathAPISession)
	body := webtest.ReadBody(t, answer)
	_ = answer.Body.Close()
	if strings.Contains(body, harness.SetupToken.Token()) {
		t.Errorf("the setup token appears in the body of %s", account.PathAPISession)
	}
}

// TestASpentTokenIsRefusedOnItsOwn asserts the single-use property of the token
// independently of the account, so neither refusal can hide behind the other.
func TestASpentTokenIsRefusedOnItsOwn(t *testing.T) {
	t.Parallel()
	token, err := auth.NewSetupToken()
	if err != nil {
		t.Fatalf("generating a setup token: %v", err)
	}
	value := token.Token()
	if err := token.Check(value); err != nil {
		t.Fatalf("a fresh token was refused: %v", err)
	}
	token.Spend()
	if err := token.Check(value); err == nil {
		t.Error("a spent token was accepted")
	}
	if err := token.Check(""); err == nil {
		t.Error("an empty token was accepted")
	}
}

// TestTheSetupTokenIsNeverWrittenIntoTheDatabase is AC4's storage half.
func TestTheSetupTokenIsNeverWrittenIntoTheDatabase(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	token := harness.SetupToken.Token()
	harness.CompleteSetup()

	if found := webtest.DatabaseContains(t, harness, token); found != "" {
		t.Errorf("the setup token was found in the database, in %s", found)
	}
}

// TestTheSetupSurfaceClosesPermanently is AC7, the criterion this cycle names as the
// one to get right before any other.
//
// IT IS ASSERTED THREE WAYS, because the hole has three shapes: a token that is still valid, a
// reset of the first account through the setup, and a state that a restart resets.
func TestTheSetupSurfaceClosesPermanently(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	password := webtest.RandomHex(t, 12)

	t.Run("with a spent token", func(t *testing.T) {
		answer := postSetup(t, webtest.FreshClient(t, harness), map[string]string{
			"setup_token": harness.SetupToken.Token(),
			"login":       "second-" + webtest.RandomHex(t, 4),
			"password":    password, "password_again": password,
		})
		accounttest.AssertStatus(t, answer, http.StatusForbidden)
		accounttest.AssertAccountCount(t, harness, 1)
	})

	t.Run("the session state says the setup is closed", func(t *testing.T) {
		state := settingstest.GetJSON(webtest.FreshClient(t, harness), account.PathAPISession)
		if state["account_exists"] != true {
			t.Errorf("after the setup the session state is %v", state)
		}
	})

	// The restart. A NEW process, a NEW and unspent setup token, the SAME data
	// directory. This is the case a flag in memory gets wrong.
	restarted := harness.Reopen()

	t.Run("after a restart, with a valid unspent token", func(t *testing.T) {
		if restarted.SetupToken.Spent() {
			t.Fatal("the restarted server's token is already spent, so this proves nothing")
		}
		if restarted.SetupToken.Token() == harness.SetupToken.Token() {
			t.Fatal("the restarted server reused the first token, so this proves nothing")
		}
		status, answer := sendAnonymous(t, restarted, account.PathAPISetup, map[string]any{
			"setup_token": restarted.SetupToken.Token(),
			"login":       "second-" + webtest.RandomHex(t, 4),
			"password":    password, "password_again": password,
		}, restarted.FormToken())
		if status != http.StatusForbidden || answer["condition"] != web.ConditionSetupClosed {
			t.Errorf("a setup after a restart answered %d %v rather than 403 setup_closed", status, answer)
		}
		accounttest.AssertAccountCount(t, restarted, 1)
	})

	t.Run("after a restart, setup cannot reset the first account either", func(t *testing.T) {
		stored, found, err := restarted.Store.AnyAccount(context.Background())
		if err != nil || !found {
			t.Fatalf("reading the account after the restart: found=%v err=%v", found, err)
		}
		before := stored.Password
		answer := postSetup(t, restarted, map[string]string{
			"setup_token": restarted.SetupToken.Token(),
			"login":       stored.Login,
			"password":    password, "password_again": password,
		})
		accounttest.AssertStatus(t, answer, http.StatusForbidden)

		after, _, err := restarted.Store.AnyAccount(context.Background())
		if err != nil {
			t.Fatalf("re-reading the account: %v", err)
		}
		if string(after.Password.Digest) != string(before.Digest) {
			t.Error("a request to the closed setup changed the stored password")
		}
	})
}

// TestConcurrentSetupsCarryingOneTokenCreateOneAccount is the same window from the
// HTTP side: the middleware's count and the insert are not one transaction, and the
// setup token is spent only once an account exists, so several requests carrying the
// one printed token can all be inside the handler at once.
//
// THE LOGINS ARE ALL DIFFERENT, which is the case the UNIQUE login does not catch.
// Exactly one of them may create an account; every other one is refused, and the
// installation holds one account whichever of them won.
func TestConcurrentSetupsCarryingOneTokenCreateOneAccount(t *testing.T) {
	t.Parallel()
	installation := webtest.New(t)
	password := webtest.RandomHex(t, 12)
	const racers = 8

	// Each browser reads its session state first, so every request carries a form token of
	// its own and the only thing they share is the one token printed on the console.
	clients := make([]*webtest.Harness, racers)
	tokens := make([]string, racers)
	for index := range racers {
		clients[index] = webtest.FreshClient(t, installation)
		tokens[index] = clients[index].FormToken()
	}

	statuses := make([]int, racers)
	failures := make([]error, racers)
	start := make(chan struct{})
	var racing sync.WaitGroup
	for index := range racers {
		racing.Add(1)
		go func() {
			defer racing.Done()
			body := setupBody(t, map[string]string{
				"setup_token":    installation.SetupToken.Token(),
				"login":          fmt.Sprintf("operator-%d", index),
				"password":       password,
				"password_again": password,
			})
			request, err := http.NewRequest(http.MethodPost, installation.HTTP.URL+account.PathAPISetup,
				strings.NewReader(body))
			if err != nil {
				failures[index] = err
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(web.HeaderCSRFToken, tokens[index])
			<-start
			answer, err := clients[index].Client.Do(request)
			if err != nil {
				failures[index] = err
				return
			}
			defer func() { _ = answer.Body.Close() }()
			_, _ = io.Copy(io.Discard, answer.Body)
			statuses[index] = answer.StatusCode
		}()
	}
	close(start)
	racing.Wait()

	created := 0
	for index, status := range statuses {
		if failures[index] != nil {
			t.Fatalf("a concurrent setup could not be issued: %v", failures[index])
		}
		switch status {
		case http.StatusOK:
			created++
		case http.StatusForbidden:
		default:
			t.Errorf("a concurrent setup answered %d, which is neither the account being "+
				"created nor a refusal", status)
		}
	}
	if created != 1 {
		t.Errorf("%d of %d concurrent setups reported creating an account", created, racers)
	}
	accounttest.AssertAccountCount(t, installation, 1)
	if !installation.SetupToken.Spent() {
		t.Error("the setup token survived a successful setup")
	}
}

// TestSetupRefusesAWeakOrMistypedPassword keeps the two new-credential rules from
// drifting, and asserts that a refusal creates nothing and burns no token.
func TestSetupRefusesAWeakOrMistypedPassword(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	long := webtest.RandomHex(t, 12)

	for name, fields := range map[string]map[string]string{
		"no login":  {"login": "", "password": long, "password_again": long},
		"too short": {"login": "operator", "password": "short", "password_again": "short"},
		"mistyped":  {"login": "operator", "password": long, "password_again": long + "x"},
	} {
		t.Run(name, func(t *testing.T) {
			fields["setup_token"] = harness.SetupToken.Token()
			answer := postSetup(t, harness, fields)
			accounttest.AssertStatus(t, answer, http.StatusBadRequest)
			accounttest.AssertNoAccount(t, harness)
			if harness.SetupToken.Spent() {
				t.Error("a rejected password burnt the setup token")
			}
		})
	}
}
