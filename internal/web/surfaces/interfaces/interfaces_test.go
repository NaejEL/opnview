package interfaces_test

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"testing"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/interfaces"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/interfacestest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// interfaceOf is one interface of the answer, failing when it is absent.
func interfaceOf(t *testing.T, answer map[string]any, id int64) any {
	t.Helper()
	for _, raw := range settingstest.Path(answer, "interfaces").([]any) {
		if settingstest.Path(raw, "id") == float64(id) {
			return raw
		}
	}
	t.Fatalf("the answer has no interface %d", id)
	return nil
}

// networkOf is one network of an answered interface, nil when absent.
func networkOf(answered any, prefix netip.Prefix) any {
	for _, raw := range settingstest.Path(answered, "networks").([]any) {
		if settingstest.Path(raw, "network") == prefix.String() {
			return raw
		}
	}
	return nil
}

// edit sends one edit and fails unless it was saved, returning the answer.
func edit(t *testing.T, h *webtest.Harness, body map[string]any) map[string]any {
	t.Helper()
	status, answer := settingstest.SendJSON(h, interfaces.PathAPIInterfaceNetworks, body)
	if status != http.StatusOK {
		t.Fatalf("the edit %v answered %d %v", body, status, answer)
	}
	return answer
}

// TestTheInterfacesRouteIsAuthenticated is AC1: the listing and the edit refuse a reader with no
// session.
func TestTheInterfacesRouteIsAuthenticated(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	declared := map[string]web.Access{}
	for _, route := range h.Server.Routes() {
		declared[route.Method+" "+route.Pattern] = route.Access
	}
	for _, name := range []string{http.MethodGet + " " + interfaces.PathAPIInterfaceNetworks,
		http.MethodPost + " " + interfaces.PathAPIInterfaceNetworks} {
		if declared[name] != web.AccessAuthenticated {
			t.Errorf("%s is declared %q, want authenticated", name, declared[name])
		}
	}
	status, _ := webtest.StatusAndBody(t, webtest.FreshClient(t, h).Get(interfaces.PathAPIInterfaceNetworks))
	if status != http.StatusUnauthorized {
		t.Errorf("the listing without a session answered %d, want 401", status)
	}
}

// TestEveryInterfaceRowIsAnsweredWithItsNetworks is AC2: one entry per interface row, the upstream
// one carrying no network, and every stored network with its origin, removal and count.
func TestEveryInterfaceRowIsAnsweredWithItsNetworks(t *testing.T) {
	t.Parallel()
	for _, insideCount := range []int{1, 4} {
		t.Run(map[int]string{1: "one inside interface", 4: "four inside interfaces"}[insideCount], func(t *testing.T) {
			t.Parallel()
			h := collectiontest.SignedInHarness(t)
			network := interfacestest.SeedBundleNetwork(h, insideCount)
			ctx := context.Background()
			if _, err := h.Store.RemoveInterfaceNetwork(ctx, network.Inside[0],
				interfacestest.ExampleNetworkV6(0).String(), h.Clock.Now().Unix()); err != nil {
				t.Fatalf("removing a network: %v", err)
			}
			answer := settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
			if got := len(settingstest.Path(answer, "interfaces").([]any)); got != insideCount+1 {
				t.Errorf("the answer lists %d interfaces for %d interface rows", got, insideCount+1)
			}
			upstream := interfaceOf(t, answer, network.Upstream)
			if settingstest.Path(upstream, "upstream") != true || len(settingstest.Path(upstream, "networks").([]any)) != 0 {
				t.Errorf("the upstream interface is answered %v", upstream)
			}
			stored, err := h.Store.InterfaceNetworks(ctx)
			if err != nil {
				t.Fatalf("reading the networks: %v", err)
			}
			for _, id := range network.Inside {
				answered := interfaceOf(t, answer, id)
				count := 0
				for _, network := range stored {
					if network.InterfaceID != id {
						continue
					}
					count++
					drawn := networkOf(answered, network.Network)
					if drawn == nil {
						t.Errorf("interface %d does not answer %s", id, network.Network)
						continue
					}
					if settingstest.Path(drawn, "origin") != web.ConditionOf(interfaces.OriginKey(network.Origin)) ||
						settingstest.Path(drawn, "removed") != network.Removed ||
						settingstest.Path(drawn, "counts") != network.Counts {
						t.Errorf("interface %d answers %v for %+v", id, drawn, network)
					}
				}
				if count != 2 {
					t.Fatalf("interface %d holds %d networks, want one of each family", id, count)
				}
			}
		})
	}
}

