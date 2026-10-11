// Package dhcplease is the dhcp_lease kind: a source of the lease table of a DHCP server.
//
// WHAT THE KIND PRODUCES. A page of Observations, each one lease generation, with the field-name
// differences between backends already normalised away by the implementation that read it. What
// is true of any lease source is here or in internal/collect (dhcplease.go): the identity
// cascade, resolving the interface the backend named, reading one page of the standard envelope
// (ReadLeaseRows), normalising the field names (DecodeLease), and writing one row per lease
// generation.
//
// THIS IS THE KIND THAT MADE THE SEAM NECESSARY RATHER THAN NOTIONAL. Kea, Dnsmasq and the
// end-of-life ISC plugin are implementations of it; on the firewall the survey probed, Dnsmasq
// serves DHCP and Kea is disabled, and somebody else's firewall is the other way round. The kind
// admits several concurrently active providers: a lease is keyed by (address, generation_key,
// provider_id).
//
// THE VALIDITY START IS REPORTED BY KEA AND BY NO OTHER BACKEND, and the model says so instead of
// substituting: StartsAt is a pointer, nil meaning "this backend cannot say". What makes a
// re-poll idempotent is dhcp_lease.generation_key, composed by store.GenerationKeyOf.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/dhcplease/<product>/ holding a
// provider.json whose kind is "dhcp_lease", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. An implementation that can never be
// read returns provider.ErrUnsupportedRead from Leases and says why. See docs/extending.md for a
// complete worked example.
package dhcplease

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Kind is the registry kind this package answers for.
const Kind = "dhcp_lease"

// Observation is one lease generation, with the field-name differences between backends already
// normalised away by the implementation that read it.
type Observation struct {
	Address      string
	MAC          *string
	Hostname     *string
	DHCPClientID *string
	DUID         *string
	IAID         *string
	VendorHint   *string
	LeaseState   string
	// InterfaceIdentifier and InterfaceDevice are whichever of the interface's names the
	// backend reported; the kind resolves them against discovery. The DESCRIPTION the
	// backends also report is deliberately absent: an interface has one description and it
	// is discovered authoritatively elsewhere.
	InterfaceIdentifier string
	InterfaceDevice     string
	// StartsAt is the validity start, and nil when the backend reports none. It is a pointer
	// rather than a zero so that "the lease began at this instant" and "this backend cannot
	// say" are two different values: only Kea reports one, through `valid_lifetime`, and an
	// implementation that substituted anything here would be asserting a figure it read
	// nowhere. What keeps a re-poll idempotent is dhcp_lease.generation_key instead.
	StartsAt  *int64
	ExpiresAt *int64
}

// Source is one implementation of the dhcp_lease kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Leases returns the backend's current lease table, or provider.ErrUnsupportedRead.
	Leases(ctx context.Context, host provider.Session, pageSize int) ([]Observation, provider.Result, error)
}

