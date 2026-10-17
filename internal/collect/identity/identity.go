package identity

import (
	"github.com/NaejEL/opnview/internal/store/flows"
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
// is treated as a different machine. It is store.AddressIdleWindow, which the
// classification reads when it mints a level-3 identity.
const AddressIdleWindow = flows.AddressIdleWindow

// A LEVEL-3 IDENTITY IS MINTED BY THE CLASSIFICATION AND BY NOTHING HERE. It used to
// be minted for any address a filter-log record named, on whatever interface the
// record happened to be logged on, which turned every remote address into a client
// row. The classification (internal/store/flows/classify.go) mints one only for an address
// with on-link evidence on a non-upstream interface, after the whole pass is stored.

// AddressIdentityKey composes the level-3 key: the interface, the address, and the
// substitute validity start. The composition is store.AddressIdentityKey's, so a
// lease falling to this level and the classification compose the same key.
func AddressIdentityKey(interfaceID *int64, address string, now int64) string {
	return flows.AddressIdentityKey(interfaceID, address, now)
}

// DayStart truncates an instant to the start of its UTC day.
//
// It stayed in this package when the decoding layer moved out, and the reason is worth a line:
// decoding turns what a SOURCE said into the canonical epoch, whereas this is arithmetic performed
// on an epoch opnview already owns. Its two callers are both identity decisions — the validity
// start of a level-3 client above, and the substitute start a lease backend that reports none gets
// in internal/collect/kinds/leases/dhcplease.go — so it belongs where those decisions are made.
func DayStart(epoch int64) int64 {
	const secondsPerDay = 86400
	return (epoch / secondsPerDay) * secondsPerDay
}
