package interfaces_test

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/surfaces/interfaces"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/interfacestest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The Interfaces surface's JSON edits (C-AC9): every edit made, or refused with nothing changed.

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

// TestTheInterfacesJSONEditsChangeOrRefuse is C-AC9 for the edits: each edit is applied, or refused
// with a code and nothing changed.
func TestTheInterfacesJSONEditsChangeOrRefuse(t *testing.T) {
	t.Parallel()
	for _, input := range []struct {
		what                       string
		action, network            string
		upstream, unknown, refused bool
		jsonLinkLocal              any
	}{
		{what: "add an operator network", action: "add", network: interfacestest.ExampleOperatorV4(1).String()},
		{what: "confirm a detected network", action: "confirm", network: interfacestest.ExampleNetworkV4(0).String()},
		{what: "remove a detected network", action: "remove", network: interfacestest.ExampleNetworkV4(0).String()},
		{what: "turn the link-local rule on", action: "link_local", jsonLinkLocal: true},
		{what: "add a network with host bits", action: "add", network: netip.PrefixFrom(interfacestest.ExampleOperatorV4(1).Addr().Next(), 28).String(), refused: true},
		{what: "add an IPv4-mapped network", action: "add", network: netip.PrefixFrom(netip.AddrFrom16(interfacestest.ExampleOperatorV4(1).Addr().As16()), 124).String(), refused: true},
		{what: "add something that is no network", action: "add", network: "inside", refused: true},
		{what: "remove a network the interface does not carry", action: "remove",
			network: interfacestest.ExampleOperatorV4(3).String(), refused: true},
		{what: "an action that is none", action: "rename", refused: true},
		{what: "a link-local rule that is no rule", action: "link_local",
			jsonLinkLocal: "maybe", refused: true},
		{what: "an edit of the upstream interface", action: "add", network: interfacestest.ExampleOperatorV4(1).String(),
			upstream: true, refused: true},
		{what: "an edit of an interface that does not exist", action: "add",
			network: interfacestest.ExampleOperatorV4(1).String(), unknown: true, refused: true},
	} {
		t.Run(input.what, func(t *testing.T) {
			t.Parallel()
			jsonSide := collectiontest.SignedInHarness(t)
			jsonNetwork := interfacestest.SeedBundleNetwork(jsonSide, 1)
			target := func(seeded interfacestest.BundleNetwork) int64 {
				switch {
				case input.upstream:
					return seeded.Upstream
				case input.unknown:
					return 999999
				}
				return seeded.Inside[0]
			}
			jsonBefore := networkRows(t, jsonSide)
			body := map[string]any{"interface": target(jsonNetwork), "action": input.action, "network": input.network}
			if input.jsonLinkLocal != nil {
				body["link_local"] = input.jsonLinkLocal
			}
			status, decoded := settingstest.SendJSON(jsonSide, interfaces.PathAPIInterfaceNetworks, body)
			if input.refused {
				if status != http.StatusBadRequest || decoded["condition"] == nil {
					t.Errorf("answered %d %v rather than refusing", status, decoded)
				}
				if networkRows(t, jsonSide) != jsonBefore {
					t.Error("a refused edit changed a row")
				}
				return
			}
			if status != http.StatusOK {
				t.Errorf("answered %d %v rather than applying the edit", status, decoded)
			}
			if networkRows(t, jsonSide) == jsonBefore {
				t.Error("the edit changed nothing")
			}
		})
	}
}
