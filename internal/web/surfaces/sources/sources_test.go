package sources_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/sources"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/interfacestest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
	"github.com/NaejEL/opnview/internal/web/webtest/sourcestest"
)

// providerOf is one provider of the Sources answer, failing when it is absent.
func providerOf(t *testing.T, answer map[string]any, kind, providerKey string) any {
	t.Helper()
	for _, raw := range settingstest.Path(answer, "providers").([]any) {
		if settingstest.Path(raw, "kind") == kind && settingstest.Path(raw, "provider_key") == providerKey {
			return raw
		}
	}
	t.Fatalf("the answer has no provider %s/%s", kind, providerKey)
	return nil
}

// sourcesAnswer is GET /api/sources.
func sourcesAnswer(h *webtest.Harness) map[string]any {
	h.T.Helper()
	return settingstest.GetJSON(h, sources.PathAPISources)
}

// TestSourcesAnswersOneEntryPerProviderWithEveryField is AC17.
func TestSourcesAnswersOneEntryPerProviderWithEveryField(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	var providers int
	if err := h.Store.DB().QueryRow("SELECT count(*) FROM provider").Scan(&providers); err != nil {
		t.Fatalf("counting the providers: %v", err)
	}
	sourcestest.SetAvailabilityDetail(h, "firewall_log", "pf", vocab.StateReachable, "test-probe", nil)
	answer := sourcesAnswer(h)
	if got := len(settingstest.Path(answer, "providers").([]any)); got != providers || providers == 0 {
		t.Errorf("the answer lists %d providers for %d provider rows", got, providers)
	}
	rows, err := h.Store.SourceAvailability(context.Background())
	if err != nil {
		t.Fatalf("reading the availability: %v", err)
	}
	for _, source := range rows {
		answered := providerOf(t, answer, source.Kind, source.ProviderKey)
		availability, _ := messages.AvailabilityOf(source)
		for _, field := range []struct {
			name string
			want any
		}{
			{"availability", web.ConditionOf(availability)},
			{"active", source.IsActive},
			{"selection", "auto"},
			{"probe", source.Probe},
			{"detail", nil},
			{"display_name", source.DisplayName},
		} {
			if got := settingstest.Path(answered, field.name); got != field.want {
				t.Errorf("%s/%s answers %s %#v, want %#v", source.Kind, source.ProviderKey, field.name, got, field.want)
			}
		}
		var checked any
		if !source.NotYetProbed() {
			checked = float64(source.CheckedAt)
		}
		if got := settingstest.Path(answered, "checked_at"); got != checked {
			t.Errorf("%s/%s answers checked_at %v, want %v", source.Kind, source.ProviderKey, got, checked)
		}
	}
}

// TestEachConditionAnswersItsOwnCodeAndOnlyReachableIsHealthy is AC18, on the Sources answer and
// on the collection's kinds alike.
func TestEachConditionAnswersItsOwnCodeAndOnlyReachableIsHealthy(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	sourcestest.SetAvailabilityDetail(h, "firewall_log", "pf", vocab.StateReachable, "test-probe", nil)
	sourcestest.SetAvailabilityDetail(h, "security_event", "suricata", vocab.StatePresentButDisabled, "test-probe", nil)
	sourcestest.SetAvailabilityDetail(h, "dhcp_lease", "kea", vocab.StateUnavailable, "test-probe", nil)
	sourcestest.DropAvailability(h, "dhcp_lease", "dnsmasq")
	sourcestest.DropAvailability(h, "dhcp_lease", "isc")
	sourcestest.DropAvailability(h, "dns_lookup", "unbound")
	sourcestest.DropAvailability(h, "dns_lookup", "dnsmasq")

	answer := sourcesAnswer(h)
	for _, expected := range []struct {
		kind, key string
		state     messageKey
	}{
		{"firewall_log", "pf", msgAvailabilityHealthy},
		{"security_event", "suricata", msgAvailabilityNotCollecting},
		{"dhcp_lease", "kea", msgAvailabilityUnavailable},
		{"dhcp_lease", "isc", msgAvailabilityUnknown},
		{"ip_geolocation", "maxmind_geolite2", msgAvailabilityNotYetProbed},
	} {
		if got := settingstest.Path(providerOf(t, answer, expected.kind, expected.key), "availability"); got != web.ConditionOf(expected.state) {
			t.Errorf("%s/%s answers %v, not %s", expected.kind, expected.key, got, web.ConditionOf(expected.state))
		}
	}

	kinds := settingstest.GetJSON(h, "/api/collection")
	for _, expected := range []struct {
		kind  string
		state messageKey
	}{
		{"firewall_log", msgAvailabilityHealthy},
		{"security_event", msgAvailabilityNotCollecting},
		{"dhcp_lease", msgAvailabilityUnavailable},
		{"dns_lookup", msgAvailabilityUnknown},
	} {
		found := false
		for _, raw := range settingstest.Path(kinds, "kinds").([]any) {
			if settingstest.Path(raw, "kind") == expected.kind {
				found = true
				if got := settingstest.Path(raw, "source_state", "availability"); got != web.ConditionOf(expected.state) {
					t.Errorf("the %s kind answers %v, not %s", expected.kind, got, web.ConditionOf(expected.state))
				}
			}
		}
		if !found {
			t.Errorf("the collection answers no kind %s", expected.kind)
		}
	}
}

