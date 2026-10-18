package sqldb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The plugin roles (ADR 0018): what a plugin's own SQL -- its write statements and its relations -- runs
// under, so that PostgreSQL itself, and no parser, decides what the SQL may touch.
//
// ONE ROLE PER PLUGIN, NEVER A LOGIN. The role is opnview_plugin_<name>: it cannot log in, it is a member
// of the read role, so it reads exactly what a widget query reads and no credential table, and it holds
// INSERT, UPDATE and DELETE on the tables of the plugin's own schema and CREATE on that schema, nowhere
// else. opnview's own connection takes the role for the span of a plugin's statements (SET LOCAL ROLE
// inside the transaction) and gives it back (RESET ROLE) before writing what the core writes. A statement
// is also planned under the role when the plugin loads, so one that would write elsewhere refuses the
// plugin by name rather than failing at its first run.

// PluginRolePrefix begins every plugin role's name.
const PluginRolePrefix = "opnview_plugin_"

// PluginRoleName is the role a plugin's SQL runs under.
func PluginRoleName(plugin string) string { return PluginRolePrefix + plugin }

// pluginStatementTimeout bounds every statement a plugin's derivation runs: a classification over a
// month of flows is expected to take seconds, never minutes.
const pluginStatementTimeout = "300s"

// EnsurePluginRole creates a plugin's role when it is absent and gives it exactly what it may do in its
// schema; applied at every start, like the schema itself.
func (s *Handle) EnsurePluginRole(ctx context.Context, plugin, schema string) error {
	role := PluginRoleName(plugin)
	quotedRole := pgx.Identifier{role}.Sanitize()
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	statements := []string{
		// Two loads may create the same role at once on one instance: the second creation is the
		// first's role, and the grants below apply to it.
		"DO $create$ BEGIN CREATE ROLE " + quotedRole +
			" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT; " +
			"EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $create$",
		// opnview's own connection may take the role.
		"GRANT " + quotedRole + " TO CURRENT_USER WITH SET TRUE",
		"GRANT USAGE, CREATE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + quotedSchema + " TO " + quotedRole,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + quotedSchema + " GRANT INSERT, UPDATE, DELETE ON TABLES TO " + quotedRole,
	}
	if s.Reader != nil {
		// What the read role reads, the plugin role reads: the core schema, every plugin schema, the mirror's
		// data tables, and none of the credential tables.
		statements = append(statements, "GRANT "+pgx.Identifier{s.Reader.role}.Sanitize()+" TO "+quotedRole)
	}
	for _, statement := range statements {
		if _, err := s.SQL.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: granting the role of the plugin %s: %w", plugin, err)
		}
	}
	return nil
}

// TakePluginRole makes the rest of a transaction run as a plugin's role, under the plugin statement
// timeout; GivePluginRoleBack ends it. Both are LOCAL: the transaction's end ends them too.
func TakePluginRole(ctx context.Context, tx *sql.Tx, plugin string) error {
	for _, statement := range []string{
		"SET LOCAL ROLE " + pgx.Identifier{PluginRoleName(plugin)}.Sanitize(),
		"SET LOCAL statement_timeout = " + quoteLiteral(pluginStatementTimeout),
		// Never the bridge (searchpath.go): a plugin names a moved table by its qualified name.
		"SET LOCAL search_path = " + CoreSearchPath(),
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: taking the role of the plugin %s: %w", plugin, err)
		}
	}
	return nil
}

// GivePluginRoleBack returns a transaction to opnview's own role.
func GivePluginRoleBack(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{"RESET ROLE", "RESET statement_timeout", "RESET search_path"} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: giving a plugin's role back: %w", err)
		}
	}
	return nil
}

// PlanAsPlugin plans one statement text under a plugin's role, its parameters bound to NULL, and reports
// why PostgreSQL would not: what the plugin loads checks. Nothing is written: the transaction is rolled
// back.
func (s *Handle) PlanAsPlugin(ctx context.Context, plugin, text string) error {
	planned, arguments, err := NullArguments(text)
	if err != nil {
		return err
	}
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := TakePluginRole(ctx, tx, plugin); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "EXPLAIN "+planned, arguments...)
	if err != nil {
		return err
	}
	return rows.Close()
}
