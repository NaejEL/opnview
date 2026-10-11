package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// The widget API's access to the database (step 5B, specs/SPEC-step-5b-widget-api.md).
//
// THE STATEMENTS ARE HERE, THE SHAPES ARE THE API'S. Every statement a widget handler
// runs is a registered named statement (a widget.*statements.sql file of internal/widget or of a widget directory), so sql/schema-checks.sh plans it at both seed
// sizes like every other statement; the handler names one, binds its parameters and
// scans its rows into the shape its preset draws. Nothing reaches this function but a
// widget_* statement, and nothing it runs can write: it runs on the read connections
// (readers.go), whose role PostgreSQL refuses to let write.

// RowScanner is one row of a widget statement.
type RowScanner interface {
	Scan(destinations ...any) error
}

// widgetStatementPrefix names the statements WidgetRows accepts.
const widgetStatementPrefix = "widget_"

// WidgetRows runs one widget statement with the parameters it names -- a parameter
// it does not name is not bound -- and hands each row to scan, in the statement's
// order. A period template takes its period from period; any other statement takes
// the zero Period.
func (s *Store) WidgetRows(ctx context.Context, name string, period Period,
	parameters map[string]any, scan func(RowScanner) error) error {
	if !strings.HasPrefix(name, widgetStatementPrefix) {
		return fmt.Errorf("store: %q is not a widget statement", name)
	}
	var (
		text string
		err  error
	)
	if period.Name != "" {
		text, err = PeriodStatement(name, period)
	} else {
		text, err = Statement(name)
	}
	if err != nil {
		return err
	}
	rows, err := s.reader.QueryContext(ctx, text, named(text, parameters)...)
	if err != nil {
		return fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("store: %s: %w", name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: %s: %w", name, err)
	}
	return nil
}

// WidgetQuery runs one widget statement exactly as WidgetRows does and returns its result columns
// and its rows, each cell an int64, a float64, a string or nil (the connection's own conversions,
// driver.go): what a widget script's host hands the script (internal/widget, host_query.go).
func (s *Store) WidgetQuery(ctx context.Context, name string, period Period, parameters map[string]any) (
	[]string, [][]any, error) {
	var (
		columns []string
		result  [][]any
	)
	err := s.WidgetRows(ctx, name, period, parameters, func(scanned RowScanner) error {
		rows, isRows := scanned.(*sql.Rows)
		if !isRows {
			return fmt.Errorf("store: %s: the rows cannot name their columns", name)
		}
		if columns == nil {
			names, err := rows.Columns()
			if err != nil {
				return err
			}
			columns = names
		}
		cells := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range cells {
			pointers[index] = &cells[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		for index, cell := range cells {
			if raw, isBytes := cell.([]byte); isBytes {
				cells[index] = string(raw)
			}
		}
		result = append(result, cells)
		return nil
	})
	return columns, result, err
}

// WidgetRow runs a widget statement expected to return at most one row and scans it
// into destinations. It reports whether a row came back.
func (s *Store) WidgetRow(ctx context.Context, name string, parameters map[string]any,
	destinations ...any) (bool, error) {
	found := false
	err := s.WidgetRows(ctx, name, Period{}, parameters, func(row RowScanner) error {
		if found {
			return nil
		}
		found = true
		return row.Scan(destinations...)
	})
	return found, err
}

// ReadStatementCount reports how many distinct statement texts the read connections have run
// since the store opened. A text is rewritten to PostgreSQL's numbering once and nothing is
// prepared on the server (driver.go), so a second call of a read function adds nothing to it; a
// test observes that (AC31 of 5B, restated by P-AC13).
func (s *Store) ReadStatementCount() int {
	if s.reader == nil {
		return 0
	}
	return s.reader.runCount()
}

// ReadStatementsOutsideTheFiles returns the text of every statement the read connections
// have run that is not a registered named statement, nor one of
// their period expansions. sql/schema-checks.sh plans every named statement at both seed
// sizes and nothing else, so a text returned here is one the API runs unplanned (AC17); a
// test serves every widget type and asserts that the list is empty.
func (s *Store) ReadStatementsOutsideTheFiles() ([]string, error) {
	if s.reader == nil {
		return nil, nil
	}
	names, err := StatementNames()
	if err != nil {
		return nil, err
	}
	planned := map[string]bool{}
	for _, name := range names {
		text, err := Statement(name)
		if err != nil {
			return nil, err
		}
		planned[text] = true
		if IsPeriodTemplate(name) {
			for _, period := range Periods() {
				expanded, err := PeriodStatement(name, period)
				if err != nil {
					return nil, err
				}
				planned[expanded] = true
			}
		}
	}
	var outside []string
	for _, text := range s.reader.runTexts() {
		if !planned[text] {
			outside = append(outside, text)
		}
	}
	return outside, nil
}

// NullInt64 and NullString re-export the nullable scan targets, so a widget handler scans
// a NULL as absent rather than as a zero without importing database/sql for it.
type (
	NullInt64   = sql.NullInt64
	NullString  = sql.NullString
	NullFloat64 = sql.NullFloat64
)
