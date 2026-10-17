package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Named parameters, translated once.
//
// TWO AUTHORING FORMS. A statement written in Go uses PostgreSQL's own numbering ($1, $2 ...) and
// is handed to database/sql as it is, with its arguments in order. A statement read from a file --
// the store's statements files, a widget's, a plugin's -- names its parameters as :name, the
// documented authoring form a widget or plugin author writes (docs/plugins.md), and is translated
// to PostgreSQL's numbering ONCE: when its file is registered (RegisterStatements, RegisterQuery),
// or, for a text composed at run time, the first time it is seen. The translation is kept, keyed by
// the text, and every later execution only orders its arguments by the names the translation
// recorded. The connection rewrites nothing and converts nothing (driver.go).
//
// A parameter is typed by PostgreSQL from the place it stands in; where that place does not type
// it (`:x IS NULL`, a SELECT list feeding an INSERT, `:a - :b`), the statement casts it.

// Translated is a statement in PostgreSQL's numbering, with the name each number binds.
type Translated struct {
	// Text is the statement with every :name replaced by $n.
	Text string
	// Names[k] is the name $(k+1) binds.
	Names []string
}

var (
	translations sync.Map // source text -> Translated
	// translated counts the texts translated since the process started: a test runs a statement many
	// times and asserts that it does not move (export_test.go).
	translated atomic.Int64
)

// Translate returns a text's translation, translating it the first time the text is seen.
func Translate(text string) (Translated, error) {
	if cached, found := translations.Load(text); found {
		return cached.(Translated), nil
	}
	result, err := translate(text)
	if err != nil {
		return Translated{}, err
	}
	if _, loaded := translations.LoadOrStore(text, result); !loaded {
		translated.Add(1)
	}
	return result, nil
}

// Arguments orders a parameter map by a translation's numbering. A name the statement binds and
// the map does not hold is refused; an entry the statement does not name is ignored, so one map
// serves several statements.
func (t Translated) Arguments(parameters map[string]any) ([]any, error) {
	arguments := make([]any, len(t.Names))
	for index, name := range t.Names {
		value, present := parameters[name]
		if !present {
			return nil, fmt.Errorf("store: the statement names :%s and no argument of that name was handed", name)
		}
		arguments[index] = value
	}
	return arguments, nil
}

// bind translates a text and orders its arguments.
func bind(text string, parameters map[string]any) (string, []any, error) {
	translation, err := Translate(text)
	if err != nil {
		return "", nil, err
	}
	arguments, err := translation.Arguments(parameters)
	if err != nil {
		return "", nil, err
	}
	return translation.Text, arguments, nil
}

// Query runs a :name statement text with its parameters.
func Query(ctx context.Context, q Querier, text string, parameters map[string]any) (*sql.Rows, error) {
	statement, arguments, err := bind(text, parameters)
	if err != nil {
		return nil, err
	}
	return q.QueryContext(ctx, statement, arguments...)
}

// Exec runs a :name statement text with its parameters.
func Exec(ctx context.Context, q Querier, text string, parameters map[string]any) (sql.Result, error) {
	statement, arguments, err := bind(text, parameters)
	if err != nil {
		return nil, err
	}
	return q.ExecContext(ctx, statement, arguments...)
}

// Row is one row a :name statement answered, or the reason it could not be run.
type Row struct {
	row *sql.Row
	err error
}

// Scan reads the row, as sql.Row.Scan does.
func (r Row) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	return r.row.Scan(destinations...)
}

// Err is the row's error, as sql.Row.Err is.
func (r Row) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.row.Err()
}

// QueryRow runs a :name statement text expected to answer at most one row.
func QueryRow(ctx context.Context, q Querier, text string, parameters map[string]any) Row {
	statement, arguments, err := bind(text, parameters)
	if err != nil {
		return Row{err: err}
	}
	return Row{row: q.QueryRowContext(ctx, statement, arguments...)}
}

// translate replaces each :name of a text by $n, the first name $1, a name used twice keeping its
// number. A :: cast, a string, a quoted identifier, a comment and a dollar-quoted body are left
// alone. A text already numbered ($1) is returned as it is; one that mixes both forms, or uses ?,
// is refused.
func translate(query string) (Translated, error) { return translateFixed(query, nil) }

