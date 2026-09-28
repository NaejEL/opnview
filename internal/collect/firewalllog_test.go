package collect

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The filter-log collector tests.

// arrangeFilterLogCollection stands up a harness whose filter log is the active source.
func arrangeFilterLogCollection(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.FirewallLog, "firewall_log.json")
	if err := harness.collector.probeFirewallLog(context.Background()); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if key := harness.activeKeyOf(t, KindFirewallLog); key != ProviderPf {
		t.Fatalf("the filter-log kind activated %q", key)
	}
	return harness
}

// TestAFilterLogPassStoresEveryRecordAndResolvesBothJoinKeys is the ingest, and the two
// first-class join keys resolving or reporting that they did not.
func TestAFilterLogPassStoresEveryRecordAndResolvesBothJoinKeys(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	harness.fake.assertEveryPathIsRegistered()

	records := len(fixtureRecords(t, "firewall_log.json"))
	if stored := countRows(t, harness.store, "flow"); stored != records {
		t.Fatalf("stored %d of the %d records in the page", stored, records)
	}

	// The record whose device is absent from the interface map, and the record whose rid
	// matches no discovered rule, are both present with their state rather than dropped.
	notFoundInterfaces := scalarCount(t, harness.store,
		"SELECT count(*) FROM flow WHERE interface_lookup_state = 'not_found'")
	if notFoundInterfaces == 0 {
		t.Error("no flow reports an unmapped device, so the not-found state is untested")
	}
	notFoundRules := scalarCount(t, harness.store,
		"SELECT count(*) FROM flow WHERE rule_lookup_state = 'not_found' AND rid IS NOT NULL")
	if notFoundRules == 0 {
		t.Error("no flow reports a rid that matched no rule, so the not-found state is untested")
	}
	resolvedRules := scalarCount(t, harness.store,
		"SELECT count(*) FROM flow WHERE rule_lookup_state = 'resolved' AND rule_id IS NOT NULL")
	if resolvedRules == 0 {
		t.Error("no flow resolved its rid to a discovered rule, so the join is untested")
	}

	// Both address families reach the table, which is the column no address can be
	// classified without.
	for _, family := range []int64{4, 6} {
		if scalarCount(t, harness.store,
			"SELECT count(*) FROM flow WHERE ip_version = ?", family) == 0 {
			t.Errorf("no flow carries IP version %d", family)
		}
	}

	// An action and a direction outside their vocabularies become unknown rather than
	// being folded into pass or into a definite direction.
	if scalarCount(t, harness.store, "SELECT count(*) FROM flow WHERE action = 'unknown'") == 0 {
		t.Error("the record whose action is outside the vocabulary was not stored as unknown")
	}
	if scalarCount(t, harness.store, "SELECT count(*) FROM flow WHERE direction = 'unknown'") == 0 {
		t.Error("the record whose direction is outside the vocabulary was not stored as unknown")
	}

	// The reason the line was written is kept, which is the only thing that separates a
	// packet a rule denied from one the firewall dropped for a reason no rule expresses.
	if scalarCount(t, harness.store,
		"SELECT count(DISTINCT log_reason) FROM flow WHERE log_reason IS NOT NULL") < 2 {
		t.Error("fewer than two distinct log reasons were stored, so a denial and a drop " +
			"cannot be told apart")
	}
}

