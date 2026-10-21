package attribution

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Site-name attribution.
//
// TWO METHODS, EXACT EVIDENCE FIRST (specs/SPEC-resolver-cache-attribution.md, which
// amends decisions D1 and D3 of the 5A specification). The resolver's query report,
// /api/unbound/overview/search_queries, returns no answer address (survey, data source
// 5); the resolver's cache, /api/unbound/diagnostics/dumpcache, does. So:
//
//	resolver_cache_answer. An eligible lookup of D by the flow's client, made at most
//	the cache-answer delay before the flow, is an EXACT CANDIDATE when the flow's
//	destination X is among D's answer addresses over the whole interval from the
//	lookup to the flow: the cache's CNAME observations from D, every link covering
//	that interval, lead to an A or AAAA observation of X covering it too. For a lookup
//	answered from Local-data, a local-data A or AAAA record of D holding X counts as
//	well. The flow is attributed exactly when its exact candidates name one distinct
//	domain; two or more give no row.
//
//	lookup_timing. With no exact candidate, the 5A rule, unchanged: the eligible
//	lookups by the same client in [observed_at - max delay, observed_at] name exactly
//	one distinct domain. It is suppressed when the cache held answers of X's address
//	family for that domain at the referenced lookup's instant and X was not among them
//	-- evidence that contradicts it (decision 3).
//
// The same client is the same client_id where both rows carry one and the same
// address otherwise. An eligible flow has an outside destination; an eligible lookup
// passed, was answered by recursion, from the cache or from local data, and named one
// address. site_name is ALWAYS the domain the client looked up, verbatim -- never a
// CNAME target -- the lookup referenced is the latest of the matching ones, and the
// delay is the flow's instant minus that lookup's, never negative because every window
// ends at the flow.
//
// Both remain inferences: the cache says the client could have been told X when it
// asked for D, not that the flow was for D. What the exact method adds is that it no
// longer needs the client to have asked for one name only.

// The attribution methods: domain_attribution.method.
const (
	// MethodResolverCacheAnswer is an attribution from exact evidence: the flow's
	// destination was among the looked-up name's answers in the resolver's records.
	MethodResolverCacheAnswer = "resolver_cache_answer"
	// MethodLookupTiming is an attribution from timing alone, the 5A rule.
	MethodLookupTiming = "lookup_timing"
)

// AttributionWindows are the two settings the rule reads.
type AttributionWindows struct {
	// LookupTimingDelay is attribution_max_delay_seconds: how long before a flow a
	// lookup may have been made and still name it by timing.
	LookupTimingDelay int64
	// CacheAnswerDelay is attribution_max_cache_answer_delay_seconds: how long before a
	// flow, at most, a lookup may have been made and still name it through the
	// resolver's records.
	CacheAnswerDelay int64
}

// Reach is how far after a lookup a flow may lie and still be named by it, under either
// method: what a pass that stores lookups widens its derivation by.
func (w AttributionWindows) Reach() int64 { return max(w.LookupTimingDelay, w.CacheAnswerDelay) }

// Attribution is what one attribution pass changed.
type Attribution struct {
	// FlowInstants are the observed_at instants of every flow whose attribution was
	// added, changed or removed: the slots of the domain family to recompute.
	FlowInstants []int64
	// Decided is how many eligible flows the pass decided on.
	Decided int
}

// Attribute decides the attribution of every eligible flow observed in [from, to], with
// lookups up to maxDelay seconds before each flow by timing, and the default
// cache-answer delay.
func (s *AttributionStore) Attribute(ctx context.Context, from, to, maxDelay, now int64) (Attribution, error) {
	return s.AttributeWith(ctx, from, to, AttributionWindows{
		LookupTimingDelay: maxDelay, CacheAnswerDelay: DefaultCacheAnswerDelaySeconds,
	}, now)
}

// attributionFlow is one eligible flow.
type attributionFlow struct {
	id          int64
	observedAt  int64
	clientID    sql.NullInt64
	address     string
	destination string
}

// attributionLookup is one eligible lookup as the rule reads it.
type attributionLookup struct {
	id           int64
	domain       string
	lookedUpAt   int64
	key          string
	answerSource sql.NullString
}

// later reports whether a lookup is the later of two, the lookup key breaking a tie so
// the choice does not depend on the order the lookups were stored in.
func (l attributionLookup) later(than attributionLookup) bool {
	if l.lookedUpAt != than.lookedUpAt {
		return l.lookedUpAt > than.lookedUpAt
	}
	return l.key > than.key
}

