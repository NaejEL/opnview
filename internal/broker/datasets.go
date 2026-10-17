package broker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/dataset"
)

// The mirror feeds the data catalogue (A15 of the broker amendment): every mirror table and stream
// recording is a dataset of origin `opnsense`, its roles inferred from its types -- a timestamp is
// the time column, a number a measure, everything else a dimension. A snapshot and a stream
// recording are sample datasets; a record stream is an event dataset. A column carrying a domain
// name is withheld under no_domains by the catalogue's own rule.

// LabelMirrorDataset is the catalogue key every learned dataset is labelled with.
const LabelMirrorDataset = "dataset.opnsense_mirror"

// errNoColumn is a mirror table nothing stored a field of: the root of an answer whose fields all
// sit in its rows. It is no dataset.
var errNoColumn = errors.New("no field of it is stored")

// datasetColumnName turns a field path into a dataset column name: lower-case letters, digits
// and underscores, beginning with a letter.
func datasetColumnName(path string, taken map[string]bool) string {
	var out strings.Builder
	underscore := false
	for _, character := range strings.ToLower(path) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			out.WriteRune(character)
			underscore = false
		default:
			if !underscore && out.Len() > 0 {
				out.WriteByte('_')
				underscore = true
			}
		}
	}
	name := strings.Trim(out.String(), "_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "f_" + name
	}
	base, index := name, 2
	for taken[name] {
		name = fmt.Sprintf("%s_%d", base, index)
		index++
	}
	taken[name] = true
	return name
}

// Datasets lists the mirror's tables and stream recordings as datasets of the data catalogue.
func (b *Broker) Datasets(ctx context.Context) []dataset.Declaration {
	if b.mirror == nil {
		return nil
	}
	rows, err := b.mirror.Query(ctx, `SELECT t.name, t.row_set,
		coalesce((SELECT d.nature FROM opnsense.mirror_datum AS d WHERE d.endpoint_id = t.endpoint_id
			AND d.row_set = t.row_set AND d.nature IS NOT NULL ORDER BY d.id LIMIT 1), 'snapshot'),
		coalesce((SELECT d.cursor_type FROM opnsense.mirror_datum AS d WHERE d.endpoint_id = t.endpoint_id
			AND d.row_set = t.row_set ORDER BY d.id LIMIT 1), '')
		FROM opnsense.mirror_table AS t ORDER BY t.name`)
	if err != nil {
		b.options.Log(fmt.Sprintf("listing the mirror's datasets: %v", err))
		return nil
	}
	type table struct{ name, rowSet, nature, cursorType string }
	var tables []table
	for rows.Next() {
		var item table
		if err := rows.Scan(&item.name, &item.rowSet, &item.nature, &item.cursorType); err != nil {
			_ = rows.Close()
			return nil
		}
		tables = append(tables, item)
	}
	_ = rows.Close()
	var declarations []dataset.Declaration
	for _, item := range tables {
		declaration, err := b.tableDataset(ctx, item.name, item.nature, item.cursorType)
		if errors.Is(err, errNoColumn) {
			continue
		}
		if err != nil {
			b.options.Log(fmt.Sprintf("the mirror table %s is no dataset: %v", item.name, err))
			continue
		}
		declarations = append(declarations, declaration)
	}
	declarations = append(declarations, b.streamDatasets(ctx)...)
	return declarations
}

