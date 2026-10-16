package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The host names the resolver logs as a lookup's client: AC14 of
// specs/SPEC-step-5a-live-corrections.md, causes H1 to H4 of finding F4. H6, the retry
// across a restart, is a collector's matter and is in internal/collect.

// leaseUnder writes one active lease under a host name, valid around the network's now.
func leaseUnder(t *testing.T, network *storetest.Network, address, hostname string, expires *int64) {
	t.Helper()
	ctx := context.Background()
	provider, err := network.DB.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	name := hostname
	if err := network.DB.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: address, Hostname: &name, LeaseState: "active",
		GenerationKey: vocab.GenerationKeyOf(nil, expires, network.Now), ExpiresAt: expires,
		ObservedAt: network.Now,
	}); err != nil {
		t.Fatalf("writing a lease: %v", err)
	}
}

// TestALoggedHostNameMatchesALeaseByFirstLabelOnBothSides is H1 and H2: a bare logged
// label matches a lease carrying the domain, and a trailing dot or a difference of case
// on either side does not stop a match.
func TestALoggedHostNameMatchesALeaseByFirstLabelOnBothSides(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 7}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			expires := network.Now + 3600
			cases := []struct{ lease, logged string }{
				{"example-alpha.example.invalid", "example-alpha"},                  // H1
				{"example-beta.", "example-beta"},                                   // H2, the lease's dot
				{"Example-Gamma.Example.Invalid.", "example-gamma.example.invalid"}, // H2, case and dot
				{"example-delta", "EXAMPLE-DELTA."},                                 // H2, the logged dot
			}
			for index, one := range cases {
				client := network.Clients[index%len(network.Clients)]
				if index >= len(network.Clients) {
					t.Fatal("the network has too few clients for the cases")
				}
				// A generation of its own: the network already leases some clients until now + 3600.
				valid := expires + int64(100+index)
				leaseUnder(t, network, client.Address, one.lease, &valid)
				address, resolution, err := network.DB.ResolveLeaseHostname(ctx, one.logged, network.Now)
				if err != nil {
					t.Fatalf("resolving %s: %v", one.logged, err)
				}
				if resolution != vocab.ClientResolutionLeased || address != client.Address {
					t.Errorf("%q against a lease under %q resolved to %q as %s, not %s", one.logged, one.lease,
						address, resolution, client.Address)
				}
			}
			// Matching stays one address or none: a label two leases carry is ambiguous.
			twinOne, twinTwo := expires+200, expires+201
			leaseUnder(t, network, network.Clients[0].Address, "example-twin.one.invalid", &twinOne)
			leaseUnder(t, network, network.Clients[1].Address, "example-twin.two.invalid", &twinTwo)
			if _, resolution, err := network.DB.ResolveLeaseHostname(ctx, "example-twin", network.Now); err != nil ||
				resolution != vocab.ClientResolutionAmbiguous {
				t.Errorf("a label two addresses lease under resolved as %s, %v", resolution, err)
			}
		})
	}
}

// TestLocalhostIsThisFirewallAndNotAnUnknownHost is H4: the querier named `localhost` is
// this firewall, whatever the leases hold.
func TestLocalhostIsThisFirewallAndNotAnUnknownHost(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 3)
	ctx := context.Background()
	for _, logged := range []string{"localhost", "LOCALHOST.", "localhost."} {
		address, resolution, err := network.DB.ResolveLeaseHostname(ctx, logged, network.Now)
		if err != nil {
			t.Fatalf("resolving %s: %v", logged, err)
		}
		if resolution != vocab.ClientResolutionThisFirewall || address != logged {
			t.Errorf("%q resolved to %q as %s; it names this firewall", logged, address, resolution)
		}
	}
}

// TestALeaseWithNoExpiryCoversEveryInstant is H3 as the code admits it: a lease the
// backend reported with an expiry of 0 -- an infinite lease -- is stored with no expiry,
// and no expiry is no bound. The 5A code already decoded it so (decode.EpochPointer
// treats 0 as absent); this pins it.
func TestALeaseWithNoExpiryCoversEveryInstant(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 3)
	ctx := context.Background()
	leaseUnder(t, network, network.Clients[0].Address, "example-forever", nil)
	for _, at := range []int64{network.Now - 30*86400, network.Now, network.Now + 30*86400} {
		address, resolution, err := network.DB.ResolveLeaseHostname(ctx, "example-forever.example.invalid", at)
		if err != nil || resolution != vocab.ClientResolutionLeased || address != network.Clients[0].Address {
			t.Errorf("at %d a lease with no expiry resolved to %q as %s, %v", at-network.Now, address, resolution, err)
		}
	}
}

// TestALookupIngestedBeforeAnyLeasePassIsExaminedByTheFirstOne is H6 at the store: the
// lease pass examines every lookup no pass has examined, whatever instant it is given --
// the collector's restart passes its own start -- and marks it, so the next pass does not.
func TestALookupIngestedBeforeAnyLeasePassIsExaminedByTheFirstOne(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 3)
	ctx := context.Background()
	logged := "example-restart.example.invalid"
	if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
		LookupKey: "example-restart", ClientAddress: logged, ClientHostname: &logged,
		ClientResolution: vocab.ClientResolutionUnknown, Domain: "example.invalid", Resolver: "unbound",
		Action: "pass", LookedUpAt: network.Now - 600, IngestedAt: network.Now - 590,
	}); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}
	expires := network.Now + 3700
	leaseUnder(t, network, network.Clients[2].Address, "example-restart", &expires)
	// A lease pass after a restart: the point it is given is later than the lookup's
	// ingestion.
	result, err := network.DB.ResolveLoggedHostnames(ctx, network.Now, network.Now)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if result.Examined != 1 || len(result.Addresses) != 1 || result.Addresses[0] != network.Clients[2].Address {
		t.Errorf("the first pass after a restart examined %d lookups and resolved %v", result.Examined,
			result.Addresses)
	}
	if resolved := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM dns_resolution
		WHERE lookup_key = 'example-restart' AND client_resolution = 'lease_hostname'`); resolved != 1 {
		t.Error("the lookup stored before the restart is still unresolved")
	}
}

// TestTheHostNameRetryReadsAnIndexAndNoGrowingTable is AC14's last point: the retry
// statement is an index search over the lookups waiting, never a scan.
func TestTheHostNameRetryReadsAnIndexAndNoGrowingTable(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	text, err := sqldb.Statement("unresolved_hostname_lookups")
	if err != nil {
		t.Fatal(err)
	}
	plan := storetest.PlanOf(t, database, text, sql.Named("ingested_before", storetest.ReferenceNow))
	if storetest.Scans(plan, "dns_resolution") {
		t.Errorf("the retry scans dns_resolution: %+v", plan)
	}
	if !storetest.UsesIndex(plan, "idx_dns_resolution_hostname_unexamined", "") {
		t.Error("the retry does not read the partial index of the lookups waiting")
	}
}

func containsWord(text, word string) bool {
	for index := 0; index+len(word) <= len(text); index++ {
		if text[index:index+len(word)] == word {
			return true
		}
	}
	return false
}