// TestTwoRefusalsOfOneProviderAreDistinguishableThroughTheDetail is AC19: the detail is answered
// verbatim, as data.
func TestTwoRefusalsOfOneProviderAreDistinguishableThroughTheDetail(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	for _, detail := range []string{"HTTP 401 from the example endpoint", "HTTP 403 from the example endpoint"} {
		value := detail
		sourcestest.SetAvailabilityDetail(h, "dhcp_lease", "kea", vocab.StateUnavailable, "GET /example/probe", &value)
		if got := settingstest.Path(providerOf(t, sourcesAnswer(h), "dhcp_lease", "kea"), "detail"); got != detail {
			t.Errorf("the detail is answered %v, not %q verbatim", got, detail)
		}
	}
}

// TestEveryRecentGapIsAnsweredWithItsReason is AC20's surface half; the query plan is the schema
// checks'.
func TestEveryRecentGapIsAnsweredWithItsReason(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	if gaps := settingstest.Path(sourcesAnswer(h), "recent_gaps").([]any); len(gaps) != 0 {
		t.Errorf("a fresh installation answers the gaps %v", gaps)
	}

	now := h.Clock.Now().Unix()
	reasons := sourcestest.GapReasonsOfSchema(t, h)
	for index, reason := range reasons {
		if _, present := catalogue.Strings[string(messages.GapReasonKey(reason))]; !present {
			t.Errorf("the gap reason %s has no catalogue key", reason)
		}
		start := now - int64(3600*(index+1))
		collectiontest.RecordGap(h, "registrable_domain", "public_suffix_list", vocab.GapReason(reason), start, start+60)
	}
	outside := now - int64(sources.RecentGapWindow/time.Second) - 1
	collectiontest.RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, outside-77, outside)

	answer := sourcesAnswer(h)
	if got := settingstest.Path(answer, "recent_gap_window_seconds"); got != float64(sources.RecentGapWindow/time.Second) {
		t.Errorf("the window is answered %v", got)
	}
	gaps := settingstest.Path(answer, "recent_gaps").([]any)
	if len(gaps) != len(reasons) {
		t.Errorf("the answer lists %d gaps for %d gaps in the window", len(gaps), len(reasons))
	}
	for index, reason := range reasons {
		start := float64(now - int64(3600*(index+1)))
		found := false
		for _, gap := range gaps {
			if settingstest.Path(gap, "reason") == reason && settingstest.Path(gap, "start_at") == start &&
				settingstest.Path(gap, "end_at") == start+60 && settingstest.Path(gap, "missed_seconds") == float64(60) {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s gap is not answered with its reason, its interval and its seconds", reason)
		}
	}
	for _, gap := range gaps {
		if settingstest.Path(gap, "end_at") == float64(outside) {
			t.Error("a gap detected outside the window was answered")
		}
	}
}

