package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/maxmind"
	"github.com/NaejEL/opnview/internal/publicsuffix"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget"
)

// The widget API over HTTP: AC3, AC4 and AC16 of specs/SPEC-step-5b-widget-api.md.

// widgetPath is one type's path with a parameters object.
func widgetPath(t *testing.T, widgetType string, parameters any) string {
	t.Helper()
	path := widget.PathPrefix + widgetType
	if parameters != nil {
		encoded, err := json.Marshal(parameters)
		if err != nil {
			t.Fatal(err)
		}
		path += "?" + widget.ParametersQueryKey + "=" + url.QueryEscape(string(encoded))
	}
	return path
}

// decodeCondition reads a JSON answer's condition and fails on anything that is not JSON.
func decodeCondition(t *testing.T, answer *http.Response, status int) (string, map[string]any) {
	t.Helper()
	body := readBody(t, answer)
	if answer.StatusCode != status {
		t.Fatalf("the answer was %d rather than %d: %.200s", answer.StatusCode, status, body)
	}
	if !strings.HasPrefix(answer.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("the answer is %q, not JSON: %.200s", answer.Header.Get("Content-Type"), body)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the answer is not a JSON object: %v: %.200s", err, body)
	}
	condition, _ := decoded["condition"].(string)
	return condition, decoded
}

// TestEveryWidgetRouteIsAuthenticatedAndBuiltFromTheRegistry is AC3's route half: every
// served type has its route, every widget route is authenticated, and the route table is
// built from the registry rather than written here.
func TestEveryWidgetRouteIsAuthenticatedAndBuiltFromTheRegistry(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	routed := map[string]bool{}
	for _, route := range harness.server.Routes() {
		if !strings.HasPrefix(route.Pattern, widget.PathPrefix) {
			continue
		}
		if route.Access != AccessAuthenticated || !route.JSON || route.Method != http.MethodGet {
			t.Errorf("%s %s is %s (JSON %v), not an authenticated JSON GET", route.Method, route.Pattern,
				route.Access, route.JSON)
		}
		routed[strings.TrimPrefix(route.Pattern, widget.PathPrefix)] = true
	}
	types := harness.server.widgets.Types()
	if len(types) < 26 {
		t.Fatalf("only %d types are served", len(types))
	}
	for _, widgetType := range types {
		if !routed[widgetType] {
			t.Errorf("the served type %s has no route", widgetType)
		}
	}
	if !routed["{type}"] {
		t.Error("no route answers a type the registry does not serve")
	}
}

// TestAnUnauthenticatedWidgetRequestGetsA401ConditionAndNoData is AC3's refusal half
// (decision 4): no session gives no_session, an expired session gives session_expired, both
// as a 401 in JSON, and neither carries any widget data.
func TestAnUnauthenticatedWidgetRequestGetsA401ConditionAndNoData(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	harness.completeSetup()
	path := widgetPath(t, "interface_traffic_matrix", nil)

	anonymous := freshClient(t, harness)
	answer := anonymous.get(path)
	condition, body := decodeCondition(t, answer, http.StatusUnauthorized)
	if condition != conditionNoSession {
		t.Errorf("no session answered %q, not %q", condition, conditionNoSession)
	}
	if _, carries := body["data"]; carries {
		t.Error("a refused request carried widget data")
	}

	// A signed-in reader whose session then expires.
	answer = harness.get(path)
	assertStatus(t, answer, http.StatusOK)
	harness.clock.advance(auth.DefaultLifetimes().Absolute + auth.DefaultLifetimes().Idle)
	answer = harness.get(path)
	condition, body = decodeCondition(t, answer, http.StatusUnauthorized)
	if condition != conditionSessionExpired {
		t.Errorf("an expired session answered %q, not %q", condition, conditionSessionExpired)
	}
	if _, carries := body["data"]; carries {
		t.Error("a refused request carried widget data")
	}
}

// TestUnknownTypesMalformedParametersAndUnknownKeysOverHTTP is AC4 over the wire: an
// unknown type -- client_activity_timeline, the mockup's deliberately unknown one, and an
// alert preset of step 6 among them -- is a 404 in JSON naming unknown_widget_type; a
// parameters payload that is not a JSON object is a 400 naming malformed_parameters; an
// unknown key is reported and is not an error.
func TestUnknownTypesMalformedParametersAndUnknownKeysOverHTTP(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	harness.completeSetup()
	for _, unknown := range []string{"client_activity_timeline", "alerts_by_signature", "no_such_widget"} {
		answer := harness.get(widget.PathPrefix + unknown)
		if condition, _ := decodeCondition(t, answer, http.StatusNotFound); condition != widget.ConditionUnknownWidgetType {
			t.Errorf("%s answered %q", unknown, condition)
		}
	}
	for _, malformed := range []string{"{", "[1,2]", "\"text\"", "{} trailing"} {
		answer := harness.get(widget.PathPrefix + "interface_traffic_matrix?" + widget.ParametersQueryKey + "=" +
			url.QueryEscape(malformed))
		if condition, _ := decodeCondition(t, answer, http.StatusBadRequest); condition != widget.ConditionMalformedParameters {
			t.Errorf("%q answered %q", malformed, condition)
		}
	}
	answer := harness.get(widgetPath(t, "interface_traffic_matrix", map[string]any{"no_such_key": 1}))
	_, body := decodeCondition(t, answer, http.StatusOK)
	unknown, _ := body["unknown_parameters"].(map[string]any)
	if _, reported := unknown["no_such_key"]; !reported {
		t.Errorf("the unknown key was not reported: %v", body["unknown_parameters"])
	}
	if answer.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("a widget answer set a CORS header")
	}
}

