package reads

import (
	"database/sql"
	"strconv"
	"strings"
)

// The cells of a widget's, a plugin's or the data catalogue's statement, as the plugin host hands
// them on: an int64, a float64, a string, a bool or nil. The connection converts nothing
// (internal/store/sqldb, driver.go); this is the one conversion the host makes, and only for the
// statements a plugin or a widget authors: a numeric whose value is whole -- a sum of integers is
// one -- is an integer, and any other numeric a double, so an author never learns PostgreSQL's
// numeric typing (docs/plugins.md, "Statement results"). A core statement casts what it reads.

// cellConverter converts the cells of one statement's rows.
type cellConverter struct {
	numeric []bool
}

// newCellConverter reads which columns of a statement's rows are numeric.
func newCellConverter(rows *sql.Rows) (cellConverter, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return cellConverter{}, err
	}
	converter := cellConverter{numeric: make([]bool, len(types))}
	for index, columnType := range types {
		converter.numeric[index] = columnType.DatabaseTypeName() == "NUMERIC"
	}
	return converter, nil
}

// convert converts one row's cells in place.
func (c cellConverter) convert(cells []any) {
	for index, cell := range cells {
		switch value := cell.(type) {
		case []byte:
			cells[index] = string(value)
			if index < len(c.numeric) && c.numeric[index] {
				cells[index] = numericValue(string(value))
			}
		case string:
			if index < len(c.numeric) && c.numeric[index] {
				cells[index] = numericValue(value)
			}
		}
	}
}

// numericValue reads a numeric's text as an integer when it has no fractional part, and as a
// double otherwise.
func numericValue(text string) any {
	if !strings.ContainsAny(text, ".eE") {
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			return integer
		}
	}
	if real, err := strconv.ParseFloat(text, 64); err == nil {
		return real
	}
	return text
}

// ScanCells scans one row of a widget-authored statement into its cells, converted as the host
// converts every such statement's cells.
func ScanCells(row RowScanner, count int) ([]any, error) {
	cells := make([]any, count)
	pointers := make([]any, count)
	for index := range cells {
		pointers[index] = &cells[index]
	}
	if err := row.Scan(pointers...); err != nil {
		return nil, err
	}
	converter := cellConverter{}
	if rows, isRows := row.(*sql.Rows); isRows {
		converted, err := newCellConverter(rows)
		if err != nil {
			return nil, err
		}
		converter = converted
	}
	converter.convert(cells)
	return cells, nil
}
