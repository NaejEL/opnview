package attribution

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The resolver's records: what its cache and its local data held, over the interval
// opnview's polls saw each record (schema.sql, resource_record_observation).
//
// THE QUERY REPORT CARRIES NO ANSWER ADDRESS, AND THE CACHE DOES. A lookup row says a
// client asked for a name; the cache says which addresses that name resolved to while
// the resolver held it. Joining the two is what the resolver-cache attribution does
// (attribute.go), and this file is the half that stores and reads the records.

// The parts of the resolver a record can be held in: resource_record_observation.held_in.
const (
	// HeldInCache is the resolver's cache, read through its dump.
	HeldInCache = "cache"
	// HeldInLocalData is the resolver's local data: host overrides and every other
	// `local-data:` record (unbound.conf(5)).
	HeldInLocalData = "local_data"
)

// The record types stored. Every other type is counted by the reader and not stored.
const (
	RRTypeA     = "A"
	RRTypeAAAA  = "AAAA"
	RRTypeCNAME = "CNAME"
	RRTypePTR   = "PTR"
)

// MaxCNAMELinks bounds how many CNAME records one chain is followed through, from a
// looked-up name to its addresses or back.
//
// It is Unbound's own bound, not one of opnview's: max-query-restarts, "Hard limit on
// the number of times Unbound is allowed to restart a query upon encountering a CNAME
// record", whose default is 11 (unbound.conf(5); util/config_file.c,
// `cfg->max_query_restarts = 11`). A chain longer than that is one the resolver
// answered SERVFAIL to, so its records name no answer and yield no evidence. RFC 1034,
// section 3.6.2, asks resolvers to follow CNAME chains and to detect loops, and
// states no length.
const MaxCNAMELinks = 11

// NO CNAME IS FOLLOWED INTO THE LOCAL DATA. A local-data A or AAAA record is evidence
// for one lookup only: a lookup answered from Local-data, of the record's own owner
// name (decision 6 of specs/SPEC-resolver-cache-attribution.md). A cached CNAME chain
// whose last name is a local-data owner gives no local-data evidence for the name the
// chain starts from. That is how Unbound answers: local data answers a query "if there
// is a match from local data" for the query's own name (unbound.conf(5), local-zone
// types static, transparent and typetransparent), and the one CNAME local data may
// carry, at the apex of a redirect zone, "proceeds with upstream DNS resolution, and
// that does not include the lookup in local zones" (unbound.conf(5), local-zone type
// redirect). A CNAME the iterator follows is resolved by recursion, not from local
// data. specs/SPEC-resolver-cache-follow-ups.md, scope 1, states this rule once, and
// three places read it: exactCandidates (attribute.go), answerAddresses below, and
// the diagnostic live_timing_fallback_other_family_cached
// (sql/queries/diagnostics.sql).

// DefaultCacheAnswerDelaySeconds is 3600 s, the maintainer's default cap on how long
// before a flow a lookup may have been made and still name it through the cache
// (decision 2 of specs/SPEC-resolver-cache-attribution.md). Like the other attribution
// setting it is written in the schema and read from the database; the constant exists
// only so a database with no row still behaves, and for the callers that pass none.
const DefaultCacheAnswerDelaySeconds int64 = 3600

// CacheAnswerDelayKey is the setting row holding that cap.
const CacheAnswerDelayKey = "attribution_max_cache_answer_delay_seconds"

// DNSName is a domain name as the records are compared: lower-cased in ASCII, every
// trailing dot removed. DNS compares names without regard to ASCII case (RFC 4343,
// section 3), and the dump writes absolute names with their final dot. It is the same
// expression idx_dns_resolution_domain_name indexes the lookups' domains on.
func DNSName(name string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(name), ".")
	lowered := []byte(trimmed)
	for index, character := range lowered {
		if character >= 'A' && character <= 'Z' {
			lowered[index] = character + ('a' - 'A')
		}
	}
	return string(lowered)
}

// CanonicalAddress returns an address in its canonical text form, which is RFC 5952's
// for IPv6 and the form inet_ntop writes -- and so the form the filter log reports --
// and whether the text was an address at all.
func CanonicalAddress(text string) (netip.Addr, bool) {
	address, err := netip.ParseAddr(strings.TrimSpace(text))
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address, true
}

// heldRecord is one observation as the evidence searches read it.
type heldRecord struct {
	id           int64
	heldIn       string
	ownerName    string
	rrtype       string
	value        string
	address      sql.NullString
	coveredFrom  int64
	coveredUntil int64
	firstSeenAt  int64
}

// scanHeldRecords reads the rows of one evidence search.
func scanHeldRecords(ctx context.Context, q sqldb.Querier, name string, parameters map[string]any) ([]heldRecord, error) {
	text, err := sqldb.Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, q, text, parameters)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var records []heldRecord
	for rows.Next() {
		var record heldRecord
		if err := rows.Scan(&record.id, &record.heldIn, &record.ownerName, &record.rrtype,
			&record.value, &record.address, &record.coveredFrom, &record.coveredUntil,
			&record.firstSeenAt); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return records, nil
}

// AnswerAddresses returns the addresses the resolver's cache held for a name at an
// instant, following CNAME observations from the name, each covering the instant, to the
// A and AAAA observations covering it -- at most MaxCNAMELinks links, and a loop yields
// nothing more than the names before it. The addresses are sorted and distinct.
func (s *AttributionStore) AnswerAddresses(ctx context.Context, name string, at int64) ([]string, error) {
	addresses, err := answerAddresses(ctx, s.SQL, DNSName(name), at, false)
	if err != nil {
		return nil, err
	}
	return addresses, nil
}

// answerAddresses is AnswerAddresses on a querier, optionally counting the local data's
// A and AAAA records of the name itself as well -- of the name, never of a name a CNAME
// chain reaches (see MaxCNAMELinks: no CNAME is followed into the local data).
func answerAddresses(ctx context.Context, q sqldb.Querier, name string, at int64, withLocalData bool) ([]string, error) {
	found := map[string]struct{}{}
	visited := map[string]struct{}{name: {}}
	frontier := []string{name}
	for links := 0; len(frontier) > 0; links++ {
		var next []string
		for _, current := range frontier {
			records, err := scanHeldRecords(ctx, q, "resource_records_of_name",
				map[string]any{"name": current, "at": at})
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				switch {
				case record.rrtype == RRTypeA || record.rrtype == RRTypeAAAA:
					if record.address.Valid &&
						(record.heldIn == HeldInCache || (withLocalData && links == 0)) {
						found[record.address.String] = struct{}{}
					}
				case record.rrtype == RRTypeCNAME && record.heldIn == HeldInCache && links < MaxCNAMELinks:
					if _, seen := visited[record.value]; !seen {
						visited[record.value] = struct{}{}
						next = append(next, record.value)
					}
				}
			}
		}
		frontier = next
	}
	addresses := make([]string, 0, len(found))
	for address := range found {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses, nil
}
