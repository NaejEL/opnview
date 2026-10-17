package reads

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// What a checked query reads, decided from PostgreSQL's own plan.
//
// THE PLAN, NOT THE TEXT. A query's text can reach a table through a view, a common table
// expression or a subquery, and can name a column through `*`; a substring search would miss each.
// PostgreSQL's verbose plan names every relation it scans, with its schema and alias, and prints
// every column it reads, qualified by that alias, whatever the text said (Reads).
//
// Two guards are built on it, and neither is a sandbox of plugins: it is the HTTP API's own
// data-leak guard (decision 12 of the backend-completion cycle makes a leak blocking). The read
// role's privileges (internal/store/sqldb/readers.go) are the second layer under the first guard.
//
//   - A widget query reading a table that holds a credential, a session or a setting row is
//     refused at load, with a closed code (CredentialTables).
//   - A widget query reading a column that carries a domain name is withheld whole under
//     aggregate_mode = no_domains (DomainColumns, plus the columns a plugin table declares as
//     carrying one).

// CredentialTables are the tables no widget query may read: the accounts and their password
// records, the sessions and their tokens, the encrypted credentials, and the setting rows, among
// which the firewall's API key and every plugin setting live.
var CredentialTables = []string{"account", "session", "encrypted_credential", "setting", "plugin_state"}

// DomainColumns are the core columns that carry a domain name, by table. A query reading any of
// them is withheld under aggregate_mode = no_domains.
var DomainColumns = map[string][]string{
	"dns_resolution":              {"domain"},
	"domain_attribution":          {"site_name"},
	"domain_volume_aggregate_1h":  {"site_name"},
	"domain_volume_aggregate_24h": {"site_name"},
	"domain_volume_aggregate_7d":  {"site_name"},
	"domain_volume_aggregate_30d": {"site_name"},
	"resource_record_observation": {"owner_name", "value", "host_label"},
}

// QueryReads is what one query reads: its tables, and its columns by table.
type QueryReads struct {
	Tables  []string
	Columns map[string][]string
}

// ReadsTable reports whether the query reads a table.
func (r QueryReads) ReadsTable(name string) bool {
	for _, table := range r.Tables {
		if table == name {
			return true
		}
	}
	return false
}

// ReadsColumn reports whether the query reads one column of a table.
func (r QueryReads) ReadsColumn(table, column string) bool {
	for _, read := range r.Columns[table] {
		if read == column {
			return true
		}
	}
	return false
}

// CarriesDomain reports whether the query reads a column carrying a domain name: a core one, or
// one a plugin table on this store declares.
func (s *ReadStore) CarriesDomain(reads QueryReads) bool {
	for table, columns := range DomainColumns {
		for _, column := range columns {
			if reads.ReadsColumn(table, column) {
				return true
			}
		}
	}
	for _, table := range plugintables.On(s.Handle).PluginTables() {
		for _, column := range table.Columns {
			if column.CarriesDomain && reads.ReadsColumn(table.SQLName(), column.Name) {
				return true
			}
		}
	}
	return false
}

// CredentialTablesRead returns the credential tables a query reads, sorted.
func CredentialTablesRead(reads QueryReads) []string {
	var read []string
	for _, table := range CredentialTables {
		if reads.ReadsTable(table) {
			read = append(read, table)
		}
	}
	sort.Strings(read)
	return read
}

