package plugintables

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// A plugin's relations and statements (ADR 0018): the views and functions it publishes in its own
// schema, applied at every start, and the statements its derivations run, planned under its role when it
// loads. Both run as the plugin's role (sqldb, pluginrole.go), so what they may touch is PostgreSQL's
// decision: a view reads what the read role reads, a statement writes in the plugin's schema alone.

// RelationFile is one relation file of a plugin's manifest: its name, for the refusal, and its text.
type RelationFile struct {
	Name string
	Text string
}

// RelationProblem is a relation file the store refused, with what was wrong.
type RelationProblem struct {
	File   string
	Detail string
}

func (p *RelationProblem) Error() string { return "the relation file " + p.File + ": " + p.Detail }

// relationHead matches the only two statements a relation file may hold, and captures the object's
// qualified name.
var relationHead = regexp.MustCompile(`(?is)^\s*create\s+or\s+replace\s+(view|function)\s+([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\b`)

var (
	wordVolatile        = regexp.MustCompile(`(?i)\bvolatile\b`)
	wordStableOrImmutab = regexp.MustCompile(`(?i)\b(stable|immutable)\b`)
	wordSecurityDefiner = regexp.MustCompile(`(?i)\bsecurity\s+definer\b`)
)

// CheckRelationFile checks one relation file without a database: every statement is a CREATE OR REPLACE
// VIEW or FUNCTION of an object in the plugin's own schema, and a function is STABLE or IMMUTABLE and never
// SECURITY DEFINER.
func CheckRelationFile(plugin string, file RelationFile) error {
	schema := plugindecl.PluginSchema(plugin)
	statements := relationStatements(file.Text)
	if len(statements) == 0 {
		return &RelationProblem{File: file.Name, Detail: "holds no statement"}
	}
	for _, statement := range statements {
		match := relationHead.FindStringSubmatch(statement)
		if match == nil {
			return &RelationProblem{File: file.Name, Detail: "holds a statement that is not CREATE OR REPLACE VIEW " +
				"or CREATE OR REPLACE FUNCTION of a schema-qualified object: " + firstWords(statement)}
		}
		if match[2] != schema {
			return &RelationProblem{File: file.Name, Detail: fmt.Sprintf("creates %s.%s, outside the plugin's schema %s",
				match[2], match[3], schema)}
		}
		if strings.EqualFold(match[1], "function") {
			switch {
			case wordSecurityDefiner.MatchString(statement):
				return &RelationProblem{File: file.Name, Detail: "the function " + match[3] + " is SECURITY DEFINER"}
			case wordVolatile.MatchString(statement):
				return &RelationProblem{File: file.Name, Detail: "the function " + match[3] + " is VOLATILE"}
			case !wordStableOrImmutab.MatchString(statement):
				return &RelationProblem{File: file.Name, Detail: "the function " + match[3] + " says neither STABLE nor IMMUTABLE"}
			}
		}
	}
	return nil
}

// relationStatements splits a relation file into its statements, each with the comments before it
// removed, and without the empty one a trailing comment leaves.
func relationStatements(text string) []string {
	var statements []string
	for _, statement := range sqldb.SplitStatements(text) {
		statement = withoutLeadingComments(statement)
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

// withoutLeadingComments removes the comments and blank lines a statement starts with.
func withoutLeadingComments(statement string) string {
	for {
		statement = strings.TrimSpace(statement)
		switch {
		case strings.HasPrefix(statement, "--"):
			end := strings.IndexByte(statement, '\n')
			if end < 0 {
				return ""
			}
			statement = statement[end+1:]
		case strings.HasPrefix(statement, "/*"):
			end := strings.Index(statement, "*/")
			if end < 0 {
				return ""
			}
			statement = statement[end+2:]
		default:
			return statement
		}
	}
}

// firstWords is the start of a statement, for a refusal's detail.
func firstWords(statement string) string {
	fields := strings.Fields(statement)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return strings.Join(fields, " ")
}

// EnsurePluginSchema creates a plugin's schema and its role when they are absent: what a plugin with
// relations and no table needs before its relations are applied.
func (s *PluginTableStore) EnsurePluginSchema(ctx context.Context, plugin string) error {
	schema := plugindecl.PluginSchema(plugin)
	if _, err := s.SQL.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+plugindecl.QuoteIdentifier(schema)); err != nil {
		return fmt.Errorf("store: creating the schema of the plugin %s: %w", plugin, err)
	}
	if err := s.GrantReadOnSchema(ctx, schema); err != nil {
		return err
	}
	return s.EnsurePluginRole(ctx, plugin, schema)
}

// ApplyRelations checks and applies a plugin's relation files, in name order, in one transaction run as
// the plugin's role, then lets the read role read what was created. CREATE OR REPLACE makes a second
// application a no-op, so it is applied at every start.
func (s *PluginTableStore) ApplyRelations(ctx context.Context, plugin string, files []RelationFile) error {
	if len(files) == 0 {
		return nil
	}
	sorted := append([]RelationFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, file := range sorted {
		if err := CheckRelationFile(plugin, file); err != nil {
			return err
		}
	}
	if err := s.EnsurePluginSchema(ctx, plugin); err != nil {
		return err
	}
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: applying the relations of the plugin %s: %w", plugin, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := sqldb.TakePluginRole(ctx, tx, plugin); err != nil {
		return err
	}
	for _, file := range sorted {
		for _, statement := range relationStatements(file.Text) {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return &RelationProblem{File: file.Name, Detail: err.Error()}
			}
		}
	}
	if err := sqldb.GivePluginRoleBack(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: applying the relations of the plugin %s: %w", plugin, err)
	}
	return s.GrantReadOnSchema(ctx, plugindecl.PluginSchema(plugin))
}

// CheckPluginStatement plans one of a plugin's statements under its role; what the plugin loads checks.
func (s *PluginTableStore) CheckPluginStatement(ctx context.Context, plugin, text string) error {
	return s.PlanAsPlugin(ctx, plugin, text)
}
