package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
				network := storetest.NewNetwork(t, 2, 4)
				ctx := context.Background()
				var client, other storetest.Client
				for _, candidate := range network.Clients {
					if candidate.V6 != v6 {
						continue
					}
					if client.Address == "" {
						client = candidate
					} else if candidate.InterfaceID != client.InterfaceID || other.Address == "" {
						other = candidate
					}
				}
				destination := storetest.Outside(index, v6)
				if testCase.eastWest {
					destination = other.Address
				}
				observed := network.Now - 100
				port := int64(443)
				addresses := network.Insert(t, []storetest.Flow{{ObservedAt: observed, Device: client.Device,
					Direction: "in", Src: client.Address, Dst: destination, DstPort: &port,
					Protocol: "tcp", Action: "pass", Bytes: 100}})
				for number, lookup := range testCase.lookups {
					if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
						LookupKey:     fmt.Sprintf("example-case-lookup-%d", number),
						ClientAddress: client.Address, Domain: lookup.domain, Resolver: "unbound",
						Action: lookup.action, AnswerSource: storetest.Ptr(lookup.source),
						LookedUpAt: observed - lookup.secondsBefore, IngestedAt: network.Now,
					}); err != nil {
						t.Fatalf("writing a lookup: %v", err)
					}
				}
				if _, err := network.DB.Reclassify(ctx, append(addresses, client.Address), network.Now); err != nil {
					t.Fatalf("classifying: %v", err)
				}
				if _, err := network.DB.Attribute(ctx, observed-60, observed+60, testCase.maxDelay, network.Now); err != nil {
					t.Fatalf("attributing: %v", err)
				}
				rows, err := network.DB.DB().Query(`SELECT site_name, correlation_delay_seconds
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	if err := network.DB.SetSetting(ctx, "aggregate_mode", "no_domains", network.Now); err != nil {
		t.Fatalf("setting the mode: %v", err)
	}
	client := network.Clients[0]
	port := int64(443)
	addresses := network.Insert(t, []storetest.Flow{{ObservedAt: network.Now - 50, Device: client.Device,
		Direction: "in", Src: client.Address, Dst: storetest.Outside(1, client.V6), DstPort: &port,
		Protocol: "tcp", Action: "pass", Bytes: 100}})
	if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
		LookupKey: "example-mode-lookup", ClientAddress: client.Address, Domain: "mode.example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: storetest.Ptr("Recursion"),
		LookedUpAt: network.Now - 52, IngestedAt: network.Now,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	network.ClassifyAndRefresh(t, addresses)
	result, err := network.DB.Attribute(ctx, network.Now-3600, network.Now, 5, network.Now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if _, err := network.DB.RefreshAggregates(ctx, network.Now, network.Now, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); rows != 1 {
		t.Errorf("with no_domains set, %d attributions were written", rows)
	}
	for _, period := range vocab.Periods() {
		if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_volume_aggregate_"+period.Name+
			" WHERE site_name = 'mode.example.invalid'"); rows != 1 {
			t.Errorf("with no_domains set, the %s domain family holds %d rows for the name", period.Name, rows)
		}
	}
}

// TestPurgingALookupOrAFlowRemovesItsAttribution is AC33.
func TestPurgingALookupOrAFlowRemovesItsAttribution(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	client := network.Clients[0]
	port := int64(443)
	addresses := network.Insert(t, []storetest.Flow{
		{ObservedAt: network.Now - 50, Device: client.Device, Direction: "in", Src: client.Address,
			Dst: storetest.Outside(1, client.V6), DstPort: &port, Protocol: "tcp", Action: "pass", Bytes: 100},
		{ObservedAt: network.Now - 500, Device: client.Device, Direction: "in", Src: client.Address,
			Dst: storetest.Outside(2, client.V6), DstPort: &port, Protocol: "tcp", Action: "pass", Bytes: 100},
	})
	for index, instant := range []int64{network.Now - 51, network.Now - 501} {
		if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
			LookupKey: fmt.Sprintf("example-purge-lookup-%d", index), ClientAddress: client.Address,
			Domain: fmt.Sprintf("purge-%d.example.invalid", index), Resolver: "unbound", Action: "pass",
			AnswerSource: storetest.Ptr("Recursion"), LookedUpAt: instant, IngestedAt: network.Now,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	network.ClassifyAndRefresh(t, addresses)
	if _, err := network.DB.Attribute(ctx, network.Now-3600, network.Now, 5, network.Now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); rows != 2 {
		t.Fatalf("%d attributions before the purge, not 2", rows)
	}
	if _, err := network.DB.DB().Exec("DELETE FROM dns_resolution WHERE lookup_key = 'example-purge-lookup-0'"); err != nil {
		t.Fatalf("purging a lookup: %v", err)
	}
	if _, err := network.DB.DB().Exec("DELETE FROM flow WHERE observed_at = ?", network.Now-500); err != nil {
		t.Fatalf("purging a flow: %v", err)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); rows != 0 {
		t.Errorf("%d attributions survived their lookup or their flow", rows)
	}
	if violations := storetest.ForeignKeyViolations(t, network.DB); violations != 0 {
		t.Errorf("%d rows name a row that does not exist", violations)
	}

	// And through the retention purge itself, on an attribution that is still there when the
	// purge runs: the surviving flow is named again from a fresh lookup first.
	if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
		LookupKey: "example-purge-lookup-again", ClientAddress: client.Address,
		Domain: "purge-again.example.invalid", Resolver: "unbound", Action: "pass",
		AnswerSource: storetest.Ptr("Recursion"), LookedUpAt: network.Now - 52, IngestedAt: network.Now,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
	if _, err := network.DB.Attribute(ctx, network.Now-3600, network.Now, 5, network.Now); err != nil {
		t.Fatalf("attributing again: %v", err)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); rows != 1 {
		t.Fatalf("%d attributions before the retention purge, not 1", rows)
	}
	if _, err := network.DB.DB().Exec("UPDATE setting SET value = '60' WHERE key = 'retention_seconds'"); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}
	if err := network.DB.Purge(ctx, network.Now+3600); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow"); rows != 0 {
		t.Fatalf("%d flows survived a horizon they are all older than", rows)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM domain_attribution"); rows != 0 {
		t.Errorf("%d attributions survived the retention purge of their flow and lookup", rows)
	}
	if violations := storetest.ForeignKeyViolations(t, network.DB); violations != 0 {
		t.Errorf("after the retention purge, %d rows name a row that does not exist", violations)
	}
}
