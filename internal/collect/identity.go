package collect

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// The client identity cascade, most stable level first, and the substitute the
// last level needs.
//
// docs/data-model.md defines three levels: the lease's own client identifier, the
// MAC, and the address behind an interface over a VALIDITY INTERVAL. The first two
// are handed to opnview by a lease or by the neighbour tables. The third is the
// one with a problem, and it is worth stating plainly rather than burying:
//
// THE FILTER LOG CARRIES NO VALIDITY START, so the level-3 key cannot be composed
// from what the record itself says. Get it wrong in one direction and two machines
// that shared an address months apart merge into one phantom client; get it wrong
// in the other and one machine becomes a new client on every poll.
//
// THE SUBSTITUTE. The validity start of a level-3 identity is the instant opnview
// first saw that (interface, address) pair after a period of AddressIdleWindow in
// which it saw nothing from it, truncated to the start of the UTC day. Two
// consequences, both deliberate:
//
//   - a machine seen continuously keeps one identity, because the existing row is
//     found and reused rather than re-minted;
//   - an address that goes quiet for longer than the window and then reappears
//     becomes a second identity, which is the reissue case the model requires to
//     stay two rows.
//
// The window is opnview's own and it is a compromise, not a measurement: the
// firewall does publish lease lifetimes, but a level-3 client is by definition one
// no lease named, so its lifetime is exactly what is unavailable. A day is chosen
// because it is the coarsest unit that still separates a reissue in practice, and
// because a shorter one would split a laptop that is shut overnight. THIS IS THE
// WEAKEST IDENTITY opnview forms, and the cascade is ordered so that it is only
// ever reached when neither a lease nor a neighbour table knows the machine — which
// is why the neighbour tables are collected at all.

// AddressIdleWindow is how long an address may go unseen before a later sighting
// is treated as a different machine.
const AddressIdleWindow = 86400

// clientForAddress returns the machine at an address, creating a level-3 identity
// only when nothing more stable knows it.
//
// interfaceID is the interface the address was seen behind, when the caller knows
// it; a level-3 key composed without one is still unique, because the address and
// the validity start remain in it, and it is honest — the alternative would be
// inventing an interface for a machine whose interface is unknown.
func (c *Collector) clientForAddress(ctx context.Context, interfaceID *int64,
	address string, now int64) (*int64, *int64, error) {
	if address == "" {
		return nil, nil, nil
	}

	// A lease, a neighbour table entry or an earlier sighting already named this
	// machine, and the sighting is recent enough to be the same one.
	id, knownInterfaceID, found, err := c.store.ClientRefByAddressSince(ctx, address, now-AddressIdleWindow)
	if err != nil {
		return nil, nil, err
	}
	if found {
		resolvedInterface := knownInterfaceID
		if resolvedInterface == nil {
			resolvedInterface = interfaceID
		}
		return &id, resolvedInterface, nil
	}

	key := addressIdentityKey(interfaceID, address, now)
	newID, err := c.store.UpsertClient(ctx, store.Client{
		Identity:    store.ClientIdentity{Kind: store.IdentityAddressInInterface, Key: key},
		InterfaceID: interfaceID,
		LastAddress: &address,
	}, now)
	if err != nil {
		return nil, nil, err
	}
	return &newID, interfaceID, nil
}

// addressIdentityKey composes the level-3 key: the interface, the address, and the
// substitute validity start.
func addressIdentityKey(interfaceID *int64, address string, now int64) string {
	interfacePart := "unknown"
	if interfaceID != nil {
		interfacePart = fmt.Sprintf("%d", *interfaceID)
	}
	return fmt.Sprintf("%s|%s|%d", interfacePart, address, DayStart(now))
}

// normaliseMAC lower-cases a hardware address and reports whether the result is
// the seventeen-character form the schema constrains.
//
// The constraint is not decoration: client.mac_is_randomised tests the SECOND HEX
// DIGIT for the IEEE locally-administered bit, and that test is only total on a
// value of known shape. A MAC opnview cannot normalise is stored as absent rather
// than stored wrong, because a wrong second digit would mark a real burned-in
// address as an unstable identity.
func normaliseMAC(raw string) (string, bool) {
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

// macPointer returns a normalised MAC as a nullable column value.
func macPointer(raw string) *string {
	normalised, ok := normaliseMAC(raw)
	if !ok {
		return nil
	}
	return &normalised
}

// DayStart truncates an instant to the start of its UTC day.
//
// It stayed in this package when the decoding layer moved out, and the reason is worth a line:
// decoding turns what a SOURCE said into the canonical epoch, whereas this is arithmetic
// performed on an epoch opnview already owns. Its two callers are both identity decisions — the
// validity start of a level-3 client above, and the substitute start a lease backend that
// reports none gets in dhcplease.go — so it belongs where those decisions are made.
func DayStart(epoch int64) int64 {
	const secondsPerDay = 86400
	return (epoch / secondsPerDay) * secondsPerDay
}
