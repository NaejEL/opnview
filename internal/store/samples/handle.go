package samples

import "github.com/NaejEL/opnview/internal/store/sqldb"

// SampleStore is the part of the store this package holds: the measurement interval the samples are taken at.
// internal/store composes it into its Store, so its methods are the Store's.
type SampleStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *SampleStore { return &SampleStore{Handle: handle} }
