package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestTheLexerCountsStatementsOnTokensNotSubstrings: a semicolon inside a string literal, a
// quoted identifier or a comment is not a terminator, and the first word is the first token.
func TestTheLexerCountsStatementsOnTokensNotSubstrings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, text, first string
		statements        int
		parameters        []string
	}{
		{"plain", "SELECT 1", "SELECT", 1, nil},
		{"terminated", "SELECT 1;", "SELECT", 1, nil},
		{"two", "SELECT 1; SELECT 2", "SELECT", 2, nil},
		{"string", "SELECT 'a;b; DELETE FROM x'", "SELECT", 1, nil},
		{"escaped quote", "SELECT 'it''s; here'", "SELECT", 1, nil},
		{"double quoted", `SELECT "a;b" FROM t`, "SELECT", 1, nil},
		{"backticks", "SELECT `a;b` FROM t", "SELECT", 1, nil},
		{"brackets", "SELECT [a;b] FROM t", "SELECT", 1, nil},
		{"line comment", "-- first; second\nSELECT 1 -- ; third", "SELECT", 1, nil},
		{"block comment", "/* ; DELETE */ SELECT 1 /* ; */", "SELECT", 1, nil},
		{"comment after semicolon", "SELECT 1; -- done", "SELECT", 1, nil},
		{"hidden second", "SELECT 1; /* x */ DELETE FROM t", "SELECT", 2, nil},
		{"parameters", "SELECT :b, :a, :a, ':in_string' FROM t WHERE x = :c", "SELECT", 1, []string{"a", "b", "c"}},
		{"empty", "  -- nothing\n /* at all */ ", "", 0, nil},
		{"leading delete", "DELETE FROM t", "DELETE", 1, nil},
	}
	for _, c := range cases {
		got := sqldb.LexSQL(c.text)
		if got.Statements != c.statements || got.FirstWord != c.first ||
			strings.Join(got.Parameters, ",") != strings.Join(c.parameters, ",") {
			t.Errorf("%s: statements %d first %q parameters %v, want %d %q %v", c.name, got.Statements,
				got.FirstWord, got.Parameters, c.statements, c.first, c.parameters)
		}
	}
}

