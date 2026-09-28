package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Unbound — one implementation of the dns_lookup kind, and the only one that can be read.
//
// Everything in this file is a fact about Unbound and about the one endpoint that opens its
// query report. What the KIND does with the records — resolving the querying machine, walking
// the pages, detecting a gap, and refusing to present what came back as coverage of a window —
// is in dnslookup.go and is written once.

func init() { registerLookupSource(unboundLookups{}) }

// unboundLookups reads /api/unbound/overview/search_queries.
//
// TWO MEASURED FACTS ABOUT THIS ENDPOINT, PULLING IN OPPOSITE DIRECTIONS.
//
// THE CALL FORM IS LOAD-BEARING. It keeps timeStart and timeEnd only if is_int() holds, which
// they can be only inside a Content-Type: application/json body; a query string or a
// form-encoded body silently degrades to the unwindowed branch with HTTP 200 and no
// diagnostic. The survey calls that the most dangerous failure mode in it.
//
// AND THE WINDOW IS IGNORED ANYWAY. Measured on a live firewall: a 5-minute request and a
// 24-hour request returned the same ~410-second span, `total` is 1000 whatever is asked, and
// pages 2 and 5 walk further back. It is a ring buffer of the last 1000 lookups. So this
// implementation sends the correct form — because the wrong one would be wrong for a second
// reason, and because a future firmware could start honouring it — and the kind never
// presents what comes back as coverage of the window that was asked for.
type unboundLookups struct{}

// providerKey is the registry row this implementation answers for.
func (unboundLookups) providerKey() string { return ProviderUnbound }

// dnsPageSize is how many lookups one page asks for.
const dnsPageSize = 200

// dnsMaxPages bounds how far back one pass walks the ring buffer. The endpoint caps the
// buffer at 1000 records whatever is asked, so five pages of 200 reach the whole of it and a
// sixth would re-read what the first five already saw.
const dnsMaxPages = 5

// dnsRequestedSpan is how wide a window this implementation asks for. It is sent, and it is
// not believed: it exists so the call is the correct form, and so the out-of-window rows a
// pass receives are visible as evidence that the endpoint still ignores it.
const dnsRequestedSpan = 3600

// pageBound is how many pages the kind may walk for this implementation.
func (unboundLookups) pageBound() int { return dnsMaxPages }

// requestedSpanSeconds is the window this implementation asks for.
func (unboundLookups) requestedSpanSeconds() int64 { return dnsRequestedSpan }

// probe reads the service state, the configured state, and whether query reporting is on.
//
// Reporting off is present-but-disabled — a setting for the user to turn on — and is never an
// absence of lookups. All three have to hold before this implementation can be read, so only
// the combination makes it separable.
func (unboundLookups) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.UnboundStatus}

	running, state, detail, err := host.serviceState(ctx, opnsense.UnboundStatus)
	if err != nil {
		return result, err
	}
	result.state, result.detail = state, detail
	if !running {
		return result, nil
	}

	settings, ok, err := host.readObject(ctx, opnsense.UnboundSettings)
	if err != nil {
		return result, err
	}
	configured := ok && decode.FlagOrFalse(decode.NestedFlag(settings, "unbound", "general", "enabled"))
	if !configured {
		result.state = store.StatePresentButDisabled
		result.detail = "the service runs and unbound.general.enabled is not set"
		return result, nil
	}

	overview, overviewOK, err := host.readObject(ctx, opnsense.UnboundIsEnabled)
	if err != nil {
		return result, err
	}
	if !overviewOK || !decode.FlagOrFalse(decode.Flag(overview, "enabled")) {
		result.state = store.StatePresentButDisabled
		result.detail = "query reporting is off, which is a setting to turn on and not an " +
			"absence of lookups"
		return result, nil
	}
	result.separable = true
	return result, nil
}

