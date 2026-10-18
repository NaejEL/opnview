// Package dashboards is the part of the store holding the dashboards: each a document of
// docs/dashboard-format.md stored as the text it was given, with its position, its revision and its
// origin (specs/SPEC-step-7-frontend.md, A7). It is not composed into store.Store: the dashboards
// surface reaches it through On over the store's handle.
package dashboards

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// DashboardStore is this part of the store.
type DashboardStore struct{ *sqldb.Handle }

// On is this part of the store on an open handle.
func On(handle *sqldb.Handle) *DashboardStore { return &DashboardStore{Handle: handle} }

// The statements this part runs, beside the Go file that runs them, registered under the store's
// name (sqldb.RegisterStatements).
//
//go:embed *.statements.sql
var statementFiles embed.FS

func init() { sqldb.RegisterStatements("store", statementFiles) }
