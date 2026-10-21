package plugindecl

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"regexp"
	"strconv"
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
	"boolean": "BOOLEAN", "ip": "INET", "mac": "MACADDR", "instant": "TIMESTAMPTZ", "json": "JSONB"}

// PluginColumnTypeList is the vocabulary in the order docs/plugins.md lists it.
var PluginColumnTypeList = []string{"integer", "real", "text", "boolean", "ip", "mac", "instant", "json"}

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
	// Identity makes an integer column a generated identity, numbered by the database (ADR 0019).
	Identity bool
	// Generated is the stored expression the column is computed from, empty for a stored column (ADR 0019).
	Generated string
	// ReferencesTable and ReferencesColumn make the column a foreign key to a qualified table (ADR 0019).
	ReferencesTable  string
	ReferencesColumn string
	// Default is the SQL literal a row written without the column takes, empty for none (ADR 0020).
	Default string
}

// Written reports whether rows written to the table carry the column: an identity or a generated
// column is the database's to fill.
func (c PluginColumn) Written() bool { return !c.Identity && c.Generated == "" }

// PluginIndex is one index of a plugin table.
type PluginIndex struct {
	Columns []string
	Unique  bool
	// Where is the predicate of a partial index, empty for a whole one (ADR 0019).
	Where string
	// NullsNotDistinct makes a unique index treat two NULLs as equal (ADR 0020).
	NullsNotDistinct bool
}

// PluginCheck is one CHECK constraint of a plugin table (ADR 0019).
type PluginCheck struct {
	Name       string
	Expression string
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
	// RetentionSetting is the stored key of the plugin setting that overrides RetentionSeconds when it holds
	// a value above zero; empty when the declaration names none.
	RetentionSetting string
	// Counters makes it a table of counters (PA4 of the plugin-API amendment): every column outside its
	// primary key is a number, and a row written with a key already held adds its figures to the held
	// row's. Only a derivation writes one.
	Counters bool
	// WritableBy are the other plugins granted a write on the table (ADR 0019): each on the columns it
	// names (UPDATE), or on the whole table (INSERT, UPDATE, DELETE) when it names none. Not part of the
	// declaration a rebuild is decided on: a grant that changes is re-granted, never a reason to set the
	// table aside.
	WritableBy []WriteGrant
	// Checks are the table's CHECK constraints (ADR 0019).
	Checks []PluginCheck
	// KeepHeld makes a row written with the key of a held row leave the held row as it is (ADR 0020).
	KeepHeld bool
}

// WrittenColumns are the columns a row written to the table carries: every column but the identity
// and the generated ones.
func (t PluginTable) WrittenColumns() []PluginColumn {
	written := make([]PluginColumn, 0, len(t.Columns))
	for _, column := range t.Columns {
		if column.Written() {
			written = append(written, column)
		}
	}
	return written
}

// WriteGrant is one plugin allowed to write a table of another (ADR 0019), or, with Kind, every
// plugin providing a kind, whole (ADR 0020 wave 4).
type WriteGrant struct {
	Plugin  string
	Kind    string
	Columns []string
}

// Grants reports whether the grant lets a plugin providing some kinds write the whole table.
func (g WriteGrant) Grants(plugin string, kinds []string) bool {
	if g.Kind != "" {
		for _, kind := range kinds {
			if kind == g.Kind {
				return true
			}
		}
		return false
	}
	return g.Plugin == plugin && len(g.Columns) == 0
}

// Growing reports whether the table grows with time, as the schema checks classify every table:
// one declaring a time column is purged by its retention -- its own, or the installation's -- and
// accumulates a row per observation until then; one without holds what its plugin keeps, and is
// bounded. The class is read from the declaration alone, never from a list.
func (t PluginTable) Growing() bool { return t.TimeColumn != "" }

// PluginSchema is the schema of a plugin's tables.
func PluginSchema(plugin string) string { return PluginSchemaPrefix + plugin }

// SQLName is the table's name as a query names it: plugin_<plugin>.<table>.
func (t PluginTable) SQLName() string { return PluginTableSQLName(t.Plugin, t.Table) }

// PluginTableSQLName composes the qualified name of a plugin's table.
func PluginTableSQLName(plugin, table string) string { return PluginSchema(plugin) + "." + table }

