package accounts

import (
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// AccountStore is the part of the store this package holds: accounts, sessions, encrypted credentials and setting rows.
// internal/store composes it into its Store, so its methods are the Store's.
type AccountStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *AccountStore { return &AccountStore{Handle: handle} }
