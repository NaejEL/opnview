// Command plugintables creates the built-in plugins' tables in a seeded database, as the product
// does, and prints each with its class, for sql/schema-checks.sh: the checks' entity,
// classification, foreign-key, seed-fill, plan and purge sections cover the plugins' tables
// without a list of them anywhere.
//
//	plugintables -database '<connection string>' -now <epoch> [-seed <rows per table>]
//	             [-statements <file>] [-older] [-purge] [-data-dir <directory>]
//
// It opens the database through the store and runs the plugin loader over the plugins built into
// the binary -- and over a data directory's plugins/ subdirectory, with -data-dir, as the product
// reads the plugins a user added -- which creates each declared table exactly as a collector's
// first probe round does. Then, for every plugin table, it prints one line,
//
//	<schema>.<table> TAB <growing|bounded> TAB <time column or ->
//
// THE CLASS COMES FROM THE MANIFEST, never from a list: a table declaring a time column is purged
// by its retention, and grows with time, so it is growing; a table without one holds what its
// plugin keeps and is bounded (plugindecl.PluginTable.Growing).
//
// -seed writes that many rows into every table, from its declared columns alone: each value is
// synthesised from the row's number and its column's type, and a time column's instants are spread
// back from -now over twice the table's retention horizon, so the purge has rows on both sides of
// it. No value is an address of anybody's network: an ip column holds an address synthesised from
// the counter.
//
// -statements writes, for every plugin dataset the loader declared, the two statements the data
// catalogue runs on it -- its default query, compiled as the catalogue compiles it, and its earliest
// row's -- into one file under "-- statement: <name>" markers, the convention every statements file
// of the tree follows, so the schema checks' plan sweep plans them with the rest.
//
// -older prints, for every growing plugin table, "<schema>.<table> TAB <rows older than its
// horizon at -now> TAB <rows>": the horizon is the table's own retention, or the installation's.
//
// -purge then runs the store's whole retention purge at -now, the product's own: the core's
// purge.sql, the plugin tables by their retention and the NetFlow tables by theirs.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
)

// options are the command's flags.
type options struct {
	database   string
	now        int64
	seed       int
	statements string
	older      bool
	purge      bool
	dataDir    string
}

func main() {
	var o options
	flag.StringVar(&o.database, "database", "", "the PostgreSQL connection string of the seeded database")
	flag.Int64Var(&o.now, "now", 0, "the seed's instant, in UTC epoch seconds")
	flag.IntVar(&o.seed, "seed", 0, "rows written into every plugin table; zero writes none")
	flag.StringVar(&o.statements, "statements", "", "a file to write the plugin datasets' statements into")
	flag.BoolVar(&o.older, "older", false, "count the rows of every growing plugin table older than its horizon")
	flag.BoolVar(&o.purge, "purge", false, "run the store's whole retention purge at -now")
	flag.StringVar(&o.dataDir, "data-dir", "", "a data directory whose plugins/ subdirectory is loaded too")
	flag.Parse()
	if o.database == "" || o.now == 0 {
		fmt.Fprintln(os.Stderr, "plugintables: -database and -now are required")
		os.Exit(2)
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "plugintables:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	ctx := context.Background()
	database, err := store.Open(ctx, o.database)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	loaded, err := plugin.Load(ctx, plugin.Options{Store: database, DataDir: o.dataDir})
	if err != nil {
		return err
	}
	if len(loaded.Refused) > 0 {
		return fmt.Errorf("the plugin %s was refused: %s", loaded.Refused[0].Plugin, loaded.Refused[0].Detail)
	}
	tables := database.PluginTables()
	sort.Slice(tables, func(i, j int) bool { return tables[i].SQLName() < tables[j].SQLName() })
	for _, table := range tables {
		if o.seed > 0 {
			span, err := seedSpan(ctx, database, table, o.now)
			if err != nil {
				return err
			}
			if _, err := database.InsertPluginRows(ctx, table, Rows(table, o.seed, o.now, span)); err != nil {
				return err
			}
		}
		class, column := "bounded", "-"
		if table.Growing() {
			class, column = "growing", table.TimeColumn
		}
		fmt.Printf("%s\t%s\t%s\n", table.SQLName(), class, column)
	}
	if o.statements != "" {
		if err := writeStatements(o.statements, loaded.Datasets(), o.now); err != nil {
			return err
		}
	}
	if o.older {
		for _, table := range tables {
			if !table.Growing() {
				continue
			}
			older, total, err := olderThanHorizon(ctx, database, table, o.now)
			if err != nil {
				return err
			}
			fmt.Printf("older\t%s\t%d\t%d\n", table.SQLName(), older, total)
		}
	}
	if o.purge {
		return database.Purge(ctx, o.now)
	}
	return nil
}

// seedSpan is how far back a table's synthesised instants reach: twice its retention horizon, its
// own or the installation's, so rows lie on both sides of it; a day when nothing is ever purged.
func seedSpan(ctx context.Context, database *store.Store, table plugindecl.PluginTable, now int64) (int64, error) {
	if !table.Growing() {
		return 0, nil
	}
	horizon, bounded, err := plugintables.RetentionHorizon(ctx, database.SQL, now, table.RetentionSeconds)
	if err != nil {
		return 0, err
	}
	if !bounded {
		return 86400, nil
	}
	return 2 * (now - horizon), nil
}

// olderThanHorizon counts a growing table's rows before its horizon at now, and all its rows.
func olderThanHorizon(ctx context.Context, database *store.Store, table plugindecl.PluginTable,
	now int64) (int64, int64, error) {
	horizon, bounded, err := plugintables.RetentionHorizon(ctx, database.SQL, now, table.RetentionSeconds)
	if err != nil {
		return 0, 0, err
	}
	var older, total int64
	if err := database.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table.Quoted()).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("counting %s: %w", table.SQLName(), err)
	}
	if !bounded {
		return 0, total, nil
	}
	bound := "?"
	if column, found := columnOf(table, table.TimeColumn); found && column.Type == "instant" {
		bound = "to_timestamp(?)"
	}
	if err := database.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table.Quoted()+" WHERE "+
		plugindecl.QuoteIdentifier(table.TimeColumn)+" < "+bound, horizon).Scan(&older); err != nil {
		return 0, 0, fmt.Errorf("counting %s: %w", table.SQLName(), err)
	}
	return older, total, nil
}

