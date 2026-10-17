package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A script's rows of a dataset (PA3 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md):
// what a source script, a derivation or a widget reads of a dataset its plugin declares, through
// host.datasets.rows. It uses the catalogue's own filter grammar and compiler, so it names nothing a
// catalogue query could not name and binds every value.
//
// A READ IS SET-BASED. A filter `in` may list up to MaxScriptValues values, and `keys` a set of up to
// MaxScriptValues tuples over several columns; each list is bound as one array parameter per column, so a
// read of a thousand keys is one statement, with one text, as a read of ten is. A derivation therefore
// reads what a batch needs in a number of statements that does not grow with the batch.

// The bounds of a script's read, written once here and documented in docs/plugins.md, host.datasets.
const (
	// MaxScriptValues is the most values one `in` filter, or the most tuples one `keys` set, may hold.
	MaxScriptValues = 10000
	// MaxScriptRows is the most rows one read answers; Truncated says more matched.
	MaxScriptRows = 10000
)

// ParseScriptFilters reads a script's filters: a query's "filters" grammar, with an `in` list bounded by
// MaxScriptValues rather than the catalogue API's MaxFilterValues.
func ParseScriptFilters(raw json.RawMessage, declaration Declaration) (*Query, *Refusal) {
	query := &Query{Unknown: map[string]json.RawMessage{}, Invalid: []Invalid{}, maxFilterValues: MaxScriptValues}
	if refusal := query.parseFilters(raw, declaration); refusal != nil {
		return nil, refusal
	}
	return query, nil
}

// KeySet is a set of tuples a read keeps the rows of: the rows whose Columns hold one of the tuples.
type KeySet struct {
	Columns []string
	// values are the tuples, one list per column, each typed as its column reads.
	values [][]any
}

// Size is how many tuples the set holds.
func (k *KeySet) Size() int {
	if k == nil || len(k.values) == 0 {
		return 0
	}
	return len(k.values[0])
}

// ParseKeys reads a key set: {"columns": [...], "values": [[...], ...]}, each tuple as long as columns,
// each value of its column's type; a set of one column may list bare values. Null or absent is no key
// set. The set is deduplicated: a tuple listed twice is kept once.
func ParseKeys(raw json.RawMessage, declaration Declaration) (*KeySet, *Refusal) {
	if raw == nil || isNull(raw) {
		return nil, nil
	}
	var shape struct {
		Columns []string          `json:"columns"`
		Values  []json.RawMessage `json:"values"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&shape) != nil || len(shape.Columns) == 0 {
		return nil, &Refusal{Code: RefusalMalformedParameters, Path: "keys", Reason: "wrong_shape"}
	}
	if len(shape.Values) > MaxScriptValues {
		return nil, refuse(RefusalTooManyRows, "keys.values")
	}
	columns := make([]Column, 0, len(shape.Columns))
	seen := map[string]bool{}
	for index, name := range shape.Columns {
		column, found := declaration.Column(name)
		if !found || seen[name] {
			return nil, refuse(RefusalUnknownColumn, fmt.Sprintf("keys.columns[%d]", index))
		}
		seen[name] = true
		columns = append(columns, column)
	}
	keys := &KeySet{Columns: append([]string(nil), shape.Columns...), values: make([][]any, len(columns))}
	tuples := map[string]bool{}
	for index, raw := range shape.Values {
		path := fmt.Sprintf("keys.values[%d]", index)
		var members []json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			if len(columns) != 1 {
				return nil, refuse(RefusalValueTypeMismatch, path)
			}
			members = []json.RawMessage{raw}
		}
		if len(members) != len(columns) {
			return nil, refuse(RefusalValueTypeMismatch, path)
		}
		typed := make([]any, len(columns))
		for position, member := range members {
			value, ok := typedValue(member, columns[position].Type)
			if !ok {
				return nil, refuse(RefusalValueTypeMismatch, fmt.Sprintf("%s[%d]", path, position))
			}
			typed[position] = value
		}
		identity, _ := json.Marshal(typed)
		if tuples[string(identity)] {
			continue
		}
		tuples[string(identity)] = true
		for position, value := range typed {
			keys.values[position] = append(keys.values[position], value)
		}
	}
	return keys, nil
}

// typedArray is a list of filter or key values as the one array parameter a statement binds, with the
// SQL array type it is cast to: whole numbers, numbers or texts, as their column reads.
func typedArray(values []any) (any, string) {
	if len(values) > 0 {
		switch values[0].(type) {
		case int64:
			numbers := make([]int64, 0, len(values))
			for _, value := range values {
				numbers = append(numbers, value.(int64))
			}
			return numbers, "BIGINT[]"
		case float64:
			numbers := make([]float64, 0, len(values))
			for _, value := range values {
				numbers = append(numbers, value.(float64))
			}
			return numbers, "DOUBLE PRECISION[]"
		}
	}
	texts := make([]string, 0, len(values))
	for _, value := range values {
		texts = append(texts, fmt.Sprint(value))
	}
	return texts, "TEXT[]"
}

// CompileRows compiles a script's read of the given columns: one statement, whatever the number of
// filter values and keys, its rows ordered by every column so the answer is the same for the same rows.
// window bounds the time column of a timed dataset; at most limit rows are read.
func CompileRows(declaration Declaration, query *Query, keys *KeySet, window Window, columns []string,
	limit int) Statement {
	c := compiler{declaration: declaration, query: query}
	if _, timed := declaration.TimeColumn(); timed {
		c.args = append(c.args, window.From, window.To)
	}
	where := c.where(window)
	if keys != nil && keys.Size() == 0 {
		// An empty key set keeps no row.
		where += " AND FALSE"
	} else if keys.Size() > 0 {
		own := make([]string, 0, len(keys.Columns))
		arrays := make([]string, 0, len(keys.Columns))
		for position, name := range keys.Columns {
			own = append(own, c.column(name))
			array, cast := typedArray(keys.values[position])
			arrays = append(arrays, "CAST("+c.bind("keys_"+strconv.Itoa(position), array)+" AS "+cast+")")
		}
		where += " AND (" + strings.Join(own, ", ") + ") IN (SELECT * FROM unnest(" + strings.Join(arrays, ", ") + "))"
	}
	selected := make([]string, 0, len(columns))
	order := make([]string, 0, len(columns))
	for index, name := range columns {
		selected = append(selected, c.column(name))
		order = append(order, strconv.Itoa(index+1))
	}
	text := "SELECT " + strings.Join(selected, ", ") + " FROM (" + declaration.relation + ") AS d WHERE " + where
	if len(order) > 0 {
		text += " ORDER BY " + strings.Join(order, ", ")
	}
	text += " LIMIT " + c.bind("row_limit", int64(limit))
	return Statement{Text: text, Args: c.args}
}