// TestAFilterLogPassRepeatedStoresNothingNew is the de-duplication the digest provides.
// The endpoint echoes back records already seen, and every poll re-offers the whole page.
func TestAFilterLogPassRepeatedStoresNothingNew(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	afterOne := countRows(t, harness.store, "flow")
	for pass := 0; pass < 3; pass++ {
		if err := harness.collector.CollectFirewallLog(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if afterTwo := countRows(t, harness.store, "flow"); afterTwo != afterOne {
		t.Fatalf("four passes of one page stored %d rows, up from %d", afterTwo, afterOne)
	}
}

// TestTheCollectorSendsNoDigestBecauseItIsNotACursor is the measured correction to the
// survey's inferred text.
//
// Passing `digest` returned byte-identical output twice on a live firewall and the
// supplied digest did not appear in the response. Sending it anyway would suggest a
// resume guarantee that does not exist, and would hide the gap detection that replaces it.
func TestTheCollectorSendsNoDigestBecauseItIsNotACursor(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	if err := harness.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	requests := harness.fake.requestsTo(opnsense.FirewallLog)
	if len(requests) == 0 {
		t.Fatal("the filter log was never requested")
	}
	for _, request := range requests {
		if request.query.Get("digest") != "" {
			t.Errorf("a poll sent digest=%q, which does nothing and implies a cursor that does "+
				"not exist", request.query.Get("digest"))
		}
		if request.query.Get("limit") == "" {
			t.Error("a poll sent no limit, so its cost is unbounded")
		}
	}
}

// TestAPageThatBeginsAfterTheNewestStoredRecordWritesAGapRow is the detection that
// replaces the cursor.
//
// The page is the most recent N records. If its oldest record is newer than the newest
// record already stored, everything between the two was produced while opnview was not
// looking, and no endpoint can recover it. The gap is detected rather than assumed, and it
// is a row rather than silence.
func TestAPageThatBeginsAfterTheNewestStoredRecordWritesAGapRow(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()

	// A record stored well before the page, standing for a poll that happened and then a
	// period during which opnview was not running.
	oldest := oldestFixtureInstant(t, harness)
	if err := harness.store.InsertFlow(ctx, store.Flow{
		LogDigest:            "example-digest-from-an-earlier-poll",
		ObservedAt:           oldest - 3600,
		IngestedAt:           oldest - 3600,
		InterfaceDevice:      "exdev0",
		InterfaceLookupState: store.LookupResolved,
		SrcAddress:           "example-source", DstAddress: "example-destination",
		Protocol: "tcp", IPVersion: 4, Action: "pass", Direction: "in",
		PacketBytes: 100, RuleLookupState: store.LookupPending,
	}); err != nil {
		t.Fatalf("seeding an earlier record: %v", err)
	}

	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	gaps := countRows(t, harness.store, "collection_gap")
	if gaps != 1 {
		t.Fatalf("the pass wrote %d gap rows, want exactly one", gaps)
	}
	var (
		start, end int64
		reason     string
		detail     *string
	)
	if err := harness.store.DB().QueryRowContext(ctx,
		"SELECT interval_start_at, interval_end_at, reason, detail FROM collection_gap").
		Scan(&start, &end, &reason, &detail); err != nil {
		t.Fatalf("reading the gap back: %v", err)
	}
	if reason != string(store.GapDigestOutsideWindow) {
		t.Errorf("the reason is %q", reason)
	}
	if start != oldest-3600 || end != oldest {
		t.Errorf("the gap covers [%d, %d], want [%d, %d] — the interval between the newest "+
			"stored record and the oldest returned one", start, end, oldest-3600, oldest)
	}
	if detail == nil || !strings.Contains(*detail, "sized for this installation") {
		t.Error("the gap does not say that the page size has to be sized per installation, " +
			"which is the action it calls for")
	}
}

// TestAPageThatOverlapsTheStoredHistoryWritesNoGap is the negative direction, and it is
// the one that would make the gap table useless if it were wrong: a gap row on every
// healthy poll would be noise nobody reads.
func TestAPageThatOverlapsTheStoredHistoryWritesNoGap(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	// The second pass sees the same page, whose oldest record is now inside the stored
	// history.
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if gaps := countRows(t, harness.store, "collection_gap"); gaps != 0 {
		t.Fatalf("a healthy overlapping poll wrote %d gap rows", gaps)
	}
}

// TestEachFilterLogFailureShapeIsRecordedAndNoneIsNoData is the per-source half of the
// outcome table: the collector has to turn each failure into a state, and none of them
// into an absence of traffic.
func TestEachFilterLogFailureShapeIsRecordedAndNoneIsNoData(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantState store.AvailabilityState
		wantRows  int
		because   string
	}{
		{
			name: "an empty body", status: http.StatusOK, body: "",
			wantState: store.StateUnavailable,
			because:   "the survey names an empty body as a backend failure or a wrong verb",
		},
		{
			name: "a failed result", status: http.StatusOK, body: `{"result":"failed"}`,
			wantState: store.StateUnavailable,
			because:   "HTTP 200 is not proof of success",
		},
		{
			name: "HTTP 404", status: http.StatusNotFound, body: `{"errorMessage":"Endpoint not found"}`,
			wantState: store.StateUnavailable,
			because:   "the endpoint is not present on this firewall",
		},
		{
			name: "HTTP 403", status: http.StatusForbidden, body: `{"errorMessage":"Forbidden"}`,
			wantState: store.StateUnavailable,
			because:   "the key owner's ACL does not cover the path",
		},
		{
			name: "an empty array", status: http.StatusOK, body: `[]`,
			wantState: store.StateReachable, wantRows: 0,
			because: "a healthy log with nothing to report is reachable but silent, which is " +
				"not an absence of traffic",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newProbeHarness(t)
			arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
			// The provider is activated from a healthy probe first, so the collector is
			// exercised rather than skipped.
			harness.fake.answerJSON(opnsense.FirewallLog, []any{})
			if err := harness.collector.probeFirewallLog(context.Background()); err != nil {
				t.Fatalf("probing: %v", err)
			}

			harness.fake.answer(opnsense.FirewallLog, testCase.status, []byte(testCase.body))
			err := harness.collector.CollectFirewallLog(context.Background())
			if testCase.wantState == store.StateUnavailable && err == nil {
				t.Fatalf("%s was collected without an error (%s)", testCase.name, testCase.because)
			}

			state, probe, checkedAt := harness.availabilityOf(t, KindFirewallLog, ProviderPf)
			if state != testCase.wantState {
				t.Errorf("the state is %q, want %q (%s)", state, testCase.wantState, testCase.because)
			}
			if probe == "" || checkedAt == 0 {
				t.Error("the state was recorded with no probe or no instant")
			}
			if stored := countRows(t, harness.store, "flow"); stored != testCase.wantRows {
				t.Errorf("%d flows were stored, want %d", stored, testCase.wantRows)
			}
		})
	}
}

