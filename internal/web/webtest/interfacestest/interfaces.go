package interfacestest

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// ExampleNetworkV4 is the index-th /28 of the second RFC 5737 range.
func ExampleNetworkV4(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, byte(16 * (index % 16))}), 28)
}

// ExampleWideV4 is the whole second RFC 5737 range, which overlaps every /28 above.
func ExampleWideV4() netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, 0}), 24)
}

// ExampleNetworkV6 is the index-th /48 of the RFC 3849 range.
func ExampleNetworkV6(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, byte(index + 1)}), 48)
}

// ExampleOperatorV4 is a /28 of the third RFC 5737 range, which no detection uses.
func ExampleOperatorV4(index int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(16 * (index % 16))}), 28)
}

// BundleNetwork is a database holding one upstream interface and inside interfaces.
type BundleNetwork struct {
	Upstream int64
	Inside   []int64
}

// SeedBundleNetwork writes one upstream interface and insideCount inside interfaces,
// each inside one carrying one detected network of each family.
func SeedBundleNetwork(h *webtest.Harness, insideCount int) BundleNetwork {
	h.T.Helper()
	ctx := context.Background()
	now := h.Clock.Now().Unix()
	var network BundleNetwork
	upstream, err := h.Store.UpsertInterface(ctx, vocab.Interface{
		Identifier: "example_upstream", Device: "exupdev0", Description: "upstream.example",
		LinkType: "example-link", LinkKind: "other", IsUpstream: true,
	}, now)
	if err != nil {
		h.T.Fatalf("writing the upstream interface: %v", err)
	}
	network.Upstream = upstream
	for index := 0; index < insideCount; index++ {
		id, err := h.Store.UpsertInterface(ctx, vocab.Interface{
			Identifier:  fmt.Sprintf("example_if_%d", index),
			Device:      fmt.Sprintf("exdev%d", index),
			Description: fmt.Sprintf("inside-%d.example", index),
			LinkType:    "example-link", LinkKind: "other",
		}, now)
		if err != nil {
			h.T.Fatalf("writing an inside interface: %v", err)
		}
		if err := h.Store.DetectNetworks(ctx, id,
			[]netip.Prefix{ExampleNetworkV4(index), ExampleNetworkV6(index)}, now); err != nil {
			h.T.Fatalf("recording the detected networks: %v", err)
		}
		network.Inside = append(network.Inside, id)
	}
	return network
}

// NetworkOf returns one stored network, failing when there is none.
func NetworkOf(h *webtest.Harness, id int64, prefix netip.Prefix) flows.InterfaceNetwork {
	h.T.Helper()
	networks, err := h.Store.InterfaceNetworks(context.Background())
	if err != nil {
		h.T.Fatalf("reading the networks: %v", err)
	}
	for _, network := range networks {
		if network.InterfaceID == id && network.Network == prefix {
			return network
		}
	}
	h.T.Fatalf("interface %d carries no network %s", id, prefix)
	return flows.InterfaceNetwork{}
}

// Fingerprint is the store's on-link fingerprint.
func Fingerprint(h *webtest.Harness) string {
	h.T.Helper()
	value, err := h.Store.OnLinkFingerprint(context.Background())
	if err != nil {
		h.T.Fatalf("reading the on-link fingerprint: %v", err)
	}
	return value
}

// NetworkRowCount counts the interface_network rows.
func NetworkRowCount(h *webtest.Harness) int {
	h.T.Helper()
	count, err := h.Store.CountRows(context.Background(), "interface_network")
	if err != nil {
		h.T.Fatalf("counting the networks: %v", err)
	}
	return count
}
