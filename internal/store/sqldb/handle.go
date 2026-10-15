package sqldb

import (
	"database/sql"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
)

// Handle is an open database as every part of the store reaches it: the read-write pool, the read
// connections, the instance lock, and the plugin tables ensured on it. internal/store opens it and
// composes the parts of the store on it; each part embeds it.
type Handle struct {
	// SQL is the read-write pool.
	SQL *sql.DB
	// Reader is the read connections every read function runs on (readers.go). It is nil only on
	// a store that failed to open.
	Reader *ReadConnections
	// Instance holds the session advisory lock that makes this process the database's only
	// opnview, for as long as the store is open.
	Instance *sql.Conn
	// Plugins are the tables the loaded plugins own on this database (internal/store/plugintables).
	Plugins plugindecl.PluginTableSet
	// ConnectionString is the connection parameter the store was opened with.
	ConnectionString string
}