// TestACollectorWithNoActiveProviderDoesNothing keeps a pass from reading a source the
// probe round has just said opnview cannot read.
func TestACollectorWithNoActiveProviderDoesNothing(t *testing.T) {
	harness := newProbeHarness(t)
	harness.fake.answerFixture(opnsense.FirewallLog, "firewall_log.json")

	if err := harness.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("collecting with no active provider: %v", err)
	}
	if harness.fake.requestCount() != 0 {
		t.Fatalf("the collector made %d requests although no provider is active",
			harness.fake.requestCount())
	}
	if stored := countRows(t, harness.store, "flow"); stored != 0 {
		t.Fatalf("%d flows were stored with no active provider", stored)
	}
}

// fixtureRecords decodes a fixture whose body is an array of records.
func fixtureRecords(t *testing.T, name string) []decode.Object {
	t.Helper()
	rows, err := decode.Rows(fixtureBody(t, name))
	if err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
	return rows
}

// oldestFixtureInstant returns the oldest normalised instant in the filter-log fixture.
func oldestFixtureInstant(t *testing.T, harness *probeHarness) int64 {
	t.Helper()
	normaliser := harness.collector.normaliser()
	oldest := int64(0)
	for _, record := range fixtureRecords(t, "firewall_log.json") {
		stamp, present := decode.String(record, "__timestamp__")
		if !present {
			continue
		}
		instant, err := normaliser.NormaliseFilterLogTimestamp(stamp)
		if err != nil {
			t.Fatalf("normalising %q: %v", stamp, err)
		}
		if oldest == 0 || instant < oldest {
			oldest = instant
		}
	}
	if oldest == 0 {
		t.Fatal("the fixture carries no readable timestamp")
	}
	return oldest
}

// scalarCount runs a counting query against the test database.
func scalarCount(t *testing.T, database *store.Store, query string, arguments ...any) int {
	t.Helper()
	var value int
	if err := database.DB().QueryRowContext(context.Background(), query, arguments...).
		Scan(&value); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return value
}
