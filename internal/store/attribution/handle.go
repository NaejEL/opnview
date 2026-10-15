package attribution

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// AttributionStore is the part of the store this package holds: DNS resource records, the attribution of flows to the names looked up, hostnames, and the record rate.
// internal/store composes it into its Store, so its methods are the Store's.
type AttributionStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *AttributionStore { return &AttributionStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
