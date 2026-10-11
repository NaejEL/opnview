package interfaces_test

import (
	"context"
	"fmt"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/interfaces"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// interfaceCardOf returns one interface's card out of a page.
func interfaceCardOf(t *testing.T, page string, id int64) string {
	t.Helper()
	start := strings.Index(page, `<section class="card" id="interface_`+strconv.FormatInt(id, 10)+`"`)
	if start < 0 {
		t.Fatalf("the page has no card for interface %d", id)
	}
	end := strings.Index(page[start:], "</section>")
	if end < 0 {
		t.Fatalf("the card of interface %d is not closed", id)
	}
	return page[start : start+end]
}

// TestTheInterfacesRouteIsAuthenticatedAndLinkedFromEveryPage is AC1.
func TestTheInterfacesRouteIsAuthenticatedAndLinkedFromEveryPage(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)

	declared := map[string]web.Access{}
	for _, route := range h.Server.Routes() {
		declared[route.Method+" "+route.Pattern] = route.Access
	}
	for _, name := range []string{
		http.MethodGet + " " + webtest.PathInterfaces, http.MethodPost + " " + webtest.PathInterfaces,
		http.MethodGet + " " + webtest.PathSources, http.MethodPost + " " + webtest.PathCollectionSettings,
	} {
		if declared[name] != web.AccessAuthenticated {
			t.Errorf("%s is declared %q, want authenticated", name, declared[name])
		}
	}

	anonymous := webtest.FreshClient(t, h)
	for _, path := range []string{webtest.PathInterfaces, webtest.PathSources} {
		answer := anonymous.Get(path)
		status, _ := webtest.StatusAndBody(t, answer)
		if status != http.StatusSeeOther || answer.Header.Get("Location") != web.PathSignIn {
			t.Errorf("GET %s without a session answered %d to %q, want a redirect to sign-in",
				path, status, answer.Header.Get("Location"))
		}
	}

	for _, path := range []string{web.PathSettings, webtest.PathCollection, webtest.PathInterfaces, webtest.PathSources} {
		page := webtest.PageSource(t, h, path)
		for _, link := range []string{webtest.PathInterfaces, webtest.PathSources} {
			if !strings.Contains(page, `<a href="`+link+`"`) {
				t.Errorf("the top bar of %s does not link to %s", path, link)
			}
		}
	}
}

// TestEveryInterfaceRowHasOneCardShowingItsNetworks is AC2, at one inside interface
// and at four.
func TestEveryInterfaceRowHasOneCardShowingItsNetworks(t *testing.T) {
	t.Parallel()
	for _, insideCount := range []int{1, 4} {
		t.Run(strconv.Itoa(insideCount), func(t *testing.T) {
			t.Parallel()
			h := webtest.SignedInHarness(t)
			network := h.SeedBundleNetwork(insideCount)
			// One network removed and one stale detection, so every combination of the
			// three facts is drawn.
			ctx := context.Background()
			now := h.Clock.Now().Unix()
			if _, err := h.Store.RemoveInterfaceNetwork(ctx, network.Inside[0],
				webtest.ExampleNetworkV6(0).String(), now); err != nil {
				t.Fatalf("removing a network: %v", err)
			}
			page := webtest.PageSource(t, h, webtest.PathInterfaces)

			if got := strings.Count(page, `<section class="card" id="interface_`); got != insideCount+1 {
				t.Errorf("the page draws %d interface cards for %d interface rows", got, insideCount+1)
			}
			upstream := interfaceCardOf(t, page, network.Upstream)
			for _, control := range []string{`name="` + webtest.FieldNetwork + `"`, `name="` + webtest.FieldLinkLocal + `"`,
				`<table`} {
				if strings.Contains(upstream, control) {
					t.Errorf("the upstream card carries %s", control)
				}
			}
			if !strings.Contains(upstream, "<dd>"+webtest.CatalogueText(t, msgYes)+"</dd>") {
				t.Error("the upstream card does not say it is upstream")
			}

			networks, err := h.Store.InterfaceNetworks(ctx)
			if err != nil {
				t.Fatalf("reading the networks: %v", err)
			}
			for _, id := range network.Inside {
				card := interfaceCardOf(t, page, id)
				drawn := 0
				for _, stored := range networks {
					if stored.InterfaceID != id {
						continue
					}
					drawn++
					counts := webtest.CatalogueText(t, webtest.YesNo(stored.Counts))
					if reason := interfaces.NotCountingReason(stored); reason != "" {
						counts += `<span class="counts-reason">` + webtest.CatalogueText(t, reason) + "</span>"
					}
					row := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
						`><span class="datum" translate="no">` + stored.Network.String() + "</span></td>\n" +
						"<td" + dataLabel(t, "interfaces.column.origin") + ">" +
						webtest.CatalogueText(t, interfaces.OriginKey(stored.Origin)) + "</td>\n" +
						"<td" + dataLabel(t, "interfaces.column.removed") + ">" +
						webtest.CatalogueText(t, webtest.YesNo(stored.Removed)) + "</td>\n" +
						"<td" + dataLabel(t, "interfaces.column.counts") + ">" + counts + "</td>"
					if !strings.Contains(card, row) {
						t.Errorf("interface %d does not draw %s with its origin, removal and count", id, stored)
					}
				}
				if drawn != 2 {
					t.Fatalf("interface %d holds %d networks, want one of each family", id, drawn)
				}
			}
		})
	}
}

