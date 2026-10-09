package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
)

// TestAFreshInstallationHasNoAccountAndRunsAnyway is AC3's half that belongs to this
// package: the service comes up against a database with no account, and the setup
// surface is the thing that is reachable.
func TestAFreshInstallationHasNoAccountAndRunsAnyway(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)

	count, err := harness.store.CountAccounts(context.Background())
	if err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != 0 {
		t.Fatalf("a freshly applied schema seeded %d accounts", count)
	}

	// The token exists and has not been used, which is what cmd/opnview prints.
	if harness.setupToken.Token() == "" {
		t.Fatal("no setup token was generated")
	}
	if harness.setupToken.Spent() {
		t.Fatal("the setup token was spent before setup ran")
	}

	answer := harness.get(PathSetup)
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the setup surface answered %d on a fresh installation", answer.StatusCode)
	}

	// The root sends an unconfigured installation to setup rather than to sign-in.
	root := harness.get(PathRoot)
	defer func() { _ = root.Body.Close() }()
	if root.StatusCode != http.StatusSeeOther {
		t.Fatalf("the root answered %d rather than a redirect", root.StatusCode)
	}
	if location := root.Header.Get("Location"); location != PathSetup {
		t.Errorf("the root sent a fresh installation to %q rather than to the setup surface",
			location)
	}
}