// TranslateFixed translates a text whose parameters take fixed numbers: each :name becomes the $n
// numbers gives it, and a name numbers does not hold is refused. A dataset relation is translated so
// ($1 its window's start, $2 its end), and the statement composed around it numbers its own
// parameters after them (internal/dataset, compile.go).
func TranslateFixed(text string, numbers map[string]int) (string, error) {
	if numbers == nil {
		numbers = map[string]int{}
	}
	result, err := translateFixed(text, numbers)
	return result.Text, err
}

// translateFixed is translate, with each name numbered by fixed when fixed is not nil.
func translateFixed(query string, fixed map[string]int) (Translated, error) {
	var out strings.Builder
	out.Grow(len(query) + 16)
	result := Translated{}
	numberOf := map[string]int{}
	dollarNumbered := false
	length := len(query)
	for i := 0; i < length; {
		character := query[i]
		switch {
		case character == '\'' || character == '"':
			end := i + 1
			for end < length {
				if query[end] == character {
					if end+1 < length && query[end+1] == character {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= length {
				return Translated{}, fmt.Errorf("store: an unterminated quote in the statement")
			}
			out.WriteString(query[i : end+1])
			i = end + 1
		case character == '-' && i+1 < length && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				end = length - i
			}
			out.WriteString(query[i : i+end])
			i += end
		case character == '/' && i+1 < length && query[i+1] == '*':
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return Translated{}, fmt.Errorf("store: an unterminated comment in the statement")
			}
			out.WriteString(query[i : i+2+end+2])
			i += 2 + end + 2
		case character == '$' && i+1 < length && (query[i+1] == '$' || isIdentifierStart(query[i+1])):
			// A dollar-quoted body: $$...$$ or $tag$...$tag$.
			tagEnd := strings.IndexByte(query[i+1:], '$')
			if tagEnd < 0 || !validDollarTag(query[i:i+1+tagEnd+1]) {
				out.WriteByte(character)
				i++
				continue
			}
			tag := query[i : i+1+tagEnd+1]
			end := strings.Index(query[i+len(tag):], tag)
			if end < 0 {
				return Translated{}, fmt.Errorf("store: an unterminated dollar-quoted body in the statement")
			}
			stop := i + len(tag) + end + len(tag)
			out.WriteString(query[i:stop])
			i = stop
		case character == '$' && i+1 < length && query[i+1] >= '0' && query[i+1] <= '9':
			dollarNumbered = true
			out.WriteByte(character)
			i++
		case character == ':' && i+1 < length && query[i+1] == ':':
			out.WriteString("::")
			i += 2
		case character == ':' && i+1 < length && isIdentifierStart(query[i+1]):
			end := i + 1
			for end < length && isParameterCharacter(query[end]) {
				end++
			}
			name := query[i+1 : end]
			number, seen := numberOf[name]
			if fixed != nil {
				number, seen = fixed[name]
				if !seen {
					return Translated{}, fmt.Errorf("store: the text names :%s, which it may not use", name)
				}
			}
			if !seen {
				result.Names = append(result.Names, name)
				number = len(result.Names)
				numberOf[name] = number
			}
			out.WriteString("$" + strconv.Itoa(number))
			i = end
		case character == '?':
			return Translated{}, fmt.Errorf("store: a statement uses ?, which opnview does not bind: name the " +
				"parameter (:name) in a statements file, or number it ($1) in Go")
		default:
			out.WriteByte(character)
			i++
		}
	}
	if dollarNumbered && len(result.Names) > 0 {
		return Translated{}, fmt.Errorf("store: a statement mixes $n with :name parameters")
	}
	result.Text = out.String()
	return result, nil
}

func isIdentifierStart(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func validDollarTag(tag string) bool {
	if len(tag) < 2 || tag[0] != '$' || tag[len(tag)-1] != '$' {
		return false
	}
	for index := 1; index < len(tag)-1; index++ {
		if !isParameterCharacter(tag[index]) {
			return false
		}
	}
	return true
}