// Quoted is the table's qualified name, each part quoted, as DDL and DML use it.
func (t PluginTable) Quoted() string {
	return QuoteIdentifier(PluginSchema(t.Plugin)) + "." + QuoteIdentifier(t.Table)
}

// QuoteIdentifier quotes an identifier.
func QuoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// CarriesDomain reports whether any column of the table carries a domain name.
func (t PluginTable) CarriesDomain() bool {
	for _, column := range t.Columns {
		if column.CarriesDomain {
			return true
		}
	}
	return false
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

// PluginTableSet is the plugin tables ensured on one store, by qualified name: the writes, the
// purge and the widget query check read them.
type PluginTableSet struct {
	sync.RWMutex
	ByName map[string]PluginTable
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
		if column.Identity && (column.Type != "integer" || column.Nullable || column.Generated != "") {
			return fmt.Errorf("store: the identity column %q of %s is not a NOT NULL integer with no expression",
				column.Name, table.SQLName())
		}
		if column.Generated != "" {
			if strings.TrimSpace(column.Generated) == "" {
				return fmt.Errorf("store: the generated column %q of %s has an empty expression", column.Name, table.SQLName())
			}
			if column.Name == table.TimeColumn || Contains(table.PrimaryKey, column.Name) {
				return fmt.Errorf("store: the generated column %q of %s is the time column or in the primary key, which "+
					"a row written must carry", column.Name, table.SQLName())
			}
		}
		if (column.ReferencesTable == "") != (column.ReferencesColumn == "") {
			return fmt.Errorf("store: the column %q of %s references a table or a column, not both", column.Name,
				table.SQLName())
		}
	}
	checkNames := map[string]bool{}
	for _, check := range table.Checks {
		if !PluginTableNamePattern.MatchString(check.Name) || checkNames[check.Name] {
			return fmt.Errorf("store: the check %q of %s is not named by a snake_case word once", check.Name, table.SQLName())
		}
		checkNames[check.Name] = true
		if strings.TrimSpace(check.Expression) == "" {
			return fmt.Errorf("store: the check %q of %s has an empty expression", check.Name, table.SQLName())
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
			// An entry that is not a column is an expression over the row (ADR 0020), which the loader has
			// checked as every declared expression is checked; it carries a call, at the least.
			if !seen[name] && !IsIndexExpression(name) {
				return fmt.Errorf("store: an index of %s names %q, which is neither a column nor an expression", table.SQLName(), name)
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
		if len(table.PrimaryKey) > 0 && !Contains(table.PrimaryKey, table.TimeColumn) {
			return fmt.Errorf("%w: the primary key of %s omits %s", ErrUniqueKeyWithoutTime, table.SQLName(),
				table.TimeColumn)
		}
		for _, index := range table.Indexes {
			if index.Unique && !Contains(index.Columns, table.TimeColumn) {
				return fmt.Errorf("%w: a unique index of %s omits %s", ErrUniqueKeyWithoutTime, table.SQLName(),
					table.TimeColumn)
			}
		}
	}
	if table.RetentionSeconds < 0 {
		return fmt.Errorf("store: the retention of %s is negative", table.SQLName())
	}
	if table.Counters {
		if len(table.PrimaryKey) == 0 {
			return fmt.Errorf("store: the counter table %s declares no primary key: a counter is added to the row of its key",
				table.SQLName())
		}
		for _, column := range table.WrittenColumns() {
			if !Contains(table.PrimaryKey, column.Name) && ((column.Type != "integer" && column.Type != "real") || column.Nullable) {
				return fmt.Errorf("store: the column %q of the counter table %s is outside its primary key and is not a "+
					"NOT NULL number: every such column is a counter", column.Name, table.SQLName())
			}
		}
	}
	return nil
}

// IsIndexExpression reports whether an index entry is an expression rather than a column name: it is
// not a snake_case word, and it holds a parenthesis.
func IsIndexExpression(entry string) bool {
	return !PluginTableNamePattern.MatchString(entry) && strings.Contains(entry, "(")
}

// indexList quotes the column names of an index and leaves its expressions as written.
func indexList(entries []string) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if IsIndexExpression(entry) {
			parts = append(parts, "("+entry+")")
			continue
		}
		parts = append(parts, QuoteIdentifier(entry))
	}
	return strings.Join(parts, ", ")
}

