package web

import (
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/publicsuffix"
	"github.com/NaejEL/opnview/internal/store"
)

// The Interfaces and Sources surfaces, the collection surface's settings cards and
// the Public Suffix List card, rendered in every state these tests can reach, so the
// catalogue's assertions -- no literal, no explanatory copy, no off-host load, every
// entry rendered by a state -- run against each.
//
// PROVENANCE: SYNTHESISED, as interfaces_test.go and sources_test.go describe.

// datumSpan is the element a template marks a datum with: a value read from the
// database -- an interface's description, a network, a probe, a collector's detail,
// a list's version -- which is data and not a catalogue string.
// reviewFirewallURL is the firewall URL the fixtures configure where a page draws links to
// the firewall's own pages: an RFC 2606 reserved name, contacted by nothing.
const reviewFirewallURL = "https://firewall.example.net"

var datumSpan = regexp.MustCompile(`<span class="datum" translate="no">[^<]*</span>`)

// withoutData removes every marked datum from a page, and nothing else. The element
// carries no markup inside it, so a catalogue lookup cannot hide in one: a template
// that put a lookup inside it would put a tag or a text node the pattern does not
// match, and the text would stay and have to be a catalogue string like any other.
func withoutData(document string) string {
	return datumSpan.ReplaceAllString(document, "")
}

