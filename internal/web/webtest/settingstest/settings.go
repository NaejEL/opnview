package settingstest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// StoreCredentials saves a firewall URL and a key/secret pair through the firewall card's JSON
// save. Every value is generated per run, and the URL is the loopback fake's, chosen by the
// operating system.
func StoreCredentials(h *webtest.Harness, apiKey, apiSecret string) *http.Response {
	h.T.Helper()
	return h.PostWithToken("/api/settings/firewall", string(webtest.MustJSON(h.T, map[string]string{
		"url":        h.Fake.BaseURL(),
		"api_key":    apiKey,
		"api_secret": apiSecret,
	})))
}

// Text resolves one catalogue key, so an assertion compares against the string the
// reader actually sees rather than a copy of it written in a test.
func Text(t *testing.T, catalogue web.Catalogue, key webtest.MessageKey) string {
	t.Helper()
	value, err := catalogue.Translate(key)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return value
}

// AssertContains fails unless every value appears.
func AssertContains(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Errorf("the answer does not say %q", value)
		}
	}
}
