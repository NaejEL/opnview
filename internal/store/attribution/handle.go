package attribution

import "github.com/NaejEL/opnview/internal/store/sqldb"

// AttributionStore is the part of the store this package holds: each provider's record rate. The
// attribution itself, the resolver's records and the host-name resolution are the unbound plugin's
// since ADR 0020 wave 4.
// internal/store composes it into its Store, so its methods are the Store's.
type AttributionStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *AttributionStore { return &AttributionStore{Handle: handle} }
