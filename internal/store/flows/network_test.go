package flows_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestNetworksAreDetectedWithoutTheLinkLocalPrefix is AC6, its first part, on the store's
// side: a link-local prefix offered as detected is never stored.
func TestNetworksAreDetectedWithoutTheLinkLocalPrefix(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 2)
	ctx := context.Background()
	if err := network.DB.DetectNetworks(ctx, network.Inside[0].ID, storetest.Prefixes(t,
		"192.0.2.129/25", "2001:db8:5::/64", "fe80::/64", "fe80::1/10"), network.Now); err != nil {
		t.Fatalf("detecting: %v", err)
	}
	networks, err := network.DB.InterfaceNetworks(ctx)
	if err != nil {
		t.Fatalf("reading the networks: %v", err)
	}
	found := map[string]bool{}
	for _, stored := range networks {
		if stored.InterfaceID == network.Inside[0].ID {
			found[stored.Network.String()] = stored.Origin == flows.NetworkOriginDetected
		}
		if flows.OverlapsLinkLocal(stored.Network) {
			t.Errorf("the link-local network %s was stored", stored.Network)
		}
	}
	for _, want := range []string{"192.0.2.128/25", "2001:db8:5::/64"} {
		if !found[want] {
			t.Errorf("the detected network %s is not stored as detected: %v", want, found)
		}
	}
}
