package aggregates

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// AggregateStore is the part of the store this package holds: the aggregate families and the refresh of their periods, and the service's own downtime.
// internal/store composes it into its Store, so its methods are the Store's.
type AggregateStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle, for another part that calls it.
func On(handle *sqldb.Handle) *AggregateStore { return &AggregateStore{Handle: handle} }

// The statements this part runs, each file beside the Go file that runs it, registered under the
// store's name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