// independentOutageCount counts the outages straight from the tables, by the rule of
// specs/SPEC-deferred-interface-bundle.md, scope 4, decision 4, written again here.
func independentOutageCount(t *testing.T, h *webtest.Harness) int {
	t.Helper()
	var count int
	if err := h.Store.DB().QueryRow(`
		WITH s AS (
		    SELECT p.kind AS kind,
		           coalesce((SELECT value FROM setting
		                     WHERE key = 'source_selection_' || p.kind || '_' || p.provider_key),
		                    'auto') AS selection,
		           coalesce(CAST(a.state = 'reachable' AS INTEGER), 0) AS reachable,
		           coalesce(CAST(a.state = 'unavailable' AND a.probe = 'not_yet_probed' AS INTEGER), 0) AS pending
		    FROM provider AS p
		    LEFT JOIN source_availability AS a ON a.provider_id = p.id),
		k AS (SELECT kind FROM s WHERE selection <> 'off' AND pending = 0
		      GROUP BY kind HAVING max(reachable) = 0)
		SELECT (SELECT count(*) FROM k)
		     + (SELECT count(*) FROM s WHERE selection = 'on' AND pending = 0 AND reachable = 0
		                                 AND kind NOT IN (SELECT kind FROM k))`).Scan(&count); err != nil {
		t.Fatalf("counting the outages independently: %v", err)
	}
	return count
}

// TestTheOutageCount is AC21: outage_count, the header's indicator, is the rule's count, and the
// outages and the providers it counts are named exactly.
func TestTheOutageCount(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	collectiontest.SetSelection(h, "dhcp_lease", "isc", config.SelectionOn)
	if got := independentOutageCount(t, h); got != 0 {
		t.Fatalf("the independent query counts %d outages on a fresh installation", got)
	}
	answer := sourcesAnswer(h)
	if answer["outage_count"] != float64(0) || len(settingstest.Path(answer, "outages").([]any)) != 0 {
		t.Errorf("a fresh installation answers %v outages: %v", answer["outage_count"], answer["outages"])
	}

	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateUnavailable)
	collectiontest.SetAvailability(h, "security_event", "suricata", vocab.StatePresentButDisabled)
	collectiontest.SetAvailability(h, "dhcp_lease", "kea", vocab.StateReachable)
	collectiontest.SetAvailability(h, "dhcp_lease", "isc", vocab.StateUnavailable)
	collectiontest.SetAvailability(h, "dhcp_lease", "dnsmasq", vocab.StateUnavailable)
	collectiontest.SetAvailability(h, "dns_lookup", "unbound", vocab.StateUnavailable)
	collectiontest.SetSelection(h, "dns_lookup", "unbound", config.SelectionOff)
	expected := independentOutageCount(t, h)
	if expected != 3 {
		t.Fatalf("the independent query counts %d outages on the fixture, want 3", expected)
	}
	answer = sourcesAnswer(h)
	if answer["outage_count"] != float64(expected) {
		t.Errorf("the answer counts %v outages, the rule %d", answer["outage_count"], expected)
	}
	named := map[string]bool{}
	for _, outage := range settingstest.Path(answer, "outages").([]any) {
		provider, _ := settingstest.Path(outage, "provider_key").(string)
		named[settingstest.Path(outage, "kind").(string)+"/"+provider] = true
	}
	for _, want := range []string{"firewall_log/", "security_event/", "dhcp_lease/isc"} {
		if !named[want] {
			t.Errorf("the outages %v do not name %s", named, want)
		}
	}
	for _, row := range []struct {
		kind, provider string
		counts         bool
	}{
		{"firewall_log", "pf", true},
		{"security_event", "suricata", true},
		{"dhcp_lease", "isc", true},
		{"dhcp_lease", "kea", false},
		{"dhcp_lease", "dnsmasq", false},
		{"dns_lookup", "unbound", false},
		{"flow_volume", "insight", false},
	} {
		if got := settingstest.Path(providerOf(t, answer, row.kind, row.provider), "in_outage"); got != row.counts {
			t.Errorf("%s/%s answers in_outage %v, want %v", row.kind, row.provider, got, row.counts)
		}
	}

	collectiontest.SetSelection(h, "firewall_log", "pf", config.SelectionOn)
	count, err := sources.CountOutages(context.Background(), h.Server)
	if err != nil {
		t.Fatalf("reading the source statuses: %v", err)
	}
	if want := independentOutageCount(t, h); count != want || count != 3 {
		t.Errorf("the rule counts %d with the filter log turned on, the query %d", count, want)
	}
}

