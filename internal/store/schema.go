package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Applying the schema, splitting SQL files, and the retention purge's file.

// applySchema applies schema.sql. It is idempotent, so this runs on every start.
//
// THE WHOLE FILE IS ONE TRANSACTION: PostgreSQL's DDL is transactional, so a start interrupted
// half-way through leaves the schema it found, never half of the new one. The transaction takes a
// transaction-scoped advisory lock first, so two starts against one database -- which the
// instance lock already refuses -- could not interleave their applies either.
func (s *Store) applySchema(ctx context.Context) error {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("store: reading the embedded schema: %w", err)
	}
	// A database that already holds the core's tables was written by some build. If the schema
	// then fails to apply BECAUSE A TABLE OR COLUMN IT NAMES IS MISSING, the cause is that
	// build's schema, which the in-place file does not upgrade, and the error says so. Any other
	// failure -- a refused connection, a cancelled start, a full disk -- is not an outdated
	// schema, and telling the operator to rebuild for it would throw history away for nothing.
	var existingTables int
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT count(*) FROM pg_tables WHERE schemaname = ?", sqldb.CoreSchema).Scan(&existingTables); err != nil {
		return fmt.Errorf("store: reading the existing schema: %w", err)
	}
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range sqldb.SplitStatements(string(schema)) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if existingTables > 0 && isMissingSchemaObject(err) {
				return fmt.Errorf("%w: %w\nstatement: %s", ErrSchemaOutdated, err, sqldb.FirstLine(statement))
			}
			return fmt.Errorf("store: applying the schema: %w\nstatement: %s", err, sqldb.FirstLine(statement))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing the schema: %w", err)
	}
	return nil
}

// isMissingSchemaObject reports whether a schema statement failed because a table or column it
// names does not exist -- the one failure an earlier build's schema causes -- read from
// PostgreSQL's error code, never from its message.
func isMissingSchemaObject(err error) bool {
	var failure *pgconn.PgError
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "42P01", "42703": // undefined_table, undefined_column
		return true
	}
	return false
}