// Reads plans a query that already passed CheckQuery and returns what it reads.
//
// It asks PostgreSQL for the query's plan, with its output lists (EXPLAIN (VERBOSE, FORMAT
// JSON)): every relation the plan scans is named with its schema and the alias the query gave it,
// and every column it reads appears, qualified by that alias, in an output list or a condition.
// A view is expanded to the tables it reads, a `*` to the columns it stands for. The plan is made
// on the owner's connection inside a read-only transaction, never run: the read role holds no
// privilege on the credential tables, so a query reading one would not plan for it at all, and
// this check exists to name that table.
func (s *ReadStore) Reads(ctx context.Context, text string, parameters []string) (QueryReads, error) {
	planned, arguments, err := sqldb.NullArguments(sqldb.StatementBody(text, sqldb.LexSQL(text)))
	if err != nil {
		return QueryReads{}, fmt.Errorf("store: planning a query: %w", err)
	}
	tx, err := s.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return QueryReads{}, fmt.Errorf("store: planning a query: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var plan string
	if err := tx.QueryRowContext(ctx, "EXPLAIN (VERBOSE, FORMAT JSON) "+planned, arguments...).Scan(&plan); err != nil {
		return QueryReads{}, fmt.Errorf("store: planning a query: %w", err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(plan), &decoded); err != nil {
		return QueryReads{}, fmt.Errorf("store: reading a query's plan: %w", err)
	}
	// Every relation the plan scans, by the alias the plan gives it, and every text of the plan,
	// where the qualified column references are.
	aliases := map[string][]string{}
	tables := map[string]bool{}
	var texts []string
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if relation, ok := typed["Relation Name"].(string); ok {
				schema, _ := typed["Schema"].(string)
				schema, relation = s.hypertableOf(ctx, tx, schema, relation)
				name := qualifiedTable(schema, relation)
				tables[name] = true
				alias, _ := typed["Alias"].(string)
				if alias == "" {
					alias = relation
				}
				aliases[alias] = appendUnique(aliases[alias], name)
			}
			for key, value := range typed {
				if text, ok := value.(string); ok && key != "Relation Name" && key != "Schema" && key != "Alias" {
					texts = append(texts, text)
					continue
				}
				walk(value)
			}
		case []any:
			for _, element := range typed {
				walk(element)
			}
		case string:
			texts = append(texts, typed)
		}
	}
	walk(decoded)
	reads := QueryReads{Columns: map[string][]string{}}
	for table := range tables {
		reads.Tables = append(reads.Tables, table)
	}
	sort.Strings(reads.Tables)
	known := map[string]map[string]bool{}
	for table := range tables {
		columns, err := s.tableColumnNames(ctx, tx, table)
		if err != nil {
			return QueryReads{}, err
		}
		known[table] = columns
	}
	seen := map[[2]string]bool{}
	for _, text := range texts {
		for _, match := range columnReference.FindAllStringSubmatch(text, -1) {
			alias, column := strings.Trim(match[1], `"`), strings.Trim(match[2], `"`)
			for _, table := range aliases[alias] {
				if !known[table][column] {
					continue
				}
				identity := [2]string{table, column}
				if !seen[identity] {
					seen[identity] = true
					reads.Columns[table] = append(reads.Columns[table], column)
				}
			}
		}
	}
	for table := range reads.Columns {
		sort.Strings(reads.Columns[table])
	}
	return reads, nil
}

// columnReference is a qualified column reference as a plan prints one: alias.column, either part
// quoted when it must be.
var columnReference = regexp.MustCompile(`("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)\.("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)`)

// qualifiedTable names a table as the guards do: a core table by its own name, any other as
// schema.table.
func qualifiedTable(schema, relation string) string {
	if schema == "" || schema == sqldb.CoreSchema {
		return relation
	}
	return schema + "." + relation
}

// tableColumnNames returns the column names of a table named as qualifiedTable names it.
func (s *ReadStore) tableColumnNames(ctx context.Context, q sqldb.Querier, table string) (map[string]bool, error) {
	schema, relation := sqldb.CoreSchema, table
	if dot := strings.IndexByte(table, '.'); dot >= 0 {
		schema, relation = table[:dot], table[dot+1:]
	}
	rows, err := q.QueryContext(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2",
		schema, relation)
	if err != nil {
		return nil, fmt.Errorf("store: reading the columns of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// appendUnique appends a value a list does not hold yet.
func appendUnique(list []string, value string) []string {
	for _, held := range list {
		if held == value {
			return list
		}
	}
	return append(list, value)
}

// ReadRows runs a checked read query on the read connections and returns its rows, each a map from
// column to value: an int64, a float64, a string or nil.
func (s *ReadStore) ReadRows(ctx context.Context, text string, parameters map[string]any) ([]map[string]any, error) {
	if s.Reader == nil {
		return nil, fmt.Errorf("store: no read connections")
	}
	translation, err := sqldb.Translate(sqldb.StatementBody(text, sqldb.LexSQL(text)))
	if err != nil {
		return nil, err
	}
	// A parameter the caller does not hand is bound to NULL.
	arguments := make([]any, len(translation.Names))
	for index, name := range translation.Names {
		arguments[index] = parameters[name]
	}
	rows, err := s.Reader.DB.QueryContext(ctx, translation.Text, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	converter, err := newCellConverter(rows)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for rows.Next() {
		cells := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range cells {
			pointers[index] = &cells[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		converter.convert(cells)
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = cells[index]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// hypertableOf names the hypertable a TimescaleDB chunk belongs to: a plan scanning a hypertable that holds
// rows scans its chunks, which live in TimescaleDB's own schema, and a query reads the hypertable it named,
// never a chunk. Any other relation is answered as it is.
func (s *ReadStore) hypertableOf(ctx context.Context, q sqldb.Querier, schema, relation string) (string, string) {
	if schema != "_timescaledb_internal" {
		return schema, relation
	}
	var hyperSchema, hyperName string
	if err := q.QueryRowContext(ctx,
		`SELECT hypertable_schema, hypertable_name FROM timescaledb_information.chunks
		 WHERE chunk_schema = $1 AND chunk_name = $2`, schema, relation).Scan(&hyperSchema, &hyperName); err != nil {
		return schema, relation
	}
	return hyperSchema, hyperName
}