// TestAnUnreadableAvailabilityIsAFailureAndNeverANought: a Sources answer that cannot be read is a
// failure the front names, never an outage count of nought, which would claim nothing is wrong.
func TestAnUnreadableAvailabilityIsAFailureAndNeverANought(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	if _, err := h.Store.DB().Exec("DROP TABLE source_availability"); err != nil {
		t.Fatalf("dropping the availability table: %v", err)
	}
	status, body := webtest.StatusAndBody(t, h.Get(sources.PathAPISources))
	if status != http.StatusInternalServerError || strings.Contains(body, "outage_count") {
		t.Errorf("an unreadable availability answered %d %s", status, body)
	}
}

// TestEveryKindAndProviderHasACatalogueLabel is AC22.
func TestEveryKindAndProviderHasACatalogueLabel(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	rows, err := h.Store.SourceAvailability(context.Background())
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the registry is empty, so this assertion is vacuous")
	}
	for _, row := range rows {
		for _, key := range []messageKey{messages.KindLabelKey(row.Kind), messages.ProviderLabelKey(row.Kind, row.ProviderKey)} {
			if _, present := catalogue.Strings[string(key)]; !present {
				t.Errorf("%s/%s has no catalogue label %s", row.Kind, row.ProviderKey, key)
			}
		}
	}
}

// offHostCounter records every request the default transport is asked to make to a host other
// than the server under test.
type offHostCounter struct {
	mutex sync.Mutex
	front string
	inner http.RoundTripper
	other []string
}

// RoundTrip records and passes the request on.
func (c *offHostCounter) RoundTrip(request *http.Request) (*http.Response, error) {
	c.mutex.Lock()
	if request.URL.Host != c.front {
		c.other = append(c.other, request.URL.Host)
	}
	c.mutex.Unlock()
	return c.inner.RoundTrip(request)
}

// TestReadingTheSettingsInterfacesAndSourcesContactsNothing is AC16: the three answers read the
// local database and make no request to the firewall or to any other host.
func TestReadingTheSettingsInterfacesAndSourcesContactsNothing(t *testing.T) {
	original := http.DefaultTransport
	counter := &offHostCounter{inner: original}
	http.DefaultTransport = counter
	t.Cleanup(func() { http.DefaultTransport = original })

	h := collectiontest.SignedInHarness(t)
	front, err := url.Parse(h.HTTP.URL)
	if err != nil {
		t.Fatalf("parsing the front address: %v", err)
	}
	counter.mutex.Lock()
	counter.front = front.Host
	counter.other = nil
	counter.mutex.Unlock()
	interfacestest.SeedBundleNetwork(h, 2)
	firewallBefore := len(h.Fake.Requests())

	for _, path := range []string{"/api/settings", "/api/interface-networks", sources.PathAPISources} {
		settingstest.GetJSON(h, path)
	}

	if got := len(h.Fake.Requests()) - firewallBefore; got != 0 {
		t.Errorf("reading the three answers sent %d requests to the firewall", got)
	}
	counter.mutex.Lock()
	defer counter.mutex.Unlock()
	if len(counter.other) != 0 {
		t.Errorf("reading the three answers contacted %v", counter.other)
	}
}

