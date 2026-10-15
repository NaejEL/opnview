package collect

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The dual-stack rule through the collector: AC37 of specs/SPEC-step-5b-widget-api.md, from
// the query report's `family` field to the stored resolution. The store half, with the
// restart and the late evidence, is internal/store/dualstack_test.go.

// TestALoggedHostNameWithAnAAndAnAAAAIsResolvedByTheReportedFamily: a host override with
// an A and an AAAA record, and a lookup logged under its name, resolve to the address of
// the family the row reports, and to dual_stack_hostname when the row reports none.
func TestALoggedHostNameWithAnAAndAnAAAAIsResolvedByTheReportedFamily(t *testing.T) {
	t.Parallel()
	const (
		hostname = "example-dual.example.test"
		v4       = "192.0.2.60"
		v6       = "2001:db8::60"
	)
	for _, testCase := range []struct {
		name, family, wantResolution, wantAddress string
		restart                                   bool
	}{
		{"the IPv4 family", "ip4", vocab.ClientResolutionLocalData, v4, false},
		{"the IPv6 family", "ip6", vocab.ClientResolutionLocalData, v6, false},
		{"the IPv6 family, decided after a restart", "ip6", vocab.ClientResolutionLocalData, v6, true},
		{"a unix socket", "unix", vocab.ClientResolutionDualStack, hostname, false},
		{"no family field", "", vocab.ClientResolutionDualStack, hostname, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := arrangeRecordCollection(t)
			ctx := context.Background()
			if err := harness.collector.probeDNSLookup(ctx); err != nil {
				t.Fatalf("probing the resolver: %v", err)
			}
			// The local data is read after the lookup, so the lookup waits for it, across a
			// restart where the case says so.
			setClock(harness, referenceInstant().Add(-5*time.Minute))
			answerRecords(harness.fake, opnsense.UnboundListLocalData,
				localDataRows("other-host.example.test", "192.0.2.61"))
			if err := harness.collector.CollectResolverLocalData(ctx); err != nil {
				t.Fatalf("the first local-data pass: %v", err)
			}
			setClock(harness, referenceInstant())
			row := lookupRow(hostname, "example-site.example.com", referenceEpoch()-120)
			if testCase.family != "" {
				row["family"] = testCase.family
			}
			answerLookups(harness.fake, row)
			if err := harness.collector.CollectDNSLookup(ctx); err != nil {
				t.Fatalf("collecting the lookup: %v", err)
			}

			collector := harness.collector
			if testCase.restart {
				collector = newTestCollector(t, providertest.NewClient(t, harness.fake), harness.store,
					newFixedClock(referenceInstant().Add(time.Hour)))
			} else {
				setClock(harness, referenceInstant().Add(5*time.Minute))
			}
			answerRecords(harness.fake, opnsense.UnboundListLocalData, []map[string]any{
				localDataRows(hostname, v4)[0], localDataRows(hostname, v6)[0]})
			if err := collector.CollectResolverLocalData(ctx); err != nil {
				t.Fatalf("the local-data pass: %v", err)
			}
			resolution, address := resolutionOf(t, harness.store, hostname)
			if resolution != testCase.wantResolution || address != testCase.wantAddress {
				t.Errorf("the lookup is %s at %q; want %s at %q", resolution, address,
					testCase.wantResolution, testCase.wantAddress)
			}
		})
	}
}
