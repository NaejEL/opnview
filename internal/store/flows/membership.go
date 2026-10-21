package flows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Membership: which interface an address belongs to, from on-link evidence alone, in order of
// strength -- a lease, a client identity, an interface network (an operator network before a detected
// one, the longest prefix within each), the most recent sighting. Evidence on an upstream interface is
// not evidence, and an IPv6 link-local address is evidence only where the operator says so.
//
// THE CLASSIFICATION ITSELF IS THE clients PLUGIN'S (plugins/clients, ADR 0020 wave 3), in SQL. What
// stays here is the membership read the sampled rates still split inside from outside with
// (internal/store/samples), until that read moves with them; and the address-level identity key the
// lease coordinator composes.

// AddressIdleWindow is how long an address may go unseen before a later sighting is treated as a
// different machine, at the address level of the identity cascade. The clients plugin declares the
// same value as its setting address_idle_window_seconds.
const AddressIdleWindow = 86400

// AddressIdentityKey composes the address-level identity key: the interface, the address, and the
// substitute validity start, which is the UTC day of now.
func AddressIdentityKey(interfaceID *int64, address string, now int64) string {
	interfacePart := "unknown"
	if interfaceID != nil {
		interfacePart = strconv.FormatInt(*interfaceID, 10)
	}
	return fmt.Sprintf("%s|%s|%d", interfacePart, address, (now/86400)*86400)
}

// The kinds of evidence, in order of strength.
const (
	evidenceLease    = "lease"
	evidenceIdentity = "identity"
	evidenceNetwork  = "network"
	evidenceSighting = "sighting"
	evidenceNone     = "none"
)

// PrefixEvidence is one interface network that counts.
type PrefixEvidence struct {
	interfaceID int64
	prefix      netip.Prefix
	operator    bool
}

// placement is where the evidence puts an address, and which evidence did.
type placement struct {
	interfaceID *int64
	kind        string
}

// Membership applies the four kinds of evidence in order of strength.
func Membership(ctx context.Context, q sqldb.Querier, address string, prefixes []PrefixEvidence) (*int64, error) {
	place, err := placeAddress(ctx, q, address, prefixes)
	return place.interfaceID, err
}

// IsLinkLocal reports whether an address, as flow stores it, is an IPv6 link-local unicast address
// (RFC 4291, section 2.5.6). An IPv4-mapped address is not.
func IsLinkLocal(address string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	parsed = parsed.WithZone("")
	return parsed.Is6() && !parsed.Is4In6() && parsed.IsLinkLocalUnicast()
}

// placeAddress applies the four kinds of evidence in order of strength, and says which one decided.
func placeAddress(ctx context.Context, q sqldb.Querier, address string, prefixes []PrefixEvidence) (
	placement, error) {
	if address == "" {
		return placement{kind: evidenceNone}, nil
	}
	linkLocal := 0
	if IsLinkLocal(address) {
		linkLocal = 1
	}
	parameters := map[string]any{"address": address, "link_local": linkLocal}
	for _, step := range []struct{ name, kind string }{
		{"membership_lease", evidenceLease}, {"membership_identity", evidenceIdentity},
	} {
		id, found, err := sqldb.QueryOptionalInt(ctx, q, step.name, parameters)
		if err != nil {
			return placement{}, err
		}
		if found {
			return placement{interfaceID: &id, kind: step.kind}, nil
		}
	}

	if parsed, err := netip.ParseAddr(address); err == nil && linkLocal == 0 {
		if id, found := longestNetwork(parsed.WithZone("").Unmap(), prefixes); found {
			return placement{interfaceID: &id, kind: evidenceNetwork}, nil
		}
	}

	var (
		chosen   int64
		chosenAt int64
		found    bool
	)
	for _, name := range []string{"membership_sighting_source", "membership_sighting_destination"} {
		text, err := sqldb.Statement(name)
		if err != nil {
			return placement{}, err
		}
		var id, seenAt int64
		err = sqldb.QueryRow(ctx, q, text, parameters).Scan(&id, &seenAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return placement{}, fmt.Errorf("store: %s: %w", name, err)
		}
		if !found || seenAt > chosenAt || (seenAt == chosenAt && id < chosen) {
			chosen, chosenAt, found = id, seenAt, true
		}
	}
	if found {
		return placement{interfaceID: &chosen, kind: evidenceSighting}, nil
	}
	return placement{kind: evidenceNone}, nil
}

// longestNetwork finds the network containing an address, decision D1: an operator network wins over a
// detected one, the longest prefix wins within each, and a tie goes to the lowest interface id.
func longestNetwork(address netip.Addr, prefixes []PrefixEvidence) (int64, bool) {
	for _, operator := range []bool{true, false} {
		bestBits := -1
		var best int64
		for _, evidence := range prefixes {
			if evidence.operator != operator || !evidence.prefix.Contains(address) {
				continue
			}
			bits := evidence.prefix.Bits()
			if bits > bestBits || (bits == bestBits && evidence.interfaceID < best) {
				bestBits, best = bits, evidence.interfaceID
			}
		}
		if bestBits >= 0 {
			return best, true
		}
	}
	return 0, false
}

// LoadPrefixes reads the interface networks that count, decision D1.
func LoadPrefixes(ctx context.Context, q sqldb.Querier) ([]PrefixEvidence, error) {
	text, err := sqldb.Statement("membership_networks")
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var evidence []PrefixEvidence
	for rows.Next() {
		var (
			interfaceID int64
			address     string
			bits        int64
			origin      string
		)
		if err := rows.Scan(&interfaceID, &address, &bits, &origin); err != nil {
			return nil, fmt.Errorf("store: membership_networks: %w", err)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		prefix, err := parsed.Unmap().Prefix(int(bits))
		if err != nil {
			continue
		}
		evidence = append(evidence, PrefixEvidence{interfaceID: interfaceID, prefix: prefix,
			operator: origin == NetworkOriginOperator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	return evidence, nil
}

// ChangedFlowInstants are the observed_at instants of the flows a derivation changed since an instant:
// the slots the aggregate refresh recomputes (ADR 0020 wave 3; plugins/clients stamps changed_at).
func (s *FlowStore) ChangedFlowInstants(ctx context.Context, since int64) ([]int64, error) {
	rows, err := s.SQL.QueryContext(ctx, "SELECT DISTINCT observed_at FROM flow WHERE changed_at >= $1", since)
	if err != nil {
		return nil, fmt.Errorf("store: reading the changed flows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var instants []int64
	for rows.Next() {
		var instant int64
		if err := rows.Scan(&instant); err != nil {
			return nil, fmt.Errorf("store: reading the changed flows: %w", err)
		}
		instants = append(instants, instant)
	}
	return instants, rows.Err()
}
