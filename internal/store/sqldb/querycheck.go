package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The check an operator-supplied read query passes before the widget API will run it.
//
// THREE LAYERS, AND THE LAST ONE IS NOT THIS FILE. A query reaches the read connections only
// after it (1) is read by a lexer that knows strings, quoted identifiers, dollar-quoted bodies,
// casts and comments, so "one statement", "starts with SELECT" and "names no writing keyword" are
// decided on tokens and not on a substring -- which is how `WITH x AS (DELETE ...) SELECT` and
// `SELECT ... FOR UPDATE`, statements that start like a read, are recognised; (2) is planned by
// PostgreSQL itself, in a read-only transaction, and refused by name when it does not plan; and
// (3) runs on the read role's connections, which can neither write nor read a credential, in
// read-only transactions (readers.go). A check here that missed something would therefore still
// not let a write through; it exists so that a bad query is refused at load, by name, instead of
// failing at the first request.

// The kinds of refusal CheckQuery reports.
const (
	// QueryMultipleStatements is a text holding more than one statement.
	QueryMultipleStatements = "multiple_statements"
	// QueryNotReadOnly is a text that is not a single SELECT, or that names a writing keyword.
	QueryNotReadOnly = "not_read_only"
	// QueryDoesNotPlan is a text PostgreSQL cannot plan.
	QueryDoesNotPlan = "does_not_plan"
	// QueryUnboundParameter is a parameter the query names that the caller does not bind.
	QueryUnboundParameter = "unbound_parameter"
	// QueryEmpty is a text with no statement at all.
	QueryEmpty = "empty"
)

// QueryProblem is a refused query: its kind is one of the constants above.
type QueryProblem struct {
	Kind   string
	Detail string
}

// Error implements error.
func (p *QueryProblem) Error() string { return "store: query refused (" + p.Kind + "): " + p.Detail }

// QueryInfo is what a query that passed the check says about itself.
type QueryInfo struct {
	// Columns are the result columns, in order.
	Columns []string
	// Parameters are the named parameters (:name) the text uses, sorted, without the colon.
	Parameters []string
}

// writingKeywords are the words only a statement that changes the database, its schema, its
// session or a lock uses. A SELECT ... FOR UPDATE names UPDATE and is refused with them: it takes
// row locks, which a read never needs.
var writingKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "CREATE": true, "DROP": true,
	"ALTER": true, "TRUNCATE": true, "GRANT": true, "REVOKE": true, "COPY": true, "CALL": true,
	"DO": true, "SET": true, "RESET": true, "LOCK": true, "VACUUM": true, "ANALYZE": true,
	"REINDEX": true, "CLUSTER": true, "COMMENT": true, "SECURITY": true, "LISTEN": true,
	"NOTIFY": true, "PREPARE": true, "EXECUTE": true, "DISCARD": true, "IMPORT": true, "REFRESH": true,
}