// bundlePages renders this cycle's surfaces in every state.
func bundlePages(t *testing.T) []renderedPage {
	t.Helper()
	var pages []renderedPage
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := statusAndBody(t, answer)
		pages = append(pages, renderedPage{name, body})
	}
	addFollowed := func(name string, h *harness, answer *http.Response) {
		t.Helper()
		_, body := followed(t, h, answer)
		pages = append(pages, renderedPage{name, body})
	}

	// A fresh installation: no interface, every source not yet probed, no gap, no
	// outage, the list never refreshed.
	fresh := signedInHarness(t)
	pages = append(pages,
		renderedPage{"the Interfaces surface with no interface", pageSource(t, fresh, PathInterfaces)},
		renderedPage{"the Sources surface on a fresh installation", pageSource(t, fresh, PathSources)},
		renderedPage{"the settings surface with the list never refreshed", pageSource(t, fresh, PathSettings)})

	// Interfaces in every state.
	h := signedInHarness(t)
	network := h.seedBundleNetwork(3)
	id := network.inside[0]
	saved := func(name string, form url.Values, interfaceID int64) {
		t.Helper()
		pages = append(pages, renderedPage{name, h.savedEdit(form, interfaceID)})
	}
	saved("the Interfaces surface after a network confirmed",
		networkForm(actionConfirmNetwork, id, exampleNetworkV4(0).String()), id)
	saved("the Interfaces surface after a network removed",
		networkForm(actionRemoveNetwork, id, exampleNetworkV6(0).String()), id)
	saved("the Interfaces surface after an overlapping network added",
		networkForm(actionAddNetwork, network.inside[1], exampleWideV4().String()), network.inside[1])
	// A detection discovery no longer reports: the third interface's first two.
	if err := h.store.DetectNetworks(t.Context(), network.inside[2],
		[]netip.Prefix{exampleNetworkV4(9)}, h.clock.Now().Unix()+60); err != nil {
		t.Fatalf("recording a later detection: %v", err)
	}
	pages = append(pages, renderedPage{"the Interfaces surface with a stale detection",
		pageSource(t, h, PathInterfaces)})
	var linkLocal [16]byte
	linkLocal[0], linkLocal[1] = 0xfe, 0x80
	mapped := netip.AddrFrom16(netip.AddrFrom4([4]byte{198, 51, 100, 0}).As16())
	for name, typed := range map[string]string{
		"malformed":     "not a network",
		"host bits set": netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 1}), 24).String(),
		"IPv4-mapped":   netip.PrefixFrom(mapped, 120).String(),
		"link-local":    netip.PrefixFrom(netip.AddrFrom16(linkLocal), 64).String(),
	} {
		add("the Interfaces surface refusing a "+name+" network", h.post(PathInterfaces,
			networkForm(actionAddNetwork, id, typed)))
	}
	add("the Interfaces surface refusing an upstream interface", h.post(PathInterfaces,
		networkForm(actionAddNetwork, network.upstream, exampleOperatorV4(1).String())))
	add("the Interfaces surface refusing an unknown interface", h.post(PathInterfaces,
		networkForm(actionAddNetwork, network.upstream+network.inside[2]+100, exampleOperatorV4(1).String())))
	add("the Interfaces surface refusing a network it does not carry", h.post(PathInterfaces,
		networkForm(actionRemoveNetwork, id, exampleOperatorV4(2).String())))
	add("the Interfaces surface refusing an unknown edit", h.post(PathInterfaces,
		networkForm("rename", id, exampleOperatorV4(2).String())))
	add("the Interfaces surface refusing a link-local rule that is neither", h.post(PathInterfaces,
		url.Values{fieldNetworkAction: {actionLinkLocal}, fieldInterfaceID: {strconv.FormatInt(id, 10)},
			fieldLinkLocal: {"maybe"}}))
	saved("the Interfaces surface after the link-local rule set",
		url.Values{fieldNetworkAction: {actionLinkLocal}, fieldInterfaceID: {strconv.FormatInt(id, 10)},
			fieldLinkLocal: {linkLocalYes}}, id)
	if _, err := h.store.UpsertInterface(t.Context(), store.Interface{
		Identifier: "example_bare", Device: "exbare0", Description: "bare.example",
		LinkType: "example-link", LinkKind: "other",
	}, h.clock.Now().Unix()); err != nil {
		t.Fatalf("writing an interface: %v", err)
	}
	pages = append(pages, renderedPage{"the Interfaces surface with a networkless interface",
		pageSource(t, h, PathInterfaces)})

	// Sources in every condition, with gaps of every reason and outages.
	s := signedInHarness(t)
	detail401, detail403 := "HTTP 401 from the example endpoint", "HTTP 403 from the example endpoint"
	s.setAvailabilityDetail("firewall_log", "pf", store.StateReachable, "GET /example/log", nil)
	s.setAvailabilityDetail("security_event", "suricata", store.StatePresentButDisabled, "GET /example/ids", nil)
	s.setAvailabilityDetail("dhcp_lease", "kea", store.StateUnavailable, "GET /example/kea", &detail401)
	s.setAvailabilityDetail("dhcp_lease", "isc", store.StateUnavailable, "GET /example/isc", &detail403)
	s.dropAvailability("dhcp_lease", "dnsmasq")
	// A provider turned on and failing beside a healthy implementation of its kind.
	s.setAvailabilityDetail("dns_lookup", "unbound", store.StateReachable, "GET /example/dns", nil)
	s.setAvailabilityDetail("dns_lookup", "dnsmasq", store.StateUnavailable, "GET /example/dnsmasq", nil)
	s.setSelection("dns_lookup", "dnsmasq", config.SelectionOn)
	now := s.clock.Now().Unix()
	for index, reason := range gapReasonsOfSchema(t, s) {
		start := now - int64(600*(index+1))
		// Gaps of every length a duration is spelled in: one of each unit.
		s.recordGap("public_suffix", "public_suffix_list", store.GapReason(reason), start-90061, start)
	}
	pages = append(pages, renderedPage{"the Sources surface in every condition", pageSource(t, s, PathSources)})

	// Every provider installed and not collecting that an OPNsense page turns on, with
	// a firewall URL configured, so each page's link is drawn.
	l := signedInHarness(t)
	if err := l.store.SetSetting(t.Context(), config.KeyOPNsenseBaseURL, reviewFirewallURL,
		l.clock.Now().Unix()); err != nil {
		t.Fatalf("storing the firewall URL: %v", err)
	}
	for _, row := range [][2]string{{"security_event", "suricata"}, {"dhcp_lease", "kea"},
		{"dhcp_lease", "dnsmasq"}, {"dns_lookup", "unbound"}, {"resolver_cache", "unbound"},
		{"measurement_sample", "insight"}} {
		l.setAvailabilityDetail(row[0], row[1], store.StatePresentButDisabled, "GET /example/status", nil)
	}
	pages = append(pages, renderedPage{"the Sources surface with installed sources not collecting",
		pageSource(t, l, PathSources)})

	// The collection surface's settings cards, saved, refused and confirming.
	c := signedInHarness(t)
	addFollowed("the collection surface with a resolver interval saved", c, c.post(PathCollectionSettings,
		url.Values{fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {"90"}}))
	add("the collection surface with a resolver interval refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {"0"}}))
	add("the collection surface with a resolver selection refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"resolver_local_data"}, cardFieldName("resolver_local_data", fieldInterval): {"300"},
			cardFieldName("resolver_local_data", fieldSelectionPrefix+"unbound"): {"maybe"}}))
	add("the collection surface with an unknown settings card", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"no_such_card"}}))
	add("the collection surface with the pairing window refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"pairing"}, config.KeyLegPairingWindow: {"-1"}}))
	add("the collection surface with the lease limit refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"lease_backends"}, config.KeyLeaseBackendFailurePassLimit: {"0"}}))
	add("the collection surface with the site-name mode refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"7776000"},
			config.KeyAggregateMode: {"everything"}}))
	add("the collection surface with a shorter retention to confirm", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"86400"},
			config.KeyAggregateMode: {"no_domains"}}))
	addFollowed("the collection surface with the attribution saved", c, c.post(PathCollectionSettings,
		url.Values{fieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			config.KeyCacheAnswerDelay: {"1800"}}))
	add("the collection surface with a retention of every unit to confirm", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"90061"},
			config.KeyAggregateMode: {"full"}}))
	add("the collection surface with a negative retention refused", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"-1"},
			config.KeyAggregateMode: {"full"}}))
	add("the collection surface with a unit that does not exist", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			unitFieldName(config.KeyAttributionMaxDelay): {"fortnights"}, config.KeyCacheAnswerDelay: {"1800"}}))
	add("the collection surface with an edit kept beside a save", c.post(PathCollectionSettings,
		url.Values{fieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			config.KeyCacheAnswerDelay: {"1800"}, config.KeyLeaseBackendFailurePassLimit: {"9"}}))
	add("the collection surface with a submission naming no card", c.post(PathCollectionSettings,
		url.Values{config.KeyLeaseBackendFailurePassLimit: {"9"}}))

	// The Public Suffix List card in every outcome, with a copy held and without.
	p := signedInHarness(t)
	for _, probe := range []string{publicsuffix.ProbeDownloaded, publicsuffix.ProbeNotModified,
		publicsuffix.ProbeFailed, publicsuffix.ProbeMalformed} {
		p.setPublicSuffixProbe(store.StateReachable, probe, nil)
		pages = append(pages, renderedPage{"the settings surface after a " + probe + " refresh",
			pageSource(t, p, PathSettings)})
		if probe == publicsuffix.ProbeNotModified {
			p.suffix.hold("2026-10-09_08-15-00_UTC", 9876, now-600)
		}
	}
	p.suffix.hold("", 9876, now-600)
	pages = append(pages, renderedPage{"the settings surface with a copy stating no version",
		pageSource(t, p, PathSettings)})
	add("the settings surface with a submission naming no card", p.post(PathSettings,
		url.Values{"theme": {string(ThemeSystem)}, fieldPublicSuffixInterval: {"daily"}}))
	addFollowed("the settings surface with the appearance card saved", p, p.post(PathSettings,
		url.Values{fieldSettingsCard: {settingsCardAppearance}, "theme": {string(ThemeSystem)}}))
	add("the settings surface refusing a card that does not exist", p.post(PathSettings,
		url.Values{fieldSettingsCard: {"no_such_card"}}))
	add("the settings surface with the refresh interval refused in its card", p.post(PathSettings,
		url.Values{fieldSettingsCard: {settingsCardPublicSuffix}, fieldPublicSuffixInterval: {"daily"}}))

	// The indicator whose read failed: its own named state, on a page still drawn.
	u := signedInHarness(t)
	if _, err := u.store.DB().Exec("DROP TABLE source_availability"); err != nil {
		t.Fatalf("dropping the availability table: %v", err)
	}
	pages = append(pages, renderedPage{"the Interfaces surface with the indicator unreadable",
		pageSource(t, u, PathInterfaces)})
	return pages
}

