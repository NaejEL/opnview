package webtest

import (
	"net/http"
	"testing"
)

// Discard reads and closes a response nothing asserts on.
func Discard(t *testing.T, answer *http.Response) {
	t.Helper()
	_, _ = StatusAndBody(t, answer)
}
