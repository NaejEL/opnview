package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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

// TransitionGrant is a write a plugin's role holds on a CORE table for the transition of ADR 0020: a
// table a later wave moves to its owner, written meanwhile by a plugin whose SQL replaced the core's.
// Each entry names the wave that removes it. The grant is applied with the plugin's role, and it is the
// only way a plugin ever writes the core schema.
type TransitionGrant struct {
	Plugin  string
	Table   string
	Columns []string
}

// TransitionGrants are the transition writes in force (ADR 0020, the progress table names each wave).
var TransitionGrants = []TransitionGrant{
	// The purged part follows its flows when an identity replaces an address-level client, until wave 5
	// moves purged_flow_hour with the rollups.
	{Plugin: "clients", Table: "purged_flow_hour", Columns: []string{"local_client_id", "src_client_id"}},
}

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
	for _, grant := range TransitionGrants {
		if grant.Plugin != plugin {
			continue
		}
		columns := make([]string, 0, len(grant.Columns))
		for _, column := range grant.Columns {
			columns = append(columns, pgx.Identifier{column}.Sanitize())
		}
		statements = append(statements, "GRANT UPDATE ("+strings.Join(columns, ", ")+") ON "+
			pgx.Identifier{CoreSchema, grant.Table}.Sanitize()+" TO "+quotedRole)
	}
	locked := []string{role}
	if s.Reader != nil {
		locked = append(locked, s.Reader.role)
	}
	return s.execUnderRoleLocks(ctx, locked, statements, "granting the role of the plugin "+plugin)
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

// execUnderRoleLocks runs statements that write the role catalogue in one transaction, under the
// advisory lock of each role name they touch -- the lock grantReadRole takes -- so two processes
// building test templates at once, or two loads, never update one role's catalogue row together
// ("tuple concurrently updated").
func (s *Handle) execUnderRoleLocks(ctx context.Context, roles []string, statements []string, what string) error {
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(2, hashtext($1))", role); err != nil {
			return fmt.Errorf("store: %s: locking the role %s: %w", what, role, err)
		}
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: %s: %w", what, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %s: %w", what, err)
	}
	return nil
}
