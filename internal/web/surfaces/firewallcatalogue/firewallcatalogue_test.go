package firewallcatalogue_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/surfaces/firewallcatalogue"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// TestTheCatalogueAnswersOnlyASession is BR-AC23's web half: without a session the catalogue
// answers 401 with a JSON condition and nothing of the catalogue; with one it answers the
// catalogue, whose endpoint and subscription lists are lists, never null.
func TestTheCatalogueAnswersOnlyASession(t *testing.T) {
	t.Parallel()
	harness := webtest.SignedInHarness(t)
	anonymous, err := http.Get(harness.HTTP.URL + firewallcatalogue.PathCatalogue)
	if err != nil {
		t.Fatal(err)
	}
	body := webtest.ReadBody(t, anonymous)
	if anonymous.StatusCode != http.StatusUnauthorized || !strings.Contains(body, `"condition"`) ||
		strings.Contains(body, `"endpoints"`) {
		t.Errorf("without a session the catalogue answered %d: %.200s", anonymous.StatusCode, body)
	}
	signed := harness.Get(firewallcatalogue.PathCatalogue)
	body = webtest.ReadBody(t, signed)
	var catalogue struct {
		Endpoints     []json.RawMessage `json:"endpoints"`
		Subscriptions []json.RawMessage `json:"subscriptions"`
	}
	if signed.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &catalogue) != nil ||
		catalogue.Endpoints == nil || catalogue.Subscriptions == nil {
		t.Errorf("with a session the catalogue answered %d: %.300s", signed.StatusCode, body)
	}
}
