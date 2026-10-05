package store

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

// The derivation and read statements, embedded.
//
// They live in two SQL files beside the schema rather than as string literals in
// Go for the reason the schema and the purge do: sql/schema-checks.sh reads the
// same files and records the query plan of every statement at both seed sizes, so
// the text that runs is the text that is checked and there is no second copy.
//
//go:embed derive.sql read.sql
var statementFiles embed.FS

// statementMarker begins each statement in the two files.
const statementMarker = "-- statement: "

// periodToken is replaced by a period's table suffix in a template statement.
const periodToken = "@period@"

var (
	statementsOnce sync.Once
	statementsByID map[string]string
	statementsErr  error
)

// Statement returns one named statement's text, with its comments removed and
// its terminating semicolon stripped. A template statement is returned with its
// period token in place; PeriodStatement expands it.
func Statement(name string) (string, error) {
	statementsOnce.Do(loadStatements)
	if statementsErr != nil {
		return "", statementsErr
	}
	text, present := statementsByID[name]
	if !present {
		return "", fmt.Errorf("store: no statement is named %q", name)
	}
	return text, nil
}

// PeriodStatement returns a template statement expanded for one period.
func PeriodStatement(name string, period Period) (string, error) {
	text, err := Statement(name)
	if err != nil {
		return "", err
	}
	if !strings.Contains(text, periodToken) {
		return "", fmt.Errorf("store: the statement %q is not a period template", name)
	}
	return strings.ReplaceAll(text, periodToken, period.Name), nil
}

// StatementNames returns every statement name, for the tests that execute and
// plan each one.
func StatementNames() ([]string, error) {
	statementsOnce.Do(loadStatements)
	if statementsErr != nil {
		return nil, statementsErr
	}
	names := make([]string, 0, len(statementsByID))
	for name := range statementsByID {
		names = append(names, name)
	}
	return names, nil
}

// IsPeriodTemplate reports whether a statement is a period template.
func IsPeriodTemplate(name string) bool {
	text, err := Statement(name)
	return err == nil && strings.Contains(text, periodToken)
}

// loadStatements parses both files once.
func loadStatements() {
	statementsByID = map[string]string{}
	for _, file := range []string{"derive.sql", "read.sql"} {
		raw, err := statementFiles.ReadFile(file)
		if err != nil {
			statementsErr = fmt.Errorf("store: reading the embedded %s: %w", file, err)
			return
		}
		parsed, err := parseStatements(string(raw))
		if err != nil {
			statementsErr = fmt.Errorf("store: parsing %s: %w", file, err)
			return
		}
		for name, text := range parsed {
			if _, duplicate := statementsByID[name]; duplicate {
				statementsErr = fmt.Errorf("store: the statement %q is named twice", name)
				return
			}
			statementsByID[name] = text
		}
	}
}

// parseStatements splits a file on its markers. Comment lines are dropped, so
// the commentary between two statements belongs to neither, and the trailing
// semicolon is removed so a statement can be prepared on its own.
func parseStatements(text string) (map[string]string, error) {
	statements := map[string]string{}
	var name string
	var body strings.Builder
	flush := func() error {
		if name == "" {
			return nil
		}
		statement := strings.TrimSpace(body.String())
		statement = strings.TrimSpace(strings.TrimSuffix(statement, ";"))
		if statement == "" {
			return fmt.Errorf("the statement %q is empty", name)
		}
		if strings.Contains(statement, ";") {
			return fmt.Errorf("the statement %q holds more than one statement", name)
		}
		if _, duplicate := statements[name]; duplicate {
			return fmt.Errorf("the statement %q is named twice", name)
		}
		statements[name] = statement
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, statementMarker) {
			if err := flush(); err != nil {
				return nil, err
			}
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, statementMarker))
			body.Reset()
			continue
		}
		if strings.HasPrefix(trimmed, "--") || name == "" {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return statements, nil
}

// childToken is replaced by the table suffix of a period's finer period in a
// composition template: see ComposeStatement.
const childToken = "@child@"

// ComposeStatement returns a composition template expanded for one period and the
// finer period its slots are composed from.
func ComposeStatement(name string, period Period) (string, error) {
	child, has := period.Child()
	if !has {
		return "", fmt.Errorf("store: the %s period has no finer period to compose from", period.Name)
	}
	text, err := PeriodStatement(name, period)
	if err != nil {
		return "", err
	}
	if !strings.Contains(text, childToken) {
		return "", fmt.Errorf("store: the statement %q is not a composition template", name)
	}
	return strings.ReplaceAll(text, childToken, child.Name), nil
}

// IsComposeTemplate reports whether a statement is a composition template.
func IsComposeTemplate(name string) bool {
	text, err := Statement(name)
	return err == nil && strings.Contains(text, childToken)
}
