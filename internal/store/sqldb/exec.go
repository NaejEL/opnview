package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// querier is what both *sql.DB and *sql.Tx offer. The derivations run inside one transaction
// each, and a helper that went back to s.db in the middle of one would read outside it, so every
// helper the derivations share takes the querier it runs on.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// named turns a parameter map into the named arguments database/sql binds,
// keeping only those the statement names: an argument a statement does not use
// is refused by the driver, and one parameter map serves several statements.
func Named(text string, parameters map[string]any) []any {
	arguments := make([]any, 0, len(parameters))
	for name, value := range parameters {
		if !UsesParameter(text, name) {
			continue
		}
		arguments = append(arguments, sql.Named(name, value))
	}
	return arguments
}

// execNamed runs one named statement and returns how many rows it changed.
func ExecNamed(ctx context.Context, q Querier, name string, parameters map[string]any) (int64, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, err
	}
	return ExecText(ctx, q, name, text, parameters)
}

// execText runs a statement text and returns how many rows it changed.
func ExecText(ctx context.Context, q Querier, name, text string, parameters map[string]any) (int64, error) {
	result, err := q.ExecContext(ctx, text, Named(text, parameters)...)
	if err != nil {
		return 0, fmt.Errorf("store: %s: %w", name, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %s: %w", name, err)
	}
	return affected, nil
}

// queryInts runs a named statement whose rows are one integer column and returns
// them; a NULL is skipped.
func QueryInts(ctx context.Context, q Querier, name string, parameters map[string]any) ([]int64, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text, Named(text, parameters)...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var values []int64
	for rows.Next() {
		var value sql.NullInt64
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		if value.Valid {
			values = append(values, value.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return values, nil
}

// queryOptionalInt runs a named statement returning at most one integer, and
// reports whether it returned a non-NULL one.
func QueryOptionalInt(ctx context.Context, q Querier, name string, parameters map[string]any) (
	int64, bool, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, false, err
	}
	return QueryOptionalIntText(ctx, q, name, text, parameters)
}

// queryOptionalIntText is queryOptionalInt over a statement text already
// expanded.
func QueryOptionalIntText(ctx context.Context, q Querier, name, text string,
	parameters map[string]any) (int64, bool, error) {
	var value sql.NullInt64
	err := q.QueryRowContext(ctx, text, Named(text, parameters)...).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: %s: %w", name, err)
	}
	return value.Int64, value.Valid, nil
}

// optional renders a nullable id as a bindable value.
func Optional(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// usesParameter reports whether a statement names :name, as a whole parameter
// rather than as the start of a longer one.
func UsesParameter(text, name string) bool {
	token := ":" + name
	for offset := 0; ; {
		index := strings.Index(text[offset:], token)
		if index < 0 {
			return false
		}
		after := offset + index + len(token)
		if after >= len(text) || !isParameterCharacter(text[after]) {
			return true
		}
		offset = after
	}
}

// isParameterCharacter reports whether a byte can continue a parameter name.
func isParameterCharacter(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')
}

// IsConstraintViolation reports whether an error is PostgreSQL's refusal of a row by a
// constraint: SQLSTATE class 23 (integrity constraint violation), whichever constraint it was.
func IsConstraintViolation(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && strings.HasPrefix(failure.Code, "23")
}

// IsUniqueViolation reports whether an error is a unique constraint's refusal (23505).
func IsUniqueViolation(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && failure.Code == "23505"
}

// nullableInt turns a nullable integer into a pointer.
func NullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copied := value.Int64
	return &copied
}

// nullableText is NULL for an empty string.
func NullableText(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func NullInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

// optionalText renders a nullable text as a bindable value.
func OptionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
