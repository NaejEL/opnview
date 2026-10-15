package accounttest

import (
	"context"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
)

// assertStatus fails unless the response carries the status expected, and always
// closes the body.
func AssertStatus(t *testing.T, answer *http.Response, expected int) {
	t.Helper()
	body := webtest.ReadBody(t, answer)
	_ = answer.Body.Close()
	if answer.StatusCode != expected {
		t.Fatalf("the response was %d rather than %d: %s", answer.StatusCode, expected, body)
	}
}

// assertNoAccount fails if any account exists.
func AssertNoAccount(t *testing.T, harness *webtest.Harness) {
	t.Helper()
	AssertAccountCount(t, harness, 0)
}

// assertAccountCount fails unless exactly expected accounts exist.
func AssertAccountCount(t *testing.T, harness *webtest.Harness, expected int) {
	t.Helper()
	count, err := harness.Store.CountAccounts(context.Background())
	if err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != expected {
		t.Fatalf("there are %d accounts rather than %d", count, expected)
	}
}
