package reads

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// ReadStore is the part of the store this package holds: the read paths: volume windows and series, connection trees, the surfaces' rows, widget statements and catalogue reads.
// internal/store composes it into its Store, so its methods are the Store's.
type ReadStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *ReadStore { return &ReadStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