// TestAnInstalledSourceNotCollectingLinksToTheOPNsensePageThatTurnsItOn: installed, not
// collecting, and a firewall URL entered, a provider carries the OPNsense pages that turn it on --
// a change opnview never makes.
func TestAnInstalledSourceNotCollectingLinksToTheOPNsensePageThatTurnsItOn(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	sourcestest.SetAvailabilityDetail(h, "security_event", "suricata", vocab.StatePresentButDisabled, "GET /example/ids", nil)
	sourcestest.SetAvailabilityDetail(h, "dns_lookup", "unbound", vocab.StatePresentButDisabled, "GET /example/dns", nil)
	sourcestest.SetAvailabilityDetail(h, "dns_lookup", "dnsmasq", vocab.StatePresentButDisabled, "GET /example/dnsmasq", nil)
	sourcestest.SetAvailabilityDetail(h, "dhcp_lease", "kea", vocab.StateUnavailable, "GET /example/kea", nil)

	pages := func(answer map[string]any, kind, key string) map[string]string {
		found := map[string]string{}
		for _, page := range settingstest.Path(providerOf(t, answer, kind, key), "opnsense_pages").([]any) {
			found[settingstest.Path(page, "url").(string)] = settingstest.Path(page, "page").(string)
		}
		return found
	}
	if got := pages(sourcesAnswer(h), "security_event", "suricata"); len(got) != 0 {
		t.Errorf("a link was answered with no firewall URL entered: %v", got)
	}

	if err := h.Store.SetSetting(context.Background(), config.KeyOPNsenseBaseURL, webtest.ReviewFirewallURL,
		h.Clock.Now().Unix()); err != nil {
		t.Fatalf("storing the firewall URL: %v", err)
	}
	answer := sourcesAnswer(h)
	if got := pages(answer, "security_event", "suricata"); got[webtest.ReviewFirewallURL+"/ui/ids"] !=
		web.ConditionOf(msgPageIDSAdministration) {
		t.Errorf("Suricata installed and not collecting does not link to Intrusion Detection: %v", got)
	}
	// Each link carries the catalogue key its plugin declares for the page's name, which the front draws as
	// given: no front file or surface catalogue lists the pages.
	for _, page := range settingstest.Path(providerOf(t, answer, "security_event", "suricata"), "opnsense_pages").([]any) {
		if label := settingstest.Path(page, "label"); label != string(msgPageIDSAdministration) {
			t.Errorf("the Intrusion Detection link is labelled %v, not the plugin's key %s", label, msgPageIDSAdministration)
		}
	}
	unbound := pages(answer, "dns_lookup", "unbound")
	if unbound[webtest.ReviewFirewallURL+"/ui/unbound/general"] != web.ConditionOf(msgPageUnboundGeneral) ||
		unbound[webtest.ReviewFirewallURL+"/ui/unbound/overview"] != web.ConditionOf(msgPageUnboundReporting) {
		t.Errorf("Unbound's lookups do not link to both pages that turn them on: %v", unbound)
	}
	for _, row := range [][2]string{{"dns_lookup", "dnsmasq"}, {"dhcp_lease", "kea"}, {"firewall_log", "pf"}} {
		if got := pages(answer, row[0], row[1]); len(got) != 0 {
			t.Errorf("%s/%s carries a link it should not: %v", row[0], row[1], got)
		}
	}

	// Every page a built-in declares, or a core kind's registered row, is an OPNsense interface path cited
	// from the menu definition at the pinned version, and no path names a host.
	loaded := plugin.BuiltinsLoaded()
	declared := 0
	for _, member := range loaded.Plugins() {
		for _, info := range member.Providers {
			for _, page := range loaded.EnablePages(info.Kind, info.Key) {
				declared++
				if !strings.HasPrefix(page.Path, "/ui/") ||
					!strings.HasPrefix(page.MenuURL, "https://github.com/opnsense/core/blob/26.7.3/") {
					t.Errorf("%s/%s cites %q from %q, not an OPNsense interface path cited at 26.7.3", info.Kind,
						info.Key, page.Path, page.MenuURL)
				}
			}
		}
	}
	if declared != 8 || len(loaded.EnablePages("flow_volume", "insight")) != 0 {
		t.Errorf("the built-ins declare %d enable pages, want the eight of today (Unbound general on three rows), and none for flow_volume/insight, which nothing probes", declared)
	}
}

// TestOneSelectionHasOneNameOnTheSourcesSurface: the operator's "on" reads the same in the outage
// list as in the selection.
func TestOneSelectionHasOneNameOnTheSourcesSurface(t *testing.T) {
	t.Parallel()
	on := collectiontest.CatalogueText(t, msgSelectionOn)
	if !strings.HasPrefix(collectiontest.CatalogueText(t, msgOutageProvider), on) {
		t.Errorf("the outage list names a provider turned on %q, the selection %q",
			collectiontest.CatalogueText(t, msgOutageProvider), on)
	}
}
