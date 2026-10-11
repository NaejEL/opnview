package dataset

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// The latest rows of a dataset: what a widget reads of another plugin's dataset by its catalogue
// name, without SQL (host.datasets.latest, docs/plugins.md). It uses the catalogue's own filter
// grammar and compiler, so it names nothing a query could not name and binds every value.
//
// WHAT "LATEST" MEANS. Among the rows the filters keep, within the dataset's retention horizon, the
// rows whose time column holds the greatest instant: every row of the most recent instant, and
// nothing older. A sample dataset written by a pass -- one instant per pass -- answers that pass's
// rows whole. A dataset with no time column has no instant: its latest rows are every row the
// filters keep. At most MaxRows rows are answered; Truncated says more matched.

// LatestLayout is what a compiled latest read's rows hold: the instant, then Columns in order.
type LatestLayout struct {
	Columns []string
}

// ParseFilters reads the filters of a latest read, a list of {column, op, value} exactly as a
// query's "filters" key: the same operators, the same type rules, the same refusals. A refusal's
// path is the filter's, filters[i].column for one.
func ParseFilters(raw json.RawMessage, declaration Declaration) (*Query, *Refusal) {
	query := &Query{Unknown: map[string]json.RawMessage{}, Invalid: []Invalid{}}
	if refusal := query.parseFilters(raw, declaration); refusal != nil {
		return nil, refusal
	}
	return query, nil
}

// CompileLatest compiles a latest read of the given columns: one statement, the instant in the
// first column of every row (null for a dataset with no time column), then the columns, ordered
// by every column so the answer is the same for the same rows. window bounds the time column; at
// most limit rows are read.
func CompileLatest(declaration Declaration, query *Query, window Window, columns []string,
	limit int) (Statement, LatestLayout) {
	c := compiler{declaration: declaration, query: query}
	relation := "(" + declaration.relation + ") AS d"
	timeColumn, timed := declaration.TimeColumn()
	if timed {
		c.args = append(c.args, sql.Named("from", window.From), sql.Named("to", window.To))
	}
	where := c.where(window)
	selected := make([]string, 0, len(columns)+1)
	order := make([]string, 0, len(columns))
	for index, name := range columns {
		selected = append(selected, c.column(name))
		order = append(order, strconv.Itoa(index+2))
	}
	limitParameter := c.bind("row_limit", int64(limit))
	var text string
	if timed {
		text = "WITH catalogue_latest AS (SELECT max(" + c.column(timeColumn.Name) + ") AS at FROM " + relation +
			" WHERE " + where + ") SELECT catalogue_latest.at"
	} else {
		text = "SELECT CAST(NULL AS BIGINT) AS at"
	}
	if len(selected) > 0 {
		text += ", " + strings.Join(selected, ", ")
	}
	if timed {
		text += " FROM " + relation + " JOIN catalogue_latest ON " + c.column(timeColumn.Name) +
			" = catalogue_latest.at WHERE " + where
	} else {
		text += " FROM " + relation + " WHERE " + where
	}
	if len(order) > 0 {
		text += " ORDER BY " + strings.Join(order, ", ")
	}
	text += " LIMIT " + limitParameter
	return Statement{Text: text, Args: c.args}, LatestLayout{Columns: append([]string(nil), columns...)}
}

// FiltersTouchDomain reports whether a filter reads a column carrying a domain name.
func (q *Query) FiltersTouchDomain(declaration Declaration) bool {
	for _, filter := range q.Filters {
		if column, found := declaration.Column(filter.Column); found && column.CarriesDomain {
			return true
		}
	}
	return false
}