// TestTheSetupSurfaceDemandsTheTokenAndSpendsItOnce is AC4.
func TestTheSetupSurfaceDemandsTheTokenAndSpendsItOnce(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	password := randomHex(t, 12)

	// Without a token: refused, and nothing created.
	withoutToken := harness.post(PathSetup, url.Values{
		"login": {"first"}, "password": {password}, "password_again": {password},
	})
	assertStatus(t, withoutToken, http.StatusForbidden)
	assertNoAccount(t, harness)

	// With a wrong one: refused, and nothing created.
	wrongToken := harness.post(PathSetup, url.Values{
		"setup_token": {randomHex(t, 32)},
		"login":       {"first"}, "password": {password}, "password_again": {password},
	})
	assertStatus(t, wrongToken, http.StatusForbidden)
	assertNoAccount(t, harness)

	// With the right one: the account exists and the token is spent.
	login, _ := harness.completeSetup()
	if !harness.setupToken.Spent() {
		t.Error("the setup token was not spent by a successful setup")
	}
	if _, found, err := harness.store.AccountByLogin(context.Background(), login); err != nil {
		t.Fatalf("reading the account: %v", err)
	} else if !found {
		t.Fatal("setup reported success and created no account")
	}

	// A replay of the same token is refused. It is refused here because the account
	// exists, which is the permanent closure; the token being spent is the second,
	// independent refusal, and TestASpentTokenIsRefusedOnItsOwn asserts that one
	// without the account in the way.
	replay := harness.postRaw(PathSetup, url.Values{
		"csrf_token":  {harness.formToken(PathSignIn)},
		"setup_token": {harness.setupToken.Token()},
		"login":       {"second"}, "password": {password}, "password_again": {password},
	})
	assertStatus(t, replay, http.StatusForbidden)
	assertAccountCount(t, harness, 1)

	// And the token appears in no response body, ever.
	for _, path := range []string{PathSetup, PathSignIn, PathRecover} {
		answer := harness.get(path)
		body := readBody(t, answer)
		_ = answer.Body.Close()
		if strings.Contains(body, harness.setupToken.Token()) {
			t.Errorf("the setup token appears in the body of %s", path)
		}
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
	harness := newHarness(t)
	token := harness.setupToken.Token()
	harness.completeSetup()

	if found := databaseContains(t, harness, token); found != "" {
		t.Errorf("the setup token was found in the database, in %s", found)
	}
}

// TestTheSetupSurfaceClosesPermanently is AC7, the criterion this cycle names as the
// one to get right before any other.
//
// IT IS ASSERTED THREE WAYS, because the hole has three shapes: a check only on the
// GET, a token that is still valid, and a state that a restart resets.
func TestTheSetupSurfaceClosesPermanently(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	harness.completeSetup()
	password := randomHex(t, 12)

	t.Run("with a spent token", func(t *testing.T) {
		answer := harness.postRaw(PathSetup, url.Values{
			"csrf_token":  {harness.formToken(PathSignIn)},
			"setup_token": {harness.setupToken.Token()},
			"login":       {"second-" + randomHex(t, 4)},
			"password":    {password}, "password_again": {password},
		})
		assertStatus(t, answer, http.StatusForbidden)
		assertAccountCount(t, harness, 1)
	})

	t.Run("the GET is closed too", func(t *testing.T) {
		answer := harness.get(PathSetup)
		defer func() { _ = answer.Body.Close() }()
		if answer.StatusCode != http.StatusSeeOther {
			t.Fatalf("the setup surface answered %d after an account existed", answer.StatusCode)
		}
		if location := answer.Header.Get("Location"); location != PathSignIn {
			t.Errorf("a closed setup surface sent the reader to %q", location)
		}
	})

	// The restart. A NEW process, a NEW and unspent setup token, the SAME data
	// directory. This is the case a flag in memory gets wrong.
	restarted := harness.reopen()

	t.Run("after a restart, with a valid unspent token", func(t *testing.T) {
		if restarted.setupToken.Spent() {
			t.Fatal("the restarted server's token is already spent, so this proves nothing")
		}
		if restarted.setupToken.Token() == harness.setupToken.Token() {
			t.Fatal("the restarted server reused the first token, so this proves nothing")
		}
		answer := restarted.postRaw(PathSetup, url.Values{
			"csrf_token":  {restarted.formToken(PathSignIn)},
			"setup_token": {restarted.setupToken.Token()},
			"login":       {"second-" + randomHex(t, 4)},
			"password":    {password}, "password_again": {password},
		})
		assertStatus(t, answer, http.StatusForbidden)
		assertAccountCount(t, restarted, 1)
	})

	t.Run("after a restart, setup cannot reset the first account either", func(t *testing.T) {
		account, found, err := restarted.store.AnyAccount(context.Background())
		if err != nil || !found {
			t.Fatalf("reading the account after the restart: found=%v err=%v", found, err)
		}
		before := account.Password
		answer := restarted.postRaw(PathSetup, url.Values{
			"csrf_token":  {restarted.formToken(PathSignIn)},
			"setup_token": {restarted.setupToken.Token()},
			"login":       {account.Login},
			"password":    {password}, "password_again": {password},
		})
		assertStatus(t, answer, http.StatusForbidden)

		after, _, err := restarted.store.AnyAccount(context.Background())
		if err != nil {
			t.Fatalf("re-reading the account: %v", err)
		}
		if string(after.Password.Digest) != string(before.Digest) {
			t.Error("a request to the closed setup surface changed the stored password")
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
	installation := newHarness(t)
	password := randomHex(t, 12)
	const racers = 8

	// Each browser loads the setup surface first, so every request carries a form token
	// of its own and the only thing they share is the one token printed on the console.
	clients := make([]*harness, racers)
	tokens := make([]string, racers)
	for index := range racers {
		clients[index] = freshClient(t, installation)
		tokens[index] = clients[index].formToken(PathSetup)
	}

	statuses := make([]int, racers)
	failures := make([]error, racers)
	start := make(chan struct{})
	var racing sync.WaitGroup
	for index := range racers {
		racing.Add(1)
		go func() {
			defer racing.Done()
			form := url.Values{
				"csrf_token":     {tokens[index]},
				"setup_token":    {installation.setupToken.Token()},
				"login":          {fmt.Sprintf("operator-%d", index)},
				"password":       {password},
				"password_again": {password},
			}
			request, err := http.NewRequest(http.MethodPost, installation.http.URL+PathSetup,
				strings.NewReader(form.Encode()))
			if err != nil {
				failures[index] = err
				return
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			<-start
			answer, err := clients[index].client.Do(request)
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
		case http.StatusSeeOther:
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
	assertAccountCount(t, installation, 1)
	if !installation.setupToken.Spent() {
		t.Error("the setup token survived a successful setup")
	}
}

// TestSetupRefusesAWeakOrMistypedPassword keeps the two new-credential rules from
// drifting, and asserts that a refusal creates nothing.
func TestSetupRefusesAWeakOrMistypedPassword(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	long := randomHex(t, 12)

	for name, form := range map[string]url.Values{
		"no login": {
			"login": {""}, "password": {long}, "password_again": {long},
		},
		"too short": {
			"login": {"operator"}, "password": {"short"}, "password_again": {"short"},
		},
		"mistyped": {
			"login": {"operator"}, "password": {long}, "password_again": {long + "x"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			form.Set("setup_token", harness.setupToken.Token())
			answer := harness.post(PathSetup, form)
			assertStatus(t, answer, http.StatusBadRequest)
			assertNoAccount(t, harness)
			if harness.setupToken.Spent() {
				t.Error("a rejected password burnt the setup token")
			}
		})
	}
}

// assertStatus fails unless the response carries the status expected, and always
// closes the body.
func assertStatus(t *testing.T, answer *http.Response, expected int) {
	t.Helper()
	body := readBody(t, answer)
	_ = answer.Body.Close()
	if answer.StatusCode != expected {
		t.Fatalf("the response was %d rather than %d: %s", answer.StatusCode, expected, body)
	}
}

// assertNoAccount fails if any account exists.
func assertNoAccount(t *testing.T, harness *harness) {
	t.Helper()
	assertAccountCount(t, harness, 0)
}

// assertAccountCount fails unless exactly expected accounts exist.
func assertAccountCount(t *testing.T, harness *harness, expected int) {
	t.Helper()
	count, err := harness.store.CountAccounts(context.Background())
	if err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != expected {
		t.Fatalf("there are %d accounts rather than %d", count, expected)
	}
}
