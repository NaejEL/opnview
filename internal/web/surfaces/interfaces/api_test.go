package interfaces_test

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/surfaces/interfaces"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/interfacestest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The Interfaces surface's JSON equivalents (C-AC9): every figure of the page in the JSON answer,
// and every edit made, and refused, as the page makes and refuses it.

// networkRows renders every stored network by its interface's identifier, and the link-local rule
// of every interface, so two installations compare.
func networkRows(t *testing.T, h *webtest.Harness) string {
	t.Helper()
	interfaceRows, err := h.Store.Interfaces(context.Background())
	if err != nil {
		t.Fatalf("reading the interfaces: %v", err)
	}
	identifiers := map[int64]string{}
	var lines []string
	for _, row := range interfaceRows {
		identifiers[row.ID] = row.Identifier
		lines = append(lines, fmt.Sprintf("%s link_local=%v", row.Identifier, row.LinkLocalEvidence))
	}
	networks, err := h.Store.InterfaceNetworks(context.Background())
	if err != nil {
		t.Fatalf("reading the networks: %v", err)
	}
	for _, network := range networks {
		lines = append(lines, fmt.Sprintf("%s %s %s removed=%v counts=%v", identifiers[network.InterfaceID],
			network.Network, network.Origin, network.Removed, network.Counts))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestTheInterfacesJSONEditsMakeThePagesChangeAndRefuseItsInputs is C-AC9 for POST /interfaces:
// each edit given to the page on one installation and to the JSON route on another.
func TestTheInterfacesJSONEditsMakeThePagesChangeAndRefuseItsInputs(t *testing.T) {
	t.Parallel()
	for _, input := range []struct {
		what                       string
		action, network, linkLocal string
		upstream, unknown, refused bool
		jsonLinkLocal              any
	}{
		{what: "add an operator network", action: "add", network: interfacestest.ExampleOperatorV4(1).String()},
		{what: "confirm a detected network", action: "confirm", network: interfacestest.ExampleNetworkV4(0).String()},
		{what: "remove a detected network", action: "remove", network: interfacestest.ExampleNetworkV4(0).String()},
		{what: "turn the link-local rule on", action: "link_local", linkLocal: "yes", jsonLinkLocal: true},
		{what: "add a network with host bits", action: "add", network: netip.PrefixFrom(interfacestest.ExampleOperatorV4(1).Addr().Next(), 28).String(), refused: true},
		{what: "add an IPv4-mapped network", action: "add", network: netip.PrefixFrom(netip.AddrFrom16(interfacestest.ExampleOperatorV4(1).Addr().As16()), 124).String(), refused: true},
		{what: "add something that is no network", action: "add", network: "inside", refused: true},
		{what: "remove a network the interface does not carry", action: "remove",
			network: interfacestest.ExampleOperatorV4(3).String(), refused: true},
		{what: "an action that is none", action: "rename", refused: true},
		{what: "a link-local rule that is no rule", action: "link_local", linkLocal: "maybe",
			jsonLinkLocal: "maybe", refused: true},
		{what: "an edit of the upstream interface", action: "add", network: interfacestest.ExampleOperatorV4(1).String(),
			upstream: true, refused: true},
		{what: "an edit of an interface that does not exist", action: "add",
			network: interfacestest.ExampleOperatorV4(1).String(), unknown: true, refused: true},
	} {
		t.Run(input.what, func(t *testing.T) {
			pageSide, jsonSide := collectiontest.SignedInHarness(t), collectiontest.SignedInHarness(t)
			pageNetwork, jsonNetwork := interfacestest.SeedBundleNetwork(pageSide, 1), interfacestest.SeedBundleNetwork(jsonSide, 1)
			target := func(seeded interfacestest.BundleNetwork) int64 {
				switch {
				case input.upstream:
					return seeded.Upstream
				case input.unknown:
					return 999999
				}
				return seeded.Inside[0]
			}
			pageBefore, jsonBefore := networkRows(t, pageSide), networkRows(t, jsonSide)
			form := url.Values{webtest.FieldNetworkAction: {input.action},
				webtest.FieldInterfaceID: {strconv.FormatInt(target(pageNetwork), 10)},
				webtest.FieldNetwork:     {input.network}, webtest.FieldLinkLocal: {input.linkLocal}}
			answer := pageSide.Post(webtest.PathInterfaces, form)
			webtest.Discard(t, answer)
			body := map[string]any{"interface": target(jsonNetwork), "action": input.action, "network": input.network}
			if input.jsonLinkLocal != nil {
				body["link_local"] = input.jsonLinkLocal
			}
			status, decoded := settingstest.SendJSON(jsonSide, interfaces.PathAPIInterfaceNetworks, body)
			if input.refused {
				if answer.StatusCode != http.StatusBadRequest {
					t.Errorf("the page answered %d rather than refusing", answer.StatusCode)
				}
				if status != http.StatusBadRequest || decoded["condition"] == nil {
					t.Errorf("the JSON route answered %d %v rather than refusing", status, decoded)
				}
				if networkRows(t, pageSide) != pageBefore || networkRows(t, jsonSide) != jsonBefore {
					t.Error("a refused edit changed a row")
				}
				return
			}
			if answer.StatusCode != http.StatusSeeOther || status != http.StatusOK {
				t.Errorf("the page answered %d and the JSON route %d %v", answer.StatusCode, status, decoded)
			}
			if page, json := networkRows(t, pageSide), networkRows(t, jsonSide); page != json || page == pageBefore {
				t.Errorf("the page and the JSON route did not make the same change:\npage:\n%s\njson:\n%s", page, json)
			}
		})
	}
}

// TestTheInterfacesJSONCarriesEveryFigureOfThePage is C-AC9's figures: every figure the page draws
// is in the JSON answer, on interfaces whose networks count, are removed, overlap and are no
// longer detected.
func TestTheInterfacesJSONCarriesEveryFigureOfThePage(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	seeded := interfacestest.SeedBundleNetwork(h, 2)
	for _, edit := range []map[string]any{
		{"interface": seeded.Inside[0], "action": "add", "network": interfacestest.ExampleWideV4().String()},
		{"interface": seeded.Inside[1], "action": "remove", "network": interfacestest.ExampleNetworkV6(1).String()},
		{"interface": seeded.Inside[1], "action": "link_local", "link_local": true},
	} {
		if status, answer := settingstest.SendJSON(h, interfaces.PathAPIInterfaceNetworks, edit); status != http.StatusOK {
			t.Fatalf("the edit %v answered %d %v", edit, status, answer)
		}
	}
	figures, err := interfaces.PageFigures(t.Context(), h.Server)
	if err != nil {
		t.Fatalf("reading the page's figures: %v", err)
	}
	answer := settingstest.GetJSON(h, interfaces.PathAPIInterfaceNetworks)
	yesNo := func(value any) string {
		if value == true {
			return "state.yes"
		}
		return "state.no"
	}
	expected := map[string][]string{}
	for _, raw := range settingstest.Path(answer, "interfaces").([]any) {
		id := strconv.FormatInt(int64(settingstest.Path(raw, "id").(float64)), 10)
		lines := []string{"description " + settingstest.Path(raw, "description").(string),
			"identifier " + settingstest.Path(raw, "identifier").(string), "device " + settingstest.Path(raw, "device").(string),
			"upstream " + yesNo(settingstest.Path(raw, "upstream"))}
		if settingstest.Path(raw, "link_local") == true {
			lines = append(lines, "link_local yes")
		} else {
			lines = append(lines, "link_local no")
		}
		for _, network := range settingstest.Path(raw, "networks").([]any) {
			reason := ""
			if code, ok := settingstest.Path(network, "not_counting_reason").(string); ok {
				reason = "state.counts." + code
			}
			lines = append(lines, fmt.Sprintf("network %s state.origin.%s removed %s counts %s reason %s",
				settingstest.Path(network, "network"), settingstest.Path(network, "origin"),
				yesNo(settingstest.Path(network, "removed")), yesNo(settingstest.Path(network, "counts")), reason))
		}
		for _, overlap := range settingstest.Path(raw, "overlaps").([]any) {
			lines = append(lines, fmt.Sprintf("overlap %s %s %s", settingstest.Path(overlap, "network"),
				settingstest.Path(overlap, "other"), settingstest.Path(overlap, "other_description")))
		}
		expected[id] = lines
	}
	if len(figures) != 3 {
		t.Fatalf("the page draws %d interfaces, want 3", len(figures))
	}
	for id, lines := range figures {
		if fmt.Sprint(lines) != fmt.Sprint(expected[id]) {
			t.Errorf("interface %s: the page draws\n%v\nand the answer carries\n%v", id, lines, expected[id])
		}
	}
}
