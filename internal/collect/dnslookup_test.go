package collect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The resolver-lookup collector tests.
//
// Two measured facts drive all of them, and they pull in opposite directions. The CALL
// FORM is load-bearing: timeStart and timeEnd are kept only if is_int() holds, which they
// can be only inside a JSON body, and any other encoding silently degrades with HTTP 200
// and no diagnostic. And the WINDOW IS IGNORED ANYWAY: a 5-minute request and a 24-hour
// request returned the same span, and `total` is 1000 whatever is asked. So the collector
// sends the correct form — because the wrong one would be wrong for a second reason — and
// never presents what it read as coverage of the window it asked for.

// arrangeResolverCollection stands up a harness whose resolver is the active source.
func arrangeResolverCollection(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
	harness.fake.answerFixture(opnsense.UnboundSettings, "unbound_settings.json")
	harness.fake.answerFixture(opnsense.UnboundIsEnabled, "unbound_is_enabled_on.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	harness.fake.answerFixture(opnsense.SearchQueries, "search_queries_page1.json")
	if err := harness.collector.probeDNSLookup(context.Background()); err != nil {
		t.Fatalf("probing the resolver: %v", err)
	}
	if key := harness.activeKeyOf(t, KindDNSLookup); key != ProviderUnbound {
		t.Fatalf("the resolver kind activated %q", key)
	}
	return harness
}

// TestTheResolverQueryIsAJSONPostWithIntegerBounds is the call form, checked at the wire.
//
// A query string, a form body or a quoted bound each make the firewall fall through to the
// unwindowed branch, with HTTP 200 and no diagnostic. This is the only thing standing
// between the product and that.
func TestTheResolverQueryIsAJSONPostWithIntegerBounds(t *testing.T) {
	harness := arrangeResolverCollection(t)
	if err := harness.collector.CollectDNSLookup(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	harness.fake.assertEveryPathIsRegistered()

	requests := harness.fake.requestsTo(opnsense.SearchQueries)
	if len(requests) == 0 {
		t.Fatal("the query report was never requested")
	}
	for index, request := range requests {
		if request.method != "POST" {
			t.Errorf("request %d was %s, not POST", index, request.method)
		}
		if request.contentType != "application/json" {
			t.Errorf("request %d carried Content-Type %q, not application/json",
				index, request.contentType)
		}
		if len(request.query) != 0 {
			t.Errorf("request %d put %v in the query string, where the bounds would be strings",
				index, request.query)
		}

		// The bounds have to be JSON numbers, not strings. Decoding into json.Number and
		// then requiring an integer conversion is what proves it: a quoted value fails
		// the type assertion, and a float would fail Int64.
		var body map[string]json.RawMessage
		if err := json.Unmarshal(request.body, &body); err != nil {
			t.Fatalf("request %d did not carry a JSON object: %v", index, err)
		}
		for _, bound := range []string{"timeStart", "timeEnd"} {
			raw, present := body[bound]
			if !present {
				t.Errorf("request %d carried no %s", index, bound)
				continue
			}
			if strings.HasPrefix(string(raw), `"`) {
				t.Errorf("request %d quoted %s as %s, so is_int() would reject it and the "+
					"call would silently degrade", index, bound, raw)
				continue
			}
			var number json.Number
			if err := json.Unmarshal(raw, &number); err != nil {
				t.Errorf("request %d sent %s as %s: %v", index, bound, raw, err)
				continue
			}
			if _, err := number.Int64(); err != nil {
				t.Errorf("request %d sent %s as %s, which is not an integer", index, bound, raw)
			}
		}
	}
}

// TestTheCollectorNeverPresentsWhatItReadAsCoverageOfTheWindow is the honesty this
// endpoint requires.
//
// The fixture reproduces the measured behaviour: every row falls outside the window the
// collector asked for, because the endpoint ignores the bounds. What the collector records
// is that it read the most recent lookups and that this is NOT coverage of the window —
// nothing anywhere claims an interval it did not cover.
func TestTheCollectorNeverPresentsWhatItReadAsCoverageOfTheWindow(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	detail := harness.detailOf(t, KindDNSLookup, ProviderUnbound)
	for _, phrase := range []string{
		"most recent lookups",
		"ignores timeStart and timeEnd",
		"not coverage",
	} {
		if !strings.Contains(detail, phrase) {
			t.Errorf("the recorded detail is %q, which does not say %q", detail, phrase)
		}
	}
	// And it says how many rows fell outside, which is the evidence that the endpoint still
	// ignores the bounds rather than an assumption that it does.
	if !strings.Contains(detail, "fell outside it") {
		t.Errorf("the detail is %q, which does not report the out-of-window rows it measured",
			detail)
	}
}

// TestTheDeduplicationKeyIsComposedFromTheRowContentBecauseTheIdentifierIsNull is the
// replacement for the key the data model assumed.
//
// `uuid` is null on every row a real firewall returns, measured 2026-09-27. The endpoint is
// a ring buffer with no cursor and no working window, so every pass re-reads rows already
// stored; without a content-addressed key each pass would insert them again and multiply
// every per-client lookup count by the number of passes that saw it.
func TestTheDeduplicationKeyIsComposedFromTheRowContentBecauseTheIdentifierIsNull(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	// The fixture's rows all carry a null uuid, which is the premise.
	for _, row := range fixtureRecords(t, "search_queries_page1.json") {
		if value, present := row["uuid"]; present && value != nil {
			t.Fatal("the fixture carries a non-null uuid, so it no longer reproduces what was " +
				"measured")
		}
	}

	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	afterOne := countRows(t, harness.store, "dns_resolution")
	if afterOne == 0 {
		t.Fatal("the first pass stored no lookup")
	}
	for pass := 0; pass < 3; pass++ {
		if err := harness.collector.CollectDNSLookup(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := countRows(t, harness.store, "dns_resolution"); repeated != afterOne {
		t.Fatalf("four passes over the ring buffer stored %d lookups, up from %d",
			repeated, afterOne)
	}

	// The stored key is content-addressed rather than an identifier the endpoint supplied.
	for _, key := range stringColumn(t, harness.store,
		"SELECT lookup_key FROM dns_resolution") {
		if !strings.HasPrefix(key, "content:") {
			t.Errorf("the stored key %q does not say that it was composed from the row content",
				key)
		}
	}
}

// TestABlockedLookupNamingNoListIsItsOwnState is the distinction the schema carries and
// the endpoint forces.
//
// The endpoint does not attribute every block. A blocked lookup naming no list is not the
// same as a lookup that was allowed, and it is not a row to drop: it is "blocked, list not
// recorded".
func TestABlockedLookupNamingNoListIsItsOwnState(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	attributed := scalarCount(t, harness.store,
		"SELECT count(*) FROM dns_resolution WHERE action IN ('block','drop') AND blocklist_id IS NOT NULL")
	unattributed := scalarCount(t, harness.store,
		"SELECT count(*) FROM dns_resolution WHERE action IN ('block','drop') AND blocklist_id IS NULL")
	if attributed == 0 {
		t.Error("no blocked lookup names the list that refused it")
	}
	if unattributed == 0 {
		t.Error("no blocked lookup carries the list-not-recorded state, so it is untested")
	}
	// A passed lookup names no list, and that is a third thing again.
	if passedWithList := scalarCount(t, harness.store,
		"SELECT count(*) FROM dns_resolution WHERE action = 'pass' AND blocklist_id IS NOT NULL"); passedWithList != 0 {
		t.Errorf("%d passed lookups were attributed to a list", passedWithList)
	}
	// The name is observed and the purpose is user input, so nothing here assigns one.
	if classified := scalarCount(t, harness.store,
		"SELECT count(*) FROM blocklist WHERE purpose IS NOT NULL"); classified != 0 {
		t.Errorf("the collector assigned a purpose to %d lists; that is the user's to assign",
			classified)
	}
}

// TestALookupWhoseVerdictWasNotReportedStaysDistinguishable keeps "not reported" apart
// from "unvalidated". The resolver reached a verdict on some rows and none on others, and
// the nullable column exists for exactly that difference.
func TestALookupWhoseVerdictWasNotReportedStaysDistinguishable(t *testing.T) {
	harness := arrangeResolverCollection(t)
	if err := harness.collector.CollectDNSLookup(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	reported := scalarCount(t, harness.store,
		"SELECT count(*) FROM dns_resolution WHERE dnssec_status IS NOT NULL")
	notReported := scalarCount(t, harness.store,
		"SELECT count(*) FROM dns_resolution WHERE dnssec_status IS NULL")
	if reported == 0 || notReported == 0 {
		t.Fatalf("%d lookups carry a verdict and %d carry none; both are needed for the "+
			"distinction to mean anything", reported, notReported)
	}
}

// TestLookupsThatRotatedOutOfTheRingBufferWriteAGapRow is the only gap detection this
// endpoint permits.
//
// The buffer holds the most recent lookups and honours no window, so if its OLDEST row is
// newer than the newest row stored, the lookups between the two rotated out before opnview
// read them. Nothing can recover them, and the loss is recorded rather than smoothed over.
func TestLookupsThatRotatedOutOfTheRingBufferWriteAGapRow(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	oldest := oldestLookupInstant(t, "search_queries_page1.json")
	if err := harness.store.InsertDNSResolution(ctx, store.DNSResolution{
		LookupKey:     "content:an-earlier-pass-stored-this",
		ClientAddress: "example-address",
		Domain:        "example-domain",
		Resolver:      ProviderUnbound,
		Action:        "pass",
		LookedUpAt:    oldest - 3600,
		IngestedAt:    oldest - 3600,
	}); err != nil {
		t.Fatalf("seeding a lookup from an earlier pass: %v", err)
	}

	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	gaps := scalarCount(t, harness.store,
		"SELECT count(*) FROM collection_gap WHERE reason = ?",
		string(store.GapResolverWindowNotHonoured))
	if gaps != 1 {
		t.Fatalf("the pass wrote %d resolver gap rows, want exactly one", gaps)
	}
	var start, end int64
	var detail *string
	if err := harness.store.DB().QueryRowContext(ctx,
		`SELECT interval_start_at, interval_end_at, detail FROM collection_gap
		 WHERE reason = ?`, string(store.GapResolverWindowNotHonoured)).
		Scan(&start, &end, &detail); err != nil {
		t.Fatalf("reading the gap back: %v", err)
	}
	if start != oldest-3600 || end != oldest {
		t.Errorf("the gap covers [%d, %d], want [%d, %d]", start, end, oldest-3600, oldest)
	}
	if detail == nil || !strings.Contains(*detail, "ring buffer") {
		t.Error("the gap does not name the ring buffer, which is why nothing can recover it")
	}
}

// TestTheCollectorWalksTheRingBufferAndStopsWhenAPageIsAllKnown is the paging behaviour the
// measurement implies: pages walk further back rather than paging a window, so the collector
// walks until a page holds nothing new.
func TestTheCollectorWalksTheRingBufferAndStopsWhenAPageIsAllKnown(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	// The fake serves one page whatever is asked, so the second request sees rows it has
	// just stored and the walk stops — which is the behaviour, not a limitation of the fake:
	// a real second page walks further back and is also eventually all known.
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	requests := harness.fake.requestsTo(opnsense.SearchQueries)
	if len(requests) < 2 {
		t.Fatalf("the collector made %d requests; it must page rather than read one page", len(requests))
	}
	if len(requests) > dnsMaxPages {
		t.Fatalf("the collector made %d requests, above the %d-page bound; the endpoint caps "+
			"its buffer, so a longer walk re-reads what it already saw", len(requests), dnsMaxPages)
	}
	pages := map[string]bool{}
	for _, request := range requests {
		var body map[string]any
		if err := json.Unmarshal(request.body, &body); err != nil {
			t.Fatalf("a request body did not decode: %v", err)
		}
		pages[string(request.body)] = true
		if _, present := body["current"]; !present {
			t.Error("a request asked for no page")
		}
	}
	if len(pages) < 2 {
		t.Error("every request asked for the same page, so the collector is not walking the buffer")
	}
}

// TestTheLookupKeyDependsOnTheRowAndNotOnWhenItWasPolled is the unit test behind the
// de-duplication, and it is the half the four-pass test above cannot reach.
//
// That test runs every pass at one fixed instant, so a key that had quietly included the
// POLL time would still have collapsed and still have passed. This one separates the two
// instants that matter: the lookup's own instant, which is part of the row and therefore
// part of its identity, and the instant opnview happened to read it, which is not.
func TestTheLookupKeyDependsOnTheRowAndNotOnWhenItWasPolled(t *testing.T) {
	row := decode.Object{
		"client":        "example-client-address",
		"domain":        "example-name.invalid",
		"action":        "block",
		"source":        "local-data",
		"rcode":         "NXDOMAIN",
		"dnssec_status": "example-dnssec-verdict",
		"blocklist":     "example-blocklist-name",
		"time":          float64(1749990000),
		"uuid":          nil,
	}
	const lookedUpAt = int64(1749990000)
	base := unboundLookupKey(row, lookedUpAt)

	if base == "" {
		t.Fatal("the key is empty")
	}
	if !strings.HasPrefix(base, "content:") {
		t.Fatalf("the key is %q, which does not say that it was composed from the row content",
			base)
	}
	// The same row, composed again. The function takes no clock, so nothing about the
	// moment of the call can reach the value.
	if again := unboundLookupKey(row, lookedUpAt); again != base {
		t.Fatalf("composing the same row twice gave %q and %q", base, again)
	}

	// Every field that distinguishes one lookup from another changes the key. A field
	// missing from this list would be a field two different lookups could differ in while
	// collapsing into one row, which is the under-count spreading from a coincidence to a
	// class.
	for _, field := range []string{
		"client", "domain", "action", "source", "rcode", "dnssec_status", "blocklist",
	} {
		altered := decode.Object{}
		for key, value := range row {
			altered[key] = value
		}
		altered[field] = "a-different-value"
		if changed := unboundLookupKey(altered, lookedUpAt); changed == base {
			t.Errorf("changing %s left the key unchanged, so two lookups differing only in "+
				"that field would collapse into one row", field)
		}
	}

	// The lookup's OWN instant is part of its identity: the same client asking for the
	// same domain a second later is a second lookup.
	if later := unboundLookupKey(row, lookedUpAt+1); later == base {
		t.Error("a lookup one second later has the same key, so a repeated query would " +
			"never be counted twice")
	}

	// And the trade, asserted rather than only described: two rows identical in every
	// field INCLUDING the instant are one row. This is the deliberate under-count, and a
	// test that did not state it would leave somebody to discover it as a bug.
	duplicate := decode.Object{}
	for key, value := range row {
		duplicate[key] = value
	}
	if unboundLookupKey(duplicate, lookedUpAt) != base {
		t.Error("two rows identical in every field including the instant have different " +
			"keys, so every re-read of the ring buffer would insert them again")
	}
}

// TestTheStoredKeysDoNotChangeWhenThePollInstantDoes is the same property at the
// collector boundary, because that is where a poll time could actually leak in.
func TestTheStoredKeysDoNotChangeWhenThePollInstantDoes(t *testing.T) {
	harness := arrangeResolverCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	firstKeys := stringColumn(t, harness.store,
		"SELECT lookup_key FROM dns_resolution ORDER BY lookup_key")
	if len(firstKeys) == 0 {
		t.Fatal("the first pass stored nothing")
	}

	// An hour later, the same ring buffer. Nothing new may be stored, and no key may
	// change: the rows are the same rows.
	harness.clock.advance(time.Hour)
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("the pass an hour later: %v", err)
	}
	secondKeys := stringColumn(t, harness.store,
		"SELECT lookup_key FROM dns_resolution ORDER BY lookup_key")

	if strings.Join(firstKeys, ",") != strings.Join(secondKeys, ",") {
		t.Fatalf("the stored keys changed between two poll instants: %d then %d rows, and "+
			"the sets differ", len(firstKeys), len(secondKeys))
	}
}

// oldestLookupInstant returns the oldest instant in a resolver fixture.
func oldestLookupInstant(t *testing.T, name string) int64 {
	t.Helper()
	oldest := int64(0)
	for _, row := range fixtureRecords(t, name) {
		instant, present := decode.Epoch(row, "time")
		if !present {
			continue
		}
		if oldest == 0 || instant < oldest {
			oldest = instant
		}
	}
	if oldest == 0 {
		t.Fatalf("%s carries no readable instant", name)
	}
	return oldest
}
