package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Applying the schema, splitting SQL files, and the retention purge's file.

// applySchema applies schema.sql, unless the database records this file's digest already. The
// file is idempotent, so an apply on a database that holds it changes nothing; the digest skips that
// apply, which is most of what an Open would otherwise spend (a restart of the same build, every test
// database cloned from the template). A database another build wrote records another digest, or none,
// and is applied as before. It returns whether the file was applied.
//
// THE WHOLE FILE IS ONE TRANSACTION: PostgreSQL's DDL is transactional, so a start interrupted
// half-way through leaves the schema it found, never half of the new one. The transaction takes a
// transaction-scoped advisory lock first, so two starts against one database -- which the
// instance lock already refuses -- could not interleave their applies either.
func (s *Store) applySchema(ctx context.Context) (bool, error) {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		return false, fmt.Errorf("store: reading the embedded schema: %w", err)
	}
	sum := sha256.Sum256(schema)
	digest := hex.EncodeToString(sum[:])
	recorded, err := s.appliedDigest(ctx)
	if err != nil {
		return false, err
	}
	if recorded == digest {
		return false, nil
	}
	// A database that already holds the core's tables was written by some build. If the schema
	// then fails to apply BECAUSE A TABLE OR COLUMN IT NAMES IS MISSING, the cause is that
	// build's schema, which the in-place file does not upgrade, and the error says so. Any other
	// failure -- a refused connection, a cancelled start, a full disk -- is not an outdated
	// schema, and telling the operator to rebuild for it would throw history away for nothing.
	var existingTables int
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT count(*) FROM pg_tables WHERE schemaname = $1", sqldb.CoreSchema).Scan(&existingTables); err != nil {
		return false, fmt.Errorf("store: reading the existing schema: %w", err)
	}
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: beginning the schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range sqldb.SplitStatements(string(schema)) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if existingTables > 0 && isMissingSchemaObject(err) {
				return false, fmt.Errorf("%w: %w\nstatement: %s", ErrSchemaOutdated, err, sqldb.FirstLine(statement))
			}
			return false, fmt.Errorf("store: applying the schema: %w\nstatement: %s", err, sqldb.FirstLine(statement))
		}
	}
	// The digest is recorded in the apply's own transaction: a start interrupted before the commit
	// records nothing, and the next start applies again.
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+sqldb.CoreSchema+".applied_schema (singleton, digest, applied_at) "+
		"VALUES (TRUE, $1, $2) ON CONFLICT (singleton) DO UPDATE SET digest = EXCLUDED.digest, "+
		"applied_at = EXCLUDED.applied_at", digest, time.Now().Unix()); err != nil {
		return false, fmt.Errorf("store: recording the applied schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: committing the schema: %w", err)
	}
	return true, nil
}

// appliedDigest is the digest of the schema file last applied to the database, or "" for a database
// that records none: an empty one, or one an earlier build wrote.
func (s *Store) appliedDigest(ctx context.Context) (string, error) {
	var table *string
	if err := s.SQL.QueryRowContext(ctx, "SELECT CAST(to_regclass($1) AS TEXT)",
		sqldb.CoreSchema+".applied_schema").Scan(&table); err != nil {
		return "", fmt.Errorf("store: looking for the applied schema: %w", err)
	}
	if table == nil {
		return "", nil
	}
	var digest string
	err := s.SQL.QueryRowContext(ctx, "SELECT digest FROM "+sqldb.CoreSchema+".applied_schema").Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: reading the applied schema: %w", err)
	}
	return digest, nil
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
