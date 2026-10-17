package widgetapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/accounttest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
)

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
	body := webtest.ReadBody(t, answer)
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
	harness := webtest.New(t)
	routed := map[string]bool{}
	for _, route := range harness.Server.Routes() {
		if !strings.HasPrefix(route.Pattern, widget.PathPrefix) {
			continue
		}
		if route.Access != web.AccessAuthenticated || !route.JSON || route.Method != http.MethodGet {
			t.Errorf("%s %s is %s (JSON %v), not an authenticated JSON GET", route.Method, route.Pattern,
				route.Access, route.JSON)
		}
		routed[strings.TrimPrefix(route.Pattern, widget.PathPrefix)] = true
	}
	types := harness.Server.Widgets().Types()
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
	harness := webtest.New(t)
	harness.CompleteSetup()
	path := widgetPath(t, "interface_traffic_matrix", nil)

	anonymous := webtest.FreshClient(t, harness)
	answer := anonymous.Get(path)
	condition, body := decodeCondition(t, answer, http.StatusUnauthorized)
	if condition != "no_session" {
		t.Errorf("no session answered %q, not %q", condition, "no_session")
	}
	if _, carries := body["data"]; carries {
		t.Error("a refused request carried widget data")
	}

	// A signed-in reader whose session then expires.
	answer = harness.Get(path)
	accounttest.AssertStatus(t, answer, http.StatusOK)
	harness.Clock.Advance(auth.DefaultLifetimes().Absolute + auth.DefaultLifetimes().Idle)
	answer = harness.Get(path)
	condition, body = decodeCondition(t, answer, http.StatusUnauthorized)
	if condition != "session_expired" {
		t.Errorf("an expired session answered %q, not %q", condition, "session_expired")
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
	harness := webtest.New(t)
	harness.CompleteSetup()
	for _, unknown := range []string{"client_activity_timeline", "alerts_by_signature", "no_such_widget"} {
		answer := harness.Get(widget.PathPrefix + unknown)
		if condition, _ := decodeCondition(t, answer, http.StatusNotFound); condition != envelope.ConditionUnknownWidgetType {
			t.Errorf("%s answered %q", unknown, condition)
		}
	}
	for _, malformed := range []string{"{", "[1,2]", "\"text\"", "{} trailing"} {
		answer := harness.Get(widget.PathPrefix + "interface_traffic_matrix?" + widget.ParametersQueryKey + "=" +
			url.QueryEscape(malformed))
		if condition, _ := decodeCondition(t, answer, http.StatusBadRequest); condition != envelope.ConditionMalformedParameters {
			t.Errorf("%q answered %q", malformed, condition)
		}
	}
	answer := harness.Get(widgetPath(t, "interface_traffic_matrix", map[string]any{"no_such_key": 1}))
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
// stands under the host guard of the plugin host's fetch facility, where the network would be.
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

func (c *countingTransport) Received() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.hosts...)
}

// pluginCollector loads the built-in plugins with the fetch facility over a counting transport,
// and builds the collector over them, as cmd/opnview builds it.
func pluginCollector(t *testing.T, client *opnsense.Client, database *store.Store, sealer plugin.Sealer,
	transport http.RoundTripper) (*plugin.Loaded, *collect.Collector) {
	t.Helper()
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: t.TempDir(), Store: database,
		Sealer: sealer, FetchTransport: transport})
	if err != nil {
		t.Fatalf("loading the plugins: %v", err)
	}
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("a built-in plugin was refused: %v", refusals)
	}
	return loaded, collect.NewWithSources(client, database, core.SystemClock{}, loaded.Set)
}

// TestServingEveryServedTypeMakesNoOutboundRequest is AC16 over the product's own server,
// with a counting transport under each outbound path: the firewall's is the harness's fake,
// which counts what it received; every other destination is reached only through the plugin
// host's fetch facility, built here over a counting transport where the network would be, and
// the collector over those plugins is handed to the server exactly as cmd/opnview hands it.
// Serving every served type, signed in -- at its defaults, and top_sites grouped by
// registrable domain, the one parameter that consults the registrable_domain kind -- adds no
// request to either.
func TestServingEveryServedTypeMakesNoOutboundRequest(t *testing.T) {
	t.Parallel()
	outbound := &countingTransport{}
	dataDir := t.TempDir()
	harness := webtest.NewAdjusted(t, dataDir, true, false, func(options *web.Options) {
		loaded, collector := pluginCollector(t, options.Client, options.Store, options.Box, outbound)
		options.Plugins = loaded
		options.GeoIP = collector
		options.SuffixLists = collector
	})
	harness.CompleteSetup()
	before := len(harness.Fake.Requests())
	paths := []string{widgetPath(t, "top_sites", map[string]any{"group_by_registrable_domain": true})}
	for _, widgetType := range harness.Server.Widgets().Types() {
		paths = append(paths, widget.PathPrefix+widgetType)
	}
	for _, path := range paths {
		answer := harness.Get(path)
		if answer.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d", path, answer.StatusCode)
		}
		body := webtest.ReadBody(t, answer)
		for _, secret := range []string{"csrf", "session_token", "api_secret", "ciphertext"} {
			if strings.Contains(strings.ToLower(body), secret) {
				t.Errorf("%s carried %q", path, secret)
			}
		}
	}
	if after := len(harness.Fake.Requests()); after != before {
		t.Errorf("serving the widgets made %d requests to the firewall", after-before)
	}
	if hosts := outbound.Received(); len(hosts) != 0 {
		t.Errorf("serving the widgets made %d requests beyond the firewall: %v", len(hosts), hosts)
	}
}

// TestTheCountingTransportsCountWhatReachesThem is the teeth of the test above: a refresh of
// each service kind's built-in provider, asked for directly, does reach the counting
// transport, so a zero above is a request that was not made and not one the counter could not
// see.
func TestTheCountingTransportsCountWhatReachesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, err := secret.OpenOrCreate(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	outbound := &countingTransport{}
	loaded, collector := pluginCollector(t, opnsense.NewClientFromSource(config.NewFirewallCredentials(), nil, 0),
		database, box, outbound)

	if err := collector.RefreshRegistrableDomains(ctx); err != nil {
		t.Logf("the registrable_domain refresh: %v", err)
	}
	if !containsHost(outbound.Received(), "publicsuffix.org") {
		t.Errorf("a registrable_domain refresh did not reach its counting transport: %v", outbound.Received())
	}
	maxmind, found := loaded.Plugin("maxmind")
	if !found {
		t.Fatal("the maxmind plugin is not loaded")
	}
	if err := maxmind.WriteSettings(ctx, database, box, map[string]any{"account_id": 100001,
		"licence_key": "example-licence-key"}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	_ = collector.RefreshGeolocation(ctx)
	if !containsHost(outbound.Received(), "download.maxmind.com") {
		t.Errorf("an ip_geolocation refresh did not reach its counting transport: %v", outbound.Received())
	}
}

// containsHost reports whether a host is among those counted.
func containsHost(hosts []string, host string) bool {
	for _, counted := range hosts {
		if counted == host {
			return true
		}
	}
	return false
}