// TestConfirmRemoveAndRestore is AC3.
func TestConfirmRemoveAndRestore(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 2)
	id, prefix := network.Inside[1], interfacestest.ExampleNetworkV4(1)

	before := interfacestest.Fingerprint(h)
	edit(t, h, map[string]any{"interface": id, "action": webtest.ActionConfirmNetwork, "network": prefix.String()})
	if got := interfacestest.NetworkOf(h, id, prefix); got.Origin != flows.NetworkOriginOperator || !got.Counts {
		t.Errorf("a confirmed network is %+v, want the operator's and counting", got)
	}
	afterConfirm := interfacestest.Fingerprint(h)
	if afterConfirm == before {
		t.Error("confirming did not change the on-link fingerprint")
	}

	answer := edit(t, h, map[string]any{"interface": id, "action": webtest.ActionRemoveNetwork, "network": prefix.String()})
	if got := interfacestest.NetworkOf(h, id, prefix); !got.Removed || got.Counts {
		t.Errorf("a removed network is %+v, want removed and not counting", got)
	}
	if drawn := networkOf(interfaceOf(t, answer, id), prefix); drawn == nil || settingstest.Path(drawn, "removed") != true {
		t.Error("a removed network is not listed as removed, so it cannot be restored")
	}
	afterRemove := interfacestest.Fingerprint(h)
	if afterRemove == afterConfirm {
		t.Error("removing did not change the on-link fingerprint")
	}

	edit(t, h, map[string]any{"interface": id, "action": webtest.ActionRestoreNetwork, "network": prefix.String()})
	if got := interfacestest.NetworkOf(h, id, prefix); got.Removed || !got.Counts {
		t.Errorf("a restored network is %+v, want counting again", got)
	}
	if interfacestest.Fingerprint(h) == afterRemove {
		t.Error("restoring did not change the on-link fingerprint")
	}

	status, refused := settingstest.SendJSON(h, interfaces.PathAPIInterfaceNetworks, map[string]any{"interface": id,
		"action": webtest.ActionRemoveNetwork, "network": interfacestest.ExampleOperatorV4(9).String()})
	if status != http.StatusBadRequest || refused["condition"] != web.ConditionOf(msgNetworkUnknown) {
		t.Errorf("removing a network the interface does not carry answered %d %v", status, refused)
	}
}

// TestAddingANetworkAndEachRefusal is AC4: each refusal its own code, against the network the
// edit typed, and nothing stored.
func TestAddingANetworkAndEachRefusal(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 1)
	id := network.Inside[0]

	for _, prefix := range []netip.Prefix{interfacestest.ExampleOperatorV4(0), interfacestest.ExampleNetworkV6(7)} {
		edit(t, h, map[string]any{"interface": id, "action": webtest.ActionAddNetwork, "network": prefix.String()})
		if got := interfacestest.NetworkOf(h, id, prefix); got.Origin != flows.NetworkOriginOperator || !got.Counts {
			t.Errorf("an added network is %+v, want the operator's and counting", got)
		}
	}

	var linkLocal [16]byte
	linkLocal[0], linkLocal[1] = 0xfe, 0x80
	mapped := netip.AddrFrom16(netip.AddrFrom4([4]byte{198, 51, 100, 0}).As16())
	for _, refused := range []struct {
		name    string
		id      int64
		typed   string
		refusal messageKey
		field   string
	}{
		{"malformed", id, "not a network", msgNetworkMalformed, "network"},
		{"host bits set", id, netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 1}), 24).String(),
			msgNetworkHostBits, "network"},
		{"IPv4-mapped", id, netip.PrefixFrom(mapped, 120).String(), msgNetworkIPv4Mapped, "network"},
		{"link-local", id, netip.PrefixFrom(netip.AddrFrom16(linkLocal), 64).String(), msgNetworkLinkLocal, "network"},
		{"upstream", network.Upstream, interfacestest.ExampleOperatorV4(3).String(), msgInterfaceUpstream, "interface"},
		{"unknown", network.Inside[0] + network.Upstream + 1000, interfacestest.ExampleOperatorV4(3).String(),
			msgInterfaceUnknown, "interface"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			rows := interfacestest.NetworkRowCount(h)
			status, answer := settingstest.SendJSON(h, interfaces.PathAPIInterfaceNetworks, map[string]any{
				"interface": refused.id, "action": webtest.ActionAddNetwork, "network": refused.typed})
			if status != http.StatusBadRequest || answer["condition"] != web.ConditionOf(refused.refusal) ||
				answer["field"] != refused.field {
				t.Errorf("answered %d %v, want 400 %s on %s", status, answer, web.ConditionOf(refused.refusal), refused.field)
			}
			if interfacestest.NetworkRowCount(h) != rows {
				t.Error("a refused network was stored")
			}
		})
	}
}