// quotedList quotes every name of a list.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, QuoteIdentifier(name))
	}
	return strings.Join(quoted, ", ")
}

// CreateStatements returns the DDL of a declaration: the schema, the table, its hypertable, then
// its indexes. Each is idempotent.
func (t PluginTable) CreateStatements() []string {
	var definition strings.Builder
	definition.WriteString("CREATE TABLE IF NOT EXISTS " + t.Quoted() + " (\n")
	for index, column := range t.Columns {
		if index > 0 {
			definition.WriteString(",\n")
		}
		definition.WriteString("    " + QuoteIdentifier(column.Name) + " " + PluginColumnTypes[column.Type])
		switch {
		case column.Identity:
			definition.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
		case column.Generated != "":
			definition.WriteString(" GENERATED ALWAYS AS (" + column.Generated + ") STORED")
		}
		if !column.Nullable {
			definition.WriteString(" NOT NULL")
		}
		if column.Default != "" {
			definition.WriteString(" DEFAULT " + column.Default)
		}
		if column.ReferencesTable != "" {
			definition.WriteString(" REFERENCES " + column.ReferencesTable + " (" + QuoteIdentifier(column.ReferencesColumn) + ")")
		}
	}
	if len(t.PrimaryKey) > 0 {
		definition.WriteString(",\n    PRIMARY KEY (" + quotedList(t.PrimaryKey) + ")")
	}
	for _, check := range t.Checks {
		definition.WriteString(",\n    CONSTRAINT " + QuoteIdentifier(t.Table+"_"+check.Name) + " CHECK (" + check.Expression + ")")
	}
	definition.WriteString("\n)")
	statements := []string{
		"CREATE SCHEMA IF NOT EXISTS " + QuoteIdentifier(PluginSchema(t.Plugin)),
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
			strings.ReplaceAll(t.Quoted(), "'", "''"), t.TimeColumn, span))
	}
	for index, declared := range t.Indexes {
		unique := ""
		if declared.Unique {
			unique = "UNIQUE "
		}
		where := ""
		if declared.Where != "" {
			where = " WHERE " + declared.Where
		}
		nulls := ""
		if declared.NullsNotDistinct {
			nulls = " NULLS NOT DISTINCT"
		}
		statements = append(statements, fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s)%s%s",
			unique, QuoteIdentifier(fmt.Sprintf("%s_index_%d", t.Table, index)), t.Quoted(),
			indexList(declared.Columns), nulls, where))
	}
	return statements
}

// Declaration is the full, canonical declaration of a table, as opnview records it: every change
// to any part of it rebuilds the table.
func (t PluginTable) Declaration() string {
	shape := struct {
		Columns    []PluginColumn `json:"columns"`
		PrimaryKey []string       `json:"primary_key"`
		Indexes    []PluginIndex  `json:"indexes"`
		TimeColumn string         `json:"time_column"`
		Checks     []PluginCheck  `json:"checks,omitempty"`
	}{t.Columns, t.PrimaryKey, t.Indexes, t.TimeColumn, t.Checks}
	encoded, _ := json.Marshal(shape)
	return string(encoded)
}

// CounterEmptyDelete is the statement that removes, among the keys of rows a derivation added to a table of
// counters, every row whose figures all came back to zero: a counter holding nothing is no row, as a row
// was never written for it. Its arguments are each row's primary key, typed as the key's columns.
func (t PluginTable) CounterEmptyDelete(rows []map[string]any) (string, []any, error) {
	columns := make(map[string]PluginColumn, len(t.Columns))
	for _, column := range t.WrittenColumns() {
		columns[column.Name] = column
	}
	tuples := make([]string, 0, len(rows))
	arguments := make([]any, 0, len(rows)*len(t.PrimaryKey))
	for _, row := range rows {
		marks := make([]string, 0, len(t.PrimaryKey))
		for _, name := range t.PrimaryKey {
			argument, err := PluginArgument(columns[name], row[name])
			if err != nil {
				return "", nil, fmt.Errorf("store: a row of %s: %w", t.SQLName(), err)
			}
			arguments = append(arguments, argument)
			marks = append(marks, "CAST($"+strconv.Itoa(len(arguments))+" AS "+PluginColumnTypes[columns[name].Type]+")")
		}
		tuples = append(tuples, "("+strings.Join(marks, ", ")+")")
	}
	var zero []string
	for _, column := range t.WrittenColumns() {
		if !Contains(t.PrimaryKey, column.Name) {
			zero = append(zero, QuoteIdentifier(column.Name)+" = 0")
		}
	}
	return "DELETE FROM " + t.Quoted() + " WHERE (" + quotedList(t.PrimaryKey) + ") IN (VALUES " +
		strings.Join(tuples, ", ") + ") AND " + strings.Join(zero, " AND "), arguments, nil
}

