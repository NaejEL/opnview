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

// Interface networks: the on-link address ranges classification reads as
// membership evidence, each one detected by discovery or set by the operator.
//
// THE TERM IS OPNSENSE'S. An interface's address and prefix length cover what
// OPNsense calls that interface's network -- "LAN net" in the rule editor, the
// "LAN network" internal alias (https://docs.opnsense.org/manual/aliases.html) --
// and opnview keeps the word for every interface, whatever it is called.
//
// WHICH NETWORKS COUNT is the maintainer's decision D1: a detected network counts
// until the operator overrides it; an operator network always wins over a detected
// one; a detected network the operator removed no longer counts. Nothing here is a
// default and no range appears in code: every network is either read from the
// firewall or typed by the operator.
//
// AN EDIT TAKES EFFECT AT THE NEXT DERIVATION. Every edit changes the fingerprint
// OnLinkFingerprint returns, and the collector, which compares it before each
// derivation, then places every stored address again and recomputes every slot
// whose flows moved (internal/collect, derive.go). The store does not run the
// derivation itself: derivations are serialised by the collector, and an edit made
// outside it must not race one.

// The two origins of an interface network, stored in interface_network.origin.
const (
	// NetworkOriginDetected is a network discovery proposed from the addresses
	// interfaces_info reports.
	NetworkOriginDetected = "detected"
	// NetworkOriginOperator is a network the operator added or confirmed.
	NetworkOriginOperator = "operator"
)

// The refusals of the operator's write path. Each is wrapped with the detail.
var (
	// ErrMalformedNetwork is a range that does not parse as "address/prefix
	// length", or that has bits set beyond its prefix length.
	ErrMalformedNetwork = errors.New("store: the network is malformed")
	// ErrLinkLocalNetwork is a range overlapping the IPv6 link-local prefix, which
	// no network places: interface.link_local_evidence is the rule for it.
	ErrLinkLocalNetwork = errors.New("store: the network overlaps the IPv6 link-local prefix")
	// ErrUnknownInterface is an interface id no row carries.
	ErrUnknownInterface = errors.New("store: no interface has that id")
	// ErrUpstreamInterface is a network set on an upstream interface, where on-link
	// evidence is not evidence.
	ErrUpstreamInterface = errors.New("store: the interface is upstream, so no network on it places an address")
	// ErrUnknownNetwork is a removal or a confirmation of a network the interface
	// does not carry.
	ErrUnknownNetwork = errors.New("store: the interface carries no such network")
)

// InterfaceNetwork is one stored network of an interface.
type InterfaceNetwork struct {
	// InterfaceID is the interface.
	InterfaceID int64
	// Network is the range, its address masked to its prefix length.
	Network netip.Prefix
	// Origin is NetworkOriginDetected or NetworkOriginOperator.
	Origin string
	// Removed is whether the operator removed it.
	Removed bool
	// Counts is whether classification reads it as membership evidence now,
	// decision D1.
	Counts bool
}

// String renders the network as "interface:range (origin)".
func (n InterfaceNetwork) String() string {
	return fmt.Sprintf("%d:%s (%s)", n.InterfaceID, n.Network, n.Origin)
}

// NetworkEdit is the outcome of one operator edit.
type NetworkEdit struct {
	// Network is the network as stored after the edit.
	Network InterfaceNetwork
	// Overlaps are the networks of OTHER interfaces that count and overlap it. An
	// overlap is reported and not refused: the operator may be correcting a
	// detection, and an operator network wins over a detected one.
	Overlaps []InterfaceNetwork
}

// ParseNetwork validates a range typed by the operator: "address/prefix length",
// with no bit set beyond the prefix length, and not overlapping the IPv6
// link-local prefix.
func ParseNetwork(text string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(text))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q: %v", ErrMalformedNetwork, text, err)
	}
	if prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%w: %q is an IPv4-mapped IPv6 range", ErrMalformedNetwork, text)
	}
	if prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("%w: %q has bits set beyond its prefix length; the network is %s",
			ErrMalformedNetwork, text, prefix.Masked())
	}
	if overlapsLinkLocal(prefix) {
		return netip.Prefix{}, fmt.Errorf("%w: %q", ErrLinkLocalNetwork, text)
	}
	return prefix, nil
}

// overlapsLinkLocal reports whether a prefix shares an address with the IPv6
// link-local prefix: it holds a link-local address, or a link-local address's
// prefix of the same length is it.
func overlapsLinkLocal(prefix netip.Prefix) bool {
	if !prefix.Addr().Is6() {
		return false
	}
	if prefix.Addr().IsLinkLocalUnicast() {
		return true
	}
	// A prefix shorter than the link-local one holds it when the bits it fixes are
	// the link-local ones; the link-local address of the all-zero interface
	// identifier is derived rather than written, so no literal appears.
	var bytes [16]byte
	bytes[0], bytes[1] = 0xfe, 0x80
	return prefix.Contains(netip.AddrFrom16(bytes))
}

