package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The Sources surface and the outage indicator.
//
// PROVENANCE OF EVERY FIXTURE HERE: SYNTHESISED. The availability rows are written
// through the store's own write path with probes and details invented for the test --
// short sentences naming an HTTP status and nothing of any real network -- and the
// gaps through the collectors' own write path, on the harness's stopped clock. The
// providers are the registry the schema seeds; nothing is read off a network.

// sourcesTable returns the providers table's body out of a Sources page.
func sourcesTable(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `aria-labelledby="sources_providers"`)
	if start < 0 {
		t.Fatal("the page has no providers table")
	}
	body := page[start:]
	body = body[strings.Index(body, "<tbody>"):]
	return body[:strings.Index(body, "</tbody>")]
}

// gapsCard returns the gaps card out of a Sources page.
func gapsCard(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `aria-labelledby="sources_gaps"`)
	if start < 0 {
		t.Fatal("the page has no gaps card")
	}
	body := page[start:]
	return body[:strings.Index(body, "</section>")]
}

// sourceRowOf returns the row of one provider, found by its two catalogue labels, with
// the line beneath it carrying its probe and its detail.
func sourceRowOf(t *testing.T, table, kind, providerKey string) string {
	t.Helper()
	marker := "<td>" + catalogueText(t, kindLabelKey(kind)) + "</td>\n<td>" +
		catalogueText(t, providerLabelKey(kind, providerKey)) + "</td>"
	start := strings.Index(table, marker)
	if start < 0 {
		t.Fatalf("the table has no row for %s/%s", kind, providerKey)
	}
	row := table[start:]
	first := strings.Index(row, "</tr>") + len("</tr>")
	return row[:first+strings.Index(row[first:], "</tr>")]
}

