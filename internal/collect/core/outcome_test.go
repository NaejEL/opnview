package core_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestAStoreFailureInTheRecordPassIsRecorded extends B8g to the resolver-record passes, the
// cache and the local data: the resolver answers with a record and storing it fails, so the
// provider reads unavailable with store_failure, and the detail the next probe round keeps
// is the store failure's, not a successful read's.
func TestAStoreFailureInTheRecordPassIsRecorded(t *testing.T) {
	t.Parallel()
	for _, read := range []struct {
		endpoint opnsense.Endpoint
		kind     string
		collect  func(*collect.Collector, context.Context) error
	}{
		{collecttest.Declared("unbound", "dump_cache"), "resolver_cache", (*collect.Collector).CollectResolverCache},
		{collecttest.Declared("unbound", "list_local_data"), "resolver_local_data", (*collect.Collector).CollectResolverLocalData},
	} {
		harness := collecttest.ArrangeRecordCollection(t)
		ctx := context.Background()
		harness.Fake.Answer(read.endpoint, http.StatusOK, []byte(`{"status":"ok","data":[{"host":`+
			`"edge.example.net.","name":"edge.example.net.","ttl":"60","type":"IN","rrtype":"A",`+
			`"value":"192.0.2.10"}]}`))
		collecttest.PlantInsertFailure(t, harness.Store, "resource_record_observation")

		if err := read.collect(harness.Collector, ctx); err == nil {
			t.Errorf("%s: a read whose records could not be stored reported no failure", read.kind)
		}
		state, _, _ := harness.AvailabilityOf(t, read.kind, "unbound")
		if state != vocab.StateUnavailable {
			t.Errorf("%s: the resolver whose records could not be stored reads %q", read.kind, state)
		}
		if got := harness.OutcomeOf(t, read.kind, "unbound"); got != vocab.OutcomeStoreFailure {
			t.Errorf("%s: the outcome is %q, not %q", read.kind, got, vocab.OutcomeStoreFailure)
		}
		providerID, err := harness.Store.ProviderID(ctx, read.kind, "unbound")
		if err != nil {
			t.Fatal(err)
		}
		if kept := harness.Collector.ReadDetail(providerID); !strings.Contains(kept, "could not be stored") {
			t.Errorf("%s: the detail the probe round keeps is %q, not the store failure", read.kind, kept)
		}
	}
}