// TestAnOverlapIsStoredAndNamed is AC5: an overlap is answered on both interfaces, with the other
// interface named by its id, for as long as it exists.
func TestAnOverlapIsStoredAndNamed(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 3)
	wide := interfacestest.ExampleWideV4()

	edit(t, h, map[string]any{"interface": network.Inside[2], "action": webtest.ActionAddNetwork, "network": wide.String()})
	if got := interfacestest.NetworkOf(h, network.Inside[2], wide); !got.Counts {
		t.Error("an overlapping network was not stored as counting")
	}
	answer := settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	overlaps := settingstest.Path(interfaceOf(t, answer, network.Inside[2]), "overlaps").([]any)
	for index := 0; index < 2; index++ {
		found := false
		for _, overlap := range overlaps {
			if settingstest.Path(overlap, "network") == wide.String() &&
				settingstest.Path(overlap, "other") == interfacestest.ExampleNetworkV4(index).String() &&
				settingstest.Path(overlap, "other_interface_id") == float64(network.Inside[index]) {
				found = true
			}
		}
		if !found {
			t.Errorf("the overlap with interface %d is not named: %v", index, overlaps)
		}
		other := settingstest.Path(interfaceOf(t, answer, network.Inside[index]), "overlaps").([]any)
		if len(other) != 1 || settingstest.Path(other[0], "other") != wide.String() {
			t.Errorf("interface %d does not name the overlap: %v", index, other)
		}
	}

	edit(t, h, map[string]any{"interface": network.Inside[2], "action": webtest.ActionRemoveNetwork, "network": wide.String()})
	answer = settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	for _, id := range network.Inside {
		if overlaps := settingstest.Path(interfaceOf(t, answer, id), "overlaps").([]any); len(overlaps) != 0 {
			t.Errorf("a resolved overlap is still answered on interface %d: %v", id, overlaps)
		}
	}
}

// TestTheLinkLocalRule is AC6.
func TestTheLinkLocalRule(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 2)
	id := network.Inside[1]

	if got := settingstest.Path(interfaceOf(t, settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks), id), "link_local"); got != false {
		t.Errorf("a fresh interface's link-local rule is answered %v, want false", got)
	}
	before := interfacestest.Fingerprint(h)
	answer := edit(t, h, map[string]any{"interface": id, "action": webtest.ActionLinkLocal, "link_local": true})
	if settingstest.Path(interfaceOf(t, answer, id), "link_local") != true {
		t.Error("the answer does not report the rule now stored")
	}
	if interfacestest.Fingerprint(h) == before {
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
	for _, refused := range []map[string]any{
		{"interface": id, "action": webtest.ActionLinkLocal, "link_local": "maybe"},
		{"interface": network.Upstream, "action": webtest.ActionLinkLocal, "link_local": true},
	} {
		if status, answer := settingstest.SendJSON(h, interfaces.PathAPIInterfaceNetworks, refused); status != http.StatusBadRequest {
			t.Errorf("the rule %v answered %d %v", refused, status, answer)
		}
	}
}

// TestAnInterfaceWithNoNetworkIsAnsweredWithNone is AC7: no interface is an empty list, and an
// interface with no network an empty one, never a fabricated row.
func TestAnInterfaceWithNoNetworkIsAnsweredWithNone(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	if got := settingstest.Path(settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks), "interfaces").([]any); len(got) != 0 {
		t.Errorf("an installation with no interface answers %v", got)
	}
	id, err := h.Store.UpsertInterface(context.Background(), vocab.Interface{
		Identifier: "example_bare", Device: "exbare0", Description: "bare.example",
		LinkType: "example-link", LinkKind: "other",
	}, h.Clock.Now().Unix())
	if err != nil {
		t.Fatalf("writing an interface: %v", err)
	}
	bare := interfaceOf(t, settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks), id)
	if networks := settingstest.Path(bare, "networks").([]any); len(networks) != 0 {
		t.Errorf("an interface with no network answers %v", networks)
	}
}

