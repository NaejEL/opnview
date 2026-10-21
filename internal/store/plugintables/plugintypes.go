package plugintables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Values of the plugin type vocabulary (PS4 of the push amendment), written and read.
//
// EVERY VALUE IS BOUND, AS TEXT OR A NUMBER, AND CAST IN THE STATEMENT. A boolean, an address, a
// MAC address and an instant each travel as text and are cast by PostgreSQL to the column's own
// type (CAST($n AS inet), ...), so a value that is not one is refused by the database rather than
// coerced; a number is typed by the column it is written into.

// maxBoundParameters bounds the parameters of one statement, below PostgreSQL's 65535.
const maxBoundParameters = 60000

// InsertPluginRows writes rows into one plugin table in ONE transaction, as multi-row INSERTs of at
// most maxBoundParameters parameters each: a push batch of n rows is ceil(n * columns / 60000)
// statements. A row whose primary key is already held replaces it, as WritePluginPass's do.
func (s *PluginTableStore) InsertPluginRows(ctx context.Context, table plugindecl.PluginTable, rows []map[string]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	held, found := s.LookupPluginTable(table.SQLName())
	if !found {
		return 0, fmt.Errorf("store: %s is not a plugin table ensured on this store", table.SQLName())
	}
	table = held
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
	}
	defer func() { _ = transaction.Rollback() }()
	statements, err := writeRows(ctx, transaction, table, rows)
	if err != nil {
		return 0, err
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
	}
	return statements, nil
}

// writeRows writes rows into one plugin table inside a transaction, as multi-row INSERTs of at most
// maxBoundParameters parameters each, and returns how many statements it ran. Of rows sharing a
// primary key the last is kept, as a sequence of single writes would keep it.
func writeRows(ctx context.Context, transaction *sql.Tx, table plugindecl.PluginTable, rows []map[string]any) (int, error) {
	rows = plugindecl.LastPerKey(table, rows)
	perStatement := max(1, maxBoundParameters/len(table.Columns))
	statements := 0
	for start := 0; start < len(rows); start += perStatement {
		statement, arguments, err := table.MultiRowInsert(rows[start:min(len(rows), start+perStatement)])
		if err != nil {
			return 0, err
		}
		if _, err := transaction.ExecContext(ctx, statement, arguments...); err != nil {
			return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
		}
		statements++
	}
	return statements, nil
}

// timeColumnIsInstant reports whether a table's time column is a timestamp, read from the
// catalogue, so the purge compares it with an instant rather than an epoch.
func timeColumnIsInstant(ctx context.Context, q sqldb.Querier, qualified, column string) (bool, error) {
	var typeName string
	if err := q.QueryRowContext(ctx,
		`SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute AS a
		 WHERE a.attrelid = CAST($1 AS regclass) AND a.attname = $2 AND NOT a.attisdropped`,
		qualified, column).Scan(&typeName); err != nil {
		return false, fmt.Errorf("store: reading the type of %s.%s: %w", qualified, column, err)
	}
	return strings.HasPrefix(typeName, "timestamp"), nil
}

// PluginTables returns every plugin table ensured on this store, ordered by name.
func (s *PluginTableStore) PluginTables() []plugindecl.PluginTable {
	s.Plugins.RLock()
	defer s.Plugins.RUnlock()
	tables := make([]plugindecl.PluginTable, 0, len(s.Plugins.ByName))
	for _, table := range s.Plugins.ByName {
		tables = append(tables, table)
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].SQLName() < tables[j].SQLName() })
	return tables
}

// LookupPluginTable returns a plugin table ensured on this store, by qualified name.
func (s *PluginTableStore) LookupPluginTable(sqlName string) (plugindecl.PluginTable, bool) {
	s.Plugins.RLock()
	defer s.Plugins.RUnlock()
	table, found := s.Plugins.ByName[sqlName]
	return table, found
}

// PluginTableEvent is what ensuring a plugin table did beyond creating it: a declaration that
// changed set the previous table aside.
type PluginTableEvent struct {
	// Code is "schema_rebuilt".
	Code string `json:"code"`
	// Table is the plugin table's qualified name.
	Table string `json:"table"`
	// Aside is the qualified name the previous table was given, and Rows how many rows it holds.
	Aside string `json:"aside"`
	Rows  int64  `json:"rows"`
}

