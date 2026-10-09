package store

import (
	"context"
	"fmt"
	"testing"
)

// Site-name attribution: AC29, AC32 and AC33.

// attributionCase is one flow and the lookups around it.
type attributionCase struct {
	name    string
	lookups []caseLookup
	// eastWest sends the flow to a client of another interface rather than outside.
	eastWest bool
	maxDelay int64
	// want is the attributed domain, or "" for no row; delay the expected delay.
	want  string
	delay int64
}

type caseLookup struct {
	secondsBefore int64
	domain        string
	action        string
	source        string
}

// TestAttributionFollowsTheRuleExactly is AC29, each outcome on its own flow.
func TestAttributionFollowsTheRuleExactly(t *testing.T) {
	t.Parallel()
	cases := []attributionCase{
		{name: "one eligible domain in the window", maxDelay: 5, want: "one.example.invalid", delay: 3,
			lookups: []caseLookup{{3, "one.example.invalid", "pass", "Recursion"}}},
		{name: "two lookups of the same domain", maxDelay: 5, want: "same.example.invalid", delay: 1,
			lookups: []caseLookup{{4, "same.example.invalid", "pass", "Cache"},
				{1, "same.example.invalid", "pass", "Recursion"}}},
		{name: "two distinct domains", maxDelay: 5,
			lookups: []caseLookup{{2, "first.example.invalid", "pass", "Recursion"},
				{1, "second.example.invalid", "pass", "Recursion"}}},
		{name: "a blocked lookup", maxDelay: 5,
			lookups: []caseLookup{{2, "blocked.example.invalid", "block", "Local"}}},
		{name: "an east-west flow", maxDelay: 5, eastWest: true,
			lookups: []caseLookup{{2, "inside.example.invalid", "pass", "Recursion"}}},
		{name: "a host-override lookup", maxDelay: 5, want: "override.example.invalid", delay: 2,
			lookups: []caseLookup{{2, "override.example.invalid", "pass", "Local-data"}}},
		{name: "a lookup six seconds before, at the default", maxDelay: 5,
			lookups: []caseLookup{{6, "early.example.invalid", "pass", "Recursion"}}},
		{name: "a lookup six seconds before, with the setting at six", maxDelay: 6,
			want: "early.example.invalid", delay: 6,
			lookups: []caseLookup{{6, "early.example.invalid", "pass", "Recursion"}}},
	}
	for index, testCase := range cases {
		for _, v6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (IPv6 %t)", testCase.name, v6), func(t *testing.T) {
				network := newTestNetwork(t, 2, 4)
				ctx := context.Background()
				var client, other testClient
				for _, candidate := range network.clients {
					if candidate.v6 != v6 {
						continue
					}
					if client.address == "" {
						client = candidate
					} else if candidate.interfaceID != client.interfaceID || other.address == "" {
						other = candidate
					}
				}
				destination := outside(index, v6)
				if testCase.eastWest {
					destination = other.address
				}
				observed := network.now - 100
				port := int64(443)
				addresses := network.insert(t, []networkFlow{{observedAt: observed, device: client.device,
					direction: "in", src: client.address, dst: destination, dstPort: &port,
					protocol: "tcp", action: "pass", bytes: 100}})
				for number, lookup := range testCase.lookups {
					if err := network.db.InsertDNSResolution(ctx, DNSResolution{
						LookupKey:     fmt.Sprintf("example-case-lookup-%d", number),
						ClientAddress: client.address, Domain: lookup.domain, Resolver: "unbound",
						Action: lookup.action, AnswerSource: ptr(lookup.source),
						LookedUpAt: observed - lookup.secondsBefore, IngestedAt: network.now,
					}); err != nil {
						t.Fatalf("writing a lookup: %v", err)
					}
				}
				if _, err := network.db.Reclassify(ctx, append(addresses, client.address), network.now); err != nil {
					t.Fatalf("classifying: %v", err)
				}
				if _, err := network.db.Attribute(ctx, observed-60, observed+60, testCase.maxDelay, network.now); err != nil {
					t.Fatalf("attributing: %v", err)
				}
				rows, err := network.db.DB().Query(`SELECT site_name, correlation_delay_seconds
					FROM domain_attribution`)
				if err != nil {
					t.Fatalf("reading the attribution: %v", err)
				}
				var got []string
				var delay int64
				for rows.Next() {
					var site string
					if err := rows.Scan(&site, &delay); err != nil {
						t.Fatalf("reading the attribution: %v", err)
					}
					got = append(got, site)
				}
				_ = rows.Close()
				switch {
				case testCase.want == "" && len(got) != 0:
					t.Errorf("an attribution was written: %v", got)
				case testCase.want != "" && (len(got) != 1 || got[0] != testCase.want):
					t.Errorf("the attribution is %v, not %s", got, testCase.want)
				case testCase.want != "" && delay != testCase.delay:
					t.Errorf("the delay is %d, not %d", delay, testCase.delay)
				}
			})
		}
	}
}

