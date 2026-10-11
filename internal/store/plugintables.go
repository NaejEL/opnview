package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The tables a plugin owns.
//
// A PLUGIN CARRIES ITS OWN DATA SCHEMA (the maintainer's decision of 10 October 2026,
// specs/SPEC-backend-completion-plugins.md, Decisions). Its manifest declares the tables it owns
// -- columns with their role and unit, primary key, indexes and retention -- and opnview creates
// them at load, writes the rows the plugin emits into them, purges them with the retention and
// lets a widget query read them. No core table changes for it, and no file of opnview's is edited:
// the plugin is a directory, and these tables are created from what that directory declares.
//
// ONE POSTGRESQL SCHEMA PER PLUGIN (P6): plugin_<plugin>, holding its tables under their declared
// names, so two plugins can never collide and a plugin's objects are listed, dumped and dropped as
// one namespace. No core schema begins with plugin_ (the core's is opnview, the learned mirror's
// opnsense), and the schema tables are set aside in, opnview_aside, is never on a search path,
// never in the data catalogue and never readable by a widget query.
//
// A TABLE WITH A TIME COLUMN IS A HYPERTABLE on it (P6), chunked by day, and purged by chunk then
// by row to the exact second (purgePluginTables). TimescaleDB requires the time column in every
// unique index of a hypertable, so a declaration whose primary key or unique index omits it is
// refused at load, with ErrUniqueKeyWithoutTime.
//
// NO MIGRATION, AS FOR THE CORE. CREATE TABLE IF NOT EXISTS is applied at every start, and a table
// whose declaration changed is rebuilt: the old one is moved into the aside schema, kept and purged
// by its recorded retention, and dropped once empty (EnsurePluginTable). A plugin no longer present
// at start keeps its schema and its tables, and the listing reports them as orphaned
// (OrphanedPluginTables); removing them is the operator's act.

// PluginColumnTypes are the column types a plugin table may declare -- the plugin type vocabulary of
// PS4 of the push amendment, words of a closed list and never SQL type strings -- as PostgreSQL
// stores them. There is no JSON type: every captured property is its own column.
var PluginColumnTypes = map[string]string{"integer": "BIGINT", "real": "DOUBLE PRECISION", "text": "TEXT",
	"boolean": "BOOLEAN", "ip": "INET", "mac": "MACADDR", "instant": "TIMESTAMPTZ"}

// PluginColumnTypeList is the vocabulary in the order docs/plugins.md lists it.
var PluginColumnTypeList = []string{"integer", "real", "text", "boolean", "ip", "mac", "instant"}

// pluginTypeInstant is the vocabulary's timestamp type, the time column of a push output table.
const pluginTypeInstant = "instant"

// The roles a plugin column may declare, which the data catalogue serves (P8).
const (
	RoleTime      = "time"
	RoleDimension = "dimension"
	RoleMeasure   = "measure"
)

// PluginTableNamePattern is what a plugin name, a table name and a column name each match.
var PluginTableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// PluginSchemaPrefix begins the schema of every plugin.
const PluginSchemaPrefix = "plugin_"

// AsideSchema is the schema tables are set aside in.
const AsideSchema = "opnview_aside"

// ErrUniqueKeyWithoutTime is a declaration whose primary key or unique index omits the table's
// time column, which a hypertable cannot carry.
var ErrUniqueKeyWithoutTime = errors.New("store: a unique key of a table with a time column must include it")

// hypertableChunkSeconds is the span of a plugin hypertable's chunks: a day, so the purge drops
// whole days and deletes rows only in the day the horizon falls in.
const hypertableChunkSeconds = 86400

// PluginColumn is one column of a plugin table.
type PluginColumn struct {
	Name string
	// Type is one of PluginColumnTypes' keys.
	Type string
	// Nullable says the column admits NULL; a column that does not is NOT NULL.
	Nullable bool
	// CarriesDomain says the column holds a domain name, so a widget query reading the table
	// is withheld under aggregate_mode = no_domains.
	CarriesDomain bool
	// Role is time, dimension or measure; empty is dimension, or time for the time column.
	Role string
	// Unit is a measure's unit, a lower-case word with no space, as measurement_sample.unit is.
	Unit string
}

