package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The dual-stack rule: AC37 of specs/SPEC-step-5b-widget-api.md, at the store. Each outcome
// is checked three ways -- decided at ingest, decided at ingest and examined again after
// the database is reopened, and decided by the retry when the local data and the leases
// arrive after the lookup, across a restart -- because the rule is applied at ingest and
// again by ResolveLoggedHostnames, and the evidence it reads, the lookup's querying family,
// is stored on the lookup so both apply it the same way.
//
// Names are under RFC 2606's reserved domains, addresses are documentation addresses
// (RFC 5737, RFC 3849) through outside(), and both families are exercised.

// dualStackCase is one outcome of the rule.
type dualStackCase struct {
	name string
	// family is the lookup's reported querying family, or 0 for none.
	family int64
	// records are the local data held for the host.
	records []attribution.ResourceRecord
	// leaseAddress, when set, is an address leased under the host name.
	leaseAddress   string
	wantResolution string
	// wantAddress is the client address the lookup ends with.
	wantAddress string
}

// The logged host names: what the query report gives in place of the querying address.
const (
	dualStackHost = "example-dual.example.test"
	twinFourHost  = "example-twin.example.test"
)

func dualStackCases() []dualStackCase {
	dual := []attribution.ResourceRecord{
		localAddressRecord(dualStackHost, outside(31, false)),
		localAddressRecord(dualStackHost, outside(31, true)),
	}
	return []dualStackCase{
		{"IPv4 family reported resolves to the A address", 4, dual, "",
			vocab.ClientResolutionLocalData, outside(31, false)},
		{"IPv6 family reported resolves to the AAAA address", 6, dual, "",
			vocab.ClientResolutionLocalData, outside(31, true)},
		{"no family reported is dual_stack_hostname", 0, dual, "",
			vocab.ClientResolutionDualStack, dualStackHost},
		{"two A records stay ambiguous whatever the family", 4, []attribution.ResourceRecord{
			localAddressRecord(twinFourHost, outside(32, false)),
			localAddressRecord(twinFourHost, outside(33, false)),
		}, "", vocab.ClientResolutionAmbiguous, twinFourHost},
		{"a lease wins over the local data", 6, dual, "192.0.2.35",
			vocab.ClientResolutionLeased, "192.0.2.35"},
	}
}

// loggedHost is the host name a case's lookup logged.
func (c dualStackCase) loggedHost() string {
	if c.wantResolution == vocab.ClientResolutionAmbiguous {
		return twinFourHost
	}
	return dualStackHost
}

func (c dualStackCase) familyPointer() *int64 {
	if c.family == 0 {
		return nil
	}
	family := c.family
	return &family
}

// leaseHostname writes one lease of an address under a host name, valid from long before
// to long after referenceNow.
func leaseHostname(t *testing.T, database *Store, address, hostname string) {
	t.Helper()
	ctx := context.Background()
	provider, err := database.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	name := hostname
	expires := referenceNow + 86400
	if err := database.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: address, Hostname: &name, LeaseState: "active",
		GenerationKey: vocab.GenerationKeyOf(nil, &expires, referenceNow-86400), ExpiresAt: &expires,
		ObservedAt: referenceNow - 86400,
	}); err != nil {
		t.Fatalf("writing a lease: %v", err)
	}
}

// storeEvidence writes a case's local data, polled twice around the lookup, and its lease.
func storeEvidence(t *testing.T, database *Store, c dualStackCase) {
	t.Helper()
	pollLocalData(t, database, referenceNow-600, c.records...)
	pollLocalData(t, database, referenceNow+600, c.records...)
	if c.leaseAddress != "" {
		leaseHostname(t, database, c.leaseAddress, c.loggedHost())
	}
}

// storeHostLookup writes one lookup logged under a host name, resolved as at ingest.
func storeHostLookup(t *testing.T, database *Store, c dualStackCase) {
	t.Helper()
	ctx := context.Background()
	logged := c.loggedHost()
	address, resolution, err := database.ResolveLoggedHostname(ctx, logged, c.familyPointer(), referenceNow)
	if err != nil {
		t.Fatalf("resolving %s: %v", logged, err)
	}
	if err := database.InsertDNSResolution(ctx, vocab.DNSResolution{
		LookupKey: "dual-stack-" + logged, ClientAddress: address, ClientHostname: &logged,
		ClientResolution: resolution, ClientFamily: c.familyPointer(), Domain: "example-site.example.com",
		Resolver: "unbound", Action: "pass", LookedUpAt: referenceNow, IngestedAt: referenceNow + 2,
	}); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}
}

// storedResolution reads the lookup's resolution, client address and family.
func storedResolution(t *testing.T, database *Store, c dualStackCase) (string, string, string) {
	t.Helper()
	var (
		resolution, address string
		family              *int64
	)
	if err := database.DB().QueryRow(`SELECT client_resolution, client_address, client_family
		FROM dns_resolution WHERE client_hostname = ?`, c.loggedHost()).Scan(&resolution, &address,
		&family); err != nil {
		t.Fatalf("reading the lookup: %v", err)
	}
	stored := "none"
	if family != nil {
		stored = fmt.Sprint(*family)
	}
	return resolution, address, stored
}