// setAvailabilityDetail records a probe outcome with a probe name and a detail.
func (h *harness) setAvailabilityDetail(kind, providerKey string, state store.AvailabilityState,
	probe string, detail *string) {
	h.t.Helper()
	if err := h.store.SetAvailability(context.Background(), h.providerID(kind, providerKey),
		state, probe, detail, h.clock.Now().Unix()); err != nil {
		h.t.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// dropAvailability deletes one provider's availability row, which the schema
// prevents and a screen still has to name.
func (h *harness) dropAvailability(kind, providerKey string) {
	h.t.Helper()
	if _, err := h.store.DB().Exec("DELETE FROM source_availability WHERE provider_id = ?",
		h.providerID(kind, providerKey)); err != nil {
		h.t.Fatalf("deleting an availability row: %v", err)
	}
}

// TestSourcesDrawsOneRowPerProviderWithEveryColumn is AC17.
func TestSourcesDrawsOneRowPerProviderWithEveryColumn(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	var providers int
	if err := h.store.DB().QueryRow("SELECT count(*) FROM provider").Scan(&providers); err != nil {
		t.Fatalf("counting the providers: %v", err)
	}
	h.setAvailabilityDetail("firewall_log", "pf", store.StateReachable, "test-probe", nil)
	page := pageSource(t, h, PathSources)
	table := sourcesTable(t, page)
	if got := strings.Count(table, "<tr>"); got != providers || providers == 0 {
		t.Errorf("the table draws %d rows for %d provider rows", got, providers)
	}

	rows, err := h.store.SourceAvailability(context.Background())
	if err != nil {
		t.Fatalf("reading the availability: %v", err)
	}
	for _, source := range rows {
		row := sourceRowOf(t, table, source.Kind, source.ProviderKey)
		availability, _ := availabilityOf(source)
		for _, expected := range []string{
			catalogueText(t, availability),
			"<td>" + catalogueText(t, yesNo(source.IsActive)) + "</td>",
			"<td>" + catalogueText(t, msgSelectionAuto) + "</td>",
			probeCell(source.Probe),
			catalogueText(t, msgNoDetail),
		} {
			if !strings.Contains(row, expected) {
				t.Errorf("the row of %s/%s does not carry %q", source.Kind, source.ProviderKey, expected)
			}
		}
		checked := catalogueText(t, msgNeverChecked)
		if !source.NotYetProbed() {
			checked = `<span class="figure">` + instant(source.CheckedAt) + "</span>"
		}
		if !strings.Contains(row, checked) {
			t.Errorf("the row of %s/%s does not carry its checked instant %q", source.Kind,
				source.ProviderKey, checked)
		}
	}
}

// TestEachConditionRendersItsOwnKeyAndOnlyReachableIsHealthy is AC18.
func TestEachConditionRendersItsOwnKeyAndOnlyReachableIsHealthy(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	h.setAvailabilityDetail("firewall_log", "pf", store.StateReachable, "test-probe", nil)
	h.setAvailabilityDetail("security_event", "suricata", store.StatePresentButDisabled, "test-probe", nil)
	h.setAvailabilityDetail("dhcp_lease", "kea", store.StateUnavailable, "test-probe", nil)
	for _, key := range []string{"kea", "dnsmasq", "isc"} {
		if key != "kea" {
			h.dropAvailability("dhcp_lease", key)
		}
	}
	h.dropAvailability("dns_lookup", "unbound")
	h.dropAvailability("dns_lookup", "dnsmasq")

	table := sourcesTable(t, pageSource(t, h, PathSources))
	for _, expected := range []struct {
		kind, key string
		state     messageKey
	}{
		{"firewall_log", "pf", msgAvailabilityHealthy},
		{"security_event", "suricata", msgAvailabilityNotCollecting},
		{"dhcp_lease", "kea", msgAvailabilityUnavailable},
		{"dhcp_lease", "isc", msgAvailabilityUnknown},
		{"geo_asn", "maxmind_geolite2", msgAvailabilityNotYetProbed},
	} {
		row := sourceRowOf(t, table, expected.kind, expected.key)
		if !strings.Contains(row, ">"+catalogueText(t, expected.state)+"</span>") {
			t.Errorf("%s/%s does not read %s", expected.kind, expected.key, expected.state)
		}
		if expected.state != msgAvailabilityHealthy &&
			strings.Contains(row, catalogueText(t, msgAvailabilityHealthy)) {
			t.Errorf("%s/%s reads healthy and is not reachable", expected.kind, expected.key)
		}
	}

	// THE COLLECTION CARDS USE THE SAME KEYS FOR THE SAME STATES.
	page := pageSource(t, h, PathCollection)
	for _, expected := range []struct {
		kind  string
		state messageKey
	}{
		{"firewall_log", msgAvailabilityHealthy},
		{"security_event", msgAvailabilityNotCollecting},
		{"dhcp_lease", msgAvailabilityUnavailable},
		{"dns_lookup", msgAvailabilityUnknown},
	} {
		if !stateShown(t, cardOf(t, page, expected.kind), msgLabelSourceState, expected.state) {
			t.Errorf("the %s card does not read %s", expected.kind, expected.state)
		}
	}
	fresh := signedInHarness(t)
	if !stateShown(t, cardOf(t, pageSource(t, fresh, PathCollection), "firewall_log"),
		msgLabelSourceState, msgAvailabilityNotYetProbed) {
		t.Error("a kind never probed does not read not yet probed on its card")
	}
}

// TestTwoRefusalsOfOneProviderAreDistinguishableThroughTheDetail is AC19.
func TestTwoRefusalsOfOneProviderAreDistinguishableThroughTheDetail(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	var rows []string
	for _, detail := range []string{"HTTP 401 from the example endpoint", "HTTP 403 from the example endpoint"} {
		value := detail
		h.setAvailabilityDetail("dhcp_lease", "kea", store.StateUnavailable, "GET /example/probe", &value)
		row := sourceRowOf(t, sourcesTable(t, pageSource(t, h, PathSources)), "dhcp_lease", "kea")
		if !strings.Contains(row, `<dd class="detail"><span class="datum" translate="no">`+detail+"</span></dd>") {
			t.Errorf("the detail %q is not rendered verbatim, as data", detail)
		}
		rows = append(rows, row)
	}
	if rows[0] == rows[1] {
		t.Error("a 401 and a 403 render the same row")
	}
}

// gapReasonsOfSchema reads the reasons collection_gap's CHECK admits from the table's
// own definition, so a reason the schema gains is a reason this test asks a key of.
func gapReasonsOfSchema(t *testing.T, h *harness) []string {
	t.Helper()
	var definition string
	if err := h.store.DB().QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'collection_gap'").
		Scan(&definition); err != nil {
		t.Fatalf("reading the collection_gap definition: %v", err)
	}
	start := strings.Index(definition, "reason IN (")
	if start < 0 {
		t.Fatal("the collection_gap definition has no CHECK on reason")
	}
	list := definition[start+len("reason IN ("):]
	list = list[:strings.Index(list, ")")]
	var reasons []string
	for _, part := range strings.Split(list, ",") {
		reasons = append(reasons, strings.Trim(strings.TrimSpace(part), "'"))
	}
	if len(reasons) < 4 {
		t.Fatalf("read %d gap reasons out of the schema, which is fewer than it holds", len(reasons))
	}
	return reasons
}

// TestEveryRecentGapRendersWithItsReasonKey is AC20's surface half; the query plan is
// asserted in internal/store, beside the statement.
func TestEveryRecentGapRendersWithItsReasonKey(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	if !strings.Contains(gapsCard(t, pageSource(t, h, PathSources)),
		catalogueText(t, "state.source.no_recent_gap")) {
		t.Error("no gap in the window is not a named state")
	}

	now := h.clock.Now().Unix()
	reasons := gapReasonsOfSchema(t, h)
	for index, reason := range reasons {
		if _, present := catalogue.Strings[string(gapReasonKey(reason))]; !present {
			t.Errorf("the gap reason %s has no catalogue key", reason)
		}
		start := now - int64(3600*(index+1))
		h.recordGap("public_suffix", "public_suffix_list", store.GapReason(reason), start, start+60)
	}
	// One detected a day and a second ago, outside the window.
	outside := now - int64(recentGapWindow/time.Second) - 1
	h.recordGap("firewall_log", "pf", store.GapDigestOutsideWindow, outside-77, outside)

	card := gapsCard(t, pageSource(t, h, PathSources))
	if got := strings.Count(card, "<tr>") - 1; got != len(reasons) {
		t.Errorf("the gaps table draws %d rows for %d gaps in the window", got, len(reasons))
	}
	for index, reason := range reasons {
		start := now - int64(3600*(index+1))
		row := "<td>" + catalogueText(t, gapReasonKey(reason)) + "</td>\n<td><span class=\"figure\">" +
			instant(start) + "</span></td>\n<td><span class=\"figure\">" + instant(start+60) +
			"</span></td>\n<td><span class=\"duration-part\"><span class=\"figure\">1</span> " +
			catalogueText(t, msgUnitMinute) + "</span></td>"
		if !strings.Contains(card, row) {
			t.Errorf("the %s gap is not drawn with its reason, its interval and its seconds", reason)
		}
	}
	if strings.Contains(card, instant(outside)) {
		t.Error("a gap detected outside the window was drawn")
	}
	// Newest first.
	if strings.Index(card, instant(now-3600)) > strings.Index(card, instant(now-7200)) {
		t.Error("the gaps are not drawn newest first")
	}
}

// independentOutageCount computes the outage rule in SQL, apart from the Go that
// draws the indicator, from the same rows.
func independentOutageCount(t *testing.T, h *harness) int {
	t.Helper()
	var count int
	if err := h.store.DB().QueryRow(`
		WITH s AS (
		    SELECT p.kind AS kind,
		           coalesce((SELECT value FROM setting
		                     WHERE key = 'source_selection_' || p.kind || '_' || p.provider_key),
		                    'auto') AS selection,
		           coalesce(a.state = 'reachable', 0) AS reachable,
		           coalesce(a.state = 'unavailable' AND a.probe = 'not_yet_probed', 0) AS pending
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

// indicatorOf returns the outage indicator out of a page.
func indicatorOf(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `<a class="outages`)
	if start < 0 {
		t.Fatal("the page carries no outage indicator")
	}
	return page[start : start+strings.Index(page[start:], "</a>")]
}

// TestTheOutageIndicator is AC21.
func TestTheOutageIndicator(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)

	// A fresh installation: every row not yet probed, so nothing is in outage, even
	// with a provider turned on.
	h.setSelection("dhcp_lease", "isc", config.SelectionOn)
	if got := independentOutageCount(t, h); got != 0 {
		t.Fatalf("the independent query counts %d outages on a fresh installation", got)
	}
	for _, path := range []string{PathSettings, PathCollection, PathInterfaces, PathSources} {
		indicator := indicatorOf(t, pageSource(t, h, path))
		if !strings.Contains(indicator, `href="`+PathSources+`"`) ||
			!strings.Contains(indicator, catalogueText(t, "indicator.no_outage")) {
			t.Errorf("the indicator on %s is not the named state at nought linking to Sources", path)
		}
	}

	// The filter log unavailable (a kind in outage); one DHCP implementation reachable
	// and another turned on and failing (a provider in outage); both resolvers off;
	// Suricata installed and switched off on auto (a kind in outage).
	h.setAvailability("firewall_log", "pf", store.StateUnavailable)
	h.setAvailability("dhcp_lease", "kea", store.StateReachable)
	h.setAvailability("dhcp_lease", "isc", store.StateUnavailable)
	h.setAvailability("dns_lookup", "unbound", store.StateUnavailable)
	h.setSelection("dns_lookup", "unbound", config.SelectionOff)
	h.setAvailability("security_event", "suricata", store.StatePresentButDisabled)
	expected := independentOutageCount(t, h)
	if expected != 3 {
		t.Fatalf("the independent query counts %d outages on the fixture, want 3", expected)
	}
	for _, path := range []string{PathSettings, PathCollection, PathInterfaces, PathSources} {
		indicator := indicatorOf(t, pageSource(t, h, path))
		if !strings.Contains(indicator, catalogueText(t, "indicator.outages")) ||
			!strings.Contains(indicator, `<span class="figure">`+strconv.Itoa(expected)+"</span>") ||
			!strings.Contains(indicator, `href="`+PathSources+`"`) {
			t.Errorf("the indicator on %s does not show %d outages: %s", path, expected, indicator)
		}
	}

	// A provider turned on in a kind already in outage is not counted twice.
	h.setSelection("firewall_log", "pf", config.SelectionOn)
	if got, want := countOutagesOf(t, h), independentOutageCount(t, h); got != want || got != 3 {
		t.Errorf("the indicator counts %d with the filter log turned on, the query %d", got, want)
	}
}

// countOutagesOf is the indicator's own count.
func countOutagesOf(t *testing.T, h *harness) int {
	t.Helper()
	statuses, err := h.server.readSourceStatuses(context.Background())
	if err != nil {
		t.Fatalf("reading the source statuses: %v", err)
	}
	return countOutages(statuses)
}

// TestEveryKindAndProviderHasACatalogueLabel is AC22.
func TestEveryKindAndProviderHasACatalogueLabel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	rows, err := h.store.SourceAvailability(context.Background())
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the registry is empty, so this assertion is vacuous")
	}
	for _, row := range rows {
		for _, key := range []messageKey{kindLabelKey(row.Kind), providerLabelKey(row.Kind, row.ProviderKey)} {
			if _, present := catalogue.Strings[string(key)]; !present {
				t.Errorf("%s/%s has no catalogue label %s", row.Kind, row.ProviderKey, key)
			}
		}
	}
}

