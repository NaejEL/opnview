package sqldb

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The named statements, and how a package contributes its own.
//
// A STATEMENT LIVES WITH THE CODE THAT RUNS IT. The store's statements sit beside the Go file that
// executes them (classify.statements.sql beside internal/store/flows/classify.go); a widget's sit
// in the widget's own directory; a provider's, in the provider's. Each owner embeds its files and
// registers them from an init() in its own package with RegisterStatements, so adding an owner adds
// a directory and edits no shared file.
//
// sql/schema-checks.sh finds the same files by walking the tree for the statement marker below,
// and records the query plan of every statement at both seed sizes, so the text that runs is the
// text that is checked and there is no second copy.

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
			if err := pretranslate(text); err != nil {
				return fmt.Errorf("the statement %q of %s: %w", name, path, err)
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
func PeriodStatement(name string, period vocab.Period) (string, error) {
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
func ComposeStatement(name string, period vocab.Period) (string, error) {
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

// SplitStatements splits a SQL file into statements on the semicolons that end one.
//
// It understands what would otherwise corrupt a statement rather than fail loudly: a semicolon
// inside a single-quoted string, a quoted identifier or a dollar-quoted body (a function's) does
// not end a statement; a quote inside a `--` or a `/* */` comment is not the start of a string --
// both files this package embeds are heavily commented and several comments carry an
// apostrophe -- and a quote doubled inside a string is an escaped quote.
//
// It is exported so a test can exercise it on its own rather than only through a database.
func SplitStatements(text string) []string {
	var statements []string
	var current strings.Builder
	length := len(text)
	for i := 0; i < length; {
		character := text[i]
		switch {
		case character == '-' && i+1 < length && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = length - i
			}
			current.WriteString(text[i : i+end])
			i += end
		case character == '/' && i+1 < length && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			stop := length
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			current.WriteString(text[i:stop])
			i = stop
		case character == '\'' || character == '"':
			end := i + 1
			for end < length {
				if text[end] == character {
					if end+1 < length && text[end+1] == character {
						end += 2
						continue
					}
					break
				}
				end++
			}
			stop := min(end+1, length)
			current.WriteString(text[i:stop])
			i = stop
		case character == '$' && i+1 < length && (text[i+1] == '$' || isIdentifierStart(text[i+1])):
			tagEnd := strings.IndexByte(text[i+1:], '$')
			if tagEnd >= 0 && validDollarTag(text[i:i+1+tagEnd+1]) {
				tag := text[i : i+1+tagEnd+1]
				end := strings.Index(text[i+len(tag):], tag)
				stop := length
				if end >= 0 {
					stop = i + len(tag) + end + len(tag)
				}
				current.WriteString(text[i:stop])
				i = stop
				continue
			}
			current.WriteByte(character)
			i++
		case character == ';':
			statements = append(statements, current.String())
			current.Reset()
			i++
		default:
			current.WriteByte(character)
			i++
		}
	}
	statements = append(statements, current.String())

	kept := make([]string, 0, len(statements))
	for _, statement := range statements {
		if !blankOrComment(statement) {
			kept = append(kept, statement)
		}
	}
	return kept
}

// blankOrComment reports whether a fragment holds nothing a database would run.
func blankOrComment(statement string) bool {
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return false
		}
	}
	return true
}

// FirstLine returns the first line of a statement that is not a comment, so an error names the
// statement without reprinting its documentation.
func FirstLine(statement string) string {
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return trimmed
		}
	}
	return strings.TrimSpace(statement)
}

// pretranslate translates a registered statement and, for a template, every expansion of it, so a
// statement file is translated when it is registered and never when it runs (translate.go).
func pretranslate(text string) error {
	if !strings.Contains(text, periodToken) {
		_, err := Translate(text)
		return err
	}
	for _, period := range vocab.Periods() {
		expanded := strings.ReplaceAll(text, periodToken, period.Name)
		if strings.Contains(expanded, childToken) {
			child, has := period.Child()
			if !has {
				continue
			}
			expanded = strings.ReplaceAll(expanded, childToken, child.Name)
		}
		if _, err := Translate(expanded); err != nil {
			return err
		}
	}
	return nil
}

// ComposeOrPeriodStatement returns a template statement expanded for one period: a composition template
// with its finer period too (ComposeStatement), any other template with its period alone.
func ComposeOrPeriodStatement(name string, period vocab.Period) (string, error) {
	text, err := Statement(name)
	if err != nil {
		return "", err
	}
	if strings.Contains(text, childToken) {
		return ComposeStatement(name, period)
	}
	return PeriodStatement(name, period)
}