// TestConfirmRemoveAndRestoreThroughThePage is AC3.
func TestConfirmRemoveAndRestoreThroughThePage(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(2)
	id, prefix := network.Inside[1], webtest.ExampleNetworkV4(1)

	page := interfaceCardOf(t, webtest.PageSource(t, h, webtest.PathInterfaces), id)
	if !strings.Contains(page, `value="`+webtest.ActionConfirmNetwork+`"`) {
		t.Fatal("a detected network carries no confirm control")
	}

	before := h.Fingerprint()
	body := h.SavedEdit(webtest.NetworkForm(webtest.ActionConfirmNetwork, id, prefix.String()), id)
	if got := h.NetworkOf(id, prefix); got.Origin != store.NetworkOriginOperator || !got.Counts {
		t.Errorf("a confirmed network is %+v, want the operator's and counting", got)
	}
	if !strings.Contains(interfaceCardOf(t, body, id), webtest.CatalogueText(t, msgInterfaceSaved)) {
		t.Error("the confirmation is not drawn in the card it was made in")
	}
	afterConfirm := h.Fingerprint()
	if afterConfirm == before {
		t.Error("confirming did not change the on-link fingerprint")
	}

	body = h.SavedEdit(webtest.NetworkForm(webtest.ActionRemoveNetwork, id, prefix.String()), id)
	if got := h.NetworkOf(id, prefix); !got.Removed || got.Counts {
		t.Errorf("a removed network is %+v, want removed and not counting", got)
	}
	card := interfaceCardOf(t, body, id)
	if !strings.Contains(card, prefix.String()) || !strings.Contains(card, `value="`+webtest.ActionRestoreNetwork+`"`) {
		t.Error("a removed network is not listed with a restore control")
	}
	afterRemove := h.Fingerprint()
	if afterRemove == afterConfirm {
		t.Error("removing did not change the on-link fingerprint")
	}

	_ = h.SavedEdit(webtest.NetworkForm(webtest.ActionRestoreNetwork, id, prefix.String()), id)
	if got := h.NetworkOf(id, prefix); got.Removed || !got.Counts {
		t.Errorf("a restored network is %+v, want counting again", got)
	}
	if h.Fingerprint() == afterRemove {
		t.Error("restoring did not change the on-link fingerprint")
	}

	// A network the interface does not carry is refused at the page, and nothing moves.
	status, body := webtest.StatusAndBody(t, h.Post(webtest.PathInterfaces,
		webtest.NetworkForm(webtest.ActionRemoveNetwork, id, webtest.ExampleOperatorV4(9).String())))
	if status != http.StatusBadRequest || !strings.Contains(body, webtest.CatalogueText(t, msgNetworkUnknown)) {
		t.Errorf("removing a network the interface does not carry answered %d", status)
	}
}

