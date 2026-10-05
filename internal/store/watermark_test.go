package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The third iteration of the step-5A corrections: the purge watermark enforced by the
// insert itself, the documented family count, and the bound on re-resolving host names.

// TestARecordOlderThanThePurgeWatermarkIsRefusedByTheInsertItself: the check that a
// record is not older than what the purge has removed is part of the insert statement, so
// a purge that commits at any moment before an insert -- after the collector read the
// page, after it checked the digest -- is seen by that insert. Both the flow and the
// lookup inserts obey it, and a record at or after the watermark is stored as before.
func TestARecordOlderThanThePurgeWatermarkIsRefusedByTheInsertItself(t *testing.T) {
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	if err := network.db.SetSetting(ctx, "retention_seconds", "600", network.now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	if err := network.db.Purge(ctx, network.now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	watermark := network.now - 600
	client := network.clients[0]
	network.insert(t, []networkFlow{
		outboundFlow(client, watermark-1, 50, 1, 0),
		outboundFlow(client, watermark, 70, 2, 0),
	})
	if got := queryInt(t, network.db, "SELECT coalesce(sum(packet_bytes), 0) FROM flow"); got != 70 {
		t.Errorf("flow holds %d bytes: the record older than the watermark was stored, or the other was not", got)
	}
	for index, at := range []int64{watermark - 1, watermark} {
		if err := network.db.InsertDNSResolution(ctx, DNSResolution{
			LookupKey: fmt.Sprintf("example-watermark-%d", index), ClientAddress: client.address,
			Domain: "example.invalid", Resolver: "unbound", Action: "pass",
			LookedUpAt: at, IngestedAt: network.now,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	if got := queryInt(t, network.db, "SELECT count(*) FROM dns_resolution"); got != 1 {
		t.Errorf("%d lookups are stored, not the one at the watermark", got)
	}
}

// TestTheDataModelCountsTheAggregateFamiliesTheSchemaHas: the document's count of the
// aggregate tables and families is the schema's.
func TestTheDataModelCountsTheAggregateFamiliesTheSchemaHas(t *testing.T) {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	tables := regexp.MustCompile(`(?m)^CREATE TABLE IF NOT EXISTS (\w*volume_aggregate)_(1h|24h|7d|30d) \(`).
		FindAllStringSubmatch(string(schema), -1)
	families := map[string]bool{}
	for _, table := range tables {
		families[table[1]] = true
	}
	words := map[int]string{4: "four", 5: "five", 6: "six", 7: "seven", 20: "twenty",
		24: "twenty-four", 28: "twenty-eight"}
	tableWord, familyWord := words[len(tables)], words[len(families)]
	if tableWord == "" || familyWord == "" {
		t.Fatalf("the schema has %d aggregate tables in %d families, which this test cannot spell",
			len(tables), len(families))
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "data-model.md"))
	if err != nil {
		t.Fatalf("reading the data model: %v", err)
	}
	text := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	stale := regexp.MustCompile(`\b(\w+(?:-\w+)?) (?:materialised tables|aggregates|aggregate tables) in (\w+) families` +
		`|\b(\w+(?:-\w+)?) aggregates [-—–]+ (\w+) families`)
	found := 0
	for _, match := range stale.FindAllStringSubmatch(text, -1) {
		tablesSaid, familiesSaid := match[1], match[2]
		if tablesSaid == "" {
			tablesSaid, familiesSaid = match[3], match[4]
		}
		found++
		if tablesSaid != tableWord || familiesSaid != familyWord {
			t.Errorf("the data model says %q; the schema has %s aggregate tables in %s families",
				match[0], tableWord, familyWord)
		}
	}
	if found < 2 {
		t.Errorf("the data model states the aggregate count %d times, not in the family section and the purge", found)
	}
	if heading := fmt.Sprintf("the aggregate families — %s keys", familyWord); !strings.Contains(text, heading) {
		t.Errorf("the family section's heading does not read %q", heading)
	}
}

// TestReResolvingHostNamesIsBoundedToTheLookupsIngestedSince: a lease pass resolves again
// only the unresolved host-name lookups ingested since the previous one began, so its work
// does not grow with the unresolved lookups that accumulate -- a host whose name no lease
// ever carries -- while a lookup read before the lease that names it is still resolved.
func TestReResolvingHostNamesIsBoundedToTheLookupsIngestedSince(t *testing.T) {
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	since := network.now - 600
	for index := 0; index < 50; index++ {
		hostname := fmt.Sprintf("example-static-%d.example.invalid", index)
		if err := network.db.InsertDNSResolution(ctx, DNSResolution{
			LookupKey: fmt.Sprintf("example-old-%d", index), ClientAddress: hostname,
			ClientHostname: &hostname, ClientResolution: ClientResolutionUnknown,
			Domain: "example.invalid", Resolver: "unbound", Action: "pass",
			LookedUpAt: since - 3600, IngestedAt: since - 3000,
		}); err != nil {
			t.Fatalf("writing an old lookup: %v", err)
		}
	}
	recent := "example-late.example.invalid"
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-recent", ClientAddress: recent, ClientHostname: &recent,
		ClientResolution: ClientResolutionUnknown, Domain: "example.invalid", Resolver: "unbound",
		Action: "pass", LookedUpAt: network.now - 120, IngestedAt: network.now - 100,
	}); err != nil {
		t.Fatalf("writing the recent lookup: %v", err)
	}
	provider, _ := network.db.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	hostname := "example-late"
	if err := network.db.InsertDHCPLease(ctx, provider, DHCPLease{
		Backend: "dnsmasq", Address: network.clients[1].address, Hostname: &hostname,
		LeaseState: "reserved", GenerationKey: GenerationKeyOf(nil, nil, network.now), ObservedAt: network.now,
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}

	result, err := network.db.ResolveLoggedHostnames(ctx, since)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if result.Examined != 1 {
		t.Errorf("the pass examined %d lookups; only the one ingested since the previous pass is due", result.Examined)
	}
	if len(result.Addresses) != 1 || result.Addresses[0] != network.clients[1].address {
		t.Errorf("the late-leased lookup resolved to %v, not %s", result.Addresses, network.clients[1].address)
	}
	if resolved := queryInt(t, network.db, `SELECT count(*) FROM dns_resolution
		WHERE lookup_key = 'example-recent' AND client_resolution = 'lease_hostname'`); resolved != 1 {
		t.Error("the lookup read before its lease was not resolved")
	}
}