// TestEveryEditRefusesAMissingFormToken is AC8: an edit without its token is refused and changes
// nothing.
func TestEveryEditRefusesAMissingFormToken(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 1)
	rows := interfacestest.NetworkRowCount(h)
	for _, action := range []string{webtest.ActionAddNetwork, webtest.ActionConfirmNetwork, webtest.ActionRemoveNetwork,
		webtest.ActionRestoreNetwork, webtest.ActionLinkLocal} {
		body := string(webtest.MustJSON(t, map[string]any{"interface": network.Inside[0], "action": action,
			"network": interfacestest.ExampleNetworkV4(0).String(), "link_local": true}))
		status, _ := webtest.StatusAndBody(t, h.PostJSON(interfaces.PathAPIInterfaceNetworks, body, "", ""))
		if status != http.StatusForbidden {
			t.Errorf("the edit %s without its token answered %d", action, status)
		}
	}
	if interfacestest.NetworkRowCount(h) != rows ||
		interfacestest.NetworkOf(h, network.Inside[0], interfacestest.ExampleNetworkV4(0)).Origin != flows.NetworkOriginDetected {
		t.Error("an edit refused for its form token changed a network")
	}
}

// TestANetworkThatDoesNotCountSaysWhy: removed by the operator, or no longer detected.
func TestANetworkThatDoesNotCountSaysWhy(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 2)
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	if err := h.Store.DetectNetworks(ctx, network.Inside[1], []netip.Prefix{interfacestest.ExampleNetworkV4(9)},
		now+60); err != nil {
		t.Fatalf("recording a later detection: %v", err)
	}
	if _, err := h.Store.RemoveInterfaceNetwork(ctx, network.Inside[0], interfacestest.ExampleNetworkV4(0).String(),
		now); err != nil {
		t.Fatalf("removing a network: %v", err)
	}
	stale := interfacestest.NetworkOf(h, network.Inside[1], interfacestest.ExampleNetworkV4(1))
	if stale.Counts || stale.Removed {
		t.Fatalf("the fixture's stale detection is %+v, want detected, not removed, not counting", stale)
	}
	answer := settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	for _, expected := range []struct {
		id     int64
		prefix netip.Prefix
		reason any
	}{
		{network.Inside[1], interfacestest.ExampleNetworkV4(1), web.ConditionOf(msgCountsNoLongerDetected)},
		{network.Inside[0], interfacestest.ExampleNetworkV4(0), web.ConditionOf(msgCountsNoRemoved)},
		{network.Inside[1], interfacestest.ExampleNetworkV4(9), nil},
	} {
		drawn := networkOf(interfaceOf(t, answer, expected.id), expected.prefix)
		if drawn == nil {
			t.Fatalf("interface %d does not answer %s", expected.id, expected.prefix)
		}
		if got := settingstest.Path(drawn, "not_counting_reason"); got != expected.reason {
			t.Errorf("%s answers the reason %v, want %v", expected.prefix, got, expected.reason)
		}
	}
}

// TestTheInterfaceLabelIsAnsweredAndNeverDerived: the label the user gives an interface is
// answered with it, null until one is given; it is never derived from the description.
func TestTheInterfaceLabelIsAnsweredAndNeverDerived(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	network := interfacestest.SeedBundleNetwork(h, 2)
	id := network.Inside[0]
	answer := settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	for _, raw := range settingstest.Path(answer, "interfaces").([]any) {
		if label, present := raw.(map[string]any)["user_label"]; !present || label != nil {
			t.Errorf("an interface nobody labelled answers user_label %v (present %v)", label, present)
		}
	}
	if status, labelled := settingstest.SendJSON(h, "/api/interfaces/"+strconv.FormatInt(id, 10)+"/label",
		map[string]any{"user_label": "Workshop"}); status != http.StatusOK {
		t.Fatalf("labelling the interface answered %d %v", status, labelled)
	}
	answer = settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	if got := settingstest.Path(interfaceOf(t, answer, id), "user_label"); got != "Workshop" {
		t.Errorf("the labelled interface answers user_label %v", got)
	}
	if got := settingstest.Path(interfaceOf(t, answer, network.Inside[1]), "user_label"); got != nil {
		t.Errorf("the other interface answers user_label %v", got)
	}
}