// countingTransport counts every request that reaches it, by host, and answers none: it
// stands under the host guard of the MaxMind and Public Suffix List clients, where the
// network would be.
type countingTransport struct {
	mutex sync.Mutex
	hosts []string
}

func (c *countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	c.mutex.Lock()
	c.hosts = append(c.hosts, request.URL.Host)
	c.mutex.Unlock()
	return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: os.ErrPermission}
}

func (c *countingTransport) received() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.hosts...)
}

// TestServingEveryServedTypeMakesNoOutboundRequest is AC16 over the product's own server,
// with a counting transport under each of the three outbound clients: the firewall's is
// the harness's fake, which counts what it received; MaxMind's and the Public Suffix
// List's are the product's own refreshers, built over the product's own clients with a
// counting transport where the network would be, and handed to the server exactly as
// cmd/opnview hands them. Serving every served type, signed in -- at its defaults, and
// top_sites grouped by registrable domain, the one parameter that consults the list --
// adds no request to any of the three.
func TestServingEveryServedTypeMakesNoOutboundRequest(t *testing.T) {
	t.Parallel()
	maxMind, suffixList := &countingTransport{}, &countingTransport{}
	dataDir := t.TempDir()
	harness := newHarnessAdjusted(t, dataDir, true, false, func(options *Options) {
		dataset, err := maxmind.OpenDataset(filepath.Join(dataDir, "geoip"))
		if err != nil {
			t.Fatalf("opening the MaxMind dataset directory: %v", err)
		}
		t.Cleanup(func() { _ = dataset.Close() })
		credentials := func(ctx context.Context) (config.MaxMindCredentials, config.CredentialState, error) {
			return config.LoadMaxMindCredentials(ctx, options.Store, options.Box)
		}
		options.GeoIP = maxmind.NewRefresher(maxmind.NewClientOver(maxMind, 5*time.Second), dataset,
			options.Store, credentials, time.Now)
		suffixes, err := publicsuffix.NewRefresher(publicsuffix.NewClientOver(suffixList, 5*time.Second),
			filepath.Join(dataDir, "publicsuffix"), options.Store, time.Now)
		if err != nil {
			t.Fatalf("building the Public Suffix List refresher: %v", err)
		}
		options.SuffixLists = suffixes
	})
	harness.completeSetup()
	before := len(harness.fake.received())
	paths := []string{widgetPath(t, "top_sites", map[string]any{"group_by_registrable_domain": true})}
	for _, widgetType := range harness.server.widgets.Types() {
		paths = append(paths, widget.PathPrefix+widgetType)
	}
	for _, path := range paths {
		answer := harness.get(path)
		if answer.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d", path, answer.StatusCode)
		}
		body := readBody(t, answer)
		for _, secret := range []string{"csrf", "session_token", "api_secret", "ciphertext"} {
			if strings.Contains(strings.ToLower(body), secret) {
				t.Errorf("%s carried %q", path, secret)
			}
		}
	}
	if after := len(harness.fake.received()); after != before {
		t.Errorf("serving the widgets made %d requests to the firewall", after-before)
	}
	if hosts := maxMind.received(); len(hosts) != 0 {
		t.Errorf("serving the widgets made %d requests to MaxMind: %v", len(hosts), hosts)
	}
	if hosts := suffixList.received(); len(hosts) != 0 {
		t.Errorf("serving the widgets made %d requests to the Public Suffix List: %v", len(hosts), hosts)
	}
}

// TestTheCountingTransportsCountWhatReachesThem is the teeth of the test above: a refresh
// of each of the two lists, asked for directly, does reach its counting transport, so a
// zero above is a request that was not made and not one the counter could not see.
func TestTheCountingTransportsCountWhatReachesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	suffixList := &countingTransport{}
	suffixes, err := publicsuffix.NewRefresher(publicsuffix.NewClientOver(suffixList, 5*time.Second),
		filepath.Join(dataDir, "publicsuffix"), database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_ = suffixes.Refresh(ctx)
	if len(suffixList.received()) == 0 {
		t.Error("a Public Suffix List refresh did not reach its counting transport")
	}
	maxMind := &countingTransport{}
	dataset, err := maxmind.OpenDataset(filepath.Join(dataDir, "geoip"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataset.Close() })
	credentials := func(context.Context) (config.MaxMindCredentials, config.CredentialState, error) {
		return config.MaxMindCredentials{AccountID: "000000", LicenceKey: "example-licence-key"},
			config.CredentialStateReady, nil
	}
	_ = maxmind.NewRefresher(maxmind.NewClientOver(maxMind, 5*time.Second), dataset, database, credentials,
		time.Now).Refresh(ctx)
	if len(maxMind.received()) == 0 {
		t.Error("a MaxMind refresh did not reach its counting transport")
	}
}