// countingTransport counts every request that leaves for a host other than the test
// server's own front, which is the one host a test's own page fetches reach.
type countingTransport struct {
	mutex sync.Mutex
	front string
	inner http.RoundTripper
	other []string
}

// RoundTrip counts a request to any host but the front, and passes every request on.
func (c *countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	c.mutex.Lock()
	if request.URL.Host != c.front {
		c.other = append(c.other, request.URL.Host)
	}
	c.mutex.Unlock()
	return c.inner.RoundTrip(request)
}

// TestDrawingTheSettingsInterfacesAndSourcesContactsNothing is AC16.
//
// IT IS NOT PARALLEL, and that is what makes it able to count every outbound request
// at all: it replaces the process's default transport, which every HTTP client built
// without one of its own uses -- the firewall client's guarded transport included,
// since the harness wraps the default -- and no parallel test runs while it does.
func TestDrawingTheSettingsInterfacesAndSourcesContactsNothing(t *testing.T) {
	original := http.DefaultTransport
	counter := &countingTransport{inner: original}
	http.DefaultTransport = counter
	t.Cleanup(func() { http.DefaultTransport = original })

	h := signedInHarness(t)
	front, err := url.Parse(h.http.URL)
	if err != nil {
		t.Fatalf("parsing the front address: %v", err)
	}
	counter.mutex.Lock()
	counter.front = front.Host
	counter.other = nil
	counter.mutex.Unlock()
	h.seedBundleNetwork(2)
	firewallBefore := len(h.fake.received())

	for _, path := range []string{PathSettings, PathInterfaces, PathSources} {
		_ = pageSource(t, h, path)
	}

	if got := len(h.fake.received()) - firewallBefore; got != 0 {
		t.Errorf("drawing the three surfaces sent %d requests to the firewall", got)
	}
	counter.mutex.Lock()
	defer counter.mutex.Unlock()
	if len(counter.other) != 0 {
		t.Errorf("drawing the three surfaces contacted %v", counter.other)
	}
}

