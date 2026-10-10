package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
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

// ResourceRecord is one record a read returned, normalised by the reader: the owner
// name and a name value through DNSName, an address value in canonical form.
type ResourceRecord struct {
	// OwnerName is the endpoint's `host` (the cache) or `name` (local data).
	OwnerName string
	// RRType is the endpoint's `rrtype`: A, AAAA, CNAME or PTR.
	RRType string
	// Value is the endpoint's `value`.
	Value string
	// Address is the address the record maps a name to: the value of an A or AAAA
	// record, the address a PTR record's owner name encodes; empty for a CNAME.
	Address string
	// HostLabel is, for local data, the first label of the host name the record
	// names; empty for the cache.
	HostLabel string
	// TTLSeconds is the seconds remaining the cache reported. It is not read for
	// local data, whose `ttl` is the configured one.
	TTLSeconds int64
}

// ResourceRecordRead is one successful poll of one provider's records.
type ResourceRecordRead struct {
	// ProviderID is the provider whose records these are.
	ProviderID int64
	// HeldIn is HeldInCache or HeldInLocalData.
	HeldIn string
	// PolledAt is the instant of the poll.
	PolledAt int64
	// Records are the records it returned, already filtered to the stored types.
	Records []ResourceRecord
}

// RecordStorage is what storing one poll changed.
type RecordStorage struct {
	// Inserted is how many observations the poll started.
	Inserted int
	// Extended is how many observations it extended.
	Extended int
	// Changed says whether any observation was started or extended, and so whether
	// ChangedFrom and ChangedTo mean anything.
	Changed bool
	// ChangedFrom and ChangedTo bound the instants at which what the resolver held
	// changed: the coverage of every new observation, and the newly covered part of
	// every extended one. The flows observed inside it are the ones whose attribution
	// the poll can change.
	ChangedFrom int64
	ChangedTo   int64
	// PreviousReadAt is the successful poll before this one, and HadPrevious whether
	// there was one.
	PreviousReadAt int64
	HadPrevious    bool
}

// widen extends the changed interval.
func (r *RecordStorage) widen(from, to int64) {
	if to < from {
		return
	}
	if !r.Changed {
		r.Changed, r.ChangedFrom, r.ChangedTo = true, from, to
		return
	}
	r.ChangedFrom = min(r.ChangedFrom, from)
	r.ChangedTo = max(r.ChangedTo, to)
}

// StoreResourceRecords stores one successful poll of a provider's records, in one
// transaction.
//
// A record seen by the provider's previous successful poll extends that observation:
// its last poll moves to this one, and for the cache its coverage to this poll plus the
// TTL it reported, if that is later. Any other record starts an observation covered from
// the previous successful poll -- or from this one, if there was none -- to this poll
// plus its TTL for the cache, to this poll for local data. A record listed twice in one
// poll is stored once, with the longer TTL. The provider's last successful poll is then
// this one.
func (s *Store) StoreResourceRecords(ctx context.Context, read ResourceRecordRead) (RecordStorage, error) {
	var result RecordStorage
	if read.HeldIn != HeldInCache && read.HeldIn != HeldInLocalData {
		return result, fmt.Errorf("store: %q is neither the cache nor local data", read.HeldIn)
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting a resolver-record write: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	prepared := newPreparedTx(transaction)

	previous, hadPrevious, err := queryOptionalInt(ctx, prepared, "resource_record_read_at",
		map[string]any{"provider_id": read.ProviderID})
	if err != nil {
		return result, err
	}
	// A poll stamped before the previous one -- the host's clock stepped back -- is
	// treated as following it at once: nothing is covered from an instant after the
	// poll that saw it.
	if hadPrevious && previous > read.PolledAt {
		previous = read.PolledAt
	}
	result.PreviousReadAt, result.HadPrevious = previous, hadPrevious
	coveredFrom := read.PolledAt
	if hadPrevious {
		coveredFrom = previous
	}

	for _, record := range deduplicateRecords(read.Records) {
		until := read.PolledAt
		if read.HeldIn == HeldInCache && record.TTLSeconds > 0 {
			until = read.PolledAt + record.TTLSeconds
		}
		parameters := map[string]any{
			"provider_id": read.ProviderID, "held_in": read.HeldIn,
			"owner_name": record.OwnerName, "rrtype": record.RRType, "value": record.Value,
			"address": nullableText(record.Address), "host_label": nullableText(record.HostLabel),
			"seen_at": read.PolledAt, "covered_from": coveredFrom, "until": until,
			"previous_at": previous,
		}
		if hadPrevious {
			id, oldUntil, found, err := currentObservation(ctx, prepared, parameters)
			if err != nil {
				return result, err
			}
			if found {
				parameters["id"] = id
				if _, err := execNamed(ctx, prepared, "resource_record_extend", parameters); err != nil {
					return result, err
				}
				result.Extended++
				if until > oldUntil {
					result.widen(oldUntil, until)
				}
				continue
			}
		}
		if _, err := execNamed(ctx, prepared, "resource_record_insert", parameters); err != nil {
			return result, err
		}
		result.Inserted++
		result.widen(coveredFrom, until)
	}

	if _, err := execNamed(ctx, prepared, "resource_record_read_upsert", map[string]any{
		"provider_id": read.ProviderID, "seen_at": read.PolledAt,
	}); err != nil {
		return result, err
	}
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("store: committing a resolver-record write: %w", err)
	}
	return result, nil
}

