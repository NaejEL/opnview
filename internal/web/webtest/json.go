package webtest

import (
	"net/http"
	"strings"

	"github.com/NaejEL/opnview/internal/web"
)

// PostJSON issues a JSON write as the application makes one: the body, the session's form token
// in its header when token is not empty, and an Origin header when origin is not empty.
func (h *Harness) PostJSON(path, body, token, origin string) *http.Response {
	h.T.Helper()
	request, err := http.NewRequest(http.MethodPost, h.HTTP.URL+path, strings.NewReader(body))
	if err != nil {
		h.T.Fatalf("building a JSON POST for %s: %v", path, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(web.HeaderCSRFToken, token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	answer, err := h.Client.Do(request)
	if err != nil {
		h.T.Fatalf("issuing a JSON POST for %s: %v", path, err)
	}
	return answer
}

// SessionFormToken is the signed-in session's own form token, read off a page as a browser would.
func (h *Harness) SessionFormToken() string {
	h.T.Helper()
	return h.FormToken(web.PathSettings)
}