// probeCell is how the providers table draws a probe: each path component its own
// datum, a line break offered after every slash.
func probeCell(probe string) string {
	var cell strings.Builder
	cell.WriteString(`<dd class="probe">`)
	for index, part := range pathParts(probe) {
		if index > 0 {
			cell.WriteString("<wbr>")
		}
		cell.WriteString(`<span class="datum" translate="no">` + part + "</span>")
	}
	return cell.String() + "</dd>"
}

// TestAProbeIsCutOnlyBetweenComponents: the parts are the probe, every character kept,
// each ending at a slash.
func TestAProbeIsCutOnlyBetweenComponents(t *testing.T) {
	t.Parallel()
	for probe, want := range map[string][]string{
		"GET /api/example/status": {"GET /", "api/", "example/", "status"},
		"no_licence_key":          {"no_licence_key"},
		"/trailing/":              {"/", "trailing/"},
		"":                        nil,
	} {
		got := pathParts(probe)
		if strings.Join(got, "") != probe || len(got) != len(want) {
			t.Errorf("%q is cut into %q, want %q", probe, got, want)
			continue
		}
		for index := range want {
			if got[index] != want[index] {
				t.Errorf("%q is cut into %q, want %q", probe, got, want)
			}
		}
	}
}

// outageFlagOf is the outage cell of one provider's row.
func outageFlagOf(t *testing.T, table, kind, providerKey string) string {
	t.Helper()
	row := sourceRowOf(t, table, kind, providerKey)
	start := strings.Index(row, `<span class="outage-flag`)
	if start < 0 {
		t.Fatalf("the row of %s/%s carries no outage cell", kind, providerKey)
	}
	return row[start : start+strings.Index(row[start:], "</span>")]
}