// TestCheckQueryRefusesWhatIsNotOneReadOnlyStatementUnderItsOwnKind covers the three layers:
// the lexer (statement count, first word, a writing keyword behind a WITH), and the planner
// itself (a text that does not plan).
func TestCheckQueryRefusesWhatIsNotOneReadOnlyStatementUnderItsOwnKind(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	allowed := []string{"now", "from"}
	cases := []struct {
		name, text, kind string
	}{
		{"empty", "-- nothing", sqldb.QueryEmpty},
		{"two statements", "SELECT 1 AS value; SELECT 2 AS value", sqldb.QueryMultipleStatements},
		{"hidden statement", "SELECT 1 AS value; /* x */ DELETE FROM setting", sqldb.QueryMultipleStatements},
		{"delete", "DELETE FROM setting", sqldb.QueryNotReadOnly},
		{"insert", "INSERT INTO setting (key, value, updated_at) VALUES ('a', 'b', 1)", sqldb.QueryNotReadOnly},
		{"pragma", "PRAGMA query_only = 0", sqldb.QueryNotReadOnly},
		{"attach", "ATTACH DATABASE ':memory:' AS other", sqldb.QueryNotReadOnly},
		{"with insert", "WITH s(a) AS (SELECT 1) INSERT INTO setting (key, value, updated_at) SELECT 'a', 'b', a FROM s",
			sqldb.QueryNotReadOnly},
		{"with delete", "WITH s(k) AS (SELECT key FROM setting) DELETE FROM setting WHERE key IN s", sqldb.QueryNotReadOnly},
		{"unbound parameter", "SELECT :other AS value", sqldb.QueryUnboundParameter},
		{"positional parameter", "SELECT ? AS value", sqldb.QueryUnboundParameter},
		{"does not plan", "SELECT no_such_column AS value FROM setting", sqldb.QueryDoesNotPlan},
		{"syntax", "SELECT FROM WHERE", sqldb.QueryDoesNotPlan},
	}
	for _, c := range cases {
		_, err := database.CheckQuery(ctx, c.text, allowed)
		var problem *sqldb.QueryProblem
		if !errors.As(err, &problem) {
			t.Errorf("%s: want a %s refusal, got %v", c.name, c.kind, err)
			continue
		}
		if problem.Kind != c.kind {
			t.Errorf("%s: refused as %s (%s), want %s", c.name, problem.Kind, problem.Detail, c.kind)
		}
	}
	var count int64
	check := func() {
		if err := database.DB().QueryRowContext(ctx, `SELECT count(*) FROM setting`).Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if count == 0 {
		t.Fatal("the settings table is empty; the refusals above could have hidden a delete")
	}
}

// TestCheckQueryAcceptsAReadAndReportsItsColumnsAndParameters, including a CTE, a semicolon in a
// string and a trailing comment after the terminator.
func TestCheckQueryAcceptsAReadAndReportsItsColumnsAndParameters(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	info, err := database.CheckQuery(context.Background(), `
		WITH recent AS (SELECT key, value FROM setting WHERE updated_at <= :now)
		SELECT key AS name, 'a;b' AS note, length(value) AS size
		FROM recent
		ORDER BY name; -- done`, []string{"now"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(info.Columns, ",") != "name,note,size" || strings.Join(info.Parameters, ",") != "now" {
		t.Errorf("columns %v parameters %v", info.Columns, info.Parameters)
	}
}

// TestTheReadConnectionsRefuseAWriteWhateverTheCheckDid is the last line of defence: a text that
// reached the registry WITHOUT the check -- registered directly -- still cannot write, because the
// connections it runs on log in as the read role, in read-only transactions
// (internal/store/sqldb/readers.go), and PostgreSQL itself refuses.
func TestTheReadConnectionsRefuseAWriteWhateverTheCheckDid(t *testing.T) {
	// Not parallel: it registers a statement in the process-wide registry, which the tests that
	// enumerate the statements must not meet, and it withdraws it before the parallel tests run.
	t.Cleanup(func() { sqldb.UnregisterOwner("test") })
	database, _ := openTestStore(t)
	ctx := context.Background()
	const name = "widget_x_unchecked_delete"
	if err := sqldb.RegisterQuery("test", name, "DELETE FROM setting"); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := database.DB().QueryRowContext(ctx, `SELECT count(*) FROM setting`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	err := database.WidgetRows(ctx, name, vocab.Period{}, nil, func(reads.RowScanner) error { return nil })
	if err == nil {
		t.Fatal("a DELETE ran on the read connections")
	}
	if err := database.DB().QueryRowContext(ctx, `SELECT count(*) FROM setting`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == 0 || before != after {
		t.Errorf("the settings table went from %d to %d rows", before, after)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") &&
		!strings.Contains(strings.ToLower(err.Error()), "read-only") &&
		!strings.Contains(strings.ToLower(err.Error()), "read only") {
		t.Logf("the refusal text is %q", err)
	}
}

// TestRegisterQueryRefusesAnAlreadyRegisteredName and withdraws an owner's statements.
func TestRegisterQueryRefusesAnAlreadyRegisteredNameAndUnregistersAnOwner(t *testing.T) {
	t.Cleanup(func() { sqldb.UnregisterOwner("first"); sqldb.UnregisterOwner("second") })
	const name = "widget_x_twice"
	if err := sqldb.RegisterQuery("first", name, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := sqldb.RegisterQuery("second", name, "SELECT 2"); err == nil {
		t.Error("a name was registered twice")
	}
	if got, _ := sqldb.Statement(name); got != "SELECT 1" {
		t.Errorf("the first registration was replaced: %q", got)
	}
	sqldb.UnregisterOwner("first")
	if _, err := sqldb.Statement(name); err == nil {
		t.Error("the owner's statement survived its withdrawal")
	}
}