// DetectNetworks records the networks discovery read for one interface as of now:
// each address of `addr4`, `addr6`, `ipv4[]` and `ipv6[]` masked to its prefix
// length. A link-local prefix is never proposed. A network already stored only has
// its last_detected_at moved: its origin and the operator's removal are kept.
func (s *Store) DetectNetworks(ctx context.Context, interfaceID int64, prefixes []netip.Prefix,
	now int64) error {
	seen := map[netip.Prefix]struct{}{}
	for _, prefix := range prefixes {
		masked := prefix.Masked()
		if !masked.IsValid() || overlapsLinkLocal(masked) || masked.Addr().Is4In6() {
			continue
		}
		if _, duplicate := seen[masked]; duplicate {
			continue
		}
		seen[masked] = struct{}{}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO interface_network (interface_id, network_address, prefix_length,
			                               address_family, origin, first_detected_at,
			                               last_detected_at, updated_at)
			 VALUES (?, ?, ?, ?, 'detected', ?, ?, ?)
			 ON CONFLICT (interface_id, network_address, prefix_length) DO UPDATE SET
			     first_detected_at = coalesce(interface_network.first_detected_at,
			                                  excluded.first_detected_at),
			     last_detected_at = max(coalesce(interface_network.last_detected_at, 0),
			                            excluded.last_detected_at)`,
			interfaceID, masked.Addr().String(), masked.Bits(), familyOfPrefix(masked),
			now, now, now); err != nil {
			return fmt.Errorf("store: recording a network of interface %d: %w", interfaceID, err)
		}
	}
	return nil
}

// AddInterfaceNetwork sets a network on an interface on the operator's behalf. A
// network already stored, detected or removed, becomes the operator's and counts.
// The networks of other interfaces it overlaps are reported.
func (s *Store) AddInterfaceNetwork(ctx context.Context, interfaceID int64, text string, now int64) (
	NetworkEdit, error) {
	prefix, err := ParseNetwork(text)
	if err != nil {
		return NetworkEdit{}, err
	}
	if err := s.requireInsideInterface(ctx, interfaceID); err != nil {
		return NetworkEdit{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO interface_network (interface_id, network_address, prefix_length,
		                               address_family, origin, updated_at)
		 VALUES (?, ?, ?, ?, 'operator', ?)
		 ON CONFLICT (interface_id, network_address, prefix_length) DO UPDATE SET
		     origin = 'operator', removed_at = NULL, updated_at = excluded.updated_at`,
		interfaceID, prefix.Addr().String(), prefix.Bits(), familyOfPrefix(prefix), now); err != nil {
		return NetworkEdit{}, fmt.Errorf("store: adding a network to interface %d: %w", interfaceID, err)
	}
	return s.networkEdit(ctx, interfaceID, prefix)
}

// ConfirmInterfaceNetwork makes a network the interface already carries the
// operator's, so it counts whatever discovery reports later.
func (s *Store) ConfirmInterfaceNetwork(ctx context.Context, interfaceID int64, text string, now int64) (
	NetworkEdit, error) {
	return s.editStoredNetwork(ctx, interfaceID, text,
		`UPDATE interface_network SET origin = 'operator', removed_at = NULL, updated_at = ?
		 WHERE interface_id = ? AND network_address = ? AND prefix_length = ?`, now)
}

// RemoveInterfaceNetwork removes a network the interface carries: it no longer
// counts, and a detected one is not proposed back into use by discovery.
func (s *Store) RemoveInterfaceNetwork(ctx context.Context, interfaceID int64, text string, now int64) (
	NetworkEdit, error) {
	return s.editStoredNetwork(ctx, interfaceID, text,
		`UPDATE interface_network SET removed_at = ?, updated_at = ?
		 WHERE interface_id = ? AND network_address = ? AND prefix_length = ?`, now, now)
}

// editStoredNetwork runs one edit of a network that must already be stored. The
// statement takes its instants first, then the interface, the address and the
// prefix length.
func (s *Store) editStoredNetwork(ctx context.Context, interfaceID int64, text, statement string,
	instants ...int64) (NetworkEdit, error) {
	prefix, err := ParseNetwork(text)
	if err != nil {
		return NetworkEdit{}, err
	}
	if err := s.requireInsideInterface(ctx, interfaceID); err != nil {
		return NetworkEdit{}, err
	}
	arguments := make([]any, 0, len(instants)+3)
	for _, instant := range instants {
		arguments = append(arguments, instant)
	}
	arguments = append(arguments, interfaceID, prefix.Addr().String(), prefix.Bits())
	result, err := s.db.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return NetworkEdit{}, fmt.Errorf("store: editing a network of interface %d: %w", interfaceID, err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return NetworkEdit{}, fmt.Errorf("store: editing a network of interface %d: %w", interfaceID, err)
	} else if changed == 0 {
		return NetworkEdit{}, fmt.Errorf("%w: interface %d, %s", ErrUnknownNetwork, interfaceID, prefix)
	}
	return s.networkEdit(ctx, interfaceID, prefix)
}