// columnOf is a declared column of a table, by name.
func columnOf(table plugindecl.PluginTable, name string) (plugindecl.PluginColumn, bool) {
	for _, column := range table.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return plugindecl.PluginColumn{}, false
}

// writeStatements writes the default query and the earliest-row statement of every plugin dataset,
// each under its marker, compiled as the data catalogue compiles them over the day before now.
func writeStatements(path string, declarations []dataset.Declaration, now int64) error {
	var text strings.Builder
	text.WriteString("-- The statements the data catalogue runs on the plugins' datasets, written by ci/plugintables.\n")
	sort.Slice(declarations, func(i, j int) bool { return declarations[i].Name < declarations[j].Name })
	for _, declaration := range declarations {
		query, refusal := dataset.Parse("", declaration)
		if refusal != nil {
			return fmt.Errorf("the default query of %s is refused: %s", declaration.Name, refusal.Code)
		}
		compiled := dataset.Compile(declaration, query, dataset.Window{From: now - 86400, To: now})
		fmt.Fprintf(&text, "\n-- statement: plugin_dataset_%s_default\n%s;\n", statementName(declaration.Name),
			compiled.Text)
		if earliest := dataset.EarliestStatement(declaration); earliest != "" {
			fmt.Fprintf(&text, "\n-- statement: plugin_dataset_%s_earliest\n%s;\n", statementName(declaration.Name),
				earliest)
		}
	}
	return os.WriteFile(path, []byte(text.String()), 0o644)
}

// statementName is a dataset's name as a statement name: its dots as underscores.
func statementName(name string) string { return strings.ReplaceAll(name, ".", "_") }

// Rows synthesises count rows of a plugin table from its declared columns: row n's values are
// derived from n and from each column's type, and a time column's instants are spread evenly over
// the span before now. Distinct rows have distinct primary keys, whatever the key.
func Rows(table plugindecl.PluginTable, count int, now, span int64) []map[string]any {
	rows := make([]map[string]any, 0, count)
	for n := 0; n < count; n++ {
		row := map[string]any{}
		instant := now - int64(n)*60
		if span > 0 {
			instant = now - (int64(n)*span)/int64(count)
		}
		for _, column := range table.Columns {
			row[column.Name] = value(column, column.Name == table.TimeColumn, n, instant)
		}
		rows = append(rows, row)
	}
	return rows
}

// value is row n's value of one column.
func value(column plugindecl.PluginColumn, timeColumn bool, n int, instant int64) any {
	switch column.Type {
	case "integer":
		if timeColumn {
			return instant
		}
		return int64(n)
	case "real":
		return float64(n) + 0.5
	case "boolean":
		return n%2 == 0
	case "ip":
		// Both families, alternately, every byte mixed from the counter: no prefix, so no
		// addressing plan, is written here.
		var raw [16]byte
		mixed := uint64(n)*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019
		for index := range raw {
			raw[index] = byte(mixed >> (8 * (index % 8)))
			mixed = mixed*0x5851f42d4c957f2d + 1
		}
		if n%2 == 0 {
			return netip.AddrFrom4([4]byte{raw[0], raw[1], raw[2], raw[3]})
		}
		return netip.AddrFrom16(raw)
	case "mac":
		return fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	case "instant":
		return time.Unix(instant, 0).UTC()
	default:
		return fmt.Sprintf("%s-%d", column.Name, n)
	}
}
