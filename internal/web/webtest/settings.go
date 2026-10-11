package webtest

import (
	"github.com/NaejEL/opnview/internal/web"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// storeCredentials types a firewall URL and a key/secret pair into the settings
// surface. Every value is generated per run, and the URL is the loopback fake's,
// chosen by the operating system.
func (h *Harness) StoreCredentials(apiKey, apiSecret string) *http.Response {
	h.T.Helper()
	return h.Post(web.PathSettings, url.Values{
		FieldSettingsCard: {SettingsCardFirewall},
		"firewall_url":    {h.Fake.BaseURL()},
		"api_key":         {apiKey},
		"api_secret":      {apiSecret},
		"theme":           {string(web.ThemeSystem)},
	})
}

// text resolves one catalogue key, so an assertion compares against the string the
// reader actually sees rather than a copy of it written in a test.
func Text(t *testing.T, catalogue web.Catalogue, key messageKey) string {
	t.Helper()
	value, err := catalogue.Translate(key)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return value
}

// pageSource fetches one page's HTML.
func PageSource(t *testing.T, harness *Harness, path string) string {
	t.Helper()
	answer := harness.Get(path)
	body := ReadBody(t, answer)
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", path, answer.StatusCode)
	}
	return body
}

// assertContains fails unless every value appears.
func AssertContains(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Errorf("the page does not say %q", value)
		}
	}
}