// ConflictKey is the key a row written again replaces the held row on: the primary key when the
// rows carry it, else the table's first unique index (ADR 0020 wave 4: a table numbered by an
// identity column and written by a pass, such as plugin_leases.dhcp_lease, is keyed by what the
// source reports). Nil for a table with neither, whose every row is a new row.
func (t PluginTable) ConflictKey() []string {
	written := map[string]bool{}
	for _, column := range t.WrittenColumns() {
		written[column.Name] = true
	}
	carried := len(t.PrimaryKey) > 0
	for _, name := range t.PrimaryKey {
		carried = carried && written[name]
	}
	if carried {
		return t.PrimaryKey
	}
	for _, index := range t.Indexes {
		if index.Unique && index.Where == "" && !hasIndexExpression(index.Columns) {
			return index.Columns
		}
	}
	return nil
}

// UpsertStatement is the INSERT a row of the table is written with: an upsert on the conflict key
// when there is one, a plain insert otherwise.
func (t PluginTable) UpsertStatement() string {
	names := make([]string, 0, len(t.Columns))
	marks := make([]string, 0, len(t.Columns))
	for index, column := range t.Columns {
		names = append(names, QuoteIdentifier(column.Name))
		marks = append(marks, column.placeholder(index+1))
	}
	statement := "INSERT INTO " + t.Quoted() + " (" + strings.Join(names, ", ") + ") VALUES (" +
		strings.Join(marks, ", ") + ")"
	conflict := t.ConflictKey()
	if len(conflict) == 0 {
		return statement
	}
	key := map[string]bool{}
	for _, name := range conflict {
		key[name] = true
	}
	var updates []string
	for _, column := range t.WrittenColumns() {
		if !key[column.Name] {
			updates = append(updates, QuoteIdentifier(column.Name)+" = excluded."+QuoteIdentifier(column.Name))
		}
	}
	if len(updates) == 0 {
		return statement + " ON CONFLICT (" + quotedList(conflict) + ") DO NOTHING"
	}
	return statement + " ON CONFLICT (" + quotedList(conflict) + ") DO UPDATE SET " +
		strings.Join(updates, ", ")
}

// placeholder is the parameter of one column in an INSERT.
func (c PluginColumn) placeholder(number int) string {
	mark := "$" + strconv.Itoa(number)
	switch c.Type {
	case "boolean", "ip", "mac", pluginTypeInstant, "json":
		return "CAST(" + mark + " AS " + PluginColumnTypes[c.Type] + ")"
	}
	return mark
}

// PluginArgument turns a row's value into the argument its column's placeholder takes: an int64,
// a float64 or text; nil stays nil. A value of the wrong kind is an error naming the column.
func PluginArgument(column PluginColumn, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	invalid := func() error {
		return fmt.Errorf("the column %s is %s and was given %T", column.Name, column.Type, value)
	}
	switch column.Type {
	case "integer":
		switch typed := value.(type) {
		case int64:
			return typed, nil
		case int:
			return int64(typed), nil
		case float64:
			if typed != math.Trunc(typed) {
				return nil, invalid()
			}
			return int64(typed), nil
		}
	case "real":
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case int64:
			return float64(typed), nil
		case int:
			return float64(typed), nil
		}
	case "text":
		if typed, ok := value.(string); ok {
			return typed, nil
		}
	case "boolean":
		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed), nil
		case string:
			return typed, nil
		}
	case "ip":
		switch typed := value.(type) {
		case netip.Addr:
			return typed.String(), nil
		case string:
			return typed, nil
		}
	case "mac":
		switch typed := value.(type) {
		case net.HardwareAddr:
			return typed.String(), nil
		case string:
			return typed, nil
		}
	case "json":
		// A document: text that is JSON, or any value, encoded.
		if typed, ok := value.(string); ok {
			return typed, nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, invalid()
		}
		return string(encoded), nil
	case pluginTypeInstant:
		switch typed := value.(type) {
		case time.Time:
			return typed.UTC().Format(time.RFC3339Nano), nil
		case int64:
			return time.Unix(typed, 0).UTC().Format(time.RFC3339Nano), nil
		case float64:
			seconds, fraction := math.Modf(typed)
			return time.Unix(int64(seconds), int64(math.Round(fraction*1e9))).UTC().Format(time.RFC3339Nano), nil
		case string:
			return typed, nil
		}
	}
	return nil, invalid()
}