// TestSourcesMarksExactlyTheRowsTheIndicatorCounts: the page the indicator opens
// lists one line per outage it counts, and marks the rows those outages are about --
// and no other, however unavailable.
func TestSourcesMarksExactlyTheRowsTheIndicatorCounts(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	// The filter log unavailable: a kind in outage. Suricata installed and not
	// collecting, on auto: a kind in outage. One DHCP implementation reachable, one
	// turned on and failing (a provider in outage), one unavailable on auto beside the
	// reachable one (counts towards nothing). The resolver lookups declined.
	h.setAvailability("firewall_log", "pf", store.StateUnavailable)
	h.setAvailability("security_event", "suricata", store.StatePresentButDisabled)
	h.setAvailability("dhcp_lease", "kea", store.StateReachable)
	h.setAvailability("dhcp_lease", "isc", store.StateUnavailable)
	h.setSelection("dhcp_lease", "isc", config.SelectionOn)
	h.setAvailability("dhcp_lease", "dnsmasq", store.StateUnavailable)
	h.setAvailability("dns_lookup", "unbound", store.StateUnavailable)
	h.setSelection("dns_lookup", "unbound", config.SelectionOff)

	page := pageSource(t, h, PathSources)
	expected := independentOutageCount(t, h)
	if expected != 3 {
		t.Fatalf("the independent query counts %d outages, want 3", expected)
	}
	start := strings.Index(page, `<ul class="outage-list">`)
	if start < 0 {
		t.Fatal("the Sources surface does not list the outages")
	}
	list := page[start : start+strings.Index(page[start:], "</ul>")]
	if got := strings.Count(list, "<li>"); got != expected {
		t.Errorf("the outage list has %d lines, the indicator counts %d", got, expected)
	}
	for _, line := range []string{
		catalogueText(t, kindLabelKey("firewall_log")) + `</span> <span class="outage-state">` +
			catalogueText(t, msgOutageKind),
		catalogueText(t, kindLabelKey("security_event")) + `</span> <span class="outage-state">` +
			catalogueText(t, msgOutageKind),
		`<span class="outage-provider">` + catalogueText(t, providerLabelKey("dhcp_lease", "isc")) +
			`</span> <span class="outage-state">` + catalogueText(t, msgOutageProvider),
	} {
		if !strings.Contains(list, line) {
			t.Errorf("the outage list does not carry %q", line)
		}
	}

	table := sourcesTable(t, page)
	counted := `<span class="outage-flag counted">` + catalogueText(t, msgYes)
	notCounted := `<span class="outage-flag">` + catalogueText(t, msgNo)
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
		want := notCounted
		if row.counts {
			want = counted
		}
		if got := outageFlagOf(t, table, row.kind, row.provider); got != want {
			t.Errorf("the row of %s/%s is marked %q, want %q", row.kind, row.provider, got, want)
		}
	}
	if got := strings.Count(table, `class="outage-flag counted"`); got != 3 {
		t.Errorf("%d rows are marked as counting, want 3", got)
	}

	// At nought, the named state.
	fresh := signedInHarness(t)
	if !strings.Contains(pageSource(t, fresh, PathSources),
		`<p class="empty-state">`+catalogueText(t, "indicator.no_outage")+"</p>") {
		t.Error("no outage is not a named state on the Sources surface")
	}
}