// PluginIndex is one index of a plugin table.
type PluginIndex struct {
	Columns []string
	Unique  bool
}

// PluginTable is one table a plugin owns, as its manifest declares it.
type PluginTable struct {
	// Plugin is the owning plugin's name and Table the table's own name.
	Plugin string
	Table  string
	// Columns are the columns, in order.
	Columns []PluginColumn
	// PrimaryKey names the columns of the primary key; empty means every row a plugin writes is
	// a new row.
	PrimaryKey []string
	// Indexes are the secondary indexes.
	Indexes []PluginIndex
	// TimeColumn is the integer column holding each row's instant, which the retention reads and
	// the hypertable is chunked on; empty means the table is a plain table, never purged.
	TimeColumn string
	// RetentionSeconds is how long a row is kept after its instant. Zero follows the
	// installation's retention_seconds setting, under which zero means unlimited.
	RetentionSeconds int64
}

// PluginSchema is the schema of a plugin's tables.
func PluginSchema(plugin string) string { return PluginSchemaPrefix + plugin }

// SQLName is the table's name as a query names it: plugin_<plugin>.<table>.
func (t PluginTable) SQLName() string { return PluginTableSQLName(t.Plugin, t.Table) }

// PluginTableSQLName composes the qualified name of a plugin's table.
func PluginTableSQLName(plugin, table string) string { return PluginSchema(plugin) + "." + table }

// quoted is the table's qualified name, each part quoted, as DDL and DML use it.
func (t PluginTable) quoted() string {
	return quoteIdentifier(PluginSchema(t.Plugin)) + "." + quoteIdentifier(t.Table)
}

// quoteIdentifier quotes an identifier.
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// CarriesDomain reports whether any column of the table carries a domain name.
func (t PluginTable) CarriesDomain() bool {
	for _, column := range t.Columns {
		if column.CarriesDomain {
			return true
		}
	}
	return false
}

// RoleOf returns a column's role: its declared one, time for the time column, dimension otherwise.
func (t PluginTable) RoleOf(column PluginColumn) string {
	switch {
	case column.Role != "":
		return column.Role
	case column.Name == t.TimeColumn:
		return RoleTime
	}
	return RoleDimension
}

// column returns a declared column by name.
func (t PluginTable) column(name string) (PluginColumn, bool) {
	for _, column := range t.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return PluginColumn{}, false
}

// pluginTableSet is the plugin tables ensured on one store, by qualified name: the writes, the
// purge and the widget query check read them.
type pluginTableSet struct {
	sync.RWMutex
	byName map[string]PluginTable
}

// PluginTables returns every plugin table ensured on this store, ordered by name.
func (s *Store) PluginTables() []PluginTable {
	s.plugins.RLock()
	defer s.plugins.RUnlock()
	tables := make([]PluginTable, 0, len(s.plugins.byName))
	for _, table := range s.plugins.byName {
		tables = append(tables, table)
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].SQLName() < tables[j].SQLName() })
	return tables
}

// LookupPluginTable returns a plugin table ensured on this store, by qualified name.
func (s *Store) LookupPluginTable(sqlName string) (PluginTable, bool) {
	s.plugins.RLock()
	defer s.plugins.RUnlock()
	table, found := s.plugins.byName[sqlName]
	return table, found
}