// tableDataset is one mirror table's dataset.
func (b *Broker) tableDataset(ctx context.Context, table, nature, cursorType string) (dataset.Declaration, error) {
	rows, err := b.mirror.Query(ctx, `SELECT path, column_name, type, carries_domain FROM opnsense.mirror_column
		WHERE table_name = $1 ORDER BY path`, table)
	if err != nil {
		return dataset.Declaration{}, err
	}
	timeColumn, timeExpression := "seen_at", `d."#last_seen"`
	if nature == infer.NatureRecordStream && cursorType == infer.TypeTimestamp {
		timeColumn, timeExpression = "cursor_at", `CAST(d."#cursor" AS BIGINT)`
	}
	taken := map[string]bool{timeColumn: true}
	columns := []dataset.Column{{Name: timeColumn, Type: dataset.TypeInteger, Role: dataset.RoleTime}}
	selected := []string{timeExpression + " AS " + quoteName(timeColumn)}
	for rows.Next() {
		var path, column, kind string
		var domains bool
		if err := rows.Scan(&path, &column, &kind, &domains); err != nil {
			_ = rows.Close()
			return dataset.Declaration{}, err
		}
		name := datasetColumnName(path, taken)
		switch kind {
		case infer.TypeJSON:
			continue
		case infer.TypeNumber:
			columns = append(columns, dataset.Column{Name: name, Type: dataset.TypeReal, Role: dataset.RoleMeasure,
				Nullable: true})
			selected = append(selected, "CAST(d."+quoteName(column)+" AS DOUBLE PRECISION) AS "+quoteName(name))
		default:
			columns = append(columns, dataset.Column{Name: name, Type: dataset.TypeText, Nullable: true,
				CarriesDomain: domains})
			selected = append(selected, "CAST(d."+quoteName(column)+" AS TEXT) AS "+quoteName(name))
		}
	}
	_ = rows.Close()
	if len(columns) == 1 {
		return dataset.Declaration{}, errNoColumn
	}
	sort.SliceStable(columns[1:], func(i, j int) bool { return columns[1+i].Name < columns[1+j].Name })
	qualified := quotedTable(table)
	relation := "SELECT " + strings.Join(selected, ", ") + " FROM " + qualified + " AS d WHERE " + timeExpression +
		" >= :from AND " + timeExpression + " < :to"
	earliest := "SELECT min(" + timeExpression + ") FROM " + qualified + " AS d"
	datasetNature := dataset.NatureSample
	if nature == infer.NatureRecordStream {
		datasetNature = dataset.NatureEvent
	}
	return dataset.Learned(dataset.Declaration{Name: dataset.OriginOpnsense + "." + table, Origin: dataset.OriginOpnsense,
		Label: LabelMirrorDataset, Nature: datasetNature, Freshness: dataset.Freshness{Kind: dataset.OriginOpnsense},
		Retention: &dataset.Retention{Column: timeColumn}, Columns: columns}, relation, earliest)
}

// streamDatasets lists the stream recordings as sample datasets: a minute with no event received
// -- the stream was cut, or silent -- has no row, which the catalogue answers as a gap, never a
// zero.
func (b *Broker) streamDatasets(ctx context.Context) []dataset.Declaration {
	rows, err := b.mirror.Query(ctx, `SELECT c.table_name, c.column_name, c.data_type FROM information_schema.columns AS c
		WHERE c.table_schema = 'opnsense' AND right(c.table_name, 8) = '__events' AND left(c.column_name, 1) <> '#'
		ORDER BY c.table_name, c.column_name`)
	if err != nil {
		b.options.Log(fmt.Sprintf("listing the stream recordings: %v", err))
		return nil
	}
	type column struct{ name, kind string }
	tables := map[string][]column{}
	var order []string
	for rows.Next() {
		var table, name, kind string
		if err := rows.Scan(&table, &name, &kind); err != nil {
			_ = rows.Close()
			return nil
		}
		if _, seen := tables[table]; !seen {
			order = append(order, table)
		}
		tables[table] = append(tables[table], column{name: name, kind: kind})
	}
	_ = rows.Close()
	var declarations []dataset.Declaration
	for _, table := range order {
		const timeColumn = "received_at"
		taken := map[string]bool{timeColumn: true}
		columns := []dataset.Column{{Name: timeColumn, Type: dataset.TypeInteger, Role: dataset.RoleTime}}
		selected := []string{`d."#received_at" AS ` + quoteName(timeColumn)}
		for _, field := range tables[table] {
			name := datasetColumnName(field.name, taken)
			if field.kind == "numeric" {
				columns = append(columns, dataset.Column{Name: name, Type: dataset.TypeReal, Role: dataset.RoleMeasure,
					Nullable: true})
				selected = append(selected, "CAST(d."+quoteName(field.name)+" AS DOUBLE PRECISION) AS "+quoteName(name))
				continue
			}
			columns = append(columns, dataset.Column{Name: name, Type: dataset.TypeText, Nullable: true})
			selected = append(selected, "CAST(d."+quoteName(field.name)+" AS TEXT) AS "+quoteName(name))
		}
		qualified := quotedTable(table)
		relation := "SELECT " + strings.Join(selected, ", ") + " FROM " + qualified + ` AS d WHERE d."#received_at" >= :from` +
			` AND d."#received_at" < :to`
		earliest := `SELECT min(d."#received_at") FROM ` + qualified + " AS d"
		declaration, err := dataset.Learned(dataset.Declaration{Name: dataset.OriginOpnsense + "." + table,
			Origin: dataset.OriginOpnsense, Label: LabelMirrorDataset, Nature: dataset.NatureSample,
			Freshness: dataset.Freshness{Kind: dataset.OriginOpnsense}, Retention: &dataset.Retention{Column: timeColumn},
			Columns: columns}, relation, earliest)
		if err != nil {
			b.options.Log(fmt.Sprintf("the stream recording %s is no dataset: %v", table, err))
			continue
		}
		declarations = append(declarations, declaration)
	}
	return declarations
}