// TestAnUnreadableIndicatorIsANamedStateAndNotAFailedPage: the bar is on every
// signed-in page, so a failed read of the outages names its own state there and the
// page is drawn.
func TestAnUnreadableIndicatorIsANamedStateAndNotAFailedPage(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	h.seedBundleNetwork(1)
	if _, err := h.store.DB().Exec("DROP TABLE source_availability"); err != nil {
		t.Fatalf("dropping the availability table: %v", err)
	}
	indicator := indicatorOf(t, pageSource(t, h, PathInterfaces))
	if !strings.Contains(indicator, catalogueText(t, "indicator.unknown")) ||
		strings.Contains(indicator, catalogueText(t, "indicator.no_outage")) ||
		!strings.Contains(indicator, `href="`+PathSources+`"`) {
		t.Errorf("an unreadable indicator is not its own named state: %s", indicator)
	}
}

// TestAnInstalledSourceNotCollectingLinksToTheOPNsensePageThatTurnsItOn: installed,
// not collecting carries the OPNsense page that turns the provider on
// (docs/widget-catalogue.md, Source availability), built from the firewall URL the
// operator entered and a path cited in opnsense_pages.go, under a label saying the
// change is made in OPNsense; any other condition, a provider no page turns on, or an
// installation with no firewall URL carries none.
func TestAnInstalledSourceNotCollectingLinksToTheOPNsensePageThatTurnsItOn(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	h.setAvailabilityDetail("security_event", "suricata", store.StatePresentButDisabled, "GET /example/ids", nil)
	h.setAvailabilityDetail("dns_lookup", "unbound", store.StatePresentButDisabled, "GET /example/dns", nil)
	h.setAvailabilityDetail("dns_lookup", "dnsmasq", store.StatePresentButDisabled, "GET /example/dnsmasq", nil)
	h.setAvailabilityDetail("dhcp_lease", "kea", store.StateUnavailable, "GET /example/kea", nil)

	// No firewall URL entered: no link anywhere.
	if strings.Contains(pageSource(t, h, PathSources), `class="opnsense-page"`) {
		t.Error("a link was drawn with no firewall URL entered")
	}

	if err := h.store.SetSetting(context.Background(), config.KeyOPNsenseBaseURL, reviewFirewallURL,
		h.clock.Now().Unix()); err != nil {
		t.Fatalf("storing the firewall URL: %v", err)
	}
	table := sourcesTable(t, pageSource(t, h, PathSources))
	link := func(path string, label messageKey) string {
		return `<a class="opnsense-page" href="` + reviewFirewallURL + path + `" rel="noreferrer noopener">` +
			catalogueText(t, label) + "</a>"
	}
	suricata := sourceRowOf(t, table, "security_event", "suricata")
	if !strings.Contains(suricata, "<dt>"+catalogueText(t, msgOpenInOPNsense)+"</dt>") ||
		!strings.Contains(suricata, link("/ui/ids", msgPageIDSAdministration)) {
		t.Error("Suricata installed and not collecting does not link to Intrusion Detection")
	}
	unbound := sourceRowOf(t, table, "dns_lookup", "unbound")
	if !strings.Contains(unbound, link("/ui/unbound/general", msgPageUnboundGeneral)) ||
		!strings.Contains(unbound, link("/ui/unbound/overview", msgPageUnboundReporting)) {
		t.Error("Unbound's lookups do not link to both pages that turn them on")
	}
	for _, row := range [][2]string{{"dns_lookup", "dnsmasq"}, {"dhcp_lease", "kea"}, {"firewall_log", "pf"}} {
		if strings.Contains(sourceRowOf(t, table, row[0], row[1]), `class="opnsense-page"`) {
			t.Errorf("%s/%s carries a link it should not", row[0], row[1])
		}
	}

	// Every page cited is an OPNsense interface path, and no path names a host.
	for _, kind := range []string{"security_event", "dhcp_lease", "dns_lookup", "resolver_cache",
		"resolver_local_data", "flow_volume", "measurement_sample"} {
		for _, key := range []string{"suricata", "kea", "dnsmasq", "isc", "unbound", "insight"} {
			for _, page := range opnsensePagesFor(kind, key) {
				if !strings.HasPrefix(page.Path, "/ui/") {
					t.Errorf("%s/%s cites %q, which is not an OPNsense interface path", kind, key, page.Path)
				}
			}
		}
	}
}

