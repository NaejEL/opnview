package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Site-name attribution.
//
// THE RESOLVER LOG CARRIES NO ANSWER ADDRESS. /api/unbound/overview/search_queries
// returns client, domain, time, action, source, rcode, dnssec_status, blocklist and
// uuid (survey, data source 5), and nothing that says which address a name
// resolved to. So an attribution cannot be "this flow went to the address that
// name resolved to"; it is "this client looked this name up, and then opened this
// flow". The rule, the maintainer's:
//
//	A flow gets an attribution exactly when the eligible lookups by the same
//	client in [observed_at - max delay, observed_at] name exactly one distinct
//	domain. Two lookups of that same domain still attribute. Otherwise the flow
//	gets no row.
//
// The same client is the same client_id where both rows carry one and the same
// address otherwise. An eligible flow has an outside destination; an eligible
// lookup passed and was answered by recursion, from the cache or from local data.
// site_name is the domain verbatim, the lookup referenced is the latest of the
// matching ones, and the delay is the flow's instant minus that lookup's -- never
// negative, because the window ends at the flow.
//
// The strict rule gives low rates on busy clients, and that is the honest outcome:
// a client that looked up three names in five seconds has not said which one the
// flow was for.

// Attribution is what one attribution pass changed.
type Attribution struct {
	// FlowInstants are the observed_at instants of every flow whose attribution was
	// added, changed or removed: the slots of the domain family to recompute.
	FlowInstants []int64
	// Decided is how many eligible flows the pass decided on.
	Decided int
}

