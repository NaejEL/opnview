package owners

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// OwnerStore is the part of the store this package holds: owners, the people clients are assigned to, their assignments, interface labels and blocklists.
// internal/store composes it into its Store, so its methods are the Store's.
type OwnerStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *OwnerStore { return &OwnerStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
