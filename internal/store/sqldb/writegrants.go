package sqldb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Granted writes (ADR 0019): what a table's owner lets another plugin write, as PostgreSQL's own
// grants on that plugin's role. With columns, UPDATE of those columns alone; without, INSERT, UPDATE
// and DELETE of the whole table. Applied at every start, like the schema: what the owner no longer
// grants is revoked from every other plugin role, so the grants in force are exactly the declaration's.

// TableWriteGrant is one grantee of a table, by plugin name.
type TableWriteGrant struct {
	Plugin  string
	Columns []string
}

// ApplyWriteGrants makes the write grants on a table exactly the ones given: every other plugin role
// loses what it held on the table, each grantee's role is created when absent and given its grant.
func (s *Handle) ApplyWriteGrants(ctx context.Context, schema, table string, grants []TableWriteGrant) error {
	qualified := pgx.Identifier{schema, table}.Sanitize()
	// The plugin roles holding a write on the table today, table-wide or on a column: the ones a revoke
	// can concern, read in one statement so a table nobody else writes costs one read and no revoke.
	rows, err := s.SQL.QueryContext(ctx, `SELECT DISTINCT grantee FROM (
		SELECT grantee FROM information_schema.role_table_grants
		 WHERE table_schema = $1 AND table_name = $2 AND privilege_type IN ('INSERT', 'UPDATE', 'DELETE')
		UNION ALL
		SELECT grantee FROM information_schema.role_column_grants
		 WHERE table_schema = $1 AND table_name = $2 AND privilege_type IN ('INSERT', 'UPDATE')) AS held
		WHERE grantee LIKE $3`, schema, table, PluginRolePrefix+"%")
	if err != nil {
		return fmt.Errorf("store: reading the writes held on %s.%s: %w", schema, table, err)
	}
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: reading the writes held on %s.%s: %w", schema, table, err)
		}
		roles = append(roles, role)
	}
	_ = rows.Close()
	granted := map[string]bool{}
	for _, grant := range grants {
		granted[PluginRoleName(grant.Plugin)] = true
	}
	owner := PluginRoleName(strings.TrimPrefix(schema, "plugin_"))
	var statements []string
	for _, role := range roles {
		if role == owner || granted[role] {
			continue
		}
		statements = append(statements, "REVOKE INSERT, UPDATE, DELETE ON "+qualified+" FROM "+pgx.Identifier{role}.Sanitize())
	}
	for _, grant := range grants {
		role := PluginRoleName(grant.Plugin)
		quotedRole := pgx.Identifier{role}.Sanitize()
		statements = append(statements,
			"DO $create$ BEGIN CREATE ROLE "+quotedRole+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT; "+
				"EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $create$",
			"GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+quotedRole,
			// The whole of what the role held on the table goes first, so a grant narrowed to columns is
			// exactly that.
			"REVOKE INSERT, UPDATE, DELETE ON "+qualified+" FROM "+quotedRole)
		if len(grant.Columns) == 0 {
			statements = append(statements, "GRANT INSERT, UPDATE, DELETE ON "+qualified+" TO "+quotedRole)
			continue
		}
		columns := make([]string, 0, len(grant.Columns))
		for _, column := range grant.Columns {
			columns = append(columns, pgx.Identifier{column}.Sanitize())
		}
		statements = append(statements, "GRANT UPDATE ("+strings.Join(columns, ", ")+") ON "+qualified+" TO "+quotedRole)
	}
	locked := []string{owner}
	for _, grant := range grants {
		locked = append(locked, PluginRoleName(grant.Plugin))
	}
	return s.execUnderRoleLocks(ctx, locked, statements, fmt.Sprintf("granting the writes on %s.%s", schema, table))
}

// GrantWholeTable gives one plugin's role INSERT, UPDATE and DELETE on a table of another plugin: a
// grant the owner declared to every provider of a kind, applied when such a provider loads, since
// the owner loads first and cannot name the providers that come after it (ADR 0020 wave 4). The
// role is created when absent, as ApplyWriteGrants creates it.
func (s *Handle) GrantWholeTable(ctx context.Context, schema, table, plugin string) error {
	qualified := pgx.Identifier{schema, table}.Sanitize()
	role := PluginRoleName(plugin)
	quotedRole := pgx.Identifier{role}.Sanitize()
	return s.execUnderRoleLocks(ctx, []string{role}, []string{
		"DO $create$ BEGIN CREATE ROLE " + quotedRole + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT; " +
			"EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $create$",
		"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + quotedRole,
		"GRANT INSERT, UPDATE, DELETE ON " + qualified + " TO " + quotedRole,
	}, fmt.Sprintf("granting %s the table %s.%s", plugin, schema, table))
}