// TestAttributionIsStoredWhateverTheAggregateModeSays is AC32: the mode withholds names at the
// API, and the store writes them in both modes.
func TestAttributionIsStoredWhateverTheAggregateModeSays(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	if err := network.db.SetSetting(ctx, "aggregate_mode", "no_domains", network.now); err != nil {
		t.Fatalf("setting the mode: %v", err)
	}
	client := network.clients[0]
	port := int64(443)
	addresses := network.insert(t, []networkFlow{{observedAt: network.now - 50, device: client.device,
		direction: "in", src: client.address, dst: outside(1, client.v6), dstPort: &port,
		protocol: "tcp", action: "pass", bytes: 100}})
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-mode-lookup", ClientAddress: client.address, Domain: "mode.example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: ptr("Recursion"),
		LookedUpAt: network.now - 52, IngestedAt: network.now,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	network.classifyAndRefresh(t, addresses)
	result, err := network.db.Attribute(ctx, network.now-3600, network.now, 5, network.now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, network.now, network.now, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); rows != 1 {
		t.Errorf("with no_domains set, %d attributions were written", rows)
	}
	for _, period := range Periods() {
		if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_volume_aggregate_"+period.Name+
			" WHERE site_name = 'mode.example.invalid'"); rows != 1 {
			t.Errorf("with no_domains set, the %s domain family holds %d rows for the name", period.Name, rows)
		}
	}
}

// TestPurgingALookupOrAFlowRemovesItsAttribution is AC33.
func TestPurgingALookupOrAFlowRemovesItsAttribution(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	client := network.clients[0]
	port := int64(443)
	addresses := network.insert(t, []networkFlow{
		{observedAt: network.now - 50, device: client.device, direction: "in", src: client.address,
			dst: outside(1, client.v6), dstPort: &port, protocol: "tcp", action: "pass", bytes: 100},
		{observedAt: network.now - 500, device: client.device, direction: "in", src: client.address,
			dst: outside(2, client.v6), dstPort: &port, protocol: "tcp", action: "pass", bytes: 100},
	})
	for index, instant := range []int64{network.now - 51, network.now - 501} {
		if err := network.db.InsertDNSResolution(ctx, DNSResolution{
			LookupKey: fmt.Sprintf("example-purge-lookup-%d", index), ClientAddress: client.address,
			Domain: fmt.Sprintf("purge-%d.example.invalid", index), Resolver: "unbound", Action: "pass",
			AnswerSource: ptr("Recursion"), LookedUpAt: instant, IngestedAt: network.now,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	network.classifyAndRefresh(t, addresses)
	if _, err := network.db.Attribute(ctx, network.now-3600, network.now, 5, network.now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); rows != 2 {
		t.Fatalf("%d attributions before the purge, not 2", rows)
	}
	if _, err := network.db.DB().Exec("PRAGMA foreign_keys = ON; DELETE FROM dns_resolution WHERE lookup_key = 'example-purge-lookup-0'"); err != nil {
		t.Fatalf("purging a lookup: %v", err)
	}
	if _, err := network.db.DB().Exec("DELETE FROM flow WHERE observed_at = ?", network.now-500); err != nil {
		t.Fatalf("purging a flow: %v", err)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); rows != 0 {
		t.Errorf("%d attributions survived their lookup or their flow", rows)
	}
	if violations := foreignKeyViolations(t, network.db); violations != 0 {
		t.Errorf("PRAGMA foreign_key_check reports %d rows", violations)
	}

	// And through the retention purge itself, on an attribution that is still there when the
	// purge runs: the surviving flow is named again from a fresh lookup first.
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-purge-lookup-again", ClientAddress: client.address,
		Domain: "purge-again.example.invalid", Resolver: "unbound", Action: "pass",
		AnswerSource: ptr("Recursion"), LookedUpAt: network.now - 52, IngestedAt: network.now,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	if _, err := network.db.Attribute(ctx, network.now-3600, network.now, 5, network.now); err != nil {
		t.Fatalf("attributing again: %v", err)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); rows != 1 {
		t.Fatalf("%d attributions before the retention purge, not 1", rows)
	}
	if _, err := network.db.DB().Exec("UPDATE setting SET value = '60' WHERE key = 'retention_seconds'"); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}
	if err := network.db.Purge(ctx, network.now+3600); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM flow"); rows != 0 {
		t.Fatalf("%d flows survived a horizon they are all older than", rows)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); rows != 0 {
		t.Errorf("%d attributions survived the retention purge of their flow and lookup", rows)
	}
	if violations := foreignKeyViolations(t, network.db); violations != 0 {
		t.Errorf("after the retention purge, PRAGMA foreign_key_check reports %d rows", violations)
	}
}