// SetLinkLocalEvidence sets the operator's rule for one interface: whether an IPv6
// link-local address seen on it is evidence that the address belongs to it.
func (s *Store) SetLinkLocalEvidence(ctx context.Context, interfaceID int64, evidence bool) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE interface SET link_local_evidence = ? WHERE id = ?", boolToInt(evidence), interfaceID)
	if err != nil {
		return fmt.Errorf("store: setting the link-local rule of interface %d: %w", interfaceID, err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("store: setting the link-local rule of interface %d: %w", interfaceID, err)
	} else if changed == 0 {
		return fmt.Errorf("%w: %d", ErrUnknownInterface, interfaceID)
	}
	return nil
}

// requireInsideInterface refuses an interface that does not exist or is upstream.
func (s *Store) requireInsideInterface(ctx context.Context, interfaceID int64) error {
	var upstream int64
	err := s.db.QueryRowContext(ctx, "SELECT is_upstream FROM interface WHERE id = ?",
		interfaceID).Scan(&upstream)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %d", ErrUnknownInterface, interfaceID)
	case err != nil:
		return fmt.Errorf("store: reading interface %d: %w", interfaceID, err)
	case upstream == 1:
		return fmt.Errorf("%w: %d", ErrUpstreamInterface, interfaceID)
	}
	return nil
}

// networkEdit reads one network back, with the counting networks of other
// interfaces that overlap it.
func (s *Store) networkEdit(ctx context.Context, interfaceID int64, prefix netip.Prefix) (NetworkEdit, error) {
	networks, err := s.InterfaceNetworks(ctx)
	if err != nil {
		return NetworkEdit{}, err
	}
	var edit NetworkEdit
	for _, network := range networks {
		if network.InterfaceID == interfaceID && network.Network == prefix {
			edit.Network = network
			continue
		}
		if network.InterfaceID != interfaceID && network.Counts && network.Network.Overlaps(prefix) {
			edit.Overlaps = append(edit.Overlaps, network)
		}
	}
	return edit, nil
}

// InterfaceNetworks returns every stored network, and whether it counts now.
func (s *Store) InterfaceNetworks(ctx context.Context) ([]InterfaceNetwork, error) {
	counting, err := loadPrefixes(ctx, s.db)
	if err != nil {
		return nil, err
	}
	counts := map[string]bool{}
	for _, evidence := range counting {
		counts[fmt.Sprintf("%d|%s", evidence.interfaceID, evidence.prefix)] = true
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT interface_id, network_address, prefix_length, origin, removed_at IS NOT NULL
		 FROM interface_network ORDER BY interface_id, network_address, prefix_length`)
	if err != nil {
		return nil, fmt.Errorf("store: reading the interface networks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var networks []InterfaceNetwork
	for rows.Next() {
		var (
			network InterfaceNetwork
			address string
			bits    int
		)
		if err := rows.Scan(&network.InterfaceID, &address, &bits, &network.Origin,
			&network.Removed); err != nil {
			return nil, fmt.Errorf("store: reading the interface networks: %w", err)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			return nil, fmt.Errorf("store: the stored network %s/%d does not parse: %w", address, bits, err)
		}
		if network.Network, err = parsed.Prefix(bits); err != nil {
			return nil, fmt.Errorf("store: the stored network %s/%d does not parse: %w", address, bits, err)
		}
		network.Counts = counts[fmt.Sprintf("%d|%s", network.InterfaceID, network.Network)]
		networks = append(networks, network)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the interface networks: %w", err)
	}
	return networks, nil
}

// OnLinkFingerprint renders everything classification reads of the interfaces
// themselves: which are upstream, which carry the link-local rule, and which
// networks count and with which origin. A change of it calls for every stored
// address to be placed again; each address is then placed in full only when its
// own evidence changed.
func (s *Store) OnLinkFingerprint(ctx context.Context) (string, error) {
	var parts []string
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, is_upstream, link_local_evidence FROM interface ORDER BY id")
	if err != nil {
		return "", fmt.Errorf("store: reading the interfaces: %w", err)
	}
	for rows.Next() {
		var id, upstream, linkLocal int64
		if err := rows.Scan(&id, &upstream, &linkLocal); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("store: reading the interfaces: %w", err)
		}
		parts = append(parts, fmt.Sprintf("interface:%d:%d:%d", id, upstream, linkLocal))
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return "", fmt.Errorf("store: reading the interfaces: %w", err)
	}
	counting, err := loadPrefixes(ctx, s.db)
	if err != nil {
		return "", err
	}
	for _, evidence := range counting {
		parts = append(parts, fmt.Sprintf("network:%d:%s:%t", evidence.interfaceID, evidence.prefix,
			evidence.operator))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";"), nil
}

// familyOfPrefix is 4 or 6.
func familyOfPrefix(prefix netip.Prefix) int64 {
	if prefix.Addr().Is4() {
		return 4
	}
	return 6
}