// TestAddingANetworkAndEachRefusal is AC4.
func TestAddingANetworkAndEachRefusal(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(1)
	id := network.Inside[0]

	for _, prefix := range []netip.Prefix{webtest.ExampleOperatorV4(0), webtest.ExampleNetworkV6(7)} {
		_ = h.SavedEdit(webtest.NetworkForm(webtest.ActionAddNetwork, id, prefix.String()), id)
		if got := h.NetworkOf(id, prefix); got.Origin != store.NetworkOriginOperator || !got.Counts {
			t.Errorf("an added network is %+v, want the operator's and counting", got)
		}
	}

	var linkLocal [16]byte
	linkLocal[0], linkLocal[1] = 0xfe, 0x80
	mapped := netip.AddrFrom16(netip.AddrFrom4([4]byte{198, 51, 100, 0}).As16())
	for _, refused := range []struct {
		name    string
		typed   string
		refusal messageKey
	}{
		{"malformed", "not a network", msgNetworkMalformed},
		{"host bits set", netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 1}), 24).String(),
			msgNetworkHostBits},
		{"IPv4-mapped", netip.PrefixFrom(mapped, 120).String(), msgNetworkIPv4Mapped},
		{"link-local", netip.PrefixFrom(netip.AddrFrom16(linkLocal), 64).String(), msgNetworkLinkLocal},
	} {
		t.Run(refused.name, func(t *testing.T) {
			rows := h.NetworkRowCount()
			status, body := webtest.StatusAndBody(t, h.Post(webtest.PathInterfaces, webtest.NetworkForm(webtest.ActionAddNetwork, id, refused.typed)))
			if status != http.StatusBadRequest {
				t.Errorf("answered %d, want 400", status)
			}
			if h.NetworkRowCount() != rows {
				t.Error("a refused network was stored")
			}
			card := interfaceCardOf(t, body, id)
			field := card[strings.Index(card, `<input id="network_`):]
			field = field[:strings.Index(field, ">")]
			describedBy := "network_refusal_" + strconv.FormatInt(id, 10)
			if !strings.Contains(field, `aria-invalid="true"`) ||
				!strings.Contains(field, `aria-describedby="`+describedBy+`"`) {
				t.Error("the add field is not marked as the cause of the refusal")
			}
			if webtest.FieldValue(card, "network_"+strconv.FormatInt(id, 10)) != refused.typed {
				t.Error("the typed text was not kept")
			}
			if !strings.Contains(card, `id="`+describedBy+`">`+webtest.CatalogueText(t, refused.refusal)+"</p>") {
				t.Errorf("the refusal is not the %s sentence", refused.refusal)
			}
		})
	}

	// The two refusals that are not about what was typed: there is no field to draw
	// them against, so the page says them, at 400, with nothing stored.
	for _, refused := range []struct {
		name    string
		id      int64
		refusal messageKey
	}{
		{"upstream", network.Upstream, msgInterfaceUpstream},
		{"unknown", network.Inside[0] + network.Upstream + 1000, msgInterfaceUnknown},
	} {
		rows := h.NetworkRowCount()
		status, body := webtest.StatusAndBody(t, h.Post(webtest.PathInterfaces,
			webtest.NetworkForm(webtest.ActionAddNetwork, refused.id, webtest.ExampleOperatorV4(3).String())))
		if status != http.StatusBadRequest {
			t.Errorf("an add to the %s interface answered %d", refused.name, status)
		}
		if h.NetworkRowCount() != rows {
			t.Errorf("an add to the %s interface stored a network", refused.name)
		}
		if !strings.Contains(body, `<p class="notice bad">`+webtest.CatalogueText(t, refused.refusal)+"</p>") {
			t.Errorf("the %s refusal is not its own sentence", refused.name)
		}
	}
}

