package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
)

// Classification: which interface an address belongs to, if any, and which client
// stands behind it.
//
// MEMBERSHIP COMES FROM ON-LINK EVIDENCE AND FROM NOTHING ELSE. In order of
// strength:
//
//  1. a lease naming the address on an interface;
//  2. a client identity at the address -- a lease's client identifier or a MAC
//     from a lease or a neighbour table -- on an interface;
//  3. an interface network that contains the address (interface_network): an
//     operator network wins over a detected one, decision D1, and the longest
//     prefix wins within each;
//  4. a sighting: a record logged inbound on an interface names its source as on
//     that interface's side, one logged outbound names its destination; the most
//     recent sighting wins.
//
// EVIDENCE ON AN UPSTREAM INTERFACE IS NOT EVIDENCE. An interface is upstream
// when interfaces_info reports a gateway behind it, and an address reached
// through one is OUTSIDE: it has no interface membership and it never becomes a
// client row. That is what stops a remote address from being minted into a
// phantom client whose interface is whatever record happened to name it first.
//
// AN IPV6 LINK-LOCAL ADDRESS IS EVIDENCE ONLY WHERE THE OPERATOR SAYS SO. Its
// prefix is the same on every interface, so the same address can sit behind any
// of them: evidence for one counts only on an interface whose link_local_evidence
// rule is set, and no interface network ever places one.
//
// THE RESULT DEPENDS ON THE EVIDENCE HELD AND NOT ON THE ORDER IT ARRIVED IN.
// Every input is a set the stores only grow, every tie is broken on a stored
// value, the addresses of one pass are processed in sorted order -- which is the
// order any new client identity is minted in -- and re-running it over the same
// evidence changes nothing.
//
// A PASS IS INCREMENTAL. The evidence an address was placed from is recorded as a
// fingerprint (address_classification). When it is unchanged, only the rows with
// an unplaced end are placed -- the rows stored since -- and a pass over an
// address with nothing new writes no row at all. When it has changed, or the
// address was never placed, every row naming the address is placed again.

// AddressIdleWindow is how long an address may go unseen before a later sighting
// is treated as a different machine, at the address level of the identity
// cascade. See internal/collect, identity.go, for why a day.
const AddressIdleWindow = 86400

// AddressIdentityKey composes the address-level identity key: the interface, the
// address, and the substitute validity start, which is the UTC day of now.
func AddressIdentityKey(interfaceID *int64, address string, now int64) string {
	interfacePart := "unknown"
	if interfaceID != nil {
		interfacePart = strconv.FormatInt(*interfaceID, 10)
	}
	return fmt.Sprintf("%s|%s|%d", interfacePart, address, (now/86400)*86400)
}

// The kinds of evidence, in order of strength, as a placement records them.
const (
	evidenceLease    = "lease"
	evidenceIdentity = "identity"
	evidenceNetwork  = "network"
	evidenceSighting = "sighting"
	evidenceNone     = "none"
)

// Reclassification is what one classification pass changed.
type Reclassification struct {
	// FlowInstants are the observed_at instants of every flow whose interface or
	// client changed, which is every slot a refresh must recompute.
	FlowInstants []int64
	// LookupInstants are the looked_up_at instants of every lookup that changed,
	// which bounds the flows whose attribution has to be decided again.
	LookupInstants []int64
	// ClientsPurged is how many client rows went because nothing named them any
	// more.
	ClientsPurged int64
	// Placed is how many addresses were placed in full because their evidence had
	// changed or they had never been placed; every other address of the pass only
	// had its unplaced rows placed.
	Placed int
}

func (r *Reclassification) add(other Reclassification) {
	r.FlowInstants = append(r.FlowInstants, other.FlowInstants...)
	r.LookupInstants = append(r.LookupInstants, other.LookupInstants...)
	r.ClientsPurged += other.ClientsPurged
	r.Placed += other.Placed
}

// prefixEvidence is one interface network that counts.
type prefixEvidence struct {
	interfaceID int64
	prefix      netip.Prefix
	operator    bool
}

// placement is where the evidence puts an address, and which evidence did.
type placement struct {
	interfaceID *int64
	kind        string
}

