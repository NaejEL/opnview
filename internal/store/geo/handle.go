package geo

import (
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// GeoStore is the part of the store this package holds: the geolocation and autonomous-system rows.
// internal/store composes it into its Store, so its methods are the Store's.
type GeoStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *GeoStore { return &GeoStore{Handle: handle} }
