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
	rows, err := transaction.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
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

	lookupsText, err := Statement("attribution_lookups")
	if err != nil {
		return result, err
	}
	for _, flow := range candidates {
		var clientID any
		if flow.clientID.Valid {
			clientID = flow.clientID.Int64
		}
		lookups, err := transaction.QueryContext(ctx, lookupsText,
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
			changed, err = execNamed(ctx, transaction, "attribution_upsert", map[string]any{
				"flow_id": flow.id, "lookup_id": latestID, "site_name": domain,
				"delay": flow.observedAt - latestAt, "now": now,
			})
		} else {
			changed, err = execNamed(ctx, transaction, "attribution_delete",
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

// ResolveLeaseHostname resolves a host name the resolver logged as a lookup's
// client to the address leased under it at the instant at: a DHCP lease under that
// name, or under its first label, that had started -- if its start is known -- and
// had not expired -- if its expiry is known.
//
// WHY THE FIRST LABEL. The resolver's query report replaces the querying address by
// what a reverse lookup of it returned (opnsense/core 26.7.3,
// scripts/unbound/logger.py, socket.gethostbyaddr, joined in by
// scripts/unbound/stats.py), and a reverse lookup answers with the fully qualified
// name, while a lease carries the host's own label.
//
// It returns the address and ClientResolutionLeased when exactly one address
// matches; otherwise the logged name verbatim and ClientResolutionAmbiguous
// or ClientResolutionUnknown, which place the lookup nowhere and attribute
// nothing.
func (s *Store) ResolveLeaseHostname(ctx context.Context, hostname string, at int64) (string, string, error) {
	label := hostname
	if dot := strings.IndexByte(hostname, '.'); dot > 0 {
		label = hostname[:dot]
	}
	text, err := Statement("lease_addresses_for_hostname")
	if err != nil {
		return "", "", err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("hostname", hostname),
		sql.Named("label", label), sql.Named("at", at))
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

// HostnameResolution is what one pass of ResolveLoggedHostnames did.
type HostnameResolution struct {
	// Addresses are the addresses lookups now resolve to, to be placed.
	Addresses []string
	// Instants are those lookups' instants, which bound the flows whose attribution
	// has to be decided again.
	Instants []int64
	// Examined is how many unresolved lookups the pass resolved again: the ones
	// ingested since the instant it was given, and no others.
	Examined int
}

// ResolveLoggedHostnames resolves again every lookup ingested at or after since whose
// logged host name named no single address when it was read, each as of its own instant,
// against the leases held now. A lease pass passes the start of the previous one: a
// lookup read before the lease that names its host -- every loop runs at start -- is
// resolved by the next lease pass, and one an earlier pass already tried is not tried
// again, so the work is bounded by the lookups read since and does not grow with the
// unresolved ones that accumulate, a host whose name no lease carries for instance.
func (s *Store) ResolveLoggedHostnames(ctx context.Context, since int64) (HostnameResolution, error) {
	var result HostnameResolution
	text, err := Statement("unresolved_hostname_lookups")
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("since", since))
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
		changed, err := execNamed(ctx, s.db, "resolve_hostname_lookup", map[string]any{
			"lookup_id": lookup.id, "address": address, "resolution": resolution,
		})
		if err != nil {
			return result, err
		}
		if changed > 0 && resolution == ClientResolutionLeased {
			result.Addresses = append(result.Addresses, address)
			result.Instants = append(result.Instants, lookup.lookedUpAt)
		}
	}
	return result, nil
}