// Membership returns the interface an address belongs to, or nil when it is
// outside.
func (s *Store) Membership(ctx context.Context, address string) (*int64, error) {
	prefixes, err := loadPrefixes(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return membership(ctx, s.db, address, prefixes)
}

// Reclassify places each address and rewrites every row naming it that needs it:
// the two ends of a flow, the source of a security event and the querier of a
// lookup. An address whose evidence is unchanged since it was last placed has only
// its unplaced rows placed.
//
// Clients that nothing names any more afterwards, at the addresses placed in full,
// are removed; see the purge statements in derive.sql for the exact guard.
func (s *Store) Reclassify(ctx context.Context, addresses []string, now int64) (Reclassification, error) {
	unique := map[string]struct{}{}
	for _, address := range addresses {
		if address != "" {
			unique[address] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(unique))
	for address := range unique {
		sorted = append(sorted, address)
	}
	sort.Strings(sorted)

	var total Reclassification
	// Bounded transactions, so a whole-history pass does not hold the single
	// connection for minutes.
	const chunk = 500
	for begin := 0; begin < len(sorted); begin += chunk {
		end := min(begin+chunk, len(sorted))
		result, err := s.reclassifyChunk(ctx, sorted[begin:end], now)
		if err != nil {
			return total, err
		}
		total.add(result)
	}
	return total, nil
}

// ReclassifyAll reclassifies every address any flow, lookup or security event
// carries, then removes every client nothing names that is address-level or sits
// behind an upstream interface. It is the operation that clears the remote-address
// clients step 4 created, and the one a change of the interfaces' networks or of
// which interfaces are upstream calls for; each address is still placed in full
// only when its own evidence changed, and running it twice changes nothing the
// second time.
func (s *Store) ReclassifyAll(ctx context.Context, now int64) (Reclassification, error) {
	addresses := map[string]struct{}{}
	for _, name := range []string{"next_source_address", "next_destination_address",
		"next_lookup_address", "next_event_address"} {
		if err := walkDistinct(ctx, s.db, name, func(address string) {
			addresses[address] = struct{}{}
		}); err != nil {
			return Reclassification{}, err
		}
	}
	list := make([]string, 0, len(addresses))
	for address := range addresses {
		list = append(list, address)
	}
	result, err := s.Reclassify(ctx, list, now)
	if err != nil {
		return result, err
	}
	purged, err := s.PurgeUnreferencedClients(ctx)
	result.ClientsPurged += purged
	return result, err
}

// PurgeUnreferencedClients removes every client that nothing names, that nobody
// attributed to a person, and that is address-level or behind an upstream
// interface.
func (s *Store) PurgeUnreferencedClients(ctx context.Context) (int64, error) {
	var total int64
	for _, name := range []string{"purge_unreferenced_address_level_clients",
		"purge_unreferenced_upstream_clients"} {
		removed, err := execNamed(ctx, s.db, name, nil)
		if err != nil {
			return total, err
		}
		total += removed
	}
	return total, nil
}

// walkDistinct walks one "next address" statement from the start.
func walkDistinct(ctx context.Context, q querier, name string, visit func(string)) error {
	text, err := Statement(name)
	if err != nil {
		return err
	}
	after := ""
	for {
		var next sql.NullString
		if err := q.QueryRowContext(ctx, text, sql.Named("address", after)).Scan(&next); err != nil {
			return fmt.Errorf("store: %s: %w", name, err)
		}
		if !next.Valid {
			return nil
		}
		visit(next.String)
		after = next.String
	}
}

// reclassifyChunk classifies a sorted run of addresses inside one transaction.
func (s *Store) reclassifyChunk(ctx context.Context, addresses []string, now int64) (
	Reclassification, error) {
	var result Reclassification
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting a classification: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	prefixes, err := loadPrefixes(ctx, transaction)
	if err != nil {
		return result, err
	}
	for _, address := range addresses {
		one, err := classifyAddress(ctx, transaction, address, prefixes, now)
		if err != nil {
			return result, err
		}
		result.add(one)
	}
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("store: committing a classification: %w", err)
	}
	return result, nil
}

// classifyAddress places one address and rewrites every row that names it and
// needs it.
func classifyAddress(ctx context.Context, q querier, address string, prefixes []prefixEvidence,
	now int64) (Reclassification, error) {
	var result Reclassification
	place, err := placeAddress(ctx, q, address, prefixes)
	if err != nil {
		return result, err
	}
	interfaceID := place.interfaceID

	var identityClient *int64
	if interfaceID != nil {
		if id, found, err := queryOptionalInt(ctx, q, "client_identity_at_address",
			map[string]any{"address": address}); err != nil {
			return result, err
		} else if found {
			identityClient = &id
		}
	}

	// The fingerprint is every input of the outcome: which evidence placed the
	// address, on which interface, and the strongest identity there.
	evidence := fmt.Sprintf("%s|%s|%s", place.kind, idText(interfaceID), idText(identityClient))
	var recorded sql.NullString
	text, err := Statement("classification_evidence")
	if err != nil {
		return result, err
	}
	err = q.QueryRowContext(ctx, text, sql.Named("address", address)).Scan(&recorded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("store: classification_evidence: %w", err)
	}
	full := !recorded.Valid || recorded.String != evidence
	if !full && interfaceID == nil {
		// An outside address whose evidence is unchanged has nothing to place: the
		// collector stores every end with no interface and no client, which is what
		// an outside end is, and a lookup from it is placed below only if pending.
		lookups, err := queryInts(ctx, q, "classify_lookup", map[string]any{
			"address": address, "interface_id": nil, "identity_client_id": nil,
			"address_client_id": nil, "full": 0,
		})
		if err != nil {
			return result, err
		}
		result.LookupInstants = lookups
		return result, nil
	}

	var addressClient *int64
	if interfaceID != nil && identityClient == nil {
		id, found, err := queryOptionalInt(ctx, q, "client_address_level", map[string]any{
			"address": address, "interface_id": *interfaceID, "not_before": now - AddressIdleWindow,
		})
		if err != nil {
			return result, err
		}
		if found {
			addressClient = &id
		} else {
			// Mint an address-level identity only when a flow end at this address
			// has no client at all: a client nobody refers to would only inflate
			// the count of machines known behind the interface.
			_, needed, err := queryOptionalInt(ctx, q, "unclassified_end_at_address",
				map[string]any{"address": address})
			if err != nil {
				return result, err
			}
			if needed {
				minted, err := upsertClient(ctx, q, Client{
					Identity: ClientIdentity{
						Kind: IdentityAddressInInterface,
						Key:  AddressIdentityKey(interfaceID, address, now),
					},
					InterfaceID: interfaceID,
					LastAddress: &address,
				}, now)
				if err != nil {
					return result, err
				}
				addressClient = &minted
			}
		}
	}

	fullFlag := 0
	if full {
		fullFlag = 1
	}
	parameters := map[string]any{
		"address":            address,
		"interface_id":       optional(interfaceID),
		"identity_client_id": optional(identityClient),
		"address_client_id":  optional(addressClient),
		"full":               fullFlag,
	}
	// The purged part of the address-level client a better identity replaces follows
	// its flows to that identity, so the hours it lies in are recomputed with them.
	if full && identityClient != nil {
		for _, name := range []string{"repoint_purged_local_client", "repoint_purged_source_client"} {
			hours, err := queryInts(ctx, q, name, parameters)
			if err != nil {
				return result, err
			}
			result.FlowInstants = append(result.FlowInstants, hours...)
		}
	}
	for _, name := range []string{"classify_flow_source", "classify_flow_destination"} {
		instants, err := queryInts(ctx, q, name, parameters)
		if err != nil {
			return result, err
		}
		result.FlowInstants = append(result.FlowInstants, instants...)
	}
	if _, err := execNamed(ctx, q, "classify_event_source", parameters); err != nil {
		return result, err
	}
	instants, err := queryInts(ctx, q, "classify_lookup", parameters)
	if err != nil {
		return result, err
	}
	result.LookupInstants = instants
	if !full {
		return result, nil
	}

	purged, err := execNamed(ctx, q, "purge_unreferenced_clients_at_address",
		map[string]any{"address": address})
	if err != nil {
		return result, err
	}
	result.ClientsPurged = purged
	if _, err := execNamed(ctx, q, "classification_record", map[string]any{
		"address": address, "evidence": evidence, "now": now,
	}); err != nil {
		return result, err
	}
	result.Placed = 1
	return result, nil
}

// idText renders a nullable id for a fingerprint.
func idText(id *int64) string {
	if id == nil {
		return "-"
	}
	return strconv.FormatInt(*id, 10)
}

// membership applies the four kinds of evidence in order of strength.
func membership(ctx context.Context, q querier, address string, prefixes []prefixEvidence) (*int64, error) {
	place, err := placeAddress(ctx, q, address, prefixes)
	return place.interfaceID, err
}

// IsLinkLocal reports whether an address, as flow stores it, is an IPv6 link-local
// unicast address (RFC 4291, section 2.5.6). An IPv4-mapped address is not.
func IsLinkLocal(address string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	parsed = parsed.WithZone("")
	return parsed.Is6() && !parsed.Is4In6() && parsed.IsLinkLocalUnicast()
}

// placeAddress applies the four kinds of evidence in order of strength, and says
// which one decided.
func placeAddress(ctx context.Context, q querier, address string, prefixes []prefixEvidence) (
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
		id, found, err := queryOptionalInt(ctx, q, step.name, parameters)
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
		text, err := Statement(name)
		if err != nil {
			return placement{}, err
		}
		var id, seenAt int64
		err = q.QueryRowContext(ctx, text, named(text, parameters)...).Scan(&id, &seenAt)
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

// longestNetwork finds the network containing an address, decision D1: an operator
// network wins over a detected one, the longest prefix wins within each, and a tie
// goes to the lowest interface id.
func longestNetwork(address netip.Addr, prefixes []prefixEvidence) (int64, bool) {
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

// loadPrefixes reads the interface networks that count, decision D1.
func loadPrefixes(ctx context.Context, q querier) ([]prefixEvidence, error) {
	text, err := Statement("membership_networks")
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var evidence []prefixEvidence
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
		evidence = append(evidence, prefixEvidence{interfaceID: interfaceID, prefix: prefix,
			operator: origin == NetworkOriginOperator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	return evidence, nil
}
