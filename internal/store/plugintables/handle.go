package plugintables

import (
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// PluginTableStore is the part of the store this package holds: the tables plugins own: ensuring, writing, reading and purging them.
// internal/store composes it into its Store, so its methods are the Store's.
type PluginTableStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *PluginTableStore { return &PluginTableStore{Handle: handle} }
