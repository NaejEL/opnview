package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

// testDatabase creates a database of the test's own, cloned from the schema's template, and drops
// it when the test ends. It returns the connection parameter.
func testDatabase(t testing.TB) string {
	t.Helper()
	database, err := NewTestDatabase(context.Background())
	if err != nil {
		t.Fatalf("creating the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping the test database: %v", err)
		}
	})
	return database.DSN
}

// explainStatement plans a statement on the database with every parameter bound to NULL, which is
// how a test asks PostgreSQL whether a statement is valid against the schema without running it.
func explainStatement(ctx context.Context, s *Store, text string) error {
	rewritten, err := rewriteParameters(text)
	if err != nil {
		return err
	}
	arguments := make([]any, 0, len(rewritten.names))
	for _, name := range rewritten.names {
		if name == "" {
			arguments = append(arguments, nil)
			continue
		}
		arguments = append(arguments, sql.Named(name, nil))
	}
	rows, err := s.db.QueryContext(ctx, "EXPLAIN "+text, arguments...)
	if err != nil {
		return err
	}
	return rows.Close()
}

// planNode is one node of a statement's plan: its type, the relation it reads and the index it
// reads it through.
type planNode struct {
	Type, Relation, Alias, Index string
}

// planOf plans a statement with sequential scans disabled and returns its nodes. With them
// disabled, PostgreSQL still chooses a sequential scan only where no index can serve the
// statement at all, so a test asserting that an index serves a statement and no growing table is
// scanned asserts what the plan checks of sql/schema-checks.sh assert at scale, on any table
// size.
func planOf(t testing.TB, s *Store, text string, arguments ...any) []planNode {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err := tx.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+text, arguments...).Scan(&plan); err != nil {
		t.Fatalf("planning: %v", err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(plan), &decoded); err != nil {
		t.Fatal(err)
	}
	var nodes []planNode
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if kind, ok := typed["Node Type"].(string); ok {
				relation, _ := typed["Relation Name"].(string)
				alias, _ := typed["Alias"].(string)
				index, _ := typed["Index Name"].(string)
				nodes = append(nodes, planNode{Type: kind, Relation: relation, Alias: alias, Index: index})
			}
			for _, value := range typed {
				walk(value)
			}
		case []any:
			for _, element := range typed {
				walk(element)
			}
		}
	}
	walk(decoded)
	return nodes
}

// scans reports the sequential scans of a table in a plan.
func scans(nodes []planNode, table string) bool {
	for _, node := range nodes {
		if node.Type == "Seq Scan" && node.Relation == table {
			return true
		}
	}
	return false
}

// usesIndex reports whether a plan reads an index, through any alias or through the one named.
func usesIndex(nodes []planNode, index, alias string) bool {
	for _, node := range nodes {
		if node.Index == index && (alias == "" || node.Alias == alias) {
			return true
		}
	}
	return false
}
