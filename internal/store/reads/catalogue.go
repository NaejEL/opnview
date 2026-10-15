package reads

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
)

// The data catalogue's statements (P8 of specs/SPEC-backend-completion-plugins.md). Each is
// compiled by internal/dataset from a dataset's declaration -- every identifier the declaration's,
// every value bound -- and runs here, on the read connections, in a read-only transaction under the
// catalogue's own statement timeout.

// catalogueStatements counts the catalogue statements run since the process started, so a test can
// show that a refused query reached no database.
var catalogueStatements atomic.Int64

// CatalogueStatementCount reports how many catalogue statements have been run.
func CatalogueStatementCount() int64 { return catalogueStatements.Load() }

// CatalogueRows runs one catalogue statement and hands each row's cells -- an int64, a float64, a
// string or nil each -- to scan.
func (s *ReadStore) CatalogueRows(ctx context.Context, text string, args []any, timeoutMilliseconds int,
	scan func(cells []any) error) error {
	if s.Reader == nil {
		return fmt.Errorf("store: no read connections")
	}
	catalogueStatements.Add(1)
	s.Reader.Note(text)
	transaction, err := s.Reader.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("store: a catalogue read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d",
		timeoutMilliseconds)); err != nil {
		return fmt.Errorf("store: bounding a catalogue read: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, text, args...)
	if err != nil {
		return fmt.Errorf("store: a catalogue read: %w", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		cells := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range cells {
			pointers[index] = &cells[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("store: a catalogue read: %w", err)
		}
		for index, cell := range cells {
			if bytes, isBytes := cell.([]byte); isBytes {
				cells[index] = string(bytes)
			}
		}
		if err := scan(cells); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: a catalogue read: %w", err)
	}
	return transaction.Commit()
}