// PluginTableRebuilt is the code of a table set aside because its declaration changed.
const PluginTableRebuilt = "schema_rebuilt"

// AsidePrefix begins the name of a set-aside table.
const AsidePrefix = "aside_"

// EnsurePluginTable creates a plugin's schema, the table, its hypertable and its indexes when they
// do not exist, and is applied at every start, like the core schema.
//
// A DECLARATION THAT CHANGED IS A REBUILD, NEVER A REFUSAL. opnview records each table's full
// declaration -- columns, types, nullability, role and unit, primary key, indexes and time column.
// When the manifest now declares something else in any of these, the existing table is moved into
// the aside schema under a name beginning AsidePrefix, its indexes and keys are dropped, the new
// table is created empty, and the plugin's durable state is reset, since a watermark into the old
// rows means nothing against the new ones. The event is returned for the plugin listing. A change
// of the retention alone rebuilds nothing.
func (s *PluginTableStore) EnsurePluginTable(ctx context.Context, table plugindecl.PluginTable) (*PluginTableEvent, error) {
	if err := plugindecl.ValidatePluginTable(table); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	declaration := table.Declaration()
	var (
		recorded, recordedTime string
		recordedRetention      int64
	)
	err := s.SQL.QueryRowContext(ctx, `SELECT declaration, coalesce(time_column, ''), retention_seconds
		FROM plugin_table WHERE plugin = $1 AND table_name = $2`,
		table.Plugin, table.Table).Scan(&recorded, &recordedTime, &recordedRetention)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: reading the declaration of %s: %w", table.SQLName(), err)
	}
	exists, err := s.relationExists(ctx, plugindecl.PluginSchema(table.Plugin), table.Table)
	if err != nil {
		return nil, err
	}
	if exists && recorded == declaration && recordedTime == table.TimeColumn &&
		recordedRetention == table.RetentionSeconds {
		// The table is the one declared, recorded as declared: a restart, or a test database cloned
		// with the plugins already loaded. Nothing is created and nothing recorded again; the read role
		// is still granted, since a role recreated since would hold nothing.
		return s.keepPluginTable(ctx, table)
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: creating %s: %w", table.SQLName(), err)
	}
	defer func() { _ = transaction.Rollback() }()
	var event *PluginTableEvent
	if exists && recorded != declaration {
		event, err = s.setAside(ctx, transaction, table, now)
		if err != nil {
			return nil, err
		}
	}
	for _, statement := range table.CreateStatements() {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("store: creating %s: %w", table.SQLName(), err)
		}
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO plugin_table (plugin, table_name, declaration, time_column, retention_seconds, declared_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (plugin, table_name) DO UPDATE SET declaration = excluded.declaration,
		     time_column = excluded.time_column, retention_seconds = excluded.retention_seconds,
		     declared_at = CASE WHEN plugin_table.declaration = excluded.declaration
		                        THEN plugin_table.declared_at ELSE excluded.declared_at END`,
		table.Plugin, table.Table, declaration, sqldb.NullableText(table.TimeColumn), table.RetentionSeconds, now); err != nil {
		return nil, fmt.Errorf("store: recording the declaration of %s: %w", table.SQLName(), err)
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: creating %s: %w", table.SQLName(), err)
	}
	if _, err := s.keepPluginTable(ctx, table); err != nil {
		return nil, err
	}
	return event, nil
}

// keepPluginTable grants the read role a plugin table's schema and records the table as this store's.
func (s *PluginTableStore) keepPluginTable(ctx context.Context, table plugindecl.PluginTable) (*PluginTableEvent, error) {
	if err := s.GrantReadOnSchema(ctx, plugindecl.PluginSchema(table.Plugin)); err != nil {
		return nil, err
	}
	if err := s.EnsurePluginRole(ctx, table.Plugin, plugindecl.PluginSchema(table.Plugin)); err != nil {
		return nil, err
	}
	grants := make([]sqldb.TableWriteGrant, 0, len(table.WritableBy))
	for _, grant := range table.WritableBy {
		if grant.Kind != "" {
			// A grant to a kind's providers is applied as each of them loads (GrantKindWrites); the
			// owner's own pass here only names the plugins it granted by name.
			continue
		}
		grants = append(grants, sqldb.TableWriteGrant{Plugin: grant.Plugin, Columns: grant.Columns})
	}
	if err := s.ApplyWriteGrants(ctx, plugindecl.PluginSchema(table.Plugin), table.Table, grants); err != nil {
		return nil, err
	}
	s.Plugins.Lock()
	if s.Plugins.ByName == nil {
		s.Plugins.ByName = map[string]plugindecl.PluginTable{}
	}
	s.Plugins.ByName[table.SQLName()] = table
	s.Plugins.Unlock()
	return nil, nil
}

// relationExists reports whether a table exists in a schema.
func (s *PluginTableStore) relationExists(ctx context.Context, schema, table string) (bool, error) {
	var exists bool
	err := s.SQL.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = $2)", schema, table).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: looking for %s.%s: %w", schema, table, err)
	}
	return exists, nil
}

// setAside moves a table whose declaration changed into the aside schema, drops its keys and
// indexes, records it, and resets its plugin's durable state, inside the caller's transaction.
func (s *PluginTableStore) setAside(ctx context.Context, transaction *sql.Tx, table plugindecl.PluginTable, now int64) (
	*PluginTableEvent, error) {
	aside := fmt.Sprintf("%s%s__%s__%d", AsidePrefix, table.Plugin, table.Table, now)
	var rows int64
	if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM "+table.Quoted()).Scan(&rows); err != nil {
		return nil, fmt.Errorf("store: counting the rows of %s: %w", table.SQLName(), err)
	}
	// The indexes and the keys go, so the next version's carry no name the aside table holds.
	indexes, err := transaction.QueryContext(ctx,
		`SELECT quote_ident(i.relname)
		 FROM pg_index AS x
		 JOIN pg_class AS i ON i.oid = x.indexrelid
		 WHERE x.indrelid = CAST($1 AS regclass) AND NOT x.indisprimary
		   AND NOT EXISTS (SELECT 1 FROM pg_constraint AS c WHERE c.conindid = x.indexrelid)`, table.Quoted())
	if err != nil {
		return nil, fmt.Errorf("store: listing the indexes of %s: %w", table.SQLName(), err)
	}
	var names []string
	for indexes.Next() {
		var name string
		if err := indexes.Scan(&name); err != nil {
			_ = indexes.Close()
			return nil, fmt.Errorf("store: listing the indexes of %s: %w", table.SQLName(), err)
		}
		names = append(names, name)
	}
	_ = indexes.Close()
	for _, name := range names {
		if _, err := transaction.ExecContext(ctx, "DROP INDEX "+plugindecl.QuoteIdentifier(plugindecl.PluginSchema(table.Plugin))+"."+
			name); err != nil {
			return nil, fmt.Errorf("store: dropping %s: %w", name, err)
		}
	}
	constraints, err := transaction.QueryContext(ctx,
		`SELECT quote_ident(conname) FROM pg_constraint
		 WHERE conrelid = CAST($1 AS regclass) AND contype IN ('p', 'u', 'c', 'f')`, table.Quoted())
	if err != nil {
		return nil, fmt.Errorf("store: listing the keys of %s: %w", table.SQLName(), err)
	}
	var keys []string
	for constraints.Next() {
		var name string
		if err := constraints.Scan(&name); err != nil {
			_ = constraints.Close()
			return nil, err
		}
		keys = append(keys, name)
	}
	_ = constraints.Close()
	for _, name := range keys {
		if _, err := transaction.ExecContext(ctx, "ALTER TABLE "+table.Quoted()+" DROP CONSTRAINT "+name); err != nil {
			return nil, fmt.Errorf("store: dropping the key %s of %s: %w", name, table.SQLName(), err)
		}
	}
	for _, statement := range []string{
		"CREATE SCHEMA IF NOT EXISTS " + plugindecl.QuoteIdentifier(plugindecl.AsideSchema),
		"ALTER TABLE " + table.Quoted() + " RENAME TO " + plugindecl.QuoteIdentifier(aside),
		"ALTER TABLE " + plugindecl.QuoteIdentifier(plugindecl.PluginSchema(table.Plugin)) + "." + plugindecl.QuoteIdentifier(aside) +
			" SET SCHEMA " + plugindecl.QuoteIdentifier(plugindecl.AsideSchema),
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("store: setting %s aside: %w", table.SQLName(), err)
		}
	}
	var timeColumn sql.NullString
	var retention int64
	if err := transaction.QueryRowContext(ctx,
		"SELECT time_column, retention_seconds FROM plugin_table WHERE plugin = $1 AND table_name = $2",
		table.Plugin, table.Table).Scan(&timeColumn, &retention); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: reading the retention of %s: %w", table.SQLName(), err)
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO plugin_aside_table (name, plugin, table_name, time_column, retention_seconds, set_aside_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		aside, table.Plugin, table.Table, timeColumn, retention, now); err != nil {
		return nil, fmt.Errorf("store: recording %s: %w", aside, err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM plugin_state WHERE plugin = $1", table.Plugin); err != nil {
		return nil, fmt.Errorf("store: resetting the state of the plugin %s: %w", table.Plugin, err)
	}
	return &PluginTableEvent{Code: PluginTableRebuilt, Table: table.SQLName(), Aside: plugindecl.AsideSchema + "." + aside,
		Rows: rows}, nil
}

// AsideTable is one table set aside, as the plugin listing reports it.
type AsideTable struct {
	Name  string `json:"name"`
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
	// SetAsideAt is when it was set aside.
	SetAsideAt int64 `json:"set_aside_at"`
}

// AsideTables returns the tables of one plugin that are set aside, with their row counts.
func (s *PluginTableStore) AsideTables(ctx context.Context, plugin string) ([]AsideTable, error) {
	rows, err := s.SQL.QueryContext(ctx,
		"SELECT name, table_name, set_aside_at FROM plugin_aside_table WHERE plugin = $1 ORDER BY name", plugin)
	if err != nil {
		return nil, fmt.Errorf("store: listing the aside tables of %s: %w", plugin, err)
	}
	var tables []AsideTable
	for rows.Next() {
		var table AsideTable
		if err := rows.Scan(&table.Name, &table.Table, &table.SetAsideAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range tables {
		if err := s.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+plugindecl.QuoteIdentifier(plugindecl.AsideSchema)+"."+
			plugindecl.QuoteIdentifier(tables[index].Name)).Scan(&tables[index].Rows); err != nil {
			return nil, fmt.Errorf("store: counting %s: %w", tables[index].Name, err)
		}
		tables[index].Name = plugindecl.AsideSchema + "." + tables[index].Name
	}
	return tables, nil
}

// PluginTableRows counts the rows of one plugin table.
func (s *PluginTableStore) PluginTableRows(ctx context.Context, table plugindecl.PluginTable) (int64, error) {
	var rows int64
	err := s.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table.Quoted()).Scan(&rows)
	return rows, err
}

// OrphanedTable is a table of a plugin that is no longer present: its schema and rows are kept,
// and the listing reports them.
type OrphanedTable struct {
	Plugin string `json:"plugin"`
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
}

// PluginRows are the rows one plugin pass emitted, per table name (the plugin's own, not the
// qualified name), each row a map from column to value: an int64, a float64, a string or nil.
type PluginRows map[string][]map[string]any

// WritePluginPass stores what one plugin pass produced, in ONE transaction: the rows of its
// tables, upserted on their primary key, and the plugin's durable state. A failure stores nothing,
// so a watermark is never advanced past rows that were not kept, nor rows kept under a watermark
// that was not.
func (s *PluginTableStore) WritePluginPass(ctx context.Context, plugin, kind, provider string, rows PluginRows,
	state map[string]string, now int64) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: storing the pass of the plugin %s: %w", plugin, err)
	}
	defer func() { _ = transaction.Rollback() }()
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		table, err := s.passTable(plugin, kind, name)
		if err != nil {
			return err
		}
		if len(rows[name]) == 0 {
			continue
		}
		if _, err := writeRows(ctx, transaction, table, rows[name]); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO plugin_state (plugin, key, value, updated_at) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (plugin, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			plugin, key, state[key], now); err != nil {
			return fmt.Errorf("store: writing the state %q of the plugin %s: %w", key, plugin, err)
		}
	}
	// Published inside the transaction: PostgreSQL delivers it on commit, and never on a rollback.
	if err := sqldb.Notify(ctx, transaction, sqldb.ChannelPluginPass, map[string]string{"plugin": plugin, "provider": provider}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: storing the pass of the plugin %s: %w", plugin, err)
	}
	return nil
}

// PluginState returns every durable value a plugin stored, by key.
func (s *PluginTableStore) PluginState(ctx context.Context, plugin string) (map[string]string, error) {
	rows, err := s.SQL.QueryContext(ctx, "SELECT key, value FROM plugin_state WHERE plugin = $1", plugin)
	if err != nil {
		return nil, fmt.Errorf("store: reading the state of the plugin %s: %w", plugin, err)
	}
	defer func() { _ = rows.Close() }()
	state := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: reading the state of the plugin %s: %w", plugin, err)
		}
		state[key] = value
	}
	return state, rows.Err()
}

// RetentionHorizon is the instant before which a table's rows are purged, or false when nothing
// is: a table's own retention, or the installation's.
func RetentionHorizon(ctx context.Context, q sqldb.Querier, now, retention int64) (int64, bool, error) {
	if retention > 0 {
		return now - retention, true, nil
	}
	horizon, found, err := sqldb.QueryOptionalIntText(ctx, q, "retention horizon",
		"SELECT :now - CAST(value AS BIGINT) FROM setting WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0",
		map[string]any{"now": now})
	return horizon, found, err
}

// PurgeTimeTable purges one table with a time column to the exact second: on a hypertable, every
// chunk entirely before the horizon is dropped whole first, and the rows before it in the chunk the
// horizon falls in are deleted.
func PurgeTimeTable(ctx context.Context, transaction *sql.Tx, qualified, column string, horizon int64) error {
	var hypertable bool
	if err := transaction.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables
		                WHERE CAST(format('%I.%I', hypertable_schema, hypertable_name) AS regclass)
		                    = CAST($1 AS regclass))`,
		qualified).Scan(&hypertable); err != nil {
		return fmt.Errorf("store: looking up %s: %w", qualified, err)
	}
	instant, err := timeColumnIsInstant(ctx, transaction, qualified, column)
	if err != nil {
		return err
	}
	// An instant column is compared with the horizon as an instant, an integer one as an epoch.
	bound := func(number string) string {
		if instant {
			return "to_timestamp(CAST(" + number + " AS bigint))"
		}
		return "CAST(" + number + " AS bigint)"
	}
	if hypertable {
		if _, err := transaction.ExecContext(ctx, "SELECT drop_chunks(CAST($1 AS regclass), older_than => "+bound("$2")+")",
			qualified, horizon); err != nil {
			return fmt.Errorf("store: dropping the chunks of %s: %w", qualified, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM "+qualified+" WHERE "+plugindecl.QuoteIdentifier(column)+" < "+bound("$1"),
		horizon); err != nil {
		return fmt.Errorf("store: purging %s: %w", qualified, err)
	}
	return nil
}

// PurgePluginTables purges every plugin table with a time column by its retention, inside the
// purge's transaction. A table with no time column is never purged; one whose retention is zero
// follows retention_seconds, under which zero deletes nothing.
func (s *PluginTableStore) PurgePluginTables(ctx context.Context, transaction *sql.Tx, now int64) error {
	for _, table := range s.PluginTables() {
		if table.TimeColumn == "" {
			continue
		}
		retention := table.RetentionSeconds
		if table.RetentionSetting != "" {
			// The operator's value of the setting the declaration names, when it holds one above zero.
			override, found, err := sqldb.QueryOptionalIntText(ctx, transaction, "retention setting",
				"SELECT CAST(value AS BIGINT) FROM setting WHERE key = :key AND value ~ '^[0-9]+$'",
				map[string]any{"key": table.RetentionSetting})
			if err != nil {
				return err
			}
			if found && override > 0 {
				retention = override
			}
		}
		horizon, bounded, err := RetentionHorizon(ctx, transaction, now, retention)
		if err != nil {
			return err
		}
		if !bounded {
			continue
		}
		if err := PurgeTimeTable(ctx, transaction, table.Quoted(), table.TimeColumn, horizon); err != nil {
			return err
		}
	}
	return s.purgeAsideTables(ctx, transaction, now)
}

// purgeAsideTables purges every table set aside by its recorded retention, and drops one that is
// then empty. A table with no time column is kept, and listed, until somebody removes it.
func (s *PluginTableStore) purgeAsideTables(ctx context.Context, transaction *sql.Tx, now int64) error {
	rows, err := transaction.QueryContext(ctx,
		"SELECT name, time_column, retention_seconds FROM plugin_aside_table WHERE time_column IS NOT NULL")
	if err != nil {
		return fmt.Errorf("store: listing the aside tables: %w", err)
	}
	type aside struct {
		name, column string
		retention    int64
	}
	var tables []aside
	for rows.Next() {
		var table aside
		if err := rows.Scan(&table.name, &table.column, &table.retention); err != nil {
			_ = rows.Close()
			return err
		}
		tables = append(tables, table)
	}
	_ = rows.Close()
	for _, table := range tables {
		qualified := plugindecl.QuoteIdentifier(plugindecl.AsideSchema) + "." + plugindecl.QuoteIdentifier(table.name)
		horizon, bounded, err := RetentionHorizon(ctx, transaction, now, table.retention)
		if err != nil {
			return err
		}
		if bounded {
			if err := PurgeTimeTable(ctx, transaction, qualified, table.column, horizon); err != nil {
				return err
			}
		}
		var remaining int64
		if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM "+qualified).Scan(&remaining); err != nil {
			return err
		}
		if remaining == 0 {
			if _, err := transaction.ExecContext(ctx, "DROP TABLE "+qualified); err != nil {
				return fmt.Errorf("store: dropping %s: %w", table.name, err)
			}
			if _, err := transaction.ExecContext(ctx, "DELETE FROM plugin_aside_table WHERE name = $1",
				table.name); err != nil {
				return err
			}
		}
	}
	return nil
}