// datumInTemplate is a datum element as a template writes it, and what it holds.
var datumInTemplate = regexp.MustCompile(`<span class="datum" translate="no">(.*?)</span>`)

// singleFieldAction is the one thing a datum element may hold in a template: one
// action printing one field or variable, and nothing else.
var singleFieldAction = regexp.MustCompile(`^\{\{[.$][A-Za-z0-9_.]*\}\}$`)

// TestEveryDatumHoldsOneFieldAndNothingElse: the catalogue tests strip every datum
// element before they look for literals, so a literal written inside one would pass
// them unseen. In every template, each datum element holds exactly one action that
// prints one field -- no text, no catalogue lookup, no second action.
func TestEveryDatumHoldsOneFieldAndNothingElse(t *testing.T) {
	t.Parallel()
	entries, err := files.ReadDir("template")
	if err != nil {
		t.Fatalf("listing the templates: %v", err)
	}
	found := 0
	for _, entry := range entries {
		raw, err := files.ReadFile("template/" + entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		// The opening tag without its closing one would escape the pattern; count both.
		if opened, matched := strings.Count(string(raw), `<span class="datum"`),
			len(datumInTemplate.FindAllString(string(raw), -1)); opened != matched {
			t.Errorf("%s opens %d datum elements and closes %d on their own line", entry.Name(),
				opened, matched)
		}
		for _, match := range datumInTemplate.FindAllStringSubmatch(string(raw), -1) {
			found++
			if !singleFieldAction.MatchString(match[1]) {
				t.Errorf("%s has a datum holding %q, which is not one field", entry.Name(), match[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("no template carries a datum, so this assertion is vacuous")
	}
}
