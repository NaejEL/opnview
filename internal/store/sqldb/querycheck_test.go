package sqldb

import (
	"strings"
	"testing"
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
		got := LexSQL(c.text)
		if got.statements != c.statements || got.firstWord != c.first ||
			strings.Join(got.Parameters, ",") != strings.Join(c.parameters, ",") {
			t.Errorf("%s: statements %d first %q parameters %v, want %d %q %v", c.name, got.statements,
				got.firstWord, got.Parameters, c.statements, c.first, c.parameters)
		}
	}
}
