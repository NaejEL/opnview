package sqldb

import (
	"database/sql"
	"database/sql/driver"
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
// PARAMETERS ARE TYPED BY THE SERVER: each statement text is prepared once per connection (pgx's
// statement cache), PostgreSQL types each parameter from the place it stands in, and the value is
// encoded as that type; a statement casts a parameter whose place does not type it. Preparing once
// is what keeps the database's planning off every execution.

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
	return sql.OpenDB(connectorFor(config)), nil
}

// connectorFor is the connector over a parsed connection: the statement cache, the search path and
// the application name.
func connectorFor(config *pgx.ConnConfig) driver.Connector {
	config.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = SearchPath()
	config.RuntimeParams["application_name"] = "opnview"
	return stdlib.GetConnector(*config)
}

// CoreSchema is the PostgreSQL schema the core's objects live in. No plugin schema can take
// its name: a plugin's carries the plugin prefix (internal/store/plugindecl/plugintables.go).
const CoreSchema = "opnview"