// passTable is the table a pass's rows named: one of the plugin's own by its name, or, as
// `<owner>.<table>`, a table of another plugin whose declaration grants this plugin the whole
// table -- by its name with no columns, or as a provider of the pass's kind (ADR 0019, ADR 0020
// wave 4). Anything else is refused by name.
func (s *PluginTableStore) passTable(plugin, kind, name string) (plugindecl.PluginTable, error) {
	owner, table, granted := strings.Cut(name, ".")
	if !granted {
		held, found := s.LookupPluginTable(plugindecl.PluginTableSQLName(plugin, name))
		if !found {
			return plugindecl.PluginTable{}, fmt.Errorf("store: the plugin %s wrote into %q, which it does not own", plugin, name)
		}
		return held, nil
	}
	held, found := s.LookupPluginTable(plugindecl.PluginTableSQLName(owner, table))
	if !found {
		return plugindecl.PluginTable{}, fmt.Errorf("store: the plugin %s wrote into %q, which no plugin declares", plugin, name)
	}
	for _, grant := range held.WritableBy {
		if grant.Grants(plugin, []string{kind}) {
			return held, nil
		}
	}
	return plugindecl.PluginTable{}, fmt.Errorf("store: the plugin %s wrote into %q, which %s does not grant it", plugin, name,
		owner)
}

// GrantKindWrites gives a plugin that provides some kinds the tables other plugins declared writable
// by those kinds' providers, and returns them by `<owner>.<table>` (ADR 0020 wave 4). The owners
// loaded first, so their tables are in the registry.
func (s *PluginTableStore) GrantKindWrites(ctx context.Context, plugin string, kinds []string) (map[string]plugindecl.PluginTable, error) {
	granted := map[string]plugindecl.PluginTable{}
	for _, table := range s.PluginTables() {
		for _, grant := range table.WritableBy {
			if grant.Kind == "" || !grant.Grants(plugin, kinds) {
				continue
			}
			if err := s.GrantWholeTable(ctx, plugindecl.PluginSchema(table.Plugin), table.Table, plugin); err != nil {
				return nil, err
			}
			granted[table.Plugin+"."+table.Table] = table
		}
	}
	return granted, nil
}
