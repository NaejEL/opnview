package store

import (
	"context"
	"testing"
)

// The lease tests that belong to the dhcp_lease kind becoming concurrent.
//
// THE DEPLOYMENT: one DHCP server issuing on one VLAN and another on a second, two scopes with
// no overlap. It is ordinary, not exotic, and on the maintainer's own firewall one of the two
// servers is disabled while the other serves — so a mixed estate is the thing the model has to
// carry. What follows is the part of that claim the store is responsible for; the part the
// identity cascade is responsible for is in internal/collect.

// leaseProviders returns the ids of the two DHCP servers the schema registers.
func leaseProviders(t *testing.T, database *Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	first, err := database.ProviderID(ctx, "dhcp_lease", "kea")
	if err != nil {
		t.Fatalf("looking up the first DHCP server: %v", err)
	}
	second, err := database.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the second DHCP server: %v", err)
	}
	return first, second
}

// TestOneAddressLeasedByTwoServersIsTwoLeases is the direct consequence of the provider joining
// a lease's identity.
//
// Two servers on two scopes can hand out the same address, and those are two leases rather than
// one contested row. The identity used to be (address, generation_key, backend), which allowed
// this too — but only by accident, for as long as one backend meant one server. `backend` is a
// normalised vocabulary of response shapes, not an identity for a server, and the concurrency
// rule is stated in terms of the provider.
func TestOneAddressLeasedByTwoServersIsTwoLeases(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	first, second := leaseProviders(t, database)

	startsAt := int64(1750000000)
	lease := DHCPLease{
		Backend: "kea", Address: "example-contested-address", LeaseState: "active",
		StartsAt: &startsAt, GenerationKey: GenerationKeyOf(&startsAt, nil, startsAt),
		ObservedAt: startsAt,
	}
	for _, provider := range []int64{first, second} {
		if err := database.InsertDHCPLease(ctx, provider, lease); err != nil {
			t.Fatalf("writing the lease of server %d: %v", provider, err)
		}
	}
	if stored := count(t, database, "dhcp_lease"); stored != 2 {
		t.Fatalf("one address leased by two servers stored %d rows, want 2", stored)
	}

	// And one server still cannot report it twice: the generation key is doing its job, and
	// adding the provider to the identity did not loosen the idempotence it guarantees.
	for pass := 0; pass < 3; pass++ {
		if err := database.InsertDHCPLease(ctx, first, lease); err != nil {
			t.Fatalf("re-polling server %d: %v", first, err)
		}
	}
	if stored := count(t, database, "dhcp_lease"); stored != 2 {
		t.Fatalf("re-polling one server stored %d rows, want the same 2", stored)
	}
}

// TestARePollOfALeaseWithNoValidityStartIsStillIdempotentUnderTwoServers is the trap the two
// changes could have made between them.
//
// A backend that reports no validity start stores NULL, and a null in a uniqueness constraint is
// distinct from every other null — so the identity rests on the generation key, not on the
// start. Adding the provider to that identity must not have reintroduced the problem from the
// other side: two servers each re-polling a start-less lease is still two rows in total.
func TestARePollOfALeaseWithNoValidityStartIsStillIdempotentUnderTwoServers(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	first, second := leaseProviders(t, database)

	expiresAt := int64(1750003600)
	lease := DHCPLease{
		Backend: "dnsmasq", Address: "example-startless-address", LeaseState: "active",
		StartsAt: nil, ExpiresAt: &expiresAt,
		GenerationKey: GenerationKeyOf(nil, &expiresAt, 1750000000),
		ObservedAt:    1750000000,
	}
	for pass := 0; pass < 3; pass++ {
		for _, provider := range []int64{first, second} {
			if err := database.InsertDHCPLease(ctx, provider, lease); err != nil {
				t.Fatalf("pass %d of server %d: %v", pass, provider, err)
			}
		}
	}
	if stored := count(t, database, "dhcp_lease"); stored != 2 {
		t.Fatalf("three passes of two servers over one start-less lease stored %d rows, want 2",
			stored)
	}
	var withAStart int
	if err := database.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM dhcp_lease WHERE starts_at IS NOT NULL").Scan(&withAStart); err != nil {
		t.Fatalf("counting leases with a start: %v", err)
	}
	if withAStart != 0 {
		t.Errorf("%d leases carry a validity start their backend never reported", withAStart)
	}
}

// TestALeaseNamesTheServerThatIssuedIt is the reading the maintainer asked for by name.
//
// A machine can now hold leases from two servers, so "which server gave this machine its
// address" is a real question, and the answer has to be READABLE per lease rather than
// recoverable from a uniqueness key. The row carries provider_id, which joins to
// provider.display_name, so a screen prints the server's name without parsing a composite or
// inferring a server from a backend token.
func TestALeaseNamesTheServerThatIssuedIt(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	first, second := leaseProviders(t, database)

	clientID, err := database.UpsertClient(ctx, Client{
		Identity: ClientIdentity{Kind: IdentityMAC, Key: "0a:00:00:00:00:01"},
	}, 1750000000)
	if err != nil {
		t.Fatalf("recording the machine: %v", err)
	}

	for index, provider := range []int64{first, second} {
		startsAt := int64(1750000000 + index)
		if err := database.InsertDHCPLease(ctx, provider, DHCPLease{
			ClientID: &clientID, Backend: "kea",
			Address: "example-address-" + string(rune('a'+index)),
			MAC:     nil, LeaseState: "active", StartsAt: &startsAt,
			GenerationKey: GenerationKeyOf(&startsAt, nil, startsAt),
			ObservedAt:    startsAt,
		}); err != nil {
			t.Fatalf("writing the lease of server %d: %v", provider, err)
		}
	}

	// The read a screen makes: one row per lease, each naming its server in words.
	rows, err := database.DB().QueryContext(ctx,
		`SELECT p.display_name FROM dhcp_lease AS l
		 JOIN provider AS p ON p.id = l.provider_id
		 WHERE l.client_id = ?
		 ORDER BY p.provider_key`, clientID)
	if err != nil {
		t.Fatalf("reading the machine's leases: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var servers []string
	for rows.Next() {
		var server string
		if err := rows.Scan(&server); err != nil {
			t.Fatalf("reading the machine's leases: %v", err)
		}
		if server == "" {
			t.Error("a lease named its server with an empty string")
		}
		servers = append(servers, server)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the machine's leases: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("the machine's leases named %d servers, want 2: %v", len(servers), servers)
	}
	if servers[0] == servers[1] {
		t.Errorf("both leases named the same server, %q", servers[0])
	}
}

// TestALeaseWithNoIssuingServerIsRejected is the other half of "a lease names its server": the
// column is mandatory, because a lease is issued by a server and an unattributed one could not
// answer the question the screen now asks.
func TestALeaseWithNoIssuingServerIsRejected(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	startsAt := int64(1750000000)
	err := database.InsertDHCPLease(context.Background(), 999999999, DHCPLease{
		Backend: "kea", Address: "example-orphan-address", LeaseState: "active",
		StartsAt: &startsAt, GenerationKey: GenerationKeyOf(&startsAt, nil, startsAt),
		ObservedAt: startsAt,
	})
	if err == nil {
		t.Fatal("a lease attributed to a server that does not exist was accepted")
	}
}
