package widgets

import (
	"context"
	"net/netip"
	"testing"

	"github.com/NaejEL/opnview/internal/specialpurpose"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// AC19: Public address and the special-purpose registries.

// TestTheSpecialPurposeRegistryDecidesWhatIsPublic checks the registries' arithmetic: every
// block parses, the most specific block decides, and a special-purpose IPv4 address that is
// not globally reachable is never public.
func TestTheSpecialPurposeRegistryDecidesWhatIsPublic(t *testing.T) {
	t.Parallel()
	for _, entry := range specialpurpose.Registry {
		if !entry.Block.IsValid() || entry.Reference == "" || entry.Name == "" {
			t.Errorf("a registry row is malformed: %+v", entry)
		}
	}
	cases := map[string]bool{
		"10.1.2.3":        false, // RFC 1918
		"100.64.0.1":      false, // RFC 6598, carrier-grade NAT
		"192.168.1.1":     false,
		"203.0.113.2":     false, // documentation
		"192.0.0.9":       true,  // the /32 inside a /24 that is not reachable
		"192.0.0.8":       false,
		"2001:db8::1":     false,
		"fe80::1":         false,
		"11.22.33.44":     true, // in no block
		"2a00:abcd::1":    true,
		"::ffff:10.0.0.1": false, // IPv4-mapped, read as the IPv4 address
	}
	for text, public := range cases {
		if got := specialpurpose.Public(netip.MustParseAddr(text)); got != public {
			t.Errorf("%s: public %v, expected %v", text, got, public)
		}
	}
}

// TestPublicAddressSaysWhenTheInstallationIsBehindAnotherNAT is AC19: per upstream
// interface, the address history and whether each held address is special-purpose, with the
// registries cited; a special-purpose IPv4 address is never presented as public, so the
// installation reads behind an upstream NAT; and the change instant is absent until a
// second address has been seen.
func TestPublicAddressSaysWhenTheInstallationIsBehindAnotherNAT(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 2, flows: 20, span: 3600})
	first := decodeData[publicAddressData](t, f.respond(t, "public_address", nil))
	if len(first.Upstreams) != 1 || len(first.Registries) != 2 {
		t.Fatalf("public address reads %+v", first)
	}
	upstream := first.Upstreams[0]
	if upstream.BehindUpstreamNAT == nil || !*upstream.BehindUpstreamNAT {
		t.Error("a documentation-range upstream address does not read behind an upstream NAT")
	}
	for _, address := range upstream.Current {
		if address.Family == "ipv4" && (address.Public || address.SpecialPurpose == nil) {
			t.Errorf("%s is presented as public", address.Address)
		}
		if address.ChangedAt != nil || address.ChangeState != "not_recorded_only_one_address_seen" {
			t.Errorf("one address seen, and a change instant reads %v (%s)", address.ChangedAt, address.ChangeState)
		}
	}
	if len(upstream.Gateways) != 1 {
		t.Errorf("the gateway is not listed: %+v", upstream.Gateways)
	}

	// A second address on the same field: the change instant appears, and the first one is
	// history.
	bits := int64(24)
	check(t, f.db.UpsertInterfaceAddress(context.Background(), vocab.InterfaceAddress{InterfaceID: f.upstream.id,
		SourceField: vocab.SourceFieldAddr4, Address: "11.22.33.44", PrefixLength: &bits, AddressFamily: 4}, nil, f.now))
	f.exec(t, "UPDATE interface SET last_seen_at = $1 WHERE id = $2", f.now, f.upstream.id)
	second := decodeData[publicAddressData](t, f.respond(t, "public_address", nil)).Upstreams[0]
	changed := false
	for _, address := range second.Current {
		if address.Address == "11.22.33.44" {
			changed = address.ChangedAt != nil && *address.ChangedAt == f.now && address.Public
		}
	}
	if !changed {
		t.Errorf("the new address carries no change instant: %+v", second.Current)
	}
	if second.BehindUpstreamNAT == nil || *second.BehindUpstreamNAT {
		t.Error("a public upstream address still reads behind an upstream NAT")
	}
	if len(second.History) == 0 {
		t.Error("the previous address is not in the history")
	}
	ipv6Only := decodeData[publicAddressData](t, f.respond(t, "public_address", map[string]any{"families": []any{"ipv6"}}))
	for _, address := range ipv6Only.Upstreams[0].Current {
		if address.Family != "ipv6" {
			t.Errorf("families = ipv6 returned %s", address.Address)
		}
	}
}
