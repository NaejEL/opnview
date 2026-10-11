package surfaces_test

import (
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var datumSpan = regexp.MustCompile(`<span class="datum" translate="no">[^<]*</span>`)

// withoutData removes every marked datum from a page, and nothing else. The element
// carries no markup inside it, so a catalogue lookup cannot hide in one: a template
// that put a lookup inside it would put a tag or a text node the pattern does not
// match, and the text would stay and have to be a catalogue string like any other.
func withoutData(document string) string {
	return datumSpan.ReplaceAllString(document, "")
}

// bundlePages renders this cycle's surfaces in every state.
func bundlePages(t *testing.T) []webtest.RenderedPage {
	t.Helper()
	var pages []webtest.RenderedPage
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := webtest.StatusAndBody(t, answer)
		pages = append(pages, webtest.RenderedPage{Name: name, Document: body})
	}
	addFollowed := func(name string, h *webtest.Harness, answer *http.Response) {
		t.Helper()
		_, body := webtest.Followed(t, h, answer)
		pages = append(pages, webtest.RenderedPage{Name: name, Document: body})
	}

	// A fresh installation: no interface, every source not yet probed, no gap, no
	// outage, the list never refreshed.
	fresh := webtest.SignedInHarness(t)
	pages = append(pages,
		webtest.RenderedPage{Name: "the Interfaces surface with no interface", Document: webtest.PageSource(t, fresh, webtest.PathInterfaces)},
		webtest.RenderedPage{Name: "the Sources surface on a fresh installation", Document: webtest.PageSource(t, fresh, webtest.PathSources)},
		webtest.RenderedPage{Name: "the settings surface with the list never refreshed", Document: webtest.PageSource(t, fresh, web.PathSettings)})

	// Interfaces in every state.
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(3)
	id := network.Inside[0]
	saved := func(name string, form url.Values, interfaceID int64) {
		t.Helper()
		pages = append(pages, webtest.RenderedPage{Name: name, Document: h.SavedEdit(form, interfaceID)})
	}
	saved("the Interfaces surface after a network confirmed",
		webtest.NetworkForm(webtest.ActionConfirmNetwork, id, webtest.ExampleNetworkV4(0).String()), id)
	saved("the Interfaces surface after a network removed",
		webtest.NetworkForm(webtest.ActionRemoveNetwork, id, webtest.ExampleNetworkV6(0).String()), id)
	saved("the Interfaces surface after an overlapping network added",
		webtest.NetworkForm(webtest.ActionAddNetwork, network.Inside[1], webtest.ExampleWideV4().String()), network.Inside[1])
	// A detection discovery no longer reports: the third interface's first two.
	if err := h.Store.DetectNetworks(t.Context(), network.Inside[2],
		[]netip.Prefix{webtest.ExampleNetworkV4(9)}, h.Clock.Now().Unix()+60); err != nil {
		t.Fatalf("recording a later detection: %v", err)
	}
	pages = append(pages, webtest.RenderedPage{Name: "the Interfaces surface with a stale detection", Document: webtest.PageSource(t, h, webtest.PathInterfaces)})
	var linkLocal [16]byte
	linkLocal[0], linkLocal[1] = 0xfe, 0x80
	mapped := netip.AddrFrom16(netip.AddrFrom4([4]byte{198, 51, 100, 0}).As16())
	for name, typed := range map[string]string{
		"malformed":     "not a network",
		"host bits set": netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 1}), 24).String(),
		"IPv4-mapped":   netip.PrefixFrom(mapped, 120).String(),
		"link-local":    netip.PrefixFrom(netip.AddrFrom16(linkLocal), 64).String(),
	} {
		add("the Interfaces surface refusing a "+name+" network", h.Post(webtest.PathInterfaces,
			webtest.NetworkForm(webtest.ActionAddNetwork, id, typed)))
	}
	add("the Interfaces surface refusing an upstream interface", h.Post(webtest.PathInterfaces,
		webtest.NetworkForm(webtest.ActionAddNetwork, network.Upstream, webtest.ExampleOperatorV4(1).String())))
	add("the Interfaces surface refusing an unknown interface", h.Post(webtest.PathInterfaces,
		webtest.NetworkForm(webtest.ActionAddNetwork, network.Upstream+network.Inside[2]+100, webtest.ExampleOperatorV4(1).String())))
	add("the Interfaces surface refusing a network it does not carry", h.Post(webtest.PathInterfaces,
		webtest.NetworkForm(webtest.ActionRemoveNetwork, id, webtest.ExampleOperatorV4(2).String())))
	add("the Interfaces surface refusing an unknown edit", h.Post(webtest.PathInterfaces,
		webtest.NetworkForm("rename", id, webtest.ExampleOperatorV4(2).String())))
	add("the Interfaces surface refusing a link-local rule that is neither", h.Post(webtest.PathInterfaces,
		url.Values{webtest.FieldNetworkAction: {webtest.ActionLinkLocal}, webtest.FieldInterfaceID: {strconv.FormatInt(id, 10)},
			webtest.FieldLinkLocal: {"maybe"}}))
	saved("the Interfaces surface after the link-local rule set",
		url.Values{webtest.FieldNetworkAction: {webtest.ActionLinkLocal}, webtest.FieldInterfaceID: {strconv.FormatInt(id, 10)},
			webtest.FieldLinkLocal: {webtest.LinkLocalYes}}, id)
	if _, err := h.Store.UpsertInterface(t.Context(), store.Interface{
		Identifier: "example_bare", Device: "exbare0", Description: "bare.example",
		LinkType: "example-link", LinkKind: "other",
	}, h.Clock.Now().Unix()); err != nil {
		t.Fatalf("writing an interface: %v", err)
	}
	pages = append(pages, webtest.RenderedPage{Name: "the Interfaces surface with a networkless interface", Document: webtest.PageSource(t, h, webtest.PathInterfaces)})

	// Sources in every condition, with gaps of every reason and outages.
	s := webtest.SignedInHarness(t)
	detail401, detail403 := "HTTP 401 from the example endpoint", "HTTP 403 from the example endpoint"
	s.SetAvailabilityDetail("firewall_log", "pf", store.StateReachable, "GET /example/log", nil)
	s.SetAvailabilityDetail("security_event", "suricata", store.StatePresentButDisabled, "GET /example/ids", nil)
	s.SetAvailabilityDetail("dhcp_lease", "kea", store.StateUnavailable, "GET /example/kea", &detail401)
	s.SetAvailabilityDetail("dhcp_lease", "isc", store.StateUnavailable, "GET /example/isc", &detail403)
	s.DropAvailability("dhcp_lease", "dnsmasq")
	// A provider turned on and failing beside a healthy implementation of its kind.
	s.SetAvailabilityDetail("dns_lookup", "unbound", store.StateReachable, "GET /example/dns", nil)
	s.SetAvailabilityDetail("dns_lookup", "dnsmasq", store.StateUnavailable, "GET /example/dnsmasq", nil)
	s.SetSelection("dns_lookup", "dnsmasq", config.SelectionOn)
	now := s.Clock.Now().Unix()
	for index, reason := range webtest.GapReasonsOfSchema(t, s) {
		start := now - int64(600*(index+1))
		// Gaps of every length a duration is spelled in: one of each unit.
		s.RecordGap("registrable_domain", "public_suffix_list", store.GapReason(reason), start-90061, start)
	}
	pages = append(pages, webtest.RenderedPage{Name: "the Sources surface in every condition", Document: webtest.PageSource(t, s, webtest.PathSources)})

	// Every provider installed and not collecting that an OPNsense page turns on, with
	// a firewall URL configured, so each page's link is drawn.
	l := webtest.SignedInHarness(t)
	if err := l.Store.SetSetting(t.Context(), config.KeyOPNsenseBaseURL, webtest.ReviewFirewallURL,
		l.Clock.Now().Unix()); err != nil {
		t.Fatalf("storing the firewall URL: %v", err)
	}
	for _, row := range [][2]string{{"security_event", "suricata"}, {"dhcp_lease", "kea"},
		{"dhcp_lease", "dnsmasq"}, {"dns_lookup", "unbound"}, {"resolver_cache", "unbound"},
		{"measurement_sample", "insight"}} {
		l.SetAvailabilityDetail(row[0], row[1], store.StatePresentButDisabled, "GET /example/status", nil)
	}
	pages = append(pages, webtest.RenderedPage{Name: "the Sources surface with installed sources not collecting", Document: webtest.PageSource(t, l, webtest.PathSources)})

	// The collection surface's settings cards, saved, refused and confirming.
	c := webtest.SignedInHarness(t)
	addFollowed("the collection surface with a resolver interval saved", c, c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {"90"}}))
	add("the collection surface with a resolver interval refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {"0"}}))
	add("the collection surface with a resolver selection refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"resolver_local_data"}, webtest.CardFieldName("resolver_local_data", webtest.FieldInterval): {"300"},
			webtest.CardFieldName("resolver_local_data", webtest.FieldSelectionPrefix+"unbound"): {"maybe"}}))
	add("the collection surface with an unknown settings card", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"no_such_card"}}))
	add("the collection surface with the pairing window refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"pairing"}, config.KeyLegPairingWindow: {"-1"}}))
	add("the collection surface with the lease limit refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"lease_backends"}, config.KeyLeaseBackendFailurePassLimit: {"0"}}))
	add("the collection surface with the site-name mode refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"7776000"},
			config.KeyAggregateMode: {"everything"}}))
	add("the collection surface with a shorter retention to confirm", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"86400"},
			config.KeyAggregateMode: {"no_domains"}}))
	addFollowed("the collection surface with the attribution saved", c, c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			config.KeyCacheAnswerDelay: {"1800"}}))
	add("the collection surface with a retention of every unit to confirm", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"90061"},
			config.KeyAggregateMode: {"full"}}))
	add("the collection surface with a negative retention refused", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"-1"},
			config.KeyAggregateMode: {"full"}}))
	add("the collection surface with a unit that does not exist", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			web.UnitFieldName(config.KeyAttributionMaxDelay): {"fortnights"}, config.KeyCacheAnswerDelay: {"1800"}}))
	add("the collection surface with an edit kept beside a save", c.Post(webtest.PathCollectionSettings,
		url.Values{webtest.FieldCard: {"attribution"}, config.KeyAttributionMaxDelay: {"7"},
			config.KeyCacheAnswerDelay: {"1800"}, config.KeyLeaseBackendFailurePassLimit: {"9"}}))
	add("the collection surface with a submission naming no card", c.Post(webtest.PathCollectionSettings,
		url.Values{config.KeyLeaseBackendFailurePassLimit: {"9"}}))

	// The Public Suffix List card in every outcome, with a copy held and without.
	p := webtest.SignedInHarness(t)
	for _, probe := range []string{registrabledomain.ProbeDownloaded, registrabledomain.ProbeNotModified,
		registrabledomain.ProbeFailed, registrabledomain.ProbeMalformed} {
		p.SetPublicSuffixProbe(store.StateReachable, probe, nil)
		pages = append(pages, webtest.RenderedPage{Name: "the settings surface after a " + probe + " refresh", Document: webtest.PageSource(t, p, web.PathSettings)})
		if probe == registrabledomain.ProbeNotModified {
			p.Suffix.Hold("2026-10-09_08-15-00_UTC", 9876, now-600)
		}
	}
	p.Suffix.Hold("", 9876, now-600)
	pages = append(pages, webtest.RenderedPage{Name: "the settings surface with a copy stating no version", Document: webtest.PageSource(t, p, web.PathSettings)})
	add("the settings surface with a submission naming no card", p.Post(web.PathSettings,
		url.Values{"theme": {string(web.ThemeSystem)}, webtest.FieldPublicSuffixInterval: {"daily"}}))
	addFollowed("the settings surface with the appearance card saved", p, p.Post(web.PathSettings,
		url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardAppearance}, "theme": {string(web.ThemeSystem)}}))
	add("the settings surface refusing a card that does not exist", p.Post(web.PathSettings,
		url.Values{webtest.FieldSettingsCard: {"no_such_card"}}))
	add("the settings surface with the refresh interval refused in its card", p.Post(web.PathSettings,
		url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix}, webtest.FieldPublicSuffixInterval: {"daily"}}))

	// The indicator whose read failed: its own named state, on a page still drawn.
	u := webtest.SignedInHarness(t)
	if _, err := u.Store.DB().Exec("DROP TABLE source_availability"); err != nil {
		t.Fatalf("dropping the availability table: %v", err)
	}
	pages = append(pages, webtest.RenderedPage{Name: "the Interfaces surface with the indicator unreadable", Document: webtest.PageSource(t, u, webtest.PathInterfaces)})
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
	sources, err := web.TemplateSources()
	if err != nil {
		t.Fatalf("listing the templates: %v", err)
	}
	found := 0
	for name, raw := range sources {
		// The opening tag without its closing one would escape the pattern; count both.
		if opened, matched := strings.Count(raw, `<span class="datum"`),
			len(datumInTemplate.FindAllString(raw, -1)); opened != matched {
			t.Errorf("%s opens %d datum elements and closes %d on their own line", name,
				opened, matched)
		}
		for _, match := range datumInTemplate.FindAllStringSubmatch(raw, -1) {
			found++
			if !singleFieldAction.MatchString(match[1]) {
				t.Errorf("%s has a datum holding %q, which is not one field", name, match[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("no template carries a datum, so this assertion is vacuous")
	}
}
