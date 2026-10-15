package specialpurpose

import (
	"net/netip"
)

// Entry is one block of a registry.
type Entry struct {
	// Block is the address block.
	Block netip.Prefix
	// Name is the registry's name for it.
	Name string
	// Reference is the RFC the registry cites.
	Reference string
	// GloballyReachable is the registry's "Globally Reachable" column; nil where the
	// registry gives no value (N/A, or no data).
	GloballyReachable *bool
}

var (
	reachable   = true
	unreachable = false
)

// entry builds one row; a malformed block is a defect in this file, caught by its test.
func entry(block, name, reference string, globallyReachable *bool) Entry {
	prefix, _ := netip.ParsePrefix(block)
	return Entry{Block: prefix, Name: name, Reference: reference, GloballyReachable: globallyReachable}
}

// Registry is both registries, in their published order.
var Registry = []Entry{
	entry("0.0.0.0/8", "This network", "RFC 791", &unreachable),
	entry("0.0.0.0/32", "This host on this network", "RFC 1122", &unreachable),
	entry("10.0.0.0/8", "Private-Use", "RFC 1918", &unreachable),
	entry("100.64.0.0/10", "Shared Address Space", "RFC 6598", &unreachable),
	entry("127.0.0.0/8", "Loopback", "RFC 1122", &unreachable),
	entry("169.254.0.0/16", "Link Local", "RFC 3927", &unreachable),
	entry("172.16.0.0/12", "Private-Use", "RFC 1918", &unreachable),
	entry("192.0.0.0/24", "IETF Protocol Assignments", "RFC 6890", &unreachable),
	entry("192.0.0.0/29", "IPv4 Service Continuity Prefix", "RFC 7335", &unreachable),
	entry("192.0.0.8/32", "IPv4 dummy address", "RFC 7600", &unreachable),
	entry("192.0.0.9/32", "Port Control Protocol Anycast", "RFC 7723", &reachable),
	entry("192.0.0.10/32", "Traversal Using Relays around NAT Anycast", "RFC 8155", &reachable),
	entry("192.0.0.170/32", "NAT64/DNS64 Discovery", "RFC 8880", &unreachable),
	entry("192.0.0.171/32", "NAT64/DNS64 Discovery", "RFC 8880", &unreachable),
	entry("192.0.2.0/24", "Documentation (TEST-NET-1)", "RFC 5737", &unreachable),
	entry("192.31.196.0/24", "AS112-v4", "RFC 7535", &reachable),
	entry("192.52.193.0/24", "AMT", "RFC 7450", &reachable),
	entry("192.88.99.0/24", "Deprecated (6to4 Relay Anycast)", "RFC 7526", nil),
	entry("192.88.99.2/32", "6a44-relay anycast address", "RFC 6751", &unreachable),
	entry("192.168.0.0/16", "Private-Use", "RFC 1918", &unreachable),
	entry("192.175.48.0/24", "Direct Delegation AS112 Service", "RFC 7534", &reachable),
	entry("198.18.0.0/15", "Benchmarking", "RFC 2544", &unreachable),
	entry("198.51.100.0/24", "Documentation (TEST-NET-2)", "RFC 5737", &unreachable),
	entry("203.0.113.0/24", "Documentation (TEST-NET-3)", "RFC 5737", &unreachable),
	entry("240.0.0.0/4", "Reserved", "RFC 1112", &unreachable),
	entry("255.255.255.255/32", "Limited Broadcast", "RFC 8190", &unreachable),

	entry("::1/128", "Loopback Address", "RFC 4291", &unreachable),
	entry("::/128", "Unspecified Address", "RFC 4291", &unreachable),
	entry("::ffff:0:0/96", "IPv4-mapped Address", "RFC 4291", &unreachable),
	entry("64:ff9b::/96", "IPv4-IPv6 Translat.", "RFC 6052", &reachable),
	entry("64:ff9b:1::/48", "IPv4-IPv6 Translat.", "RFC 8215", &reachable),
	entry("100::/64", "Discard-Only Address Block", "RFC 6666", &reachable),
	entry("100:0:0:1::/64", "Dummy IPv6 Prefix", "RFC 9780", &unreachable),
	entry("2001::/23", "IETF Protocol Assignments", "RFC 2928", &unreachable),
	entry("2001::/32", "TEREDO", "RFC 4380", &reachable),
	entry("2001:1::1/128", "Port Control Protocol Anycast", "RFC 7723", &reachable),
	entry("2001:1::2/128", "Traversal Using Relays around NAT Anycast", "RFC 8155", &reachable),
	entry("2001:1::3/128", "DNS-SD Service Registration Protocol Anycast", "RFC 9665", &reachable),
	entry("2001:2::/48", "Benchmarking", "RFC 5180", &reachable),
	entry("2001:3::/32", "AMT", "RFC 7450", &reachable),
	entry("2001:4:112::/48", "AS112-v6", "RFC 7535", &reachable),
	entry("2001:10::/28", "Deprecated (previously ORCHID)", "RFC 4843", nil),
	entry("2001:20::/28", "ORCHIDv2", "RFC 7343", &reachable),
	entry("2001:30::/28", "Drone Remote ID Protocol Entity Tags (DETs) Prefix", "RFC 9374", &reachable),
	entry("2001:db8::/32", "Documentation", "RFC 3849", &unreachable),
	entry("2002::/16", "6to4", "RFC 3056", &reachable),
	entry("2620:4f:8000::/48", "Direct Delegation AS112 Service", "RFC 7534", &reachable),
	entry("3fff::/20", "Documentation", "RFC 9637", &unreachable),
	// The registry names this block after SRv6, the routing it serves, in a phrase whose
	// first word this repository does not use (CLAUDE.md, "Vocabulary"); the RFC is the
	// unambiguous reference.
	entry("5f00::/16", "SRv6 SIDs", "RFC 9602", &reachable),
	entry("fc00::/7", "Unique-Local", "RFC 4193", &reachable),
	entry("fe80::/10", "Link-Local Unicast", "RFC 4291", &unreachable),
}

// Lookup returns the most specific registry block holding an address, and whether one
// does. The most specific block decides reachability: 192.0.0.9/32 is globally reachable
// although the /24 holding it is not.
func Lookup(address netip.Addr) (Entry, bool) {
	address = address.Unmap()
	var (
		best  Entry
		found bool
	)
	for _, candidate := range Registry {
		if !candidate.Block.IsValid() || !candidate.Block.Contains(address) {
			continue
		}
		if !found || candidate.Block.Bits() > best.Block.Bits() {
			best, found = candidate, true
		}
	}
	return best, found
}

// Public reports whether an address is one the Internet can reach: outside every block,
// or inside a block the registry marks globally reachable. A block with no value in the
// registry is not presented as public.
func Public(address netip.Addr) bool {
	block, special := Lookup(address)
	if !special {
		return true
	}
	return block.GloballyReachable != nil && *block.GloballyReachable
}