// ValidatePluginTable checks a declaration without touching the database.
func ValidatePluginTable(table PluginTable) error {
	if !PluginTableNamePattern.MatchString(table.Plugin) || !PluginTableNamePattern.MatchString(table.Table) {
		return fmt.Errorf("store: a plugin table is named by its plugin and its own name, each a snake_case "+
			"word with no double underscore; %q and %q are not", table.Plugin, table.Table)
	}
	if len(table.Columns) == 0 {
		return fmt.Errorf("store: the table %s declares no column", table.SQLName())
	}
	seen := map[string]bool{}
	timeColumns := 0
	for _, column := range table.Columns {
		if !PluginTableNamePattern.MatchString(column.Name) {
			return fmt.Errorf("store: the column %q of %s is not a snake_case word", column.Name, table.SQLName())
		}
		if seen[column.Name] {
			return fmt.Errorf("store: the column %q of %s is declared twice", column.Name, table.SQLName())
		}
		seen[column.Name] = true
		if _, known := PluginColumnTypes[column.Type]; !known {
			return fmt.Errorf("store: the column %q of %s has the type %q, not one of %v",
				column.Name, table.SQLName(), column.Type, PluginColumnTypeList)
		}
		switch column.Role {
		case "", RoleDimension:
		case RoleTime:
			timeColumns++
			if column.Name != table.TimeColumn {
				return fmt.Errorf("store: the column %q of %s has the role time and is not the column the "+
					"retention reads", column.Name, table.SQLName())
			}
		case RoleMeasure:
			if column.Type != "integer" && column.Type != "real" {
				return fmt.Errorf("store: the column %q of %s is a measure and is not a number", column.Name,
					table.SQLName())
			}
		default:
			return fmt.Errorf("store: the column %q of %s has the role %q, not time, dimension or measure",
				column.Name, table.SQLName(), column.Role)
		}
		if column.Unit != "" {
			if column.Role != RoleMeasure {
				return fmt.Errorf("store: the column %q of %s has a unit and is not a measure", column.Name,
					table.SQLName())
			}
			if column.Unit != strings.ToLower(column.Unit) || strings.ContainsAny(column.Unit, " \t") {
				return fmt.Errorf("store: the unit %q of %s.%s is not a lower-case word with no space",
					column.Unit, table.SQLName(), column.Name)
			}
		}
	}
	if timeColumns > 1 {
		return fmt.Errorf("store: %s declares more than one time column", table.SQLName())
	}
	for _, name := range table.PrimaryKey {
		if !seen[name] {
			return fmt.Errorf("store: the primary key of %s names %q, which is not a column", table.SQLName(), name)
		}
	}
	for _, index := range table.Indexes {
		if len(index.Columns) == 0 {
			return fmt.Errorf("store: an index of %s names no column", table.SQLName())
		}
		for _, name := range index.Columns {
			if !seen[name] {
				return fmt.Errorf("store: an index of %s names %q, which is not a column", table.SQLName(), name)
			}
		}
	}
	if table.TimeColumn != "" {
		column, found := table.column(table.TimeColumn)
		if !found || (column.Type != "integer" && column.Type != pluginTypeInstant) || column.Nullable {
			return fmt.Errorf("store: the retention of %s reads %q, which is not a NOT NULL integer or instant column",
				table.SQLName(), table.TimeColumn)
		}
		if !strings.HasSuffix(column.Name, "_at") {
			return fmt.Errorf("store: the retention of %s reads %q; a column holding an instant is named "+
				"*_at, as every instant in opnview's schema is", table.SQLName(), column.Name)
		}
		if len(table.PrimaryKey) > 0 && !contains(table.PrimaryKey, table.TimeColumn) {
			return fmt.Errorf("%w: the primary key of %s omits %s", ErrUniqueKeyWithoutTime, table.SQLName(),
				table.TimeColumn)
		}
		for _, index := range table.Indexes {
			if index.Unique && !contains(index.Columns, table.TimeColumn) {
				return fmt.Errorf("%w: a unique index of %s omits %s", ErrUniqueKeyWithoutTime, table.SQLName(),
					table.TimeColumn)
			}
		}
	}
	if table.RetentionSeconds < 0 {
		return fmt.Errorf("store: the retention of %s is negative", table.SQLName())
	}
	return nil
}

// contains reports whether a list holds a value.
func contains(list []string, value string) bool {
	for _, held := range list {
		if held == value {
			return true
		}
	}
	return false
}

// quotedList quotes every name of a list.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, quoteIdentifier(name))
	}
	return strings.Join(quoted, ", ")
}

