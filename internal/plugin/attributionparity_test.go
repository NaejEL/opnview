package plugin_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// THE PARITY OF THE UNBOUND PLUGIN'S ATTRIBUTION WITH THE GO IT REPLACES (ADR 0019 §4, ADR 0020 wave 4).
// One network, one set of flows, lookups and resolver records; the Go of internal/store/attribution
// attributed them once, before it was deleted, and its result is recorded in attributionGolden (the
// commit that recorded it ran both and compared them). The derivation must keep giving it: every attribution by the flow it
// names, the lookup, the site, the method and the observation, read by what they are.

const attributionGolden = "testdata/attribution_parity.golden"

// attributionFixture stores, beside the network's flows, the lookups and the records the two
// methods decide from: exact evidence through an A record, through a CNAME chain and through the
// local data; a timing attribution; a timing refused by the answers' family; two names in the
// window; and a flow nothing names.
func attributionFixture(t *testing.T, network *storetest.Network) {
	t.Helper()
	ctx := context.Background()
	specs := network.FlowsFor(60, 7200)
	network.Insert(t, specs)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		if _, err := network.DB.DB().ExecContext(ctx, statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement[:40], err)
		}
	}
	lookup := func(key, client, domain string, at int64, source string) {
		exec(`INSERT INTO plugin_unbound.dns_resolution (lookup_key, client_address, domain, resolver, action, answer_source,
			looked_up_at, ingested_at) VALUES ($1, $2, $3, 'unbound', 'pass', $4, $5, $5)`, key, client, domain, source, at)
	}
	record := func(heldIn, owner, rrtype, value string, address any, label any, from, until int64) {
		exec(`INSERT INTO plugin_unbound.resource_record_observation (provider_id, held_in, owner_name, rrtype, value, address,
			host_label, first_seen_at, last_seen_at, covered_from_at, covered_until_at)
			VALUES ((SELECT id FROM provider WHERE kind = 'resolver_cache' AND provider_key = 'unbound'), $1, $2, $3, $4, $5, $6,
			$7, $8, $7, $8)`, heldIn, owner, rrtype, value, address, label, from, until)
	}
	for index, spec := range specs {
		if index%4 > 1 {
			continue
		}
		name := fmt.Sprintf("name-%d.invalid", index)
		at := spec.ObservedAt - 2
		switch index % 8 {
		case 0:
			// Exact: an A record of the name holds the destination over the interval.
			lookup(fmt.Sprintf("k%d", index), spec.Src, name, at-600, "Recursion")
			record("cache", name, rrtypeOf(spec.Dst), spec.Dst, spec.Dst, nil, at-700, spec.ObservedAt+100)
		case 1:
			// Exact through a CNAME chain: the name points at an alias whose A record holds the destination.
			alias := fmt.Sprintf("alias-%d.invalid", index)
			lookup(fmt.Sprintf("k%d", index), spec.Src, name, at-600, "Recursion")
			record("cache", name, "CNAME", alias, nil, nil, at-700, spec.ObservedAt+100)
			record("cache", alias, rrtypeOf(spec.Dst), spec.Dst, spec.Dst, nil, at-700, spec.ObservedAt+100)
		case 4:
			// Exact through the local data, for a lookup answered from it.
			lookup(fmt.Sprintf("k%d", index), spec.Src, name, at-600, "Local-data")
			record("local_data", name, rrtypeOf(spec.Dst), spec.Dst, spec.Dst, "name-"+fmt.Sprint(index), at-700, spec.ObservedAt+100)
		case 5:
			// Timing: one name in the window, no record.
			lookup(fmt.Sprintf("k%d", index), spec.Src, name, at, "Recursion")
		}
		switch index % 12 {
		case 9:
			// Timing refused: the name's answer is another address of the destination's family.
			lookup(fmt.Sprintf("t%d", index), spec.Src, name, at, "Recursion")
			record("cache", name, rrtypeOf(spec.Dst), storetest.Outside(index+100, strings.Contains(spec.Dst, ":")),
				storetest.Outside(index+100, strings.Contains(spec.Dst, ":")), nil, at-700, spec.ObservedAt+100)
		case 1:
			// Two names in the timing window, exact evidence for neither: unnamed.
			lookup(fmt.Sprintf("u%d", index), spec.Src, fmt.Sprintf("other-%d.invalid", index), at-1, "Recursion")
		}
	}
}

func rrtypeOf(address string) string {
	if strings.Contains(address, ":") {
		return "AAAA"
	}
	return "A"
}

// attributionSnapshot renders every attribution by what it names.
func attributionSnapshot(t *testing.T, database *store.Store) string {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(), `
		SELECT f.src_address || ' ' || f.dst_address || ' ' || f.observed_at, a.site_name, a.method, a.correlation_delay_seconds,
		       r.lookup_key, coalesce(o.owner_name || '/' || o.rrtype || '/' || o.value, '-')
		FROM plugin_unbound.domain_attribution AS a
		JOIN plugin_pf.flow AS f ON f.id = a.flow_id
		JOIN plugin_unbound.dns_resolution AS r ON r.id = a.dns_resolution_id
		LEFT JOIN plugin_unbound.resource_record_observation AS o ON o.id = a.address_observation_id`)
	if err != nil {
		t.Fatalf("reading the attributions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var flow, site, method, key, observation string
		var delay int64
		if err := rows.Scan(&flow, &site, &method, &delay, &key, &observation); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Join([]string{flow, site, method, fmt.Sprint(delay), key, observation}, " | "))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestTheUnboundAttributionNamesFlowsAsTheGoDid(t *testing.T) {
	ctx := context.Background()
	network := storetest.NewNetwork(t, 2, 4)
	attributionFixture(t, network)
	loaded, err := plugin.Load(ctx, plugin.Options{Store: network.DB, DerivationParameters: defaultDerivationParameters})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Fatalf("a built-in was refused: %v", refusal)
	}
	classifyWith(loaded)(t)

	recorded, err := os.ReadFile(attributionGolden)
	if err != nil {
		t.Fatalf("the recorded result of the Go: %v", err)
	}
	want := strings.TrimSuffix(string(recorded), "\n")

	unbound, found := loaded.Plugin("unbound")
	if !found {
		t.Fatal("the unbound plugin did not load")
	}
	outcome, err := unbound.RunDerivation(ctx, nil, "attribute", network.Now)
	if err != nil {
		t.Fatalf("the derivation: %v", err)
	}
	if !outcome.Ran {
		t.Fatal("the derivation did not run")
	}
	got := attributionSnapshot(t, network.DB)
	if got != want {
		t.Errorf("the SQL attributed differently from the Go:\n--- Go ---\n%s\n--- SQL ---\n%s", want, got)
	}
	if got == "" {
		t.Error("nothing was attributed: the fixture proves nothing")
	}
	// A second run over the same evidence changes nothing.
	later := network.Now + 1
	if _, err := unbound.RunDerivation(ctx, nil, "attribute", later); err != nil {
		t.Fatalf("the second run: %v", err)
	}
	if changed := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM plugin_pf.flow WHERE changed_at = $1", later); changed != 0 {
		t.Errorf("the second run over unchanged evidence changed %d flows", changed)
	}
}
