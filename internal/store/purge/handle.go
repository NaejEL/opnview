package purge

import (
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// PurgeStore is the part of the store this package holds: the retention purge.
// internal/store composes it into its Store, so its methods are the Store's.
type PurgeStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *PurgeStore { return &PurgeStore{Handle: handle} }
