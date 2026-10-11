package widget

import (
	"embed"

	"github.com/NaejEL/opnview/internal/store"
)

// The statements the core runs for every widget: the availability and gap reads, the settings,
// the reference lookups, the installation's interfaces, clients and owners, and the figures
// several widgets share. Statements used by one widget alone live in that widget's own
// plugin directory (plugins/<plugin>/widgets/<type>/*.statements.sql) and are registered when
// the plugin loads (pluginwidget.go); a core statements file is named for the core file that runs it.
//
//go:embed *.statements.sql
var coreStatements embed.FS

func init() { store.RegisterStatements("widget", coreStatements) }
