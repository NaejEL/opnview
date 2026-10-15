package figures

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The statements this part runs for every widget, each file named for the Go file that runs it and
// registered under the widget core's name. Statements used by one widget alone live in that
// widget's own plugin directory (plugins/<plugin>/widgets/<type>/*.statements.sql) and are
// registered when the plugin loads (internal/widget, pluginwidget.go).
//
//go:embed *.statements.sql
var coreStatements embed.FS

func init() { sqldb.RegisterStatements("widget", coreStatements) }