// createStatements returns the DDL of a declaration: the schema, the table, its hypertable, then
// its indexes. Each is idempotent.
func (t PluginTable) createStatements() []string {
	var definition strings.Builder
	definition.WriteString("CREATE TABLE IF NOT EXISTS " + t.quoted() + " (\n")
	for index, column := range t.Columns {
		if index > 0 {
			definition.WriteString(",\n")
		}
		definition.WriteString("    " + quoteIdentifier(column.Name) + " " + PluginColumnTypes[column.Type])
		if !column.Nullable {
			definition.WriteString(" NOT NULL")
		}
	}
	if len(t.PrimaryKey) > 0 {
		definition.WriteString(",\n    PRIMARY KEY (" + quotedList(t.PrimaryKey) + ")")
	}
	definition.WriteString("\n)")
	statements := []string{
		"CREATE SCHEMA IF NOT EXISTS " + quoteIdentifier(PluginSchema(t.Plugin)),
		definition.String(),
	}
	if t.TimeColumn != "" {
		// An integer time column is chunked by a span of its own unit; an instant one by an interval.
		span := fmt.Sprintf("%d", hypertableChunkSeconds)
		if column, _ := t.column(t.TimeColumn); column.Type == pluginTypeInstant {
			span = fmt.Sprintf("INTERVAL '%d seconds'", hypertableChunkSeconds)
		}
		statements = append(statements, fmt.Sprintf(
			"SELECT create_hypertable('%s', by_range('%s', %s), if_not_exists => true, migrate_data => true)",
			strings.ReplaceAll(t.quoted(), "'", "''"), t.TimeColumn, span))
	}
	for index, declared := range t.Indexes {
		unique := ""
		if declared.Unique {
			unique = "UNIQUE "
		}
		statements = append(statements, fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s)",
			unique, quoteIdentifier(fmt.Sprintf("%s_index_%d", t.Table, index)), t.quoted(),
			quotedList(declared.Columns)))
	}
	return statements
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

