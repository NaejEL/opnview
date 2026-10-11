package store

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// The named statements, and how a package contributes its own.
//
// A STATEMENT LIVES WITH THE CODE THAT RUNS IT. The store's statements sit beside the Go file
// that executes them (classify.statements.sql beside classify.go); a widget's sit in the
// widget's own directory; a provider's, in the provider's. Each owner embeds its files and
// registers them from an init() in its own package with RegisterStatements, so adding an owner
// adds a directory and edits no shared file.
//
// sql/schema-checks.sh finds the same files by walking the tree for the statement marker below,
// and records the query plan of every statement at both seed sizes, so the text that runs is the
// text that is checked and there is no second copy.
//
//go:embed *.statements.sql
var ownStatements embed.FS

func init() { RegisterStatements("store", ownStatements) }

// StatementMarker begins each statement in a statements file.
const StatementMarker = "-- statement: "

// periodToken is replaced by a period's table suffix in a template statement.
const periodToken = "@period@"

// childToken is replaced by the table suffix of a period's finer period in a
// composition template: see ComposeStatement.
const childToken = "@child@"

var (
	statementsMutex sync.RWMutex
	statementsByID  = map[string]string{}
	// statementOwners maps a statement to the owner that registered it, for the message a
	// duplicate produces and for a test.
	statementOwners = map[string]string{}
	// registrationFailures holds every refusal RegisterStatements met. Statement and
	// StatementNames report them, so a malformed or duplicated file is never silently half
	// registered.
	registrationFailures []error
)

// RegisterStatements parses every statements file of files (any file whose name ends in
// ".sql") and adds its statements to the registry under owner's name.
//
// It is called from the init() of the package that embeds the files, and by the operator
// widget loader. A file that does not parse, a statement that holds more than one statement,
// and a name registered twice are refused: nothing of that file is registered, and the refusal is
// returned and kept, so Statement reports it too.
func RegisterStatements(owner string, files fs.FS) error {
	parsed := map[string]string{}
	walkErr := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		raw, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		statements, err := parseStatements(string(raw))
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		for name, text := range statements {
			if _, duplicate := parsed[name]; duplicate {
				return fmt.Errorf("the statement %q is named twice in %s", name, owner)
			}
			parsed[name] = text
		}
		return nil
	})
	statementsMutex.Lock()
	defer statementsMutex.Unlock()
	if walkErr == nil {
		for name := range parsed {
			if previous, duplicate := statementOwners[name]; duplicate {
				walkErr = fmt.Errorf("the statement %q of %s is already registered by %s", name, owner, previous)
				break
			}
		}
	}
	if walkErr != nil {
		failure := fmt.Errorf("store: registering the statements of %s: %w", owner, walkErr)
		registrationFailures = append(registrationFailures, failure)
		return failure
	}
	for name, text := range parsed {
		statementsByID[name] = text
		statementOwners[name] = owner
	}
	return nil
}

// RegistrationFailures returns every refusal RegisterStatements met, for the test that asserts
// there were none.
func RegistrationFailures() []error {
	statementsMutex.RLock()
	defer statementsMutex.RUnlock()
	return append([]error(nil), registrationFailures...)
}

// StatementOwner names the owner that registered a statement, or "" for an unknown one.
func StatementOwner(name string) string {
	statementsMutex.RLock()
	defer statementsMutex.RUnlock()
	return statementOwners[name]
}

// failure joins the registration refusals; the caller holds the read lock.
func failure() error {
	if len(registrationFailures) == 0 {
		return nil
	}
	return errors.Join(registrationFailures...)
}

// Statement returns one named statement's text, with its comments removed and
// its terminating semicolon stripped. A template statement is returned with its
// period token in place; PeriodStatement expands it.
func Statement(name string) (string, error) {
	statementsMutex.RLock()
	defer statementsMutex.RUnlock()
	if err := failure(); err != nil {
		return "", err
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

// StatementNames returns every statement name, sorted, for the tests that execute and
// plan each one.
func StatementNames() ([]string, error) {
	statementsMutex.RLock()
	defer statementsMutex.RUnlock()
	if err := failure(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(statementsByID))
	for name := range statementsByID {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// IsPeriodTemplate reports whether a statement is a period template.
func IsPeriodTemplate(name string) bool {
	text, err := Statement(name)
	return err == nil && strings.Contains(text, periodToken)
}

// ParseStatements splits one statements file on its markers, exactly as RegisterStatements does,
// without registering anything: a widget plugin's loader reads its declared files with it and
// registers each statement under the name it chooses (internal/widget, pluginwidget.go).
func ParseStatements(text string) (map[string]string, error) {
	return parseStatements(text)
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
		if strings.HasPrefix(trimmed, StatementMarker) {
			if err := flush(); err != nil {
				return nil, err
			}
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, StatementMarker))
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
