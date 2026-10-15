package netflow

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// NetflowStore is the part of the store this package holds: NetFlow records, the capture configuration, their join to the logged connections, and the measured volumes.
// internal/store composes it into its Store, so its methods are the Store's.
type NetflowStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *NetflowStore { return &NetflowStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