// declaration is the full, canonical declaration of a table, as opnview records it: every change
// to any part of it rebuilds the table.
func (t PluginTable) declaration() string {
	shape := struct {
		Columns    []PluginColumn `json:"columns"`
		PrimaryKey []string       `json:"primary_key"`
		Indexes    []PluginIndex  `json:"indexes"`
		TimeColumn string         `json:"time_column"`
	}{t.Columns, t.PrimaryKey, t.Indexes, t.TimeColumn}
	encoded, _ := json.Marshal(shape)
	return string(encoded)
}

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
func (s *Store) EnsurePluginTable(ctx context.Context, table PluginTable) (*PluginTableEvent, error) {
	if err := ValidatePluginTable(table); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	declaration := table.declaration()
	var recorded string
	err := s.db.QueryRowContext(ctx, "SELECT declaration FROM plugin_table WHERE plugin = ? AND table_name = ?",
		table.Plugin, table.Table).Scan(&recorded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: reading the declaration of %s: %w", table.SQLName(), err)
	}
	exists, err := s.relationExists(ctx, PluginSchema(table.Plugin), table.Table)
	if err != nil {
		return nil, err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
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
	for _, statement := range table.createStatements() {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("store: creating %s: %w", table.SQLName(), err)
		}
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO plugin_table (plugin, table_name, declaration, time_column, retention_seconds, declared_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (plugin, table_name) DO UPDATE SET declaration = excluded.declaration,
		     time_column = excluded.time_column, retention_seconds = excluded.retention_seconds,
		     declared_at = CASE WHEN plugin_table.declaration = excluded.declaration
		                        THEN plugin_table.declared_at ELSE excluded.declared_at END`,
		table.Plugin, table.Table, declaration, nullableText(table.TimeColumn), table.RetentionSeconds, now); err != nil {
		return nil, fmt.Errorf("store: recording the declaration of %s: %w", table.SQLName(), err)
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: creating %s: %w", table.SQLName(), err)
	}
	if err := s.GrantReadOnSchema(ctx, PluginSchema(table.Plugin)); err != nil {
		return nil, err
	}
	s.plugins.Lock()
	if s.plugins.byName == nil {
		s.plugins.byName = map[string]PluginTable{}
	}
	s.plugins.byName[table.SQLName()] = table
	s.plugins.Unlock()
	return event, nil
}

// relationExists reports whether a table exists in a schema.
func (s *Store) relationExists(ctx context.Context, schema, table string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = ? AND tablename = ?)", schema, table).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: looking for %s.%s: %w", schema, table, err)
	}
	return exists, nil
}

// setAside moves a table whose declaration changed into the aside schema, drops its keys and
// indexes, records it, and resets its plugin's durable state, inside the caller's transaction.
func (s *Store) setAside(ctx context.Context, transaction *sql.Tx, table PluginTable, now int64) (
	*PluginTableEvent, error) {
	aside := fmt.Sprintf("%s%s__%s__%d", AsidePrefix, table.Plugin, table.Table, now)
	var rows int64
	if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM "+table.quoted()).Scan(&rows); err != nil {
		return nil, fmt.Errorf("store: counting the rows of %s: %w", table.SQLName(), err)
	}
	// The indexes and the keys go, so the next version's carry no name the aside table holds.
	indexes, err := transaction.QueryContext(ctx,
		`SELECT quote_ident(i.relname)
		 FROM pg_index AS x
		 JOIN pg_class AS i ON i.oid = x.indexrelid
		 WHERE x.indrelid = CAST(? AS regclass) AND NOT x.indisprimary
		   AND NOT EXISTS (SELECT 1 FROM pg_constraint AS c WHERE c.conindid = x.indexrelid)`, table.quoted())
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
		if _, err := transaction.ExecContext(ctx, "DROP INDEX "+quoteIdentifier(PluginSchema(table.Plugin))+"."+
			name); err != nil {
			return nil, fmt.Errorf("store: dropping %s: %w", name, err)
		}
	}
	constraints, err := transaction.QueryContext(ctx,
		`SELECT quote_ident(conname) FROM pg_constraint
		 WHERE conrelid = CAST(? AS regclass) AND contype IN ('p', 'u')`, table.quoted())
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
		if _, err := transaction.ExecContext(ctx, "ALTER TABLE "+table.quoted()+" DROP CONSTRAINT "+name); err != nil {
			return nil, fmt.Errorf("store: dropping the key %s of %s: %w", name, table.SQLName(), err)
		}
	}
	for _, statement := range []string{
		"CREATE SCHEMA IF NOT EXISTS " + quoteIdentifier(AsideSchema),
		"ALTER TABLE " + table.quoted() + " RENAME TO " + quoteIdentifier(aside),
		"ALTER TABLE " + quoteIdentifier(PluginSchema(table.Plugin)) + "." + quoteIdentifier(aside) +
			" SET SCHEMA " + quoteIdentifier(AsideSchema),
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("store: setting %s aside: %w", table.SQLName(), err)
		}
	}
	var timeColumn sql.NullString
	var retention int64
	if err := transaction.QueryRowContext(ctx,
		"SELECT time_column, retention_seconds FROM plugin_table WHERE plugin = ? AND table_name = ?",
		table.Plugin, table.Table).Scan(&timeColumn, &retention); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: reading the retention of %s: %w", table.SQLName(), err)
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO plugin_aside_table (name, plugin, table_name, time_column, retention_seconds, set_aside_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		aside, table.Plugin, table.Table, timeColumn, retention, now); err != nil {
		return nil, fmt.Errorf("store: recording %s: %w", aside, err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM plugin_state WHERE plugin = ?", table.Plugin); err != nil {
		return nil, fmt.Errorf("store: resetting the state of the plugin %s: %w", table.Plugin, err)
	}
	return &PluginTableEvent{Code: PluginTableRebuilt, Table: table.SQLName(), Aside: AsideSchema + "." + aside,
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
func (s *Store) AsideTables(ctx context.Context, plugin string) ([]AsideTable, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name, table_name, set_aside_at FROM plugin_aside_table WHERE plugin = ? ORDER BY name", plugin)
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
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdentifier(AsideSchema)+"."+
			quoteIdentifier(tables[index].Name)).Scan(&tables[index].Rows); err != nil {
			return nil, fmt.Errorf("store: counting %s: %w", tables[index].Name, err)
		}
		tables[index].Name = AsideSchema + "." + tables[index].Name
	}
	return tables, nil
}

// PluginTableRows counts the rows of one plugin table.
func (s *Store) PluginTableRows(ctx context.Context, table PluginTable) (int64, error) {
	var rows int64
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table.quoted()).Scan(&rows)
	return rows, err
}

// OrphanedTable is a table of a plugin that is no longer present: its schema and rows are kept,
// and the listing reports them.
type OrphanedTable struct {
	Plugin string `json:"plugin"`
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
}

// OrphanedPluginTables returns the recorded tables of every plugin not among the loaded ones, with
// their row counts. Nothing of them is touched: removing a plugin's data is the operator's act,
// one statement (DROP SCHEMA plugin_<name> CASCADE), as docs/plugins.md says.
func (s *Store) OrphanedPluginTables(ctx context.Context, loaded []string) ([]OrphanedTable, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT plugin, table_name FROM plugin_table ORDER BY plugin, table_name")
	if err != nil {
		return nil, fmt.Errorf("store: listing the plugin tables: %w", err)
	}
	var orphaned []OrphanedTable
	for rows.Next() {
		var table OrphanedTable
		if err := rows.Scan(&table.Plugin, &table.Table); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !contains(loaded, table.Plugin) {
			orphaned = append(orphaned, table)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range orphaned {
		exists, err := s.relationExists(ctx, PluginSchema(orphaned[index].Plugin), orphaned[index].Table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+
			PluginTable{Plugin: orphaned[index].Plugin, Table: orphaned[index].Table}.quoted()).
			Scan(&orphaned[index].Rows); err != nil {
			return nil, err
		}
	}
	return orphaned, nil
}

// PluginRows are the rows one plugin pass emitted, per table name (the plugin's own, not the
// qualified name), each row a map from column to value: an int64, a float64, a string or nil.
type PluginRows map[string][]map[string]any

// WritePluginPass stores what one plugin pass produced, in ONE transaction: the rows of its
// tables, upserted on their primary key, and the plugin's durable state. A failure stores nothing,
// so a watermark is never advanced past rows that were not kept, nor rows kept under a watermark
// that was not.
func (s *Store) WritePluginPass(ctx context.Context, plugin, provider string, rows PluginRows, state map[string]string,
	now int64) error {
	transaction, err := s.db.BeginTx(ctx, nil)
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
		table, found := s.LookupPluginTable(PluginTableSQLName(plugin, name))
		if !found {
			return fmt.Errorf("store: the plugin %s wrote into %q, which it does not own", plugin, name)
		}
		statement := table.upsertStatement()
		for _, row := range rows[name] {
			arguments := make([]any, 0, len(table.Columns))
			for _, column := range table.Columns {
				argument, err := pluginArgument(column, row[column.Name])
				if err != nil {
					return fmt.Errorf("store: a row of %s: %w", table.SQLName(), err)
				}
				arguments = append(arguments, argument)
			}
			if _, err := transaction.ExecContext(ctx, statement, arguments...); err != nil {
				return fmt.Errorf("store: writing a row of %s: %w", table.SQLName(), err)
			}
		}
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO plugin_state (plugin, key, value, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT (plugin, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			plugin, key, state[key], now); err != nil {
			return fmt.Errorf("store: writing the state %q of the plugin %s: %w", key, plugin, err)
		}
	}
	// Published inside the transaction: PostgreSQL delivers it on commit, and never on a rollback.
	if err := notify(ctx, transaction, ChannelPluginPass, map[string]string{"plugin": plugin, "provider": provider}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: storing the pass of the plugin %s: %w", plugin, err)
	}
	return nil
}

// upsertStatement is the INSERT a row of the table is written with: an upsert on the primary key
// when there is one, a plain insert otherwise.
func (t PluginTable) upsertStatement() string {
	names := make([]string, 0, len(t.Columns))
	marks := make([]string, 0, len(t.Columns))
	for _, column := range t.Columns {
		names = append(names, quoteIdentifier(column.Name))
		marks = append(marks, column.placeholder())
	}
	statement := "INSERT INTO " + t.quoted() + " (" + strings.Join(names, ", ") + ") VALUES (" +
		strings.Join(marks, ", ") + ")"
	if len(t.PrimaryKey) == 0 {
		return statement
	}
	key := map[string]bool{}
	for _, name := range t.PrimaryKey {
		key[name] = true
	}
	var updates []string
	for _, column := range t.Columns {
		if !key[column.Name] {
			updates = append(updates, quoteIdentifier(column.Name)+" = excluded."+quoteIdentifier(column.Name))
		}
	}
	if len(updates) == 0 {
		return statement + " ON CONFLICT (" + quotedList(t.PrimaryKey) + ") DO NOTHING"
	}
	return statement + " ON CONFLICT (" + quotedList(t.PrimaryKey) + ") DO UPDATE SET " +
		strings.Join(updates, ", ")
}

// PluginState returns every durable value a plugin stored, by key.
func (s *Store) PluginState(ctx context.Context, plugin string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM plugin_state WHERE plugin = ?", plugin)
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

// retentionHorizon is the instant before which a table's rows are purged, or false when nothing
// is: a table's own retention, or the installation's.
func retentionHorizon(ctx context.Context, q querier, now, retention int64) (int64, bool, error) {
	if retention > 0 {
		return now - retention, true, nil
	}
	horizon, found, err := queryOptionalIntText(ctx, q, "retention horizon",
		"SELECT :now - CAST(value AS BIGINT) FROM setting WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0",
		map[string]any{"now": now})
	return horizon, found, err
}

// purgeTimeTable purges one table with a time column to the exact second: on a hypertable, every
// chunk entirely before the horizon is dropped whole first, and the rows before it in the chunk the
// horizon falls in are deleted.
func purgeTimeTable(ctx context.Context, transaction *sql.Tx, qualified, column string, horizon int64) error {
	var hypertable bool
	if err := transaction.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables
		                WHERE CAST(format('%I.%I', hypertable_schema, hypertable_name) AS regclass)
		                    = CAST(? AS regclass))`,
		qualified).Scan(&hypertable); err != nil {
		return fmt.Errorf("store: looking up %s: %w", qualified, err)
	}
	instant, err := timeColumnIsInstant(ctx, transaction, qualified, column)
	if err != nil {
		return err
	}
	// An instant column is compared with the horizon as an instant, an integer one as an epoch.
	bound := "?"
	if instant {
		bound = "to_timestamp(?)"
	}
	if hypertable {
		if _, err := transaction.ExecContext(ctx, "SELECT drop_chunks(CAST(? AS regclass), older_than => "+bound+")",
			qualified, horizon); err != nil {
			return fmt.Errorf("store: dropping the chunks of %s: %w", qualified, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM "+qualified+" WHERE "+quoteIdentifier(column)+" < "+bound,
		horizon); err != nil {
		return fmt.Errorf("store: purging %s: %w", qualified, err)
	}
	return nil
}

// purgePluginTables purges every plugin table with a time column by its retention, inside the
// purge's transaction. A table with no time column is never purged; one whose retention is zero
// follows retention_seconds, under which zero deletes nothing.
func (s *Store) purgePluginTables(ctx context.Context, transaction *sql.Tx, now int64) error {
	for _, table := range s.PluginTables() {
		if table.TimeColumn == "" {
			continue
		}
		horizon, bounded, err := retentionHorizon(ctx, transaction, now, table.RetentionSeconds)
		if err != nil {
			return err
		}
		if !bounded {
			continue
		}
		if err := purgeTimeTable(ctx, transaction, table.quoted(), table.TimeColumn, horizon); err != nil {
			return err
		}
	}
	return s.purgeAsideTables(ctx, transaction, now)
}

// purgeAsideTables purges every table set aside by its recorded retention, and drops one that is
// then empty. A table with no time column is kept, and listed, until somebody removes it.
func (s *Store) purgeAsideTables(ctx context.Context, transaction *sql.Tx, now int64) error {
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
		qualified := quoteIdentifier(AsideSchema) + "." + quoteIdentifier(table.name)
		horizon, bounded, err := retentionHorizon(ctx, transaction, now, table.retention)
		if err != nil {
			return err
		}
		if bounded {
			if err := purgeTimeTable(ctx, transaction, qualified, table.column, horizon); err != nil {
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
			if _, err := transaction.ExecContext(ctx, "DELETE FROM plugin_aside_table WHERE name = ?",
				table.name); err != nil {
				return err
			}
		}
	}
	return nil
}