// attributionDecision is what the rule decided for one flow.
type attributionDecision struct {
	named       bool
	method      string
	lookup      attributionLookup
	observation *int64
}

// AttributeWith decides the attribution of every eligible flow observed in [from, to].
func (s *AttributionStore) AttributeWith(ctx context.Context, from, to int64, windows AttributionWindows,
	now int64) (Attribution, error) {
	var result Attribution
	if windows.LookupTimingDelay <= 0 {
		return result, fmt.Errorf("store: the attribution delay must be a positive number of seconds, got %d",
			windows.LookupTimingDelay)
	}
	if windows.CacheAnswerDelay <= 0 {
		return result, fmt.Errorf("store: the cache-answer delay must be a positive number of seconds, got %d",
			windows.CacheAnswerDelay)
	}
	if to < from {
		return result, nil
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting an attribution: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	// The same statements run once per record; each text is prepared once per connection by the
	// driver's statement cache (internal/store/sqldb/driver.go).
	prepared := transaction

	text, err := sqldb.Statement("attribution_candidates")
	if err != nil {
		return result, err
	}
	rows, err := sqldb.Query(ctx, prepared, text, map[string]any{"from": from, "to": to})
	if err != nil {
		return result, fmt.Errorf("store: attribution_candidates: %w", err)
	}
	var candidates []attributionFlow
	for rows.Next() {
		var c attributionFlow
		if err := rows.Scan(&c.id, &c.observedAt, &c.clientID, &c.address, &c.destination); err != nil {
			_ = rows.Close()
			return result, fmt.Errorf("store: attribution_candidates: %w", err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, fmt.Errorf("store: attribution_candidates: %w", err)
	}

	// A flow attributed earlier that is no longer eligible -- its destination was placed
	// inside or recognised as this firewall since, or it was paired as a second leg --
	// loses its attribution, whichever method gave it.
	ineligibleText, err := sqldb.Statement("attribution_ineligible")
	if err != nil {
		return result, err
	}
	ineligible, err := sqldb.Query(ctx, prepared, ineligibleText, map[string]any{"from": from, "to": to})
	if err != nil {
		return result, fmt.Errorf("store: attribution_ineligible: %w", err)
	}
	type stale struct{ id, observedAt int64 }
	var stales []stale
	for ineligible.Next() {
		var one stale
		if err := ineligible.Scan(&one.id, &one.observedAt); err != nil {
			_ = ineligible.Close()
			return result, fmt.Errorf("store: attribution_ineligible: %w", err)
		}
		stales = append(stales, one)
	}
	err = ineligible.Err()
	_ = ineligible.Close()
	if err != nil {
		return result, fmt.Errorf("store: attribution_ineligible: %w", err)
	}
	for _, one := range stales {
		changed, err := sqldb.ExecNamed(ctx, prepared, "attribution_delete", map[string]any{"flow_id": one.id})
		if err != nil {
			return result, err
		}
		if changed > 0 {
			result.FlowInstants = append(result.FlowInstants, one.observedAt)
		}
	}

	for _, flow := range candidates {
		decision, err := decideAttribution(ctx, prepared, flow, windows)
		if err != nil {
			return result, err
		}
		result.Decided++

		var changed int64
		if decision.named {
			var observation any
			if decision.observation != nil {
				observation = *decision.observation
			}
			changed, err = sqldb.ExecNamed(ctx, prepared, "attribution_upsert", map[string]any{
				"flow_id": flow.id, "lookup_id": decision.lookup.id, "site_name": decision.lookup.domain,
				"delay": flow.observedAt - decision.lookup.lookedUpAt, "now": now,
				"method": decision.method, "observation_id": observation,
			})
		} else {
			changed, err = sqldb.ExecNamed(ctx, prepared, "attribution_delete",
				map[string]any{"flow_id": flow.id})
		}
		if err != nil {
			return result, err
		}
		if changed > 0 {
			result.FlowInstants = append(result.FlowInstants, flow.observedAt)
		}
	}
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("store: committing an attribution: %w", err)
	}
	return result, nil
}

// decideAttribution applies the rule to one flow.
func decideAttribution(ctx context.Context, q sqldb.Querier, flow attributionFlow,
	windows AttributionWindows) (attributionDecision, error) {
	var clientID any
	if flow.clientID.Valid {
		clientID = flow.clientID.Int64
	}

	// Exact evidence first.
	destination, isAddress := CanonicalAddress(flow.destination)
	if isAddress {
		exact, err := exactCandidates(ctx, q, flow, destination.String(), clientID, windows.CacheAnswerDelay)
		if err != nil {
			return attributionDecision{}, err
		}
		switch len(exact) {
		case 0:
		case 1:
			for _, candidate := range exact {
				observation := candidate.observation
				return attributionDecision{named: true, method: MethodResolverCacheAnswer,
					lookup: candidate.lookup, observation: &observation}, nil
			}
		default:
			// Two names the client looked up both answered with the destination: the
			// evidence does not say which the flow was for.
			return attributionDecision{}, nil
		}
	}

	// Timing, as the fallback.
	text, err := sqldb.Statement("attribution_lookups")
	if err != nil {
		return attributionDecision{}, err
	}
	lookups, err := scanLookups(ctx, q, "attribution_lookups", text, map[string]any{
		"client_id": clientID, "address": flow.address,
		"start": flow.observedAt - windows.LookupTimingDelay, "end": flow.observedAt,
	})
	if err != nil {
		return attributionDecision{}, err
	}
	domains := map[string]struct{}{}
	var latest attributionLookup
	for index, lookup := range lookups {
		domains[lookup.domain] = struct{}{}
		if index == 0 || lookup.later(latest) {
			latest = lookup
		}
	}
	if len(domains) != 1 {
		return attributionDecision{}, nil
	}

	// Contradicting evidence: the resolver's records held answers of the destination's
	// family for the name at the lookup's instant, and the destination was not one.
	if isAddress {
		answers, err := answerAddresses(ctx, q, DNSName(latest.domain), latest.lookedUpAt,
			latest.answerSource.Valid && latest.answerSource.String == "Local-data")
		if err != nil {
			return attributionDecision{}, err
		}
		sameFamily, includesDestination := false, false
		for _, answer := range answers {
			parsed, ok := CanonicalAddress(answer)
			if !ok || parsed.Is4() != destination.Is4() {
				continue
			}
			sameFamily = true
			if parsed == destination {
				includesDestination = true
			}
		}
		if sameFamily && !includesDestination {
			return attributionDecision{}, nil
		}
	}
	return attributionDecision{named: true, method: MethodLookupTiming, lookup: latest}, nil
}

// scanLookups reads the rows of a lookup statement.
func scanLookups(ctx context.Context, q sqldb.Querier, name, text string, parameters map[string]any) (
	[]attributionLookup, error) {
	rows, err := sqldb.Query(ctx, q, text, parameters)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var lookups []attributionLookup
	for rows.Next() {
		var lookup attributionLookup
		if err := rows.Scan(&lookup.id, &lookup.domain, &lookup.lookedUpAt, &lookup.key,
			&lookup.answerSource); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		lookups = append(lookups, lookup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return lookups, nil
}

// answerPath is one way a name reached the flow's destination: through CNAME
// observations from the name to an A or AAAA observation of the destination. from is
// the latest coverage start along it, so the path covers every instant from there to the
// flow; every link covers the flow by construction.
type answerPath struct {
	from        int64
	heldIn      string
	observation heldRecord
}

// exactCandidate is the lookup an exact candidate name is matched to, and the address
// observation that matched.
type exactCandidate struct {
	lookup      attributionLookup
	observation int64
	terminal    heldRecord
}

// exactCandidates returns, per distinct looked-up name, the exact candidate of one flow.
//
// It works BACKWARDS FROM THE DESTINATION, because that is the search an index can
// serve: the A and AAAA observations holding the address and covering the flow, then the
// cache's CNAME observations pointing at their names, link by link up to MaxCNAMELinks,
// each covering the flow -- which yields every name that resolved to the address at the
// flow's instant and how far back each path's coverage reaches. A lookup of one of those
// names by the flow's client, inside the cache-answer delay and inside its path's
// coverage, is an exact candidate. A local-data record is a path only for a lookup
// answered from Local-data, of the record's own owner name: no CNAME is followed into
// the local data (the rule is stated at MaxCNAMELinks, records.go), so a local-data
// terminal is never climbed from.
func exactCandidates(ctx context.Context, q sqldb.Querier, flow attributionFlow, destination string,
	clientID any, cacheAnswerDelay int64) (map[string]exactCandidate, error) {
	terminals, err := scanHeldRecords(ctx, q, "resource_records_of_address",
		map[string]any{"address": destination, "at": flow.observedAt})
	if err != nil {
		return nil, err
	}
	if len(terminals) == 0 {
		return nil, nil
	}

	// paths holds, per name, the paths that reach the destination from it.
	paths := map[string][]answerPath{}
	type step struct {
		name string
		path answerPath
	}
	var frontier []step
	for _, terminal := range terminals {
		path := answerPath{from: terminal.coveredFrom, heldIn: terminal.heldIn, observation: terminal}
		paths[terminal.ownerName] = append(paths[terminal.ownerName], path)
		if terminal.heldIn == HeldInCache {
			frontier = append(frontier, step{name: terminal.ownerName, path: path})
		}
	}
	// A name is climbed from once per terminal, which is what ends a loop.
	climbed := map[string]struct{}{}
	for links := 0; links < MaxCNAMELinks && len(frontier) > 0; links++ {
		var next []step
		for _, current := range frontier {
			key := current.name + "\x1f" + fmt.Sprint(current.path.observation.id)
			if _, seen := climbed[key]; seen {
				continue
			}
			climbed[key] = struct{}{}
			owners, err := scanHeldRecords(ctx, q, "resource_record_cname_owners",
				map[string]any{"name": current.name, "at": flow.observedAt})
			if err != nil {
				return nil, err
			}
			for _, owner := range owners {
				if owner.heldIn != HeldInCache {
					continue
				}
				path := answerPath{from: max(current.path.from, owner.coveredFrom), heldIn: HeldInCache,
					observation: current.path.observation}
				paths[owner.ownerName] = append(paths[owner.ownerName], path)
				next = append(next, step{name: owner.ownerName, path: path})
			}
		}
		frontier = next
	}

	lookupsText, err := sqldb.Statement("attribution_name_lookups")
	if err != nil {
		return nil, err
	}
	candidates := map[string]exactCandidate{}
	for name, reaching := range paths {
		earliest := reaching[0].from
		for _, path := range reaching[1:] {
			earliest = min(earliest, path.from)
		}
		lookups, err := scanLookups(ctx, q, "attribution_name_lookups", lookupsText, map[string]any{
			"name": name, "client_id": clientID, "address": flow.address,
			"start": max(flow.observedAt-cacheAnswerDelay, earliest), "end": flow.observedAt,
		})
		if err != nil {
			return nil, err
		}
		for _, lookup := range lookups {
			localData := lookup.answerSource.Valid && lookup.answerSource.String == "Local-data"
			var matched *heldRecord
			for index := range reaching {
				path := reaching[index]
				if path.from > lookup.lookedUpAt || (path.heldIn == HeldInLocalData && !localData) {
					continue
				}
				if matched == nil || earlierObservation(path.observation, *matched) {
					observation := path.observation
					matched = &observation
				}
			}
			if matched == nil {
				continue
			}
			current, seen := candidates[name]
			if !seen || lookup.later(current.lookup) ||
				(lookup.id == current.lookup.id && earlierObservation(*matched, current.terminal)) {
				candidates[name] = exactCandidate{lookup: lookup, observation: matched.id, terminal: *matched}
			}
		}
	}
	return candidates, nil
}

// earlierObservation orders the address observations one lookup could name by their
// content, so the one named does not depend on the order they were stored in: the
// earliest first poll, then the cache before local data, then the owner name.
func earlierObservation(a, b heldRecord) bool {
	if a.firstSeenAt != b.firstSeenAt {
		return a.firstSeenAt < b.firstSeenAt
	}
	if a.heldIn != b.heldIn {
		return a.heldIn < b.heldIn
	}
	return a.ownerName < b.ownerName
}

// HostnameLabel is a host name's first label, lower-cased in ASCII, a trailing dot
// removed: the same expression as dhcp_lease.hostname_label, which is what a logged
// host name is matched on.
func HostnameLabel(hostname string) string {
	trimmed := strings.TrimRight(hostname, ".")
	if dot := strings.IndexByte(trimmed, '.'); dot >= 0 {
		trimmed = trimmed[:dot]
	}
	lowered := []byte(trimmed)
	for index, character := range lowered {
		if character >= 'A' && character <= 'Z' {
			lowered[index] = character + ('a' - 'A')
		}
	}
	return string(lowered)
}
