package web

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// The Interfaces surface.
//
// PROVENANCE OF EVERY FIXTURE HERE: SYNTHESISED. The interfaces are written through the
// store's own discovery write path, named with RFC 2606 example names and identifiers
// that resemble no product's; the networks are carved, at run time and from bytes, out
// of the RFC 5737 documentation ranges for IPv4 and the RFC 3849 range for IPv6, so no
// address is written in this file as text. The number of interfaces is a parameter of
// every fixture. Nothing here was read off a network.

// exampleNetworkV4 is the index-th /28 of the second RFC 5737 range.
func exampleNetworkV4(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, byte(16 * (index % 16))}), 28)
}

// exampleWideV4 is the whole second RFC 5737 range, which overlaps every /28 above.
func exampleWideV4() netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 0}), 24)
}

// exampleNetworkV6 is the index-th /48 of the RFC 3849 range.
func exampleNetworkV6(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, byte(index + 1)}), 48)
}

// exampleOperatorV4 is a /28 of the third RFC 5737 range, which no detection uses.
func exampleOperatorV4(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(16 * (index % 16))}), 28)
}

// bundleNetwork is a database holding one upstream interface and inside interfaces.
type bundleNetwork struct {
	upstream int64
	inside   []int64
}

// seedBundleNetwork writes one upstream interface and insideCount inside interfaces,
// each inside one carrying one detected network of each family.
func (h *harness) seedBundleNetwork(insideCount int) bundleNetwork {
	h.t.Helper()
	ctx := context.Background()
	now := h.clock.Now().Unix()
	var network bundleNetwork
	upstream, err := h.store.UpsertInterface(ctx, store.Interface{
		Identifier: "example_upstream", Device: "exupdev0", Description: "upstream.example",
		LinkType: "example-link", LinkKind: "other", IsUpstream: true,
	}, now)
	if err != nil {
		h.t.Fatalf("writing the upstream interface: %v", err)
	}
	network.upstream = upstream
	for index := 0; index < insideCount; index++ {
		id, err := h.store.UpsertInterface(ctx, store.Interface{
			Identifier:  fmt.Sprintf("example_if_%d", index),
			Device:      fmt.Sprintf("exdev%d", index),
			Description: fmt.Sprintf("inside-%d.example", index),
			LinkType:    "example-link", LinkKind: "other",
		}, now)
		if err != nil {
			h.t.Fatalf("writing an inside interface: %v", err)
		}
		if err := h.store.DetectNetworks(ctx, id,
			[]netip.Prefix{exampleNetworkV4(index), exampleNetworkV6(index)}, now); err != nil {
			h.t.Fatalf("recording the detected networks: %v", err)
		}
		network.inside = append(network.inside, id)
	}
	return network
}

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

// networkForm is what one control of the Interfaces surface submits.
func networkForm(action string, id int64, network string) url.Values {
	return url.Values{
		fieldNetworkAction: {action},
		fieldInterfaceID:   {strconv.FormatInt(id, 10)},
		fieldNetwork:       {network},
	}
}

// networkOf returns one stored network, failing when there is none.
func (h *harness) networkOf(id int64, prefix netip.Prefix) store.InterfaceNetwork {
	h.t.Helper()
	networks, err := h.store.InterfaceNetworks(context.Background())
	if err != nil {
		h.t.Fatalf("reading the networks: %v", err)
	}
	for _, network := range networks {
		if network.InterfaceID == id && network.Network == prefix {
			return network
		}
	}
	h.t.Fatalf("interface %d carries no network %s", id, prefix)
	return store.InterfaceNetwork{}
}

// fingerprint is the store's on-link fingerprint.
func (h *harness) fingerprint() string {
	h.t.Helper()
	value, err := h.store.OnLinkFingerprint(context.Background())
	if err != nil {
		h.t.Fatalf("reading the on-link fingerprint: %v", err)
	}
	return value
}

// networkRowCount counts the interface_network rows.
func (h *harness) networkRowCount() int {
	h.t.Helper()
	count, err := h.store.CountRows(context.Background(), "interface_network")
	if err != nil {
		h.t.Fatalf("counting the networks: %v", err)
	}
	return count
}