// Attribute decides the attribution of every eligible flow observed in
// [from, to], with lookups up to maxDelay seconds before each flow.
func (s *Store) Attribute(ctx context.Context, from, to, maxDelay, now int64) (Attribution, error) {
	var result Attribution
	if maxDelay <= 0 {
		return result, fmt.Errorf("store: the attribution delay must be a positive number of seconds, got %d",
			maxDelay)
	}
	if to < from {
		return result, nil
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting an attribution: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	// The same statements run once per record, so each is prepared once (exec.go).
	prepared := newPreparedTx(transaction)

	type candidate struct {
		id         int64
		observedAt int64
		clientID   sql.NullInt64
		address    string
	}
	text, err := Statement("attribution_candidates")
	if err != nil {
		return result, err
	}
	rows, err := prepared.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return result, fmt.Errorf("store: attribution_candidates: %w", err)
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.observedAt, &c.clientID, &c.address); err != nil {
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
	// loses its attribution.
	ineligibleText, err := Statement("attribution_ineligible")
	if err != nil {
		return result, err
	}
	ineligible, err := prepared.QueryContext(ctx, ineligibleText, sql.Named("from", from),
		sql.Named("to", to))
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
		changed, err := execNamed(ctx, prepared, "attribution_delete", map[string]any{"flow_id": one.id})
		if err != nil {
			return result, err
		}
		if changed > 0 {
			result.FlowInstants = append(result.FlowInstants, one.observedAt)
		}
	}

	lookupsText, err := Statement("attribution_lookups")
	if err != nil {
		return result, err
	}
	for _, flow := range candidates {
		var clientID any
		if flow.clientID.Valid {
			clientID = flow.clientID.Int64
		}
		lookups, err := prepared.QueryContext(ctx, lookupsText,
			sql.Named("client_id", clientID), sql.Named("address", flow.address),
			sql.Named("start", flow.observedAt-maxDelay), sql.Named("end", flow.observedAt))
		if err != nil {
			return result, fmt.Errorf("store: attribution_lookups: %w", err)
		}
		domains := map[string]struct{}{}
		var (
			latestID int64
			latestAt int64
			domain   string
			matched  bool
		)
		for lookups.Next() {
			var (
				id         int64
				name       string
				lookedUpAt int64
			)
			if err := lookups.Scan(&id, &name, &lookedUpAt); err != nil {
				_ = lookups.Close()
				return result, fmt.Errorf("store: attribution_lookups: %w", err)
			}
			domains[name] = struct{}{}
			if !matched || lookedUpAt > latestAt || (lookedUpAt == latestAt && id > latestID) {
				latestID, latestAt, domain, matched = id, lookedUpAt, name, true
			}
		}
		err = lookups.Err()
		_ = lookups.Close()
		if err != nil {
			return result, fmt.Errorf("store: attribution_lookups: %w", err)
		}
		result.Decided++

		var changed int64
		if len(domains) == 1 {
			changed, err = execNamed(ctx, prepared, "attribution_upsert", map[string]any{
				"flow_id": flow.id, "lookup_id": latestID, "site_name": domain,
				"delay": flow.observedAt - latestAt, "now": now,
			})
		} else {
			changed, err = execNamed(ctx, prepared, "attribution_delete",
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

// ResolveLeaseHostname resolves a host name the resolver logged as a lookup's client to
// the address leased under it at the instant at: a DHCP lease whose host name has the
// same first label, compared without regard to case and with a trailing dot removed on
// both sides, that had started -- if its start is known -- and had not expired -- if
// its expiry is known.
//
// WHY THE FIRST LABEL, ON BOTH SIDES. The resolver's query report replaces the querying
// address by what a reverse lookup of it returned (opnsense/core 26.7.3,
// scripts/unbound/logger.py, socket.gethostbyaddr, joined in by
// scripts/unbound/stats.py). That name may be fully qualified, may carry the trailing
// dot of an absolute name, or may be a bare label, and a lease may carry any of the
// three: 5A compared the logged name and its label with the lease's whole name, so a
// bare logged label never matched a lease carrying `host.domain` (cause H1 of the
// step-5A live corrections) and a trailing dot on either side defeated both (H2).
//
// `localhost` names this firewall -- it is what a reverse lookup of a loopback address
// returns (RFC 6761, section 6.3) -- and is ClientResolutionThisFirewall whatever the
// leases hold (H4; decision 11).
//
// It returns the address and ClientResolutionLeased when exactly one address matches;
// otherwise the logged name verbatim and ClientResolutionAmbiguous,
// ClientResolutionUnknown or ClientResolutionThisFirewall, which attribute nothing.
func (s *Store) ResolveLeaseHostname(ctx context.Context, hostname string, at int64) (string, string, error) {
	if IsLocalhostName(hostname) {
		return hostname, ClientResolutionThisFirewall, nil
	}
	label := HostnameLabel(hostname)
	text, err := Statement("lease_addresses_for_hostname")
	if err != nil {
		return "", "", err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("label", label), sql.Named("at", at))
	if err != nil {
		return "", "", fmt.Errorf("store: lease_addresses_for_hostname: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var addresses []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return "", "", fmt.Errorf("store: lease_addresses_for_hostname: %w", err)
		}
		addresses = append(addresses, address)
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("store: lease_addresses_for_hostname: %w", err)
	}
	switch len(addresses) {
	case 1:
		return addresses[0], ClientResolutionLeased, nil
	case 0:
		return hostname, ClientResolutionUnknown, nil
	default:
		return hostname, ClientResolutionAmbiguous, nil
	}
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

// HostnameResolution is what one pass of ResolveLoggedHostnames did.
type HostnameResolution struct {
	// Addresses are the addresses lookups now resolve to, to be placed.
	Addresses []string
	// Instants are those lookups' instants, which bound the flows whose attribution
	// has to be decided again.
	Instants []int64
	// Examined is how many unresolved lookups the pass resolved again: the ones no
	// lease pass had examined since they were ingested, and no others.
	Examined int
}

// ResolveLoggedHostnames resolves again every lookup whose logged host name named no
// single address when it was read and that no lease pass has examined since, each as of
// its own instant, against the leases held now, and marks it examined at now.
//
// Only the lookups ingested before ingestedBefore are examined: the caller passes the
// instant its lease read began, and a lookup ingested since may postdate the leases it
// read, so it is left for the next lease pass. The caller runs it only after a lease
// pass read every active backend: one examination is all a lookup gets.
//
// The mark is stored, so the first lease pass after a restart examines what the run
// before it left unexamined (cause H6 of the step-5A live corrections, which kept the
// point to resume from in memory, starting at the collector's creation). A lookup is
// examined once by a lease pass after it was ingested, and not again: the work of a
// pass is the lookups waiting for it, read through a partial index that holds those
// and nothing else, and it does not grow with the unresolved lookups that accumulate
// -- a host whose name no lease carries, for instance.
func (s *Store) ResolveLoggedHostnames(ctx context.Context, ingestedBefore, now int64) (HostnameResolution, error) {
	var result HostnameResolution
	text, err := Statement("unresolved_hostname_lookups")
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("ingested_before", ingestedBefore))
	if err != nil {
		return result, fmt.Errorf("store: unresolved_hostname_lookups: %w", err)
	}
	type pending struct {
		id         int64
		hostname   string
		lookedUpAt int64
	}
	var lookups []pending
	for rows.Next() {
		var (
			lookup   pending
			hostname sql.NullString
		)
		if err := rows.Scan(&lookup.id, &hostname, &lookup.lookedUpAt); err != nil {
			_ = rows.Close()
			return result, fmt.Errorf("store: unresolved_hostname_lookups: %w", err)
		}
		if hostname.Valid {
			lookup.hostname = hostname.String
			lookups = append(lookups, lookup)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, fmt.Errorf("store: unresolved_hostname_lookups: %w", err)
	}

	for _, lookup := range lookups {
		result.Examined++
		address, resolution, err := s.ResolveLeaseHostname(ctx, lookup.hostname, lookup.lookedUpAt)
		if err != nil {
			return result, err
		}
		before, err := s.lookupResolution(ctx, lookup.id)
		if err != nil {
			return result, err
		}
		if _, err := execNamed(ctx, s.db, "resolve_hostname_lookup", map[string]any{
			"lookup_id": lookup.id, "address": address, "resolution": resolution, "now": now,
		}); err != nil {
			return result, err
		}
		if before != resolution {
			// The lookup now names another querier, or this firewall, so it is placed
			// again from what it now holds and the flows it could name are decided
			// again.
			result.Addresses = append(result.Addresses, address)
			result.Instants = append(result.Instants, lookup.lookedUpAt)
		}
	}
	return result, nil
}

// lookupResolution reads one lookup's client_resolution.
func (s *Store) lookupResolution(ctx context.Context, id int64) (string, error) {
	var resolution string
	if err := s.db.QueryRowContext(ctx, "SELECT client_resolution FROM dns_resolution WHERE id = ?",
		id).Scan(&resolution); err != nil {
		return "", fmt.Errorf("store: reading lookup %d: %w", id, err)
	}
	return resolution, nil
}