// TestAnOverlapIsStoredAndNamed is AC5.
func TestAnOverlapIsStoredAndNamed(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(3)
	wide := webtest.ExampleWideV4()

	body := h.SavedEdit(webtest.NetworkForm(webtest.ActionAddNetwork, network.Inside[2], wide.String()), network.Inside[2])
	if got := h.NetworkOf(network.Inside[2], wide); !got.Counts {
		t.Error("an overlapping network was not stored as counting")
	}
	// The overlap is a state of the card, read on every draw: on the page the save
	// lands on, and on a plain load of the page afterwards.
	for name, page := range map[string]string{"after the save": body,
		"on a later load": webtest.PageSource(t, h, webtest.PathInterfaces)} {
		card := interfaceCardOf(t, page, network.Inside[2])
		if !strings.Contains(card, webtest.CatalogueText(t, "interfaces.heading.overlaps")) {
			t.Fatalf("the card does not report the overlaps %s", name)
		}
		for index := 0; index < 2; index++ {
			overlap := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
				`><span class="datum" translate="no">` + wide.String() + `</span></td>` + "\n" +
				`<td class="network"` + dataLabel(t, "interfaces.column.overlapping") +
				`><span class="datum" translate="no">` + webtest.ExampleNetworkV4(index).String() + `</span></td>` + "\n" +
				`<td` + dataLabel(t, "interfaces.column.interface") + `><span class="datum" translate="no">` +
				fmt.Sprintf("inside-%d.example", index) +
				`</span></td>`
			if !strings.Contains(card, overlap) {
				t.Errorf("the overlap with interface %d is not named with its network and its interface %s",
					index, name)
			}
			// And the other interface's card names it too.
			other := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
				`><span class="datum" translate="no">` + webtest.ExampleNetworkV4(index).String() + `</span></td>` + "\n" +
				`<td class="network"` + dataLabel(t, "interfaces.column.overlapping") +
				`><span class="datum" translate="no">` + wide.String() + `</span></td>`
			if !strings.Contains(interfaceCardOf(t, page, network.Inside[index]), other) {
				t.Errorf("interface %d's card does not name the overlap %s", index, name)
			}
		}
	}

	// Resolved -- the wide network removed -- the overlap is gone from every card.
	_ = h.SavedEdit(webtest.NetworkForm(webtest.ActionRemoveNetwork, network.Inside[2], wide.String()), network.Inside[2])
	if strings.Contains(webtest.PageSource(t, h, webtest.PathInterfaces), webtest.CatalogueText(t, "interfaces.heading.overlaps")) {
		t.Error("a resolved overlap is still reported")
	}
}

// TestTheLinkLocalRule is AC6.
func TestTheLinkLocalRule(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(2)
	id := network.Inside[1]
	field := "link_local_" + strconv.FormatInt(id, 10)

	if got := webtest.SelectedIn(t, interfaceCardOf(t, webtest.PageSource(t, h, webtest.PathInterfaces), id), field); got != webtest.LinkLocalNo {
		t.Errorf("a fresh interface's link-local rule shows %q, want no", got)
	}
	before := h.Fingerprint()
	form := url.Values{webtest.FieldNetworkAction: {webtest.ActionLinkLocal}, webtest.FieldInterfaceID: {strconv.FormatInt(id, 10)},
		webtest.FieldLinkLocal: {webtest.LinkLocalYes}}
	body := h.SavedEdit(form, id)
	if webtest.SelectedIn(t, interfaceCardOf(t, body, id), field) != webtest.LinkLocalYes {
		t.Error("the card does not show the rule now stored")
	}
	if h.Fingerprint() == before {
		t.Error("setting the rule did not change the on-link fingerprint")
	}
	rows, err := h.Store.Interfaces(context.Background())
	if err != nil {
		t.Fatalf("reading the interfaces: %v", err)
	}
	for _, row := range rows {
		if row.LinkLocalEvidence != (row.ID == id) {
			t.Errorf("interface %d carries link_local_evidence %t", row.ID, row.LinkLocalEvidence)
		}
	}

	// A value that is neither, and the rule on an upstream interface, are refused.
	form.Set(webtest.FieldLinkLocal, "maybe")
	if status, _ := webtest.StatusAndBody(t, h.Post(webtest.PathInterfaces, form)); status != http.StatusBadRequest {
		t.Errorf("a rule that is neither yes nor no answered %d", status)
	}
	form.Set(webtest.FieldLinkLocal, webtest.LinkLocalYes)
	form.Set(webtest.FieldInterfaceID, strconv.FormatInt(network.Upstream, 10))
	if status, _ := webtest.StatusAndBody(t, h.Post(webtest.PathInterfaces, form)); status != http.StatusBadRequest {
		t.Errorf("the rule on an upstream interface answered %d", status)
	}
}

