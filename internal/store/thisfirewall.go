package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// This firewall as an end of a flow, the source of a security event or the querier of
// a lookup.
//
// THE TERM IS OPNSENSE'S. The rule editor offers "This Firewall" as a source or a
// destination (opnsense/core 26.7.3, src/opnsense/mvc/app/library/OPNsense/Firewall/
// FilterRule.php, where the stored value `(self)` is rendered as gettext("This
// Firewall"); docs.opnsense.org uses it as a destination in
// https://docs.opnsense.org/manual/how-tos/caddy.html), and the ruleset passes `(self)`
// to pf unchanged (Firewall/Rule.php), where `self` "Expands to all addresses assigned
// to all interfaces" (pf.conf(5), FreeBSD 15.1-RELEASE). The identifiers here are
// this_firewall, after it; the blocked_decision view's target_is_this_firewall already
// used it.
//
// WHAT IS RECOGNISED. An address is this firewall at an instant when the firewall held
// it then -- interface_address under addr4, addr6, ipv4 or ipv6, on any interface, read
// through the this_firewall_address view, whose comment defines "held at an instant"
// -- or when it is a loopback address or the name `localhost`, which are protocol
// constants (RFC 1122, the internet-loopback rule of its IP addressing section, for
// the IPv4 loopback network; RFC 4291, "The Loopback Address", for the IPv6 one; RFC
// 6761, section 6.3, for `localhost`) and not configuration (decision 11 of
// the step-5A live corrections). Recognition comes before every on-link rule, and an
// end recognised as this firewall is never a client, never outside and never a peer.

// DiscoveryIntervalKey is the setting row holding the discovery interval, in seconds,
// and DefaultDiscoveryIntervalSeconds the interval in force when the row is absent.
// internal/config reads the same row and derives its default from this constant, so
// the interval discovery runs at and the margin "held at an instant" allows cannot
// drift apart.
const (
	DiscoveryIntervalKey            = "refresh_interval_discovery_seconds"
	DefaultDiscoveryIntervalSeconds = 300
)

// localhostName is the name RFC 6761, section 6.3, reserves for the loopback, and the
// name a reverse lookup of a loopback address returns.
const localhostName = "localhost"

// IsLoopback reports whether an address or a name, as a row stores it, is the
// loopback: an address of the IPv4 loopback network or the IPv6 loopback address, as
// netip.Addr.IsLoopback decides it -- an IPv4-mapped one included -- or the name
// `localhost`, without regard to case or to a trailing dot.
func IsLoopback(address string) bool {
	if parsed, err := netip.ParseAddr(address); err == nil {
		return parsed.WithZone("").Unmap().IsLoopback()
	}
	return strings.EqualFold(strings.TrimRight(address, "."), localhostName)
}

// IsLocalhostName reports whether a host name is `localhost`, without regard to case
// or to a trailing dot.
func IsLocalhostName(name string) bool {
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	return IsLoopback(name)
}

// heldMargin is how far either side of a sighting an address counts as held: the
// discovery interval in force, from its setting row.
func heldMargin(ctx context.Context, q querier) (int64, error) {
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = ?", DiscoveryIntervalKey).
		Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefaultDiscoveryIntervalSeconds, nil
		}
		return 0, fmt.Errorf("store: reading %s: %w", DiscoveryIntervalKey, err)
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("store: the setting %s is %q, not a positive whole number of seconds",
			DiscoveryIntervalKey, value)
	}
	return seconds, nil
}

// firewallHoldings renders every interval this firewall held an address in, for the
// evidence fingerprint: empty when it never held it.
func firewallHoldings(ctx context.Context, q querier, address string) (string, error) {
	text, err := Statement("firewall_address_holdings")
	if err != nil {
		return "", err
	}
	rows, err := q.QueryContext(ctx, text, named(text, map[string]any{"address": address})...)
	if err != nil {
		return "", fmt.Errorf("store: firewall_address_holdings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var interfaceID, firstSeen int64
		if err := rows.Scan(&interfaceID, &firstSeen); err != nil {
			return "", fmt.Errorf("store: firewall_address_holdings: %w", err)
		}
		parts = append(parts, fmt.Sprintf("%d@%d", interfaceID, firstSeen))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("store: firewall_address_holdings: %w", err)
	}
	return strings.Join(parts, ","), nil
}

// IsThisFirewallAt reports whether an address is this firewall at an instant: a
// loopback address, or one the firewall held then, as the this_firewall_address view
// defines "held at an instant". The address is compared in its canonical form, a zone
// and an IPv4 mapping removed, which is the form discovery stores the firewall's own
// addresses in; a text that is no address is compared as it is.
//
// The neighbour pass asks it of every ARP and NDP entry before it creates a client:
// the tables list the firewall's own interface entries, and an entry at an address the
// firewall holds is this firewall, never a client (defect L2 of
// specs/SPEC-test-budget-and-two-live-defects.md).
func (s *Store) IsThisFirewallAt(ctx context.Context, address string, at int64) (bool, error) {
	if parsed, err := netip.ParseAddr(address); err == nil {
		address = parsed.WithZone("").Unmap().String()
	}
	if IsLoopback(address) {
		return true, nil
	}
	margin, err := heldMargin(ctx, s.db)
	if err != nil {
		return false, err
	}
	_, held, err := queryOptionalInt(ctx, s.db, "this_firewall_held_at",
		map[string]any{"address": address, "at": at, "held_margin": margin})
	return held, err
}