// currentObservation finds the observation a record extends: the one of the same
// provider, name, type and value that the previous successful poll saw.
func currentObservation(ctx context.Context, q querier, parameters map[string]any) (int64, int64, bool, error) {
	const name = "resource_record_current"
	text, err := Statement(name)
	if err != nil {
		return 0, 0, false, err
	}
	var id, until int64
	err = q.QueryRowContext(ctx, text, named(text, parameters)...).Scan(&id, &until)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, 0, false, nil
	case err != nil:
		return 0, 0, false, fmt.Errorf("store: %s: %w", name, err)
	}
	return id, until, true, nil
}

// deduplicateRecords keeps one record per name, type and value, with the longest TTL,
// in a stable order.
func deduplicateRecords(records []ResourceRecord) []ResourceRecord {
	type key struct{ owner, rrtype, value string }
	byKey := map[key]ResourceRecord{}
	for _, record := range records {
		k := key{record.OwnerName, record.RRType, record.Value}
		if kept, seen := byKey[k]; !seen || record.TTLSeconds > kept.TTLSeconds {
			byKey[k] = record
		}
	}
	kept := make([]ResourceRecord, 0, len(byKey))
	for _, record := range byKey {
		kept = append(kept, record)
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].OwnerName != kept[j].OwnerName {
			return kept[i].OwnerName < kept[j].OwnerName
		}
		if kept[i].RRType != kept[j].RRType {
			return kept[i].RRType < kept[j].RRType
		}
		return kept[i].Value < kept[j].Value
	})
	return kept
}

// nullableText is NULL for an empty string.
func nullableText(text string) any {
	if text == "" {
		return nil
	}
	return text
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
func scanHeldRecords(ctx context.Context, q querier, name string, parameters map[string]any) ([]heldRecord, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text, named(text, parameters)...)
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
func (s *Store) AnswerAddresses(ctx context.Context, name string, at int64) ([]string, error) {
	addresses, err := answerAddresses(ctx, s.db, DNSName(name), at, false)
	if err != nil {
		return nil, err
	}
	return addresses, nil
}

// answerAddresses is AnswerAddresses on a querier, optionally counting the local data's
// A and AAAA records of the name itself as well -- of the name, never of a name a CNAME
// chain reaches (see MaxCNAMELinks: no CNAME is followed into the local data).
func answerAddresses(ctx context.Context, q querier, name string, at int64, withLocalData bool) ([]string, error) {
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

// ResolveLocalDataHostname resolves a host name through the resolver's local data held
// at an instant: the A and AAAA records whose owner name, and the PTR records whose
// target, has the host name's first label -- compared without regard to case and with a
// trailing dot removed, the rule the leases are matched by. It returns the address when
// exactly one answers, and how many did otherwise (0, or 2 for two or more).
func (s *Store) ResolveLocalDataHostname(ctx context.Context, hostname string, at int64) (string, int, error) {
	text, err := Statement("local_data_addresses_for_hostname")
	if err != nil {
		return "", 0, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("label", HostnameLabel(hostname)), sql.Named("at", at))
	if err != nil {
		return "", 0, fmt.Errorf("store: local_data_addresses_for_hostname: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var addresses []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return "", 0, fmt.Errorf("store: local_data_addresses_for_hostname: %w", err)
		}
		addresses = append(addresses, address)
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("store: local_data_addresses_for_hostname: %w", err)
	}
	if len(addresses) == 1 {
		return addresses[0], 1, nil
	}
	return "", len(addresses), nil
}