// CheckQuery validates an operator query against the read connections and returns its
// columns and parameters. allowed lists the parameter names the caller binds; a parameter
// outside it is a QueryUnboundParameter refusal.
func (s *Handle) CheckQuery(ctx context.Context, text string, allowed []string) (QueryInfo, error) {
	if s.Reader == nil {
		return QueryInfo{}, errors.New("store: no read connections")
	}
	lexed := LexSQL(text)
	switch {
	case lexed.Statements == 0:
		return QueryInfo{}, &QueryProblem{Kind: QueryEmpty, Detail: "the text holds no statement"}
	case lexed.Statements > 1:
		return QueryInfo{}, &QueryProblem{Kind: QueryMultipleStatements,
			Detail: fmt.Sprintf("the text holds %d statements; a widget query is exactly one", lexed.Statements)}
	}
	if first := strings.ToUpper(lexed.FirstWord); first != "SELECT" && first != "WITH" {
		return QueryInfo{}, &QueryProblem{Kind: QueryNotReadOnly,
			Detail: fmt.Sprintf("a widget query is a SELECT, and this one starts with %q", lexed.FirstWord)}
	}
	for _, name := range lexed.positional {
		return QueryInfo{}, &QueryProblem{Kind: QueryUnboundParameter,
			Detail: fmt.Sprintf("the parameter %q is not a named :parameter and nothing binds it", name)}
	}
	bound := map[string]bool{}
	for _, name := range allowed {
		bound[name] = true
	}
	for _, name := range lexed.Parameters {
		if !bound[name] {
			return QueryInfo{}, &QueryProblem{Kind: QueryUnboundParameter,
				Detail: fmt.Sprintf("the query uses :%s, which no parameter of the widget supplies", name)}
		}
	}
	body := StatementBody(text, lexed)
	if err := s.planIsReadOnly(ctx, body, NullArguments(lexed.Parameters)); err != nil {
		return QueryInfo{}, err
	}
	columns, err := s.queryColumns(ctx, body, NullArguments(lexed.Parameters))
	if err != nil {
		return QueryInfo{}, &QueryProblem{Kind: QueryDoesNotPlan, Detail: err.Error()}
	}
	return QueryInfo{Columns: columns, Parameters: lexed.Parameters}, nil
}

// planIsReadOnly refuses a text naming a writing keyword, and one PostgreSQL cannot plan.
func (s *Handle) planIsReadOnly(ctx context.Context, body string, arguments []any) error {
	for _, word := range LexSQL(body).words {
		if writingKeywords[strings.ToUpper(word)] {
			return &QueryProblem{Kind: QueryNotReadOnly,
				Detail: "the text names " + strings.ToUpper(word) + ", which only a statement that writes uses"}
		}
	}
	tx, err := s.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return &QueryProblem{Kind: QueryDoesNotPlan, Detail: err.Error()}
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "EXPLAIN "+body, arguments...)
	if err != nil {
		return &QueryProblem{Kind: QueryDoesNotPlan, Detail: err.Error()}
	}
	return rows.Close()
}

// queryColumns plans the text and returns its result columns without running it: the text is
// wrapped so that it reads no row, in a read-only transaction.
func (s *Handle) queryColumns(ctx context.Context, body string, arguments []any) ([]string, error) {
	tx, err := s.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT * FROM (\n"+body+"\n) AS checked LIMIT 0", arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Columns()
}

// nullArguments binds every named parameter of a text to NULL, which is all planning needs.
func NullArguments(names []string) []any {
	arguments := make([]any, 0, len(names))
	for _, name := range names {
		arguments = append(arguments, sql.Named(name, nil))
	}
	return arguments
}

// statementBody is the text of the single statement a text holds: everything before its
// terminating semicolon, which the lexer found outside any string or comment, so that what follows
// it -- only whitespace and comments, once a text holds one statement -- is gone.
func StatementBody(text string, lexed lexed) string {
	if lexed.semicolonAt >= 0 {
		text = string([]rune(text)[:lexed.semicolonAt])
	}
	return strings.TrimSpace(text)
}

// QueryParameters returns the named parameters (:name) a text uses, without the colon: the names a
// widget script may bind when it runs the statement.
func QueryParameters(text string) []string {
	return append([]string(nil), LexSQL(text).Parameters...)
}

// RegisterQuery registers one whole text as a named statement under an owner, bypassing the
// marker format: an operator's query file is one statement and may hold a semicolon inside a
// string literal. The caller has already passed it through CheckQuery.
func RegisterQuery(owner, name, text string) error {
	body := StatementBody(text, LexSQL(text))
	statementsMutex.Lock()
	defer statementsMutex.Unlock()
	if previous, duplicate := statementOwners[name]; duplicate {
		failure := fmt.Errorf("store: the statement %q of %s is already registered by %s", name, owner, previous)
		return failure
	}
	statementsByID[name] = body
	statementOwners[name] = owner
	return nil
}

