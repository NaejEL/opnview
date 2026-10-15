package flows

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// FlowStore is the part of the store this package holds: the typed writes, the flows and their pairing, the classification of addresses, the interfaces' networks and this firewall's own addresses.
// internal/store composes it into its Store, so its methods are the Store's.
type FlowStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *FlowStore { return &FlowStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