// reopen closes a database and opens the same directory again: a restart.
func reopen(t *testing.T, database *Store, directory string) *Store {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}
	reopened, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatalf("reopening the database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

// assertOutcome fails unless the stored lookup carries the case's expected outcome and
// the family it was given.
func assertOutcome(t *testing.T, database *Store, c dualStackCase, when string) {
	t.Helper()
	resolution, address, family := storedResolution(t, database, c)
	if resolution != c.wantResolution || address != c.wantAddress {
		t.Errorf("%s: the lookup is %s at %q; want %s at %q", when, resolution, address,
			c.wantResolution, c.wantAddress)
	}
	wantFamily := "none"
	if c.family != 0 {
		wantFamily = fmt.Sprint(c.family)
	}
	if family != wantFamily {
		t.Errorf("%s: the lookup's querying family is %s, not %s", when, family, wantFamily)
	}
}

// TestADualStackHostNameResolvesByTheQueryingFamilyAtIngest is AC37 at ingest, and the
// same after a restart: the decision is stored, and a pass examining the lookup again
// on the reopened database decides the same from the family stored with it.
func TestADualStackHostNameResolvesByTheQueryingFamilyAtIngest(t *testing.T) {
	t.Parallel()
	for _, testCase := range dualStackCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			database, directory := openTestStore(t)
			storeEvidence(t, database, testCase)
			storeHostLookup(t, database, testCase)
			assertOutcome(t, database, testCase, "at ingest")

			reopened := reopen(t, database, directory)
			assertOutcome(t, reopened, testCase, "after a restart")
			if _, err := reopened.ResolveLoggedHostnames(context.Background(), referenceNow+3600,
				referenceNow+3600); err != nil {
				t.Fatalf("examining the lookups again: %v", err)
			}
			assertOutcome(t, reopened, testCase, "examined again after a restart")
		})
	}
}

// TestADualStackHostNameIsResolvedWhenTheEvidenceArrivesLater is AC37 when the lookup is
// stored before any local data or lease names its host, the service restarts, and the
// local data and the lease arrive afterwards: the retry resolves it from the family
// stored with the lookup, through the partial index that keeps it waiting.
func TestADualStackHostNameIsResolvedWhenTheEvidenceArrivesLater(t *testing.T) {
	t.Parallel()
	for _, testCase := range dualStackCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			database, directory := openTestStore(t)
			storeHostLookup(t, database, testCase)
			if resolution, _, _ := storedResolution(t, database, testCase); resolution != vocab.ClientResolutionUnknown {
				t.Fatalf("with no evidence yet the lookup is %s, not %s", resolution, vocab.ClientResolutionUnknown)
			}
			reopened := reopen(t, database, directory)
			storeEvidence(t, reopened, testCase)
			result, err := reopened.ResolveLoggedHostnames(context.Background(), referenceNow+3600,
				referenceNow+3600)
			if err != nil {
				t.Fatalf("examining the lookups: %v", err)
			}
			if result.Examined != 1 {
				t.Errorf("the pass examined %d lookups, not the one waiting", result.Examined)
			}
			assertOutcome(t, reopened, testCase, "when the evidence arrived later")
		})
	}
}

// TestADualStackLookupWaitsForALeaseThatArrivesLater: a lookup decided dual_stack_hostname
// at ingest is still examined by the next pass, so a lease read after it wins, as a
// lease does over the local data.
func TestADualStackLookupWaitsForALeaseThatArrivesLater(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	noFamily := dualStackCases()[2]
	storeEvidence(t, database, noFamily)
	storeHostLookup(t, database, noFamily)
	assertOutcome(t, database, noFamily, "at ingest")

	reopened := reopen(t, database, directory)
	leaseHostname(t, reopened, "192.0.2.36", dualStackHost)
	if _, err := reopened.ResolveLoggedHostnames(context.Background(), referenceNow+3600,
		referenceNow+3600); err != nil {
		t.Fatalf("examining the lookups: %v", err)
	}
	leased := noFamily
	leased.wantResolution, leased.wantAddress = vocab.ClientResolutionLeased, "192.0.2.36"
	assertOutcome(t, reopened, leased, "after a lease arrived")
}

// TestTwoAddressesUnderTwoNamesAreNotADualStackPair: one A and one AAAA that share a first
// label but no name are two hosts, not one host override with two records, so the
// family does not choose between them.
func TestTwoAddressesUnderTwoNamesAreNotADualStackPair(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	split := []attribution.ResourceRecord{
		localAddressRecord("example-split.one.example.test", outside(41, false)),
		localAddressRecord("example-split.two.example.test", outside(41, true)),
	}
	pollLocalData(t, database, referenceNow-600, split...)
	pollLocalData(t, database, referenceNow+600, split...)
	family := int64(4)
	address, resolution, err := database.ResolveLoggedHostname(ctx, "example-split", &family, referenceNow)
	if err != nil {
		t.Fatal(err)
	}
	if resolution != vocab.ClientResolutionAmbiguous || address != "example-split" {
		t.Errorf("two names under one label resolved to %q as %s, not ambiguous", address, resolution)
	}
}

// TestTheQueryingFamilyOutsideFourAndSixIsRefused: the column holds a family, and nothing
// else.
func TestTheQueryingFamilyOutsideFourAndSixIsRefused(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	family := int64(5)
	logged := dualStackHost
	if err := database.InsertDNSResolution(context.Background(), vocab.DNSResolution{
		LookupKey: "dual-stack-bad-family", ClientAddress: logged, ClientHostname: &logged,
		ClientResolution: vocab.ClientResolutionUnknown, ClientFamily: &family, Domain: "example.com",
		Resolver: "unbound", Action: "pass", LookedUpAt: referenceNow, IngestedAt: referenceNow,
	}); err == nil {
		t.Error("a querying family of 5 was stored")
	}
}