// lookups reads one page of the ring buffer.
func (unboundLookups) lookups(ctx context.Context, host session, page int) (
	[]lookupRecord, probeResult, error) {
	result := probeResult{probe: opnsense.SearchQueries, state: store.StateUnavailable}

	now := host.now()
	response, err := host.call(ctx, opnsense.SearchQueries, opnsense.RequestOptions{
		// Integers, in a JSON body. Written as Go values so encoding/json emits JSON numbers:
		// a string here would reach the controller as a PHP string, is_int() would fail, and
		// the call would silently degrade to the unwindowed branch.
		JSON: map[string]any{
			"current":   page,
			"rowCount":  dnsPageSize,
			"timeStart": now - dnsRequestedSpan,
			"timeEnd":   now,
		},
	})
	if err != nil && response.Outcome == "" {
		return nil, result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return nil, result, fmt.Errorf("collect: the resolver query report answered %s",
			response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		result.detail = "the query report body could not be read"
		return nil, result, fmt.Errorf("collect: reading the resolver query report: %w", err)
	}
	result.state = store.StateReachable
	result.separable = true

	records := make([]lookupRecord, 0, len(rows))
	for _, row := range rows {
		lookedUpAt, present := decode.Epoch(row, "time")
		if !present {
			continue
		}
		domain, present := decode.String(row, "domain")
		if !present {
			continue
		}
		action := normaliseLookupAction(decode.RawString(row, "action"))
		blocklistName := ""
		if action == "block" || action == "drop" {
			// The endpoint does not attribute every block. An unattributed one stays "blocked,
			// list not recorded", which is its own state and not a merge into a bucket, so the
			// name is taken only when the endpoint gave one.
			blocklistName = decode.RawString(row, "blocklist")
		}
		records = append(records, lookupRecord{
			DeduplicationKey: unboundLookupKey(row, lookedUpAt),
			ClientAddress:    decode.RawString(row, "client"),
			Domain:           domain,
			Action:           action,
			AnswerSource:     decode.StringPointer(row, "source"),
			Rcode:            decode.StringPointer(row, "rcode"),
			DNSSECStatus:     decode.StringPointer(row, "dnssec_status"),
			BlocklistName:    blocklistName,
			LookedUpAt:       lookedUpAt,
		})
	}
	return records, result, nil
}

// unboundLookupKey composes the de-duplication key from the row's own content.
//
// WHY IT IS NOT `uuid`: that field is null on every row the firewall returns, measured
// 2026-09-27, so the key the data model assumed does not exist. WHY IT IS THE CONTENT: the
// endpoint is a ring buffer with no cursor and no working window, so every pass re-reads rows
// it has already stored, and without a content-addressed key each pass would insert them
// again and multiply every per-client lookup count by the number of passes that saw it.
//
// THE TRADE, STATED RATHER THAN HIDDEN: two genuinely distinct lookups by the same client, for
// the same domain, with the same verdict, in the same second collapse into one row, so such a
// pair is under-counted by one. That is preferred deliberately. The endpoint returns nothing
// that could tell those two apart — no identifier, no sub-second instant, no sequence — so the
// only alternative is a key that is not stable across passes, and that would over-count every
// row on every poll instead of under-counting a rare coincidence. An under-count of identical
// lookups is also the direction that cannot invent traffic.
//
// The fields are joined with a unit separator, which cannot occur in any of them, and hashed
// so the column stays bounded whatever a domain's length is.
func unboundLookupKey(row decode.Object, lookedUpAt int64) string {
	const unitSeparator = "\x1f"
	parts := []string{
		ProviderUnbound,
		fmt.Sprintf("%d", lookedUpAt),
		decode.RawString(row, "client"),
		decode.RawString(row, "domain"),
		decode.RawString(row, "action"),
		decode.RawString(row, "source"),
		decode.RawString(row, "rcode"),
		decode.RawString(row, "dnssec_status"),
		decode.RawString(row, "blocklist"),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, unitSeparator)))
	return "content:" + hex.EncodeToString(sum[:])
}
