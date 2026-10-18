package sqldb

import (
	"fmt"
	"strings"
)

// CheckExpression checks an SQL fragment a plugin's table declaration carries -- a CHECK, a generated
// column, a partial index's predicate (ADR 0019) -- before it goes into the table's DDL: one expression,
// no statement terminator, no parameter, and no word a statement that writes uses. PostgreSQL then
// refuses what does not parse, and the plugin is refused with it.
func CheckExpression(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the expression is empty")
	}
	lexed := LexSQL(text)
	if lexed.semicolonAt >= 0 || lexed.statements > 1 {
		return fmt.Errorf("the expression holds a statement terminator")
	}
	if len(lexed.Parameters) > 0 || len(lexed.positional) > 0 {
		return fmt.Errorf("the expression names a parameter, which a declaration cannot bind")
	}
	for _, word := range lexed.words {
		if writingKeywords[strings.ToUpper(word)] || strings.EqualFold(word, "SELECT") {
			return fmt.Errorf("the expression names %s, which an expression over a row never uses", strings.ToUpper(word))
		}
	}
	return nil
}