// TestAnInterfaceWithNoNetworkIsANamedState is AC7, and the page with no interface at
// all.
func TestAnInterfaceWithNoNetworkIsANamedState(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	if !strings.Contains(webtest.PageSource(t, h, webtest.PathInterfaces), webtest.CatalogueText(t, "state.interface.none")) {
		t.Error("a page with no interface does not say so")
	}
	id, err := h.Store.UpsertInterface(context.Background(), store.Interface{
		Identifier: "example_bare", Device: "exbare0", Description: "bare.example",
		LinkType: "example-link", LinkKind: "other",
	}, h.Clock.Now().Unix())
	if err != nil {
		t.Fatalf("writing an interface: %v", err)
	}
	card := interfaceCardOf(t, webtest.PageSource(t, h, webtest.PathInterfaces), id)
	if !strings.Contains(card, `<p class="empty-state">`+webtest.CatalogueText(t, "state.interface.no_network")+"</p>") {
		t.Error("an interface with no network does not name that state")
	}
	if strings.Contains(card, "<table") {
		t.Error("an interface with no network draws an empty table")
	}
}

// TestEveryNewMutatingRouteRefusesAMissingFormToken is AC8, one route at a time.
func TestEveryNewMutatingRouteRefusesAMissingFormToken(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(1)

	rows := h.NetworkRowCount()
	_ = h.FormToken(webtest.PathInterfaces)
	for _, action := range []string{webtest.ActionAddNetwork, webtest.ActionConfirmNetwork, webtest.ActionRemoveNetwork,
		webtest.ActionRestoreNetwork} {
		webtest.AssertStatus(t, h.PostRaw(webtest.PathInterfaces,
			webtest.NetworkForm(action, network.Inside[0], webtest.ExampleNetworkV4(0).String())), http.StatusForbidden)
	}
	webtest.AssertStatus(t, h.PostRaw(webtest.PathInterfaces, url.Values{webtest.FieldNetworkAction: {webtest.ActionLinkLocal},
		webtest.FieldInterfaceID: {strconv.FormatInt(network.Inside[0], 10)}, webtest.FieldLinkLocal: {webtest.LinkLocalYes}}),
		http.StatusForbidden)
	if h.NetworkRowCount() != rows || h.NetworkOf(network.Inside[0], webtest.ExampleNetworkV4(0)).Origin !=
		store.NetworkOriginDetected {
		t.Error("a submission refused for its form token changed a network")
	}

	_ = h.FormToken(webtest.PathCollection)
	webtest.AssertStatus(t, h.PostRaw(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_cache"}, webtest.FieldInterval: {"90"},
	}), http.StatusForbidden)
	webtest.AssertStatus(t, h.PostRaw(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"pairing"}, "leg_pairing_window_seconds": {"3"},
	}), http.StatusForbidden)
	for _, key := range []string{"poll_interval_resolver_cache_seconds"} {
		if _, present, err := h.Store.Setting(context.Background(), key); err != nil || present {
			t.Errorf("a submission refused for its form token stored %s", key)
		}
	}
	if got := h.Setting("leg_pairing_window_seconds"); got != "1" {
		t.Errorf("a submission refused for its form token stored the pairing window %q", got)
	}
}

// TestASavedEditReturnsToItsCard: every control posts to its card's anchor, a saved
// edit redirects there with the card named, and only that card says it was saved.
func TestASavedEditReturnsToItsCard(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(3)
	id := network.Inside[1]
	page := webtest.PageSource(t, h, webtest.PathInterfaces)
	card := interfaceCardOf(t, page, id)
	action := `action="` + webtest.PathInterfaces + `#interface_` + strconv.FormatInt(id, 10) + `"`
	// Confirm and remove for each of two networks, add, and the link-local rule.
	if got := strings.Count(card, "<form "); got != 6 || strings.Count(card, action) != got {
		t.Errorf("the card's %d forms do not all post to its anchor", got)
	}

	body := h.SavedEdit(webtest.NetworkForm(webtest.ActionConfirmNetwork, id, webtest.ExampleNetworkV4(1).String()), id)
	saved := webtest.CatalogueText(t, msgInterfaceSaved)
	if !strings.Contains(interfaceCardOf(t, body, id), saved) {
		t.Error("the card the edit was made in does not say it was saved")
	}
	if strings.Count(body, saved) != 1 {
		t.Error("the saved notice is drawn outside the card the edit was made in")
	}
	// A plain load says nothing was saved.
	if strings.Contains(webtest.PageSource(t, h, webtest.PathInterfaces), saved) {
		t.Error("a plain load of the surface says an edit was saved")
	}
}

