package store

import (
	"context"
	"fmt"
	"testing"
)

// Defect L2 of specs/SPEC-test-budget-and-two-live-defects.md: the ARP and NDP neighbour
// tables list this firewall's own interface entries, and each became a MAC-level `client`
// at an address the firewall itself holds. The collector no longer mints them
// (internal/collect); these are the store's half -- recognising such an address as of an
// instant, and purging the clients an earlier build minted.

// TestAnAddressThisFirewallHoldsIsRecognisedAsOfTheNeighbourEntrysInstant: what the
// neighbour pass asks before it creates a client. Both families, loopback included, and
// decided as of the instant: an address the firewall held only before it is not this
// firewall now.
func TestAnAddressThisFirewallHoldsIsRecognisedAsOfTheNeighbourEntrysInstant(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 7}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			for _, iface := range network.inside {
				for _, address := range []string{iface.address, iface.v6} {
					held, err := network.db.IsThisFirewallAt(ctx, address, network.now)
					if err != nil || !held {
						t.Errorf("%s, held on interface %d, is this firewall %t (%v)",
							address, iface.id, held, err)
					}
				}
			}
			for _, address := range []string{"127.0.0.1", "::1"} {
				if held, err := network.db.IsThisFirewallAt(ctx, address, network.now); err != nil || !held {
					t.Errorf("the loopback %s is this firewall %t (%v)", address, held, err)
				}
			}
			for _, client := range network.clients {
				if held, err := network.db.IsThisFirewallAt(ctx, client.address, network.now); err != nil || held {
					t.Errorf("the client address %s is this firewall %t (%v)", client.address, held, err)
				}
			}
			// The holding begins at the interface's first discovery; an instant a whole
			// discovery interval before it is outside it.
			margin, err := heldMargin(ctx, network.db.DB())
			if err != nil {
				t.Fatalf("reading the margin: %v", err)
			}
			before := network.now - 86400 - margin - 1
			if held, err := network.db.IsThisFirewallAt(ctx, network.inside[0].address, before); err != nil || held {
				t.Errorf("an address is this firewall %t (%v) before the firewall held it", held, err)
			}
		})
	}
}

// TestTheClientsMintedAtThisFirewallsAddressesArePurgedAtTheNextFullPlacement is AC6's
// second point. A database holding the MAC-level clients the neighbour tables minted at
// the firewall's own addresses, in both families, with nothing naming them, loses them at
// the next ReclassifyAll. A client at such an address that something still names, or
// that a person assigned to an owner, is kept, as is a MAC-level client elsewhere that
// nothing names: the purge removes the defect's rows and nothing else.
func TestTheClientsMintedAtThisFirewallsAddressesArePurgedAtTheNextFullPlacement(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {4, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			mint := func(index int, address string, interfaceID int64) int64 {
				t.Helper()
				mac := fmt.Sprintf("0a:11:22:33:%02x:%02x", 0xf0+index/256, index%256)
				id, err := network.db.UpsertClient(ctx, Client{
					Identity:    ClientIdentity{Kind: IdentityMAC, Key: mac},
					InterfaceID: &interfaceID, MAC: &mac, LastAddress: &address,
				}, network.now-60)
				if err != nil {
					t.Fatalf("minting the client at %s: %v", address, err)
				}
				return id
			}

			var minted []int64
			for index, iface := range network.inside {
				minted = append(minted, mint(2*index, iface.address, iface.id),
					mint(2*index+1, iface.v6, iface.id))
			}
			// One the store must keep because a person assigned it, and one because a
			// flow names it.
			if _, err := network.db.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
				VALUES (1, 'example owner', 0, 0)`); err != nil {
				t.Fatalf("writing an owner: %v", err)
			}
			if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0
				WHERE id = ?`, minted[0]); err != nil {
				t.Fatalf("assigning the owner: %v", err)
			}
			named := minted[1]
			port := int64(53)
			network.insert(t, []networkFlow{{observedAt: network.now - 30, device: network.inside[0].device,
				direction: "in", src: network.clients[0].address, dst: "198.51.100.9", dstPort: &port,
				protocol: "udp", action: "pass", bytes: 64}})
			if _, err := network.db.DB().Exec(`UPDATE flow SET src_client_id = ?`, named); err != nil {
				t.Fatalf("naming the client from a flow: %v", err)
			}
			// A MAC-level client nothing names, at an address the firewall never held.
			elsewhere := mint(999, network.clients[len(network.clients)-1].address,
				network.clients[len(network.clients)-1].interfaceID)

			if _, err := network.db.ReclassifyAll(ctx, network.now); err != nil {
				t.Fatalf("reclassifying everything: %v", err)
			}
			for _, id := range minted[2:] {
				if left := queryInt(t, network.db, "SELECT count(*) FROM client WHERE id = ?", id); left != 0 {
					t.Errorf("the client %d minted at a firewall address survived", id)
				}
			}
			for _, kept := range []int64{minted[0], named, elsewhere} {
				if left := queryInt(t, network.db, "SELECT count(*) FROM client WHERE id = ?", kept); left != 1 {
					t.Errorf("the client %d, which the purge must keep, is gone", kept)
				}
			}
			if violations := foreignKeyViolations(t, network.db); violations != 0 {
				t.Errorf("%d foreign-key violations after the purge", violations)
			}
			// And a second full placement writes nothing more.
			if _, err := network.db.ReclassifyAll(ctx, network.now); err != nil {
				t.Fatalf("reclassifying again: %v", err)
			}
			if purged, err := network.db.PurgeUnreferencedClients(ctx); err != nil || purged != 0 {
				t.Errorf("a second purge removed %d clients (%v)", purged, err)
			}
		})
	}
}