// MultiRowInsert is one INSERT of several rows, with the upsert of upsertStatement.
func (t PluginTable) MultiRowInsert(rows []map[string]any) (string, []any, error) {
	return t.multiRowInsert(rows, t.KeepHeld)
}

// MultiRowInsertKeeping is MultiRowInsert for rows that never replace a held row of their key: a live
// pass's fine samples, which a baseline pass that read the same capture may already hold (ADR 0020).
func (t PluginTable) MultiRowInsertKeeping(rows []map[string]any) (string, []any, error) {
	return t.multiRowInsert(rows, true)
}

func (t PluginTable) multiRowInsert(rows []map[string]any, keeping bool) (string, []any, error) {
	names := make([]string, 0, len(t.Columns))
	for _, column := range t.WrittenColumns() {
		names = append(names, QuoteIdentifier(column.Name))
	}
	tuples := make([]string, 0, len(rows))
	arguments := make([]any, 0, len(rows)*len(t.Columns))
	for _, row := range rows {
		marks := make([]string, 0, len(t.Columns))
		for _, column := range t.WrittenColumns() {
			if row[column.Name] == nil && column.Default != "" {
				// A row leaving out a column with a declared default takes the default (ADR 0020).
				marks = append(marks, "DEFAULT")
				continue
			}
			argument, err := PluginArgument(column, row[column.Name])
			if err != nil {
				return "", nil, fmt.Errorf("store: a row of %s: %w", t.SQLName(), err)
			}
			arguments = append(arguments, argument)
			marks = append(marks, column.placeholder(len(arguments)))
		}
		tuples = append(tuples, "("+strings.Join(marks, ", ")+")")
	}
	statement := "INSERT INTO " + t.Quoted() + " (" + strings.Join(names, ", ") + ") VALUES " +
		strings.Join(tuples, ", ")
	upsert := t.UpsertStatement()
	if conflict := strings.Index(upsert, " ON CONFLICT "); conflict >= 0 {
		if keeping {
			statement += " ON CONFLICT (" + quotedList(t.ConflictKey()) + ") DO NOTHING"
		} else {
			statement += upsert[conflict:]
		}
	}
	return statement, arguments, nil
}

// LastPerKey keeps, of rows sharing a conflict key, the last: one INSERT may not touch a row twice,
// and the last row written is the one a sequence of single writes would have kept. A key holding a
// column the rows do not carry -- a generated one -- cannot be compared here, and the rows are
// written as they are.
func LastPerKey(table PluginTable, rows []map[string]any) []map[string]any {
	conflict := table.ConflictKey()
	written := map[string]bool{}
	for _, column := range table.WrittenColumns() {
		written[column.Name] = true
	}
	for _, name := range conflict {
		if !written[name] {
			return rows
		}
	}
	if len(conflict) == 0 {
		return rows
	}
	position := map[string]int{}
	var kept []map[string]any
	for _, row := range rows {
		parts := make([]string, 0, len(conflict))
		for _, name := range conflict {
			parts = append(parts, fmt.Sprintf("%T:%v", row[name], row[name]))
		}
		key := strings.Join(parts, "\x00")
		if index, seen := position[key]; seen {
			kept[index] = row
			continue
		}
		position[key] = len(kept)
		kept = append(kept, row)
	}
	return kept
}

