package flows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
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

// IsLocalDelivery reports whether a destination address is delivered to the firewall itself whatever interface
// holds it: the loopback, the IPv4 limited broadcast -- the address of all ones, RFC 919 -- and every multicast
// address, IPv4's (RFC 5771) and IPv6's (RFC 4291), as netip.Addr.IsMulticast decides it, an IPv4-mapped one
// included. A packet to one of them that pf logged
// is the firewall's to receive, so its end is This Firewall and the flow is never north_south (D5 of the live
// validation of 17 October 2026, specs/SPEC-backend-completion-plugins.md).
func IsLocalDelivery(address string) bool {
	if IsLoopback(address) {
		return true
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	parsed = parsed.WithZone("").Unmap()
	return parsed.IsMulticast() || parsed == netip.AddrFrom4([4]byte{255, 255, 255, 255})
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
func heldMargin(ctx context.Context, q sqldb.Querier) (int64, error) {
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = $1", DiscoveryIntervalKey).
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
func (s *FlowStore) IsThisFirewallAt(ctx context.Context, address string, at int64) (bool, error) {
	if parsed, err := netip.ParseAddr(address); err == nil {
		address = parsed.WithZone("").Unmap().String()
	}
	if IsLocalDelivery(address) {
		return true, nil
	}
	margin, err := heldMargin(ctx, s.SQL)
	if err != nil {
		return false, err
	}
	_, held, err := sqldb.QueryOptionalInt(ctx, s.SQL, "this_firewall_held_at",
		map[string]any{"address": address, "at": at, "held_margin": margin})
	return held, err
}

// HeldMargin is the discovery interval in force, which "held at an instant" allows on either side of a
// sighting: what the clients plugin's derivation is handed as :held_margin (ADR 0020 wave 3).
func (s *FlowStore) HeldMargin(ctx context.Context) (int64, error) { return heldMargin(ctx, s.SQL) }