// TestTheInterfacesRouteIsAuthenticatedAndLinkedFromEveryPage is AC1.
func TestTheInterfacesRouteIsAuthenticatedAndLinkedFromEveryPage(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)

	declared := map[string]Access{}
	for _, route := range h.server.Routes() {
		declared[route.Method+" "+route.Pattern] = route.Access
	}
	for _, name := range []string{
		http.MethodGet + " " + PathInterfaces, http.MethodPost + " " + PathInterfaces,
		http.MethodGet + " " + PathSources, http.MethodPost + " " + PathCollectionSettings,
	} {
		if declared[name] != AccessAuthenticated {
			t.Errorf("%s is declared %q, want authenticated", name, declared[name])
		}
	}

	anonymous := freshClient(t, h)
	for _, path := range []string{PathInterfaces, PathSources} {
		answer := anonymous.get(path)
		status, _ := statusAndBody(t, answer)
		if status != http.StatusSeeOther || answer.Header.Get("Location") != PathSignIn {
			t.Errorf("GET %s without a session answered %d to %q, want a redirect to sign-in",
				path, status, answer.Header.Get("Location"))
		}
	}

	for _, path := range []string{PathSettings, PathCollection, PathInterfaces, PathSources} {
		page := pageSource(t, h, path)
		for _, link := range []string{PathInterfaces, PathSources} {
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
			h := signedInHarness(t)
			network := h.seedBundleNetwork(insideCount)
			// One network removed and one stale detection, so every combination of the
			// three facts is drawn.
			ctx := context.Background()
			now := h.clock.Now().Unix()
			if _, err := h.store.RemoveInterfaceNetwork(ctx, network.inside[0],
				exampleNetworkV6(0).String(), now); err != nil {
				t.Fatalf("removing a network: %v", err)
			}
			page := pageSource(t, h, PathInterfaces)

			if got := strings.Count(page, `<section class="card" id="interface_`); got != insideCount+1 {
				t.Errorf("the page draws %d interface cards for %d interface rows", got, insideCount+1)
			}
			upstream := interfaceCardOf(t, page, network.upstream)
			for _, control := range []string{`name="` + fieldNetwork + `"`, `name="` + fieldLinkLocal + `"`,
				`<table`} {
				if strings.Contains(upstream, control) {
					t.Errorf("the upstream card carries %s", control)
				}
			}
			if !strings.Contains(upstream, "<dd>"+catalogueText(t, msgYes)+"</dd>") {
				t.Error("the upstream card does not say it is upstream")
			}

			networks, err := h.store.InterfaceNetworks(ctx)
			if err != nil {
				t.Fatalf("reading the networks: %v", err)
			}
			for _, id := range network.inside {
				card := interfaceCardOf(t, page, id)
				drawn := 0
				for _, stored := range networks {
					if stored.InterfaceID != id {
						continue
					}
					drawn++
					counts := catalogueText(t, yesNo(stored.Counts))
					if reason := notCountingReason(stored); reason != "" {
						counts += `<span class="counts-reason">` + catalogueText(t, reason) + "</span>"
					}
					row := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
						`><span class="datum" translate="no">` + stored.Network.String() + "</span></td>\n" +
						"<td" + dataLabel(t, "interfaces.column.origin") + ">" +
						catalogueText(t, originKey(stored.Origin)) + "</td>\n" +
						"<td" + dataLabel(t, "interfaces.column.removed") + ">" +
						catalogueText(t, yesNo(stored.Removed)) + "</td>\n" +
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
	h := signedInHarness(t)
	network := h.seedBundleNetwork(2)
	id, prefix := network.inside[1], exampleNetworkV4(1)

	page := interfaceCardOf(t, pageSource(t, h, PathInterfaces), id)
	if !strings.Contains(page, `value="`+actionConfirmNetwork+`"`) {
		t.Fatal("a detected network carries no confirm control")
	}

	before := h.fingerprint()
	body := h.savedEdit(networkForm(actionConfirmNetwork, id, prefix.String()), id)
	if got := h.networkOf(id, prefix); got.Origin != store.NetworkOriginOperator || !got.Counts {
		t.Errorf("a confirmed network is %+v, want the operator's and counting", got)
	}
	if !strings.Contains(interfaceCardOf(t, body, id), catalogueText(t, msgInterfaceSaved)) {
		t.Error("the confirmation is not drawn in the card it was made in")
	}
	afterConfirm := h.fingerprint()
	if afterConfirm == before {
		t.Error("confirming did not change the on-link fingerprint")
	}

	body = h.savedEdit(networkForm(actionRemoveNetwork, id, prefix.String()), id)
	if got := h.networkOf(id, prefix); !got.Removed || got.Counts {
		t.Errorf("a removed network is %+v, want removed and not counting", got)
	}
	card := interfaceCardOf(t, body, id)
	if !strings.Contains(card, prefix.String()) || !strings.Contains(card, `value="`+actionRestoreNetwork+`"`) {
		t.Error("a removed network is not listed with a restore control")
	}
	afterRemove := h.fingerprint()
	if afterRemove == afterConfirm {
		t.Error("removing did not change the on-link fingerprint")
	}

	_ = h.savedEdit(networkForm(actionRestoreNetwork, id, prefix.String()), id)
	if got := h.networkOf(id, prefix); got.Removed || !got.Counts {
		t.Errorf("a restored network is %+v, want counting again", got)
	}
	if h.fingerprint() == afterRemove {
		t.Error("restoring did not change the on-link fingerprint")
	}

	// A network the interface does not carry is refused at the page, and nothing moves.
	status, body := statusAndBody(t, h.post(PathInterfaces,
		networkForm(actionRemoveNetwork, id, exampleOperatorV4(9).String())))
	if status != http.StatusBadRequest || !strings.Contains(body, catalogueText(t, msgNetworkUnknown)) {
		t.Errorf("removing a network the interface does not carry answered %d", status)
	}
}

// TestAddingANetworkAndEachRefusal is AC4.
func TestAddingANetworkAndEachRefusal(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(1)
	id := network.inside[0]

	for _, prefix := range []netip.Prefix{exampleOperatorV4(0), exampleNetworkV6(7)} {
		_ = h.savedEdit(networkForm(actionAddNetwork, id, prefix.String()), id)
		if got := h.networkOf(id, prefix); got.Origin != store.NetworkOriginOperator || !got.Counts {
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
			rows := h.networkRowCount()
			status, body := statusAndBody(t, h.post(PathInterfaces, networkForm(actionAddNetwork, id, refused.typed)))
			if status != http.StatusBadRequest {
				t.Errorf("answered %d, want 400", status)
			}
			if h.networkRowCount() != rows {
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
			if fieldValue(card, "network_"+strconv.FormatInt(id, 10)) != refused.typed {
				t.Error("the typed text was not kept")
			}
			if !strings.Contains(card, `id="`+describedBy+`">`+catalogueText(t, refused.refusal)+"</p>") {
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
		{"upstream", network.upstream, msgInterfaceUpstream},
		{"unknown", network.inside[0] + network.upstream + 1000, msgInterfaceUnknown},
	} {
		rows := h.networkRowCount()
		status, body := statusAndBody(t, h.post(PathInterfaces,
			networkForm(actionAddNetwork, refused.id, exampleOperatorV4(3).String())))
		if status != http.StatusBadRequest {
			t.Errorf("an add to the %s interface answered %d", refused.name, status)
		}
		if h.networkRowCount() != rows {
			t.Errorf("an add to the %s interface stored a network", refused.name)
		}
		if !strings.Contains(body, `<p class="notice bad">`+catalogueText(t, refused.refusal)+"</p>") {
			t.Errorf("the %s refusal is not its own sentence", refused.name)
		}
	}
}

// TestAnOverlapIsStoredAndNamed is AC5.
func TestAnOverlapIsStoredAndNamed(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(3)
	wide := exampleWideV4()

	body := h.savedEdit(networkForm(actionAddNetwork, network.inside[2], wide.String()), network.inside[2])
	if got := h.networkOf(network.inside[2], wide); !got.Counts {
		t.Error("an overlapping network was not stored as counting")
	}
	// The overlap is a state of the card, read on every draw: on the page the save
	// lands on, and on a plain load of the page afterwards.
	for name, page := range map[string]string{"after the save": body,
		"on a later load": pageSource(t, h, PathInterfaces)} {
		card := interfaceCardOf(t, page, network.inside[2])
		if !strings.Contains(card, catalogueText(t, "interfaces.heading.overlaps")) {
			t.Fatalf("the card does not report the overlaps %s", name)
		}
		for index := 0; index < 2; index++ {
			overlap := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
				`><span class="datum" translate="no">` + wide.String() + `</span></td>` + "\n" +
				`<td class="network"` + dataLabel(t, "interfaces.column.overlapping") +
				`><span class="datum" translate="no">` + exampleNetworkV4(index).String() + `</span></td>` + "\n" +
				`<td` + dataLabel(t, "interfaces.column.interface") + `><span class="datum" translate="no">` +
				fmt.Sprintf("inside-%d.example", index) +
				`</span></td>`
			if !strings.Contains(card, overlap) {
				t.Errorf("the overlap with interface %d is not named with its network and its interface %s",
					index, name)
			}
			// And the other interface's card names it too.
			other := `<td class="network"` + dataLabel(t, "interfaces.column.network") +
				`><span class="datum" translate="no">` + exampleNetworkV4(index).String() + `</span></td>` + "\n" +
				`<td class="network"` + dataLabel(t, "interfaces.column.overlapping") +
				`><span class="datum" translate="no">` + wide.String() + `</span></td>`
			if !strings.Contains(interfaceCardOf(t, page, network.inside[index]), other) {
				t.Errorf("interface %d's card does not name the overlap %s", index, name)
			}
		}
	}

	// Resolved -- the wide network removed -- the overlap is gone from every card.
	_ = h.savedEdit(networkForm(actionRemoveNetwork, network.inside[2], wide.String()), network.inside[2])
	if strings.Contains(pageSource(t, h, PathInterfaces), catalogueText(t, "interfaces.heading.overlaps")) {
		t.Error("a resolved overlap is still reported")
	}
}

// TestTheLinkLocalRule is AC6.
func TestTheLinkLocalRule(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(2)
	id := network.inside[1]
	field := "link_local_" + strconv.FormatInt(id, 10)

	if got := selectedIn(t, interfaceCardOf(t, pageSource(t, h, PathInterfaces), id), field); got != linkLocalNo {
		t.Errorf("a fresh interface's link-local rule shows %q, want no", got)
	}
	before := h.fingerprint()
	form := url.Values{fieldNetworkAction: {actionLinkLocal}, fieldInterfaceID: {strconv.FormatInt(id, 10)},
		fieldLinkLocal: {linkLocalYes}}
	body := h.savedEdit(form, id)
	if selectedIn(t, interfaceCardOf(t, body, id), field) != linkLocalYes {
		t.Error("the card does not show the rule now stored")
	}
	if h.fingerprint() == before {
		t.Error("setting the rule did not change the on-link fingerprint")
	}
	rows, err := h.store.Interfaces(context.Background())
	if err != nil {
		t.Fatalf("reading the interfaces: %v", err)
	}
	for _, row := range rows {
		if row.LinkLocalEvidence != (row.ID == id) {
			t.Errorf("interface %d carries link_local_evidence %t", row.ID, row.LinkLocalEvidence)
		}
	}

	// A value that is neither, and the rule on an upstream interface, are refused.
	form.Set(fieldLinkLocal, "maybe")
	if status, _ := statusAndBody(t, h.post(PathInterfaces, form)); status != http.StatusBadRequest {
		t.Errorf("a rule that is neither yes nor no answered %d", status)
	}
	form.Set(fieldLinkLocal, linkLocalYes)
	form.Set(fieldInterfaceID, strconv.FormatInt(network.upstream, 10))
	if status, _ := statusAndBody(t, h.post(PathInterfaces, form)); status != http.StatusBadRequest {
		t.Errorf("the rule on an upstream interface answered %d", status)
	}
}

// TestAnInterfaceWithNoNetworkIsANamedState is AC7, and the page with no interface at
// all.
func TestAnInterfaceWithNoNetworkIsANamedState(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	if !strings.Contains(pageSource(t, h, PathInterfaces), catalogueText(t, "state.interface.none")) {
		t.Error("a page with no interface does not say so")
	}
	id, err := h.store.UpsertInterface(context.Background(), store.Interface{
		Identifier: "example_bare", Device: "exbare0", Description: "bare.example",
		LinkType: "example-link", LinkKind: "other",
	}, h.clock.Now().Unix())
	if err != nil {
		t.Fatalf("writing an interface: %v", err)
	}
	card := interfaceCardOf(t, pageSource(t, h, PathInterfaces), id)
	if !strings.Contains(card, `<p class="empty-state">`+catalogueText(t, "state.interface.no_network")+"</p>") {
		t.Error("an interface with no network does not name that state")
	}
	if strings.Contains(card, "<table") {
		t.Error("an interface with no network draws an empty table")
	}
}

// TestEveryNewMutatingRouteRefusesAMissingFormToken is AC8, one route at a time.
func TestEveryNewMutatingRouteRefusesAMissingFormToken(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(1)

	rows := h.networkRowCount()
	_ = h.formToken(PathInterfaces)
	for _, action := range []string{actionAddNetwork, actionConfirmNetwork, actionRemoveNetwork,
		actionRestoreNetwork} {
		assertStatus(t, h.postRaw(PathInterfaces,
			networkForm(action, network.inside[0], exampleNetworkV4(0).String())), http.StatusForbidden)
	}
	assertStatus(t, h.postRaw(PathInterfaces, url.Values{fieldNetworkAction: {actionLinkLocal},
		fieldInterfaceID: {strconv.FormatInt(network.inside[0], 10)}, fieldLinkLocal: {linkLocalYes}}),
		http.StatusForbidden)
	if h.networkRowCount() != rows || h.networkOf(network.inside[0], exampleNetworkV4(0)).Origin !=
		store.NetworkOriginDetected {
		t.Error("a submission refused for its form token changed a network")
	}

	_ = h.formToken(PathCollection)
	assertStatus(t, h.postRaw(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_cache"}, fieldInterval: {"90"},
	}), http.StatusForbidden)
	assertStatus(t, h.postRaw(PathCollectionSettings, url.Values{
		fieldCard: {"pairing"}, "leg_pairing_window_seconds": {"3"},
	}), http.StatusForbidden)
	for _, key := range []string{"poll_interval_resolver_cache_seconds"} {
		if _, present, err := h.store.Setting(context.Background(), key); err != nil || present {
			t.Errorf("a submission refused for its form token stored %s", key)
		}
	}
	if got := h.setting("leg_pairing_window_seconds"); got != "1" {
		t.Errorf("a submission refused for its form token stored the pairing window %q", got)
	}
}

// savedEdit posts one edit that must be saved, asserts that the answer sends the
// browser back to the card the edit was made in -- a 303 to the surface naming that
// card, at that card's anchor -- and returns the page the browser then loads.
func (h *harness) savedEdit(form url.Values, id int64) string {
	h.t.Helper()
	answer := h.post(PathInterfaces, form)
	status, _ := statusAndBody(h.t, answer)
	location := answer.Header.Get("Location")
	anchor := "#interface_" + strconv.FormatInt(id, 10)
	if status != http.StatusSeeOther || location != savedLocation(id) || !strings.HasSuffix(location, anchor) {
		h.t.Fatalf("a saved edit answered %d to %q, want a redirect to %s", status, location, anchor)
	}
	// A browser does not send the fragment; it scrolls to it.
	return pageSource(h.t, h, strings.TrimSuffix(location, anchor))
}

// TestASavedEditReturnsToItsCard: every control posts to its card's anchor, a saved
// edit redirects there with the card named, and only that card says it was saved.
func TestASavedEditReturnsToItsCard(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(3)
	id := network.inside[1]
	page := pageSource(t, h, PathInterfaces)
	card := interfaceCardOf(t, page, id)
	action := `action="` + PathInterfaces + `#interface_` + strconv.FormatInt(id, 10) + `"`
	// Confirm and remove for each of two networks, add, and the link-local rule.
	if got := strings.Count(card, "<form "); got != 6 || strings.Count(card, action) != got {
		t.Errorf("the card's %d forms do not all post to its anchor", got)
	}

	body := h.savedEdit(networkForm(actionConfirmNetwork, id, exampleNetworkV4(1).String()), id)
	saved := catalogueText(t, msgInterfaceSaved)
	if !strings.Contains(interfaceCardOf(t, body, id), saved) {
		t.Error("the card the edit was made in does not say it was saved")
	}
	if strings.Count(body, saved) != 1 {
		t.Error("the saved notice is drawn outside the card the edit was made in")
	}
	// A plain load says nothing was saved.
	if strings.Contains(pageSource(t, h, PathInterfaces), saved) {
		t.Error("a plain load of the surface says an edit was saved")
	}
}

// TestANetworkThatDoesNotCountSaysWhy: removed by the operator, or no longer detected
// -- decision D1's two ways out -- each named beside the "no".
func TestANetworkThatDoesNotCountSaysWhy(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(2)
	ctx := context.Background()
	now := h.clock.Now().Unix()
	// Discovery reports a different network on the second interface: its first two
	// detections are stale from then on.
	if err := h.store.DetectNetworks(ctx, network.inside[1], []netip.Prefix{exampleNetworkV4(9)},
		now+60); err != nil {
		t.Fatalf("recording a later detection: %v", err)
	}
	if _, err := h.store.RemoveInterfaceNetwork(ctx, network.inside[0], exampleNetworkV4(0).String(),
		now); err != nil {
		t.Fatalf("removing a network: %v", err)
	}
	page := pageSource(t, h, PathInterfaces)
	stale := h.networkOf(network.inside[1], exampleNetworkV4(1))
	if stale.Counts || stale.Removed {
		t.Fatalf("the fixture's stale detection is %+v, want detected, not removed, not counting", stale)
	}
	for _, expected := range []struct {
		id     int64
		prefix netip.Prefix
		reason messageKey
	}{
		{network.inside[1], exampleNetworkV4(1), msgCountsNoLongerDetected},
		{network.inside[0], exampleNetworkV4(0), msgCountsNoRemoved},
	} {
		row := `<span class="datum" translate="no">` + expected.prefix.String() + "</span>"
		card := interfaceCardOf(t, page, expected.id)
		at := strings.Index(card, row)
		if at < 0 {
			t.Fatalf("interface %d does not list %s", expected.id, expected.prefix)
		}
		line := card[at : at+strings.Index(card[at:], "</tr>")]
		if !strings.Contains(line, catalogueText(t, msgNo)+`<span class="counts-reason">`+
			catalogueText(t, expected.reason)+"</span>") {
			t.Errorf("%s does not say why it does not count: %s", expected.prefix, line)
		}
	}
	// A network that counts gives no reason.
	if notCountingReason(h.networkOf(network.inside[1], exampleNetworkV4(9))) != "" {
		t.Error("a counting network is given a reason not to count")
	}
}

// dataLabel is the data-label attribute a table cell carries: its column's name from
// the catalogue, which a narrow screen draws beside the value when the row stacks.
func dataLabel(t *testing.T, key messageKey) string {
	t.Helper()
	return ` data-label="` + catalogueText(t, key) + `"`
}

// TestANarrowScreenStacksTheInterfaceTables: below the narrow breakpoint the networks
// and overlaps tables stack rather than scroll, every cell carrying its column's name,
// so whether a network counts, why not, its controls and the other interface's name
// are all on screen.
func TestANarrowScreenStacksTheInterfaceTables(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	network := h.seedBundleNetwork(2)
	_ = h.savedEdit(networkForm(actionAddNetwork, network.inside[1], exampleWideV4().String()),
		network.inside[1])
	page := pageSource(t, h, PathInterfaces)
	for _, table := range []string{`<table class="data stack">`, `<table class="data overlaps stack">`} {
		if !strings.Contains(page, table) {
			t.Errorf("the page carries no %s", table)
		}
	}
	if !strings.Contains(page, `<td class="row-actions"`+dataLabel(t, "interfaces.column.action")+`>`) {
		t.Error("the controls' cell carries no column name")
	}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	rules := stripCSSComments(string(stylesheet))
	for _, rule := range []string{"table.data.stack td::before {\n    content: attr(data-label);",
		"table.data.stack thead {"} {
		if !strings.Contains(rules, rule) {
			t.Errorf("the stylesheet has no %q rule", rule)
		}
	}
}