// SumPerKey adds up, for a table of counters, the rows sharing a primary key, so a statement writes each key
// once -- as a sequence of single writes would have added them -- and PostgreSQL never meets one key twice in one
// INSERT. The figures are whole numbers or reals, as a counter table's columns are.
func SumPerKey(table PluginTable, rows []map[string]any) []map[string]any {
	position := map[string]int{}
	var summed []map[string]any
	for _, row := range rows {
		parts := make([]string, 0, len(table.PrimaryKey))
		for _, name := range table.PrimaryKey {
			parts = append(parts, fmt.Sprintf("%T:%v", row[name], row[name]))
		}
		key := strings.Join(parts, "\x00")
		index, seen := position[key]
		if !seen {
			position[key] = len(summed)
			copied := make(map[string]any, len(row))
			for name, value := range row {
				copied[name] = value
			}
			summed = append(summed, copied)
			continue
		}
		held := summed[index]
		for _, column := range table.WrittenColumns() {
			if Contains(table.PrimaryKey, column.Name) {
				continue
			}
			switch value := row[column.Name].(type) {
			case int64:
				total, _ := held[column.Name].(int64)
				held[column.Name] = total + value
			case float64:
				total, _ := held[column.Name].(float64)
				held[column.Name] = total + value
			}
		}
	}
	return summed
}

// Contains reports whether a list holds a value.
func Contains(list []string, value string) bool {
	for _, held := range list {
		if held == value {
			return true
		}
	}
	return false
}

// DerivationInsert is the INSERT a derivation's rows are written with (PA4 of the plugin-API amendment): one
// multi-row statement, on the primary key every derivation output declares. A row of an ordinary table
// replaces the held row only when one of its values differs, and a row of a counter table adds its
// figures to the held row's, so a statement writing what is already held changes no row and the rows it
// reports affected are exactly the rows it changed.
func (t PluginTable) DerivationInsert(rows []map[string]any) (string, []any, error) {
	if len(t.PrimaryKey) == 0 {
		return "", nil, fmt.Errorf("store: %s declares no primary key, which a derivation's output needs", t.SQLName())
	}
	names := make([]string, 0, len(t.Columns))
	for _, column := range t.WrittenColumns() {
		names = append(names, QuoteIdentifier(column.Name))
	}
	tuples := make([]string, 0, len(rows))
	arguments := make([]any, 0, len(rows)*len(t.Columns))
	for _, row := range rows {
		marks := make([]string, 0, len(t.Columns))
		for _, column := range t.WrittenColumns() {
			argument, err := PluginArgument(column, row[column.Name])
			if err != nil {
				return "", nil, fmt.Errorf("store: a row of %s: %w", t.SQLName(), err)
			}
			arguments = append(arguments, argument)
			marks = append(marks, column.placeholder(len(arguments)))
		}
		tuples = append(tuples, "("+strings.Join(marks, ", ")+")")
	}
	var updates, held, offered []string
	for _, column := range t.WrittenColumns() {
		if Contains(t.PrimaryKey, column.Name) {
			continue
		}
		name := QuoteIdentifier(column.Name)
		if t.Counters {
			updates = append(updates, name+" = t."+name+" + excluded."+name)
			offered = append(offered, "excluded."+name+" <> 0")
			continue
		}
		updates = append(updates, name+" = excluded."+name)
		held, offered = append(held, "t."+name), append(offered, "excluded."+name)
	}
	statement := "INSERT INTO " + t.Quoted() + " AS t (" + strings.Join(names, ", ") + ") VALUES " +
		strings.Join(tuples, ", ") + " ON CONFLICT (" + quotedList(t.PrimaryKey) + ")"
	switch {
	case len(updates) == 0:
		statement += " DO NOTHING"
	case t.Counters:
		statement += " DO UPDATE SET " + strings.Join(updates, ", ") + " WHERE " + strings.Join(offered, " OR ")
	default:
		statement += " DO UPDATE SET " + strings.Join(updates, ", ") + " WHERE (" + strings.Join(held, ", ") +
			") IS DISTINCT FROM (" + strings.Join(offered, ", ") + ")"
	}
	return statement, arguments, nil
}

// hasIndexExpression reports whether any entry of an index is an expression rather than a column.
func hasIndexExpression(entries []string) bool {
	for _, entry := range entries {
		if IsIndexExpression(entry) {
			return true
		}
	}
	return false
}