// TestOneSelectionHasOneNameOnTheSourcesSurface: the operator's "on" reads the same in
// the providers table and in the outage list.
func TestOneSelectionHasOneNameOnTheSourcesSurface(t *testing.T) {
	t.Parallel()
	on := catalogueText(t, msgSelectionOn)
	if !strings.HasPrefix(catalogueText(t, msgOutageProvider), on) {
		t.Errorf("the outage list names a provider turned on %q, the table %q",
			catalogueText(t, msgOutageProvider), on)
	}
}

// TestTheDetailIsNeverAColumnBeyondThePage: the probe and the detail are on a line of
// their own under their row, spanning every column, so the detail cannot be the column
// a wide table pushes past the edge of its frame; and an instant never breaks inside
// itself.
func TestTheDetailIsNeverAColumnBeyondThePage(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	h.recordGap("public_suffix", "public_suffix_list", store.GapDownloadFailed,
		h.clock.Now().Unix()-120, h.clock.Now().Unix()-60)
	page := pageSource(t, h, PathSources)
	providers := page[strings.Index(page, `aria-labelledby="sources_providers"`):]
	header := providers[strings.Index(providers, "<thead>"):strings.Index(providers, "</thead>")]
	if columns := strings.Count(header, "<th "); !strings.Contains(providers, `<td colspan="`+
		strconv.Itoa(columns)+`">`) || strings.Contains(header, catalogueText(t, "sources.column.detail")) {
		t.Error("the providers' detail is a column rather than a line spanning the row")
	}
	gaps := gapsCard(t, page)
	header = gaps[strings.Index(gaps, "<thead>"):strings.Index(gaps, "</thead>")]
	if columns := strings.Count(header, "<th "); !strings.Contains(gaps, `<td colspan="`+
		strconv.Itoa(columns)+`">`) {
		t.Error("a gap's detail is a column rather than a line spanning the row")
	}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	if !strings.Contains(stripCSSComments(string(stylesheet)), "table.data .figure {\n  white-space: nowrap;\n}") {
		t.Error("an instant in a table may break inside itself")
	}
}
