package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// querier is what both *sql.DB and *sql.Tx offer. The derivations run inside one
// transaction each, and the database holds one connection, so a helper that went
// back to s.db in the middle of a transaction would wait for itself for ever;
// every helper the derivations share therefore takes the querier it runs on.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// named turns a parameter map into the named arguments database/sql binds,
// keeping only those the statement names: an argument a statement does not use
// is refused by the driver, and one parameter map serves several statements.
func named(text string, parameters map[string]any) []any {
	arguments := make([]any, 0, len(parameters))
	for name, value := range parameters {
		if !usesParameter(text, name) {
			continue
		}
		arguments = append(arguments, sql.Named(name, value))
	}
	return arguments
}

// execNamed runs one named statement and returns how many rows it changed.
func execNamed(ctx context.Context, q querier, name string, parameters map[string]any) (int64, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, err
	}
	return execText(ctx, q, name, text, parameters)
}

// execText runs a statement text and returns how many rows it changed.
func execText(ctx context.Context, q querier, name, text string, parameters map[string]any) (int64, error) {
	result, err := q.ExecContext(ctx, text, named(text, parameters)...)
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
func queryInts(ctx context.Context, q querier, name string, parameters map[string]any) ([]int64, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text, named(text, parameters)...)
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
func queryOptionalInt(ctx context.Context, q querier, name string, parameters map[string]any) (
	int64, bool, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, false, err
	}
	return queryOptionalIntText(ctx, q, name, text, parameters)
}

// queryOptionalIntText is queryOptionalInt over a statement text already
// expanded.
func queryOptionalIntText(ctx context.Context, q querier, name, text string,
	parameters map[string]any) (int64, bool, error) {
	var value sql.NullInt64
	err := q.QueryRowContext(ctx, text, named(text, parameters)...).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: %s: %w", name, err)
	}
	return value.Int64, value.Valid, nil
}

// optional renders a nullable id as a bindable value.
func optional(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// usesParameter reports whether a statement names :name, as a whole parameter
// rather than as the start of a longer one.
func usesParameter(text, name string) bool {
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