// ReadLeaseRows is the part of a lease read that is the same for every backend: one page, the
// standard envelope, and the availability state the answer implies.
//
// The page holds pageSize rows, the operator's page size for the kind. The table is small --
// one row per active lease -- so one page is the whole of it in practice.
//
// IT IS A FREE FUNCTION OVER THE PORT RATHER THAN A METHOD ON IT. Only this kind's
// implementations call it, and a port that gains a method for one kind's convenience stops
// being a boundary. Written this way it reads identically at the call site and costs a connector
// nothing, and when connectors speak a protocol it travels with the lease reader instead of
// becoming a verb the far side has to implement.
func ReadLeaseRows(ctx context.Context, host provider.Session, endpoint opnsense.Endpoint, pageSize int) (
	[]decode.Object, provider.Result, error) {
	result := provider.Result{Probe: endpoint, State: store.StateUnavailable,
		Outcome: store.OutcomeRequestNotMade}

	response, err := host.Call(ctx, endpoint, opnsense.RequestOptions{
		Form: opnsense.Pagination(1, pageSize),
	})
	if err != nil && response.Outcome == "" {
		return nil, result, err
	}
	if !response.OK() {
		result.Outcome = provider.ResponseOutcome(response)
		result.Detail = response.Detail
		return nil, result, fmt.Errorf("collect: %s answered %s", endpoint.Path, response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		result.Outcome = store.OutcomeUndecodable
		result.Detail = "the lease table could not be read"
		return nil, result, fmt.Errorf("collect: reading %s: %w", endpoint.Path, err)
	}
	result.State, result.Outcome = store.StateReachable, store.OutcomeHealthy
	result.Separable = true
	if len(rows) == 0 {
		result.Outcome = store.OutcomeHealthyEmpty
		// Returned as an empty, non-nil slice so a caller can tell "read nothing" from "did
		// not read".
		return []decode.Object{}, result, nil
	}
	return rows, result, nil
}

// DecodeLease reads the fields the two current backends and the legacy plugin share, with the
// field-name differences normalised -- `hwaddr` against `mac`, `state` against `lease_type` and
// `is_reserved`.
//
// IT DOES NOT INVENT A VALIDITY START. What the shared envelope carries is an expiry; the start
// is left nil here, and only a backend that genuinely reports one -- Kea, through
// `valid_lifetime` -- fills it in its own directory.
func DecodeLease(row decode.Object) (Observation, bool) {
	address, present := decode.String(row, "address")
	if !present {
		// A lease with no address names no machine and keys nothing.
		return Observation{}, false
	}

	mac := MACPointer(decode.RawString(row, "hwaddr"))
	if mac == nil {
		// `mac` is the legacy plugin's spelling of the same field, and normalising the two
		// names is the only normalisation the survey says leases need.
		mac = MACPointer(decode.RawString(row, "mac"))
	}

	expiresAt := decode.EpochPointer(row, "expire")
	if expiresAt == nil {
		expiresAt = decode.EpochPointer(row, "ends")
	}

	observation := Observation{
		Address:             address,
		MAC:                 mac,
		Hostname:            decode.StringPointer(row, "hostname"),
		DHCPClientID:        decode.StringPointer(row, "client_id"),
		DUID:                decode.StringPointer(row, "duid"),
		IAID:                decode.StringPointer(row, "iaid"),
		VendorHint:          decode.StringPointer(row, "mac_info"),
		LeaseState:          NormaliseLeaseState(row),
		InterfaceIdentifier: decode.RawString(row, "if_name"),
		InterfaceDevice:     decode.RawString(row, "device"),
		ExpiresAt:           expiresAt,
	}
	return observation, true
}

// NormaliseLeaseState maps the three backends' several state fields onto the one vocabulary
// the schema constrains: `state` on Kea, `lease_type` plus `is_reserved` on Dnsmasq, and
// `state` plus `status` on the legacy plugin.
//
// A value none of them matches is `unknown`, never folded into `active`: a screen that showed
// an expired lease as active would name a machine that has gone.
func NormaliseLeaseState(row decode.Object) string {
	if reserved := decode.Flag(row, "is_reserved"); reserved != nil && *reserved {
		return "reserved"
	}
	for _, key := range []string{"state", "lease_type", "status"} {
		value, present := decode.String(row, key)
		if !present {
			continue
		}
		switch decode.LowerASCII(value) {
		case "active", "assigned", "1":
			return "active"
		case "expired", "released", "free", "backup", "0":
			return "expired"
		case "reserved", "static":
			return "reserved"
		}
	}
	return "unknown"
}

// NormaliseMAC lower-cases a hardware address and reports whether the result is
// the seventeen-character form the schema constrains.
//
// The constraint is not decoration: client.mac_is_randomised tests the SECOND HEX
// DIGIT for the IEEE locally-administered bit, and that test is only total on a
// value of known shape. A MAC opnview cannot normalise is stored as absent rather
// than stored wrong, because a wrong second digit would mark a real burned-in
// address as an unstable identity.
func NormaliseMAC(raw string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if len(trimmed) != 17 {
		return "", false
	}
	for index := 0; index < len(trimmed); index++ {
		character := trimmed[index]
		if index%3 == 2 {
			if character != ':' {
				return "", false
			}
			continue
		}
		isDigit := character >= '0' && character <= '9'
		isHex := character >= 'a' && character <= 'f'
		if !isDigit && !isHex {
			return "", false
		}
	}
	return trimmed, true
}

// MACPointer returns a normalised MAC as a nullable column value.
func MACPointer(raw string) *string {
	normalised, ok := NormaliseMAC(raw)
	if !ok {
		return nil
	}
	return &normalised
}
