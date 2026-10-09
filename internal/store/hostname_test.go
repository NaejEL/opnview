package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// The host names the resolver logs as a lookup's client: AC14 of
// specs/SPEC-step-5a-live-corrections.md, causes H1 to H4 of finding F4. H6, the retry
// across a restart, is a collector's matter and is in internal/collect.

// leaseUnder writes one active lease under a host name, valid around the network's now.
func leaseUnder(t *testing.T, network *testNetwork, address, hostname string, expires *int64) {
	t.Helper()
	ctx := context.Background()
	provider, err := network.db.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	name := hostname
	if err := network.db.InsertDHCPLease(ctx, provider, DHCPLease{
		Backend: "dnsmasq", Address: address, Hostname: &name, LeaseState: "active",
		GenerationKey: GenerationKeyOf(nil, expires, network.now), ExpiresAt: expires,
		ObservedAt: network.now,
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
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			expires := network.now + 3600
			cases := []struct{ lease, logged string }{
				{"example-alpha.example.invalid", "example-alpha"},                  // H1
				{"example-beta.", "example-beta"},                                   // H2, the lease's dot
				{"Example-Gamma.Example.Invalid.", "example-gamma.example.invalid"}, // H2, case and dot
				{"example-delta", "EXAMPLE-DELTA."},                                 // H2, the logged dot
			}
			for index, one := range cases {
				client := network.clients[index%len(network.clients)]
				if index >= len(network.clients) {
					t.Fatal("the network has too few clients for the cases")
				}
				// A generation of its own: the network already leases some clients until now + 3600.
				valid := expires + int64(100+index)
				leaseUnder(t, network, client.address, one.lease, &valid)
				address, resolution, err := network.db.ResolveLeaseHostname(ctx, one.logged, network.now)
				if err != nil {
					t.Fatalf("resolving %s: %v", one.logged, err)
				}
				if resolution != ClientResolutionLeased || address != client.address {
					t.Errorf("%q against a lease under %q resolved to %q as %s, not %s", one.logged, one.lease,
						address, resolution, client.address)
				}
			}
			// Matching stays one address or none: a label two leases carry is ambiguous.
			twinOne, twinTwo := expires+200, expires+201
			leaseUnder(t, network, network.clients[0].address, "example-twin.one.invalid", &twinOne)
			leaseUnder(t, network, network.clients[1].address, "example-twin.two.invalid", &twinTwo)
			if _, resolution, err := network.db.ResolveLeaseHostname(ctx, "example-twin", network.now); err != nil ||
				resolution != ClientResolutionAmbiguous {
				t.Errorf("a label two addresses lease under resolved as %s, %v", resolution, err)
			}
		})
	}
}

// TestLocalhostIsThisFirewallAndNotAnUnknownHost is H4: the querier named `localhost` is
// this firewall, whatever the leases hold.
func TestLocalhostIsThisFirewallAndNotAnUnknownHost(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 3)
	ctx := context.Background()
	for _, logged := range []string{"localhost", "LOCALHOST.", "localhost."} {
		address, resolution, err := network.db.ResolveLeaseHostname(ctx, logged, network.now)
		if err != nil {
			t.Fatalf("resolving %s: %v", logged, err)
		}
		if resolution != ClientResolutionThisFirewall || address != logged {
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
	network := newTestNetwork(t, 2, 3)
	ctx := context.Background()
	leaseUnder(t, network, network.clients[0].address, "example-forever", nil)
	for _, at := range []int64{network.now - 30*86400, network.now, network.now + 30*86400} {
		address, resolution, err := network.db.ResolveLeaseHostname(ctx, "example-forever.example.invalid", at)
		if err != nil || resolution != ClientResolutionLeased || address != network.clients[0].address {
			t.Errorf("at %d a lease with no expiry resolved to %q as %s, %v", at-network.now, address, resolution, err)
		}
	}
}

// TestALookupIngestedBeforeAnyLeasePassIsExaminedByTheFirstOne is H6 at the store: the
// lease pass examines every lookup no pass has examined, whatever instant it is given --
// the collector's restart passes its own start -- and marks it, so the next pass does not.
func TestALookupIngestedBeforeAnyLeasePassIsExaminedByTheFirstOne(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 3)
	ctx := context.Background()
	logged := "example-restart.example.invalid"
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-restart", ClientAddress: logged, ClientHostname: &logged,
		ClientResolution: ClientResolutionUnknown, Domain: "example.invalid", Resolver: "unbound",
		Action: "pass", LookedUpAt: network.now - 600, IngestedAt: network.now - 590,
	}); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}
	expires := network.now + 3700
	leaseUnder(t, network, network.clients[2].address, "example-restart", &expires)
	// A lease pass after a restart: the point it is given is later than the lookup's
	// ingestion.
	result, err := network.db.ResolveLoggedHostnames(ctx, network.now, network.now)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if result.Examined != 1 || len(result.Addresses) != 1 || result.Addresses[0] != network.clients[2].address {
		t.Errorf("the first pass after a restart examined %d lookups and resolved %v", result.Examined,
			result.Addresses)
	}
	if resolved := queryInt(t, network.db, `SELECT count(*) FROM dns_resolution
		WHERE lookup_key = 'example-restart' AND client_resolution = 'lease_hostname'`); resolved != 1 {
		t.Error("the lookup stored before the restart is still unresolved")
	}
}

// TestTheHostNameRetryReadsAnIndexAndNoGrowingTable is AC14's last point: the retry
// statement is an index search over the lookups waiting, never a scan.
func TestTheHostNameRetryReadsAnIndexAndNoGrowingTable(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	text, err := Statement("unresolved_hostname_lookups")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := database.DB().Query("EXPLAIN QUERY PLAN "+text, sql.Named("ingested_before", referenceNow))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	defer func() { _ = rows.Close() }()
	searched := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail) >= 4 && detail[:4] == "SCAN" && containsWord(detail, "dns_resolution") {
			t.Errorf("the retry scans dns_resolution: %s", detail)
		}
		if containsWord(detail, "idx_dns_resolution_hostname_unexamined") {
			searched = true
		}
	}
	if !searched {
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
