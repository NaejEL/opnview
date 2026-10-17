package sqldb

import (
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The connection to PostgreSQL. It runs each statement as it is handed: it rewrites no statement and
// converts no value it reads back. A statement read from a file is in PostgreSQL's numbering already
// (translate.go), and a column is read as PostgreSQL types it -- a boolean as a bool, a numeric as
// its text -- so a reader scans what it asked for: a core statement casts what it reads to the type
// its Go reader takes (an integral sum is cast to bigint), and the plugin host converts an integral
// numeric of a plugin's or widget's statement (internal/store/reads, widgetrows.go).
//
// PARAMETERS ARE SENT AS TEXT AND TYPED BY THE SERVER (pgx's exec mode): PostgreSQL types each from
// the place it stands in, as it types a literal, and a statement casts the one whose place does not
// type it. Each execution is one round trip and prepares nothing on the connection.

// OpenPool opens a database/sql pool over the connection string.
func OpenPool(dsn string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: reading the database connection parameter: %w", err)
	}
	return openPoolWithConfig(config)
}

// openPoolWithConfig opens a database/sql pool over a parsed connection.
func openPoolWithConfig(config *pgx.ConnConfig) (*sql.DB, error) {
	config.DefaultQueryExecMode = pgx.QueryExecModeExec
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = searchPath
	config.RuntimeParams["application_name"] = "opnview"
	return sql.OpenDB(stdlib.GetConnector(*config)), nil
}

// CoreSchema is the PostgreSQL schema the core's objects live in. No plugin schema can take
// its name: a plugin's carries the plugin prefix (internal/store/plugindecl/plugintables.go).
const CoreSchema = "opnview"

// searchPath is every connection's search path: the core's objects, then public, where the
// TimescaleDB extension's functions are.
const searchPath = CoreSchema + ", public"
