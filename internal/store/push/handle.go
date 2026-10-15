package push

import (
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// PushStore is the part of the store this package holds: the push sources' bookkeeping: their gaps, their listeners' liveness and their source periods.
// internal/store composes it into its Store, so its methods are the Store's.
type PushStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *PushStore { return &PushStore{Handle: handle} }