// TestANetworkThatDoesNotCountSaysWhy: removed by the operator, or no longer detected
// -- decision D1's two ways out -- each named beside the "no".
func TestANetworkThatDoesNotCountSaysWhy(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(2)
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	// Discovery reports a different network on the second interface: its first two
	// detections are stale from then on.
	if err := h.Store.DetectNetworks(ctx, network.Inside[1], []netip.Prefix{webtest.ExampleNetworkV4(9)},
		now+60); err != nil {
		t.Fatalf("recording a later detection: %v", err)
	}
	if _, err := h.Store.RemoveInterfaceNetwork(ctx, network.Inside[0], webtest.ExampleNetworkV4(0).String(),
		now); err != nil {
		t.Fatalf("removing a network: %v", err)
	}
	page := webtest.PageSource(t, h, webtest.PathInterfaces)
	stale := h.NetworkOf(network.Inside[1], webtest.ExampleNetworkV4(1))
	if stale.Counts || stale.Removed {
		t.Fatalf("the fixture's stale detection is %+v, want detected, not removed, not counting", stale)
	}
	for _, expected := range []struct {
		id     int64
		prefix netip.Prefix
		reason messageKey
	}{
		{network.Inside[1], webtest.ExampleNetworkV4(1), msgCountsNoLongerDetected},
		{network.Inside[0], webtest.ExampleNetworkV4(0), msgCountsNoRemoved},
	} {
		row := `<span class="datum" translate="no">` + expected.prefix.String() + "</span>"
		card := interfaceCardOf(t, page, expected.id)
		at := strings.Index(card, row)
		if at < 0 {
			t.Fatalf("interface %d does not list %s", expected.id, expected.prefix)
		}
		line := card[at : at+strings.Index(card[at:], "</tr>")]
		if !strings.Contains(line, webtest.CatalogueText(t, msgNo)+`<span class="counts-reason">`+
			webtest.CatalogueText(t, expected.reason)+"</span>") {
			t.Errorf("%s does not say why it does not count: %s", expected.prefix, line)
		}
	}
	// A network that counts gives no reason.
	if interfaces.NotCountingReason(h.NetworkOf(network.Inside[1], webtest.ExampleNetworkV4(9))) != "" {
		t.Error("a counting network is given a reason not to count")
	}
}

// dataLabel is the data-label attribute a table cell carries: its column's name from
// the catalogue, which a narrow screen draws beside the value when the row stacks.
func dataLabel(t *testing.T, key messageKey) string {
	t.Helper()
	return ` data-label="` + webtest.CatalogueText(t, key) + `"`
}

// TestANarrowScreenStacksTheInterfaceTables: below the narrow breakpoint the networks
// and overlaps tables stack rather than scroll, every cell carrying its column's name,
// so whether a network counts, why not, its controls and the other interface's name
// are all on screen.
func TestANarrowScreenStacksTheInterfaceTables(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	network := h.SeedBundleNetwork(2)
	_ = h.SavedEdit(webtest.NetworkForm(webtest.ActionAddNetwork, network.Inside[1], webtest.ExampleWideV4().String()),
		network.Inside[1])
	page := webtest.PageSource(t, h, webtest.PathInterfaces)
	for _, table := range []string{`<table class="data stack">`, `<table class="data overlaps stack">`} {
		if !strings.Contains(page, table) {
			t.Errorf("the page carries no %s", table)
		}
	}
	if !strings.Contains(page, `<td class="row-actions"`+dataLabel(t, "interfaces.column.action")+`>`) {
		t.Error("the controls' cell carries no column name")
	}
	stylesheet, err := web.Stylesheet()
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	rules := webtest.StripCSSComments(string(stylesheet))
	for _, rule := range []string{"table.data.stack td::before {\n    content: attr(data-label);",
		"table.data.stack thead {"} {
		if !strings.Contains(rules, rule) {
			t.Errorf("the stylesheet has no %q rule", rule)
		}
	}
}
