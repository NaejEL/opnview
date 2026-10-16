package sqldb_test

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestEveryStatementPreparesForEveryPeriod is the statement files' own check: every named
// statement, and every period of every template, is valid SQL against the schema.
func TestEveryStatementPreparesForEveryPeriod(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	ctx := context.Background()
	names := sortedStatementNames(t)
	if len(names) < 40 {
		t.Fatalf("only %d statements were parsed, so the parser is not reaching them", len(names))
	}
	for _, name := range names {
		texts := []string{}
		if sqldb.IsComposeTemplate(name) {
			composed := 0
			for _, period := range vocab.Periods() {
				if _, has := period.Child(); !has {
					continue
				}
				text, err := sqldb.ComposeStatement(name, period)
				if err != nil {
					t.Fatalf("expanding %s: %v", name, err)
				}
				texts = append(texts, text)
				composed++
			}
			if composed != 3 {
				t.Errorf("the composition %s was expanded for %d periods, not the three with a finer one", name, composed)
			}
		} else if sqldb.IsPeriodTemplate(name) {
			for _, period := range vocab.Periods() {
				text, err := sqldb.PeriodStatement(name, period)
				if err != nil {
					t.Fatalf("expanding %s: %v", name, err)
				}
				texts = append(texts, text)
			}
		} else {
			text, err := sqldb.Statement(name)
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			texts = append(texts, text)
		}
		for _, text := range texts {
			if err := explainStatement(ctx, database, text); err != nil {
				t.Errorf("the statement %s does not plan: %v", name, err)
			}
		}
	}
	if _, err := sqldb.Statement("no_such_statement"); err == nil {
		t.Error("a statement that does not exist was returned")
	}
	if _, err := sqldb.PeriodStatement("dirty_hours", vocab.PeriodHour); err == nil {
		t.Error("a statement that is not a template was expanded as one")
	}
	if _, err := sqldb.ComposeStatement("compose_volume", vocab.PeriodHour); err == nil {
		t.Error("an hour, which has no finer period, was composed")
	}
	if _, err := sqldb.ComposeStatement("insert_volume", vocab.PeriodDay); err == nil {
		t.Error("a statement that is not a composition was expanded as one")
	}
}

// explainStatement plans a statement on the database with every parameter bound to NULL, which is
// how a test asks PostgreSQL whether a statement is valid against the schema without running it.
func explainStatement(ctx context.Context, s *store.Store, text string) error {
	names, err := sqldb.ParameterNames(text)
	if err != nil {
		return err
	}
	arguments := make([]any, 0, len(names))
	for _, name := range names {
		if name == "" {
			arguments = append(arguments, nil)
			continue
		}
		arguments = append(arguments, sql.Named(name, nil))
	}
	rows, err := s.SQL.QueryContext(ctx, "EXPLAIN "+text, arguments...)
	if err != nil {
		return err
	}
	return rows.Close()
}

// sortedStatementNames is every registered statement's name, sorted.
func sortedStatementNames(t *testing.T) []string {
	t.Helper()
	names, err := sqldb.StatementNames()
	if err != nil {
		t.Fatalf("reading the statements: %v", err)
	}
	sort.Strings(names)
	return names
}