// UnregisterOwner removes every statement an owner registered. Only the operator widget loader
// uses it, to withdraw the statements of a widget it refuses after registering them.
func UnregisterOwner(owner string) {
	statementsMutex.Lock()
	defer statementsMutex.Unlock()
	var names []string
	for name, registered := range statementOwners {
		if registered == owner {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		delete(statementsByID, name)
		delete(statementOwners, name)
	}
}

// lexed is what lexSQL learns of a text.
type lexed struct {
	// semicolonAt is the rune index of the first statement terminator outside any string or
	// comment, or -1.
	semicolonAt int
	Statements  int
	FirstWord   string
	Parameters  []string
	positional  []string
	// words are every bare word of the text, outside strings, identifiers and comments.
	words []string
}

// lexSQL walks a text once, skipping comments and quoted strings and identifiers, and counts
// the statements, finds the first word and collects the named parameters.
func LexSQL(text string) lexed {
	result := lexed{semicolonAt: -1}
	runes := []rune(text)
	seenParams := map[string]bool{}
	inStatement := false
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			i++
		case c == '\'' || c == '"' || c == '`':
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = string(c)
				}
			}
			i = skipQuoted(runes, i, c)
		case c == '[':
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = "["
				}
			}
			for i < len(runes) && runes[i] != ']' {
				i++
			}
		case c == ';':
			if result.semicolonAt < 0 {
				result.semicolonAt = i
			}
			inStatement = false
		case unicode.IsSpace(c):
		case c == ':' && i+1 < len(runes) && runes[i+1] == ':':
			// A cast, ::type: not a parameter.
			i++
		case c == '$' && dollarTagAt(runes, i) != "":
			// A dollar-quoted string, $$...$$ or $tag$...$tag$.
			tag := []rune(dollarTagAt(runes, i))
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = string(tag)
				}
			}
			i = skipDollarQuoted(runes, i, tag)
		case c == ':' || c == '@' || c == '$':
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = string(c)
				}
			}
			start := i + 1
			end := start
			for end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
				end++
			}
			name := string(runes[start:end])
			switch {
			case c == ':' && name != "":
				if !seenParams[name] {
					seenParams[name] = true
					result.Parameters = append(result.Parameters, name)
				}
			case c != ':':
				result.positional = append(result.positional, string(c)+name)
			}
			if end > start {
				i = end - 1
			}
		case c == '?':
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = "?"
				}
			}
			result.positional = append(result.positional, "?")
		case unicode.IsLetter(c) || c == '_':
			end := i
			for end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
				end++
			}
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = string(runes[i:end])
				}
			}
			result.words = append(result.words, string(runes[i:end]))
			i = end - 1
		default:
			if !inStatement {
				inStatement = true
				result.Statements++
				if result.Statements == 1 {
					result.FirstWord = string(c)
				}
			}
		}
	}
	sort.Strings(result.Parameters)
	return result
}

// skipDollarQuoted returns the index of the last rune of the closing tag of a dollar-quoted string
// opened at i, or the end of the text.
func skipDollarQuoted(runes []rune, i int, tag []rune) int {
	for end := i + len(tag); end+len(tag) <= len(runes); end++ {
		if string(runes[end:end+len(tag)]) == string(tag) {
			return end + len(tag) - 1
		}
	}
	return len(runes)
}

// dollarTagAt returns the dollar-quote tag starting at i ($$ or $tag$), or "" when none starts there:
// $1, a positional parameter, is not one.
func dollarTagAt(runes []rune, i int) string {
	if i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
		return ""
	}
	for end := i + 1; end < len(runes); end++ {
		if runes[end] == '$' {
			return string(runes[i : end+1])
		}
		if !(unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
			return ""
		}
	}
	return ""
}

// skipQuoted returns the index of the closing quote of a quoted run starting at i, treating a
// doubled quote as an escaped one.
func skipQuoted(runes []rune, i int, quote rune) int {
	for i++; i < len(runes); i++ {
		if runes[i] != quote {
			continue
		}
		if i+1 < len(runes) && runes[i+1] == quote {
			i++
			continue
		}
		return i
	}
	return len(runes)
}
