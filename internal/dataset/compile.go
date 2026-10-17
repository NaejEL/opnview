package dataset

import (
	"fmt"
	"strconv"
	"strings"
)

// The compiler: a parsed query and its dataset's declaration become ONE parameterised SELECT.
//
// Every identifier in the text comes from the declaration, quoted; every value from the query is a
// bound parameter. The dataset's relation -- the server's own SELECT, or a plugin's table -- is the
// one source of rows, read as a subquery named d. The ranking a split or a dimension axis needs is
// a common table expression of the same statement, so the whole answer is one round trip.

// Statement is a compiled query: its text and its arguments, every one a named parameter.
type Statement struct {
	Text string
	Args []any
	// Layout says what the columns of a result row are.
	Layout Layout
}

// Layout is the shape of a compiled query's rows.
//
// A time axis gives [bucket start, (split rank, split value,) measures...]; a dimension axis gives
// [key rank, key count, key, (split rank, split value,) measures...]. A split rank and value are
// null for the remainder: the rows whose split value is outside the ranked ones, or null.
type Layout struct {
	TimeAxis bool
	Split    bool
	Measures int
}

// Window is the interval a query reads, in UTC epoch seconds, half-open.
type Window struct {
	From int64
	To   int64
}

// Compile turns a query into its statement.
func Compile(declaration Declaration, query *Query, window Window) Statement {
	c := compiler{declaration: declaration, query: query}
	return c.compile(window)
}

// EarliestStatement is the statement that reads the instant of a dataset's oldest row, or the
// empty string for a dataset with no time column.
func EarliestStatement(declaration Declaration) string {
	if declaration.earliest != "" {
		return declaration.earliest
	}
	column, timed := declaration.TimeColumn()
	if !timed {
		return ""
	}
	return "SELECT min(d." + quote(column.Name) + ") FROM (" + declaration.relation + ") AS d"
}

type compiler struct {
	declaration Declaration
	query       *Query
	args        []any
}

// column is a column of the relation, as the statement names it.
func (c *compiler) column(name string) string { return "d." + quote(name) }

// bind adds an argument and returns its parameter.
func (c *compiler) bind(name string, value any) string {
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(len(c.args))
}

// aggregate is one measure's expression over the rows of a group.
func (c *compiler) aggregate(measure Measure) string {
	if measure.Column == "" {
		return "count(*)"
	}
	column := c.column(measure.Column)
	switch measure.Aggregation {
	case AggregationSum:
		return "sum(" + column + ")"
	case AggregationMean:
		return "avg(" + column + ")"
	case AggregationMin:
		return "min(" + column + ")"
	case AggregationMax:
		return "max(" + column + ")"
	case AggregationCount:
		return "count(" + column + ")"
	case AggregationCountDistinct:
		return "count(DISTINCT " + column + ")"
	case AggregationLast:
		timeColumn, _ := c.declaration.TimeColumn()
		return "(array_agg(" + column + " ORDER BY " + c.column(timeColumn.Name) + " DESC) FILTER (WHERE " +
			column + " IS NOT NULL))[1]"
	}
	// Parse admits no other aggregation; an unknown one counts nothing rather than reaching SQL.
	return "NULL"
}

// where is the statement's filter: the window on the time column, and every filter.
func (c *compiler) where(window Window) string {
	var conditions []string
	if timeColumn, timed := c.declaration.TimeColumn(); timed {
		conditions = append(conditions, c.column(timeColumn.Name)+" >= $1", c.column(timeColumn.Name)+" < $2")
	}
	for index, filter := range c.query.Filters {
		column := c.column(filter.Column)
		values := c.query.values[index]
		parameter := func(position int) string {
			return c.bind(fmt.Sprintf("filter_%d_%d", index, position), values[position])
		}
		switch filter.Operator {
		case OperatorEq:
			conditions = append(conditions, column+" = "+parameter(0))
		case OperatorNe:
			conditions = append(conditions, column+" IS DISTINCT FROM "+parameter(0))
		case OperatorLt:
			conditions = append(conditions, column+" < "+parameter(0))
		case OperatorLe:
			conditions = append(conditions, column+" <= "+parameter(0))
		case OperatorGt:
			conditions = append(conditions, column+" > "+parameter(0))
		case OperatorGe:
			conditions = append(conditions, column+" >= "+parameter(0))
		case OperatorIsNull:
			conditions = append(conditions, column+" IS NULL")
		case OperatorIsNotNull:
			conditions = append(conditions, column+" IS NOT NULL")
		case OperatorIn, OperatorNotIn:
			// ONE ARRAY PARAMETER WHATEVER THE LIST'S LENGTH, so a list of a thousand keys is one statement
			// with one bound value and one text, which the read connections prepare once (PA3 of the
			// plugin-API amendment).
			array, cast := typedArray(values)
			list := "CAST(" + c.bind(fmt.Sprintf("filter_%d", index), array) + " AS " + cast + ")"
			if filter.Operator == OperatorIn {
				conditions = append(conditions, column+" = ANY("+list+")")
			} else {
				conditions = append(conditions, "("+column+" IS NULL OR "+column+" <> ALL("+list+"))")
			}
		}
	}
	if len(conditions) == 0 {
		return "TRUE"
	}
	return strings.Join(conditions, " AND ")
}

// bucket is the time axis' bucket start of a row.
func (c *compiler) bucket() string {
	timeColumn, _ := c.declaration.TimeColumn()
	at := c.column(timeColumn.Name)
	switch c.query.alignment {
	case AlignmentISOWeek:
		// The epoch was a Thursday: a day's ISO weekday from Monday is (days + 3) mod 7.
		return "(" + at + " - " + at + " % 86400 - ((" + at + " / 86400 + 3) % 7) * 86400)"
	case AlignmentCalendarMonth:
		return "month_start_at(" + at + ")"
	}
	return "(" + at + " - " + at + " % " + c.bind("bucket", c.query.bucketSeconds) + ")"
}

// ranking is the ORDER BY a ranking common table expression sorts its candidates with: the first
// measure, then the value itself, so equal figures rank in byte order.
func (c *compiler) ranking(value, direction string) string {
	return c.aggregate(c.query.Y[0]) + " " + direction + " NULLS LAST, " + value
}

func (c *compiler) compile(window Window) Statement {
	relation := "(" + c.declaration.relation + ") AS d"
	if _, timed := c.declaration.TimeColumn(); timed {
		c.args = append(c.args, window.From, window.To)
	}
	where := c.where(window)
	measures := make([]string, 0, len(c.query.Y))
	for _, measure := range c.query.Y {
		measures = append(measures, c.aggregate(measure))
	}
	layout := Layout{TimeAxis: c.query.timeAxis, Split: c.query.SplitBy != nil, Measures: len(c.query.Y)}

	var with []string
	splitJoin, splitColumns, splitGroup := "", "", ""
	if c.query.SplitBy != nil {
		split := c.column(c.query.SplitBy.Column)
		with = append(with, "catalogue_splits AS (SELECT "+split+" AS value, row_number() OVER (ORDER BY "+
			c.ranking(split, "DESC")+") AS rank FROM "+relation+" WHERE "+where+" AND "+split+
			" IS NOT NULL GROUP BY "+split+" ORDER BY "+c.ranking(split, "DESC")+" LIMIT "+
			c.bind("split_limit", int64(c.query.SplitBy.Limit))+")")
		splitJoin = " LEFT JOIN catalogue_splits ON catalogue_splits.value = " + split
		splitColumns = "catalogue_splits.rank AS split_rank, catalogue_splits.value AS split_value, "
		splitGroup = ", 2, 3"
	}

	var text string
	if c.query.timeAxis {
		text = "SELECT " + c.bucket() + " AS bucket, " + splitColumns + strings.Join(measures, ", ") +
			" FROM " + relation + splitJoin + " WHERE " + where + " GROUP BY 1" + splitGroup + " ORDER BY 1" + splitGroup
		if len(with) > 0 {
			text = "WITH " + strings.Join(with, ", ") + " " + text
		}
		return Statement{Text: text, Args: c.args, Layout: layout}
	}

	// A dimension axis: the ranked keys, with how many there are in all, then the groups of the
	// ranked keys only. A null key is a key like any other.
	key := c.column(c.query.X.Column)
	direction := "DESC"
	if c.query.Order == OrderAscending {
		direction = "ASC"
	}
	keys := "catalogue_keys AS (SELECT " + key + " AS value, row_number() OVER (ORDER BY " +
		c.ranking(key, direction) + ") AS rank, count(*) OVER () AS members FROM " + relation + " WHERE " +
		where + " GROUP BY " + key + " ORDER BY " + c.ranking(key, direction) + " LIMIT " +
		c.bind("row_limit", int64(c.query.Limit)) + ")"
	with = append([]string{keys}, with...)
	grouped := "SELECT " + key + " AS key_value, " + splitColumns + strings.Join(measures, ", ") + " FROM " +
		relation + splitJoin + " WHERE " + where + " AND (" + key + " IN (SELECT value FROM catalogue_keys) OR (" +
		key + " IS NULL AND EXISTS (SELECT 1 FROM catalogue_keys WHERE value IS NULL))) GROUP BY 1" + splitGroup
	text = "WITH " + strings.Join(with, ", ") + " SELECT catalogue_keys.rank, catalogue_keys.members, catalogue_groups.* FROM (" +
		grouped + ") AS catalogue_groups JOIN catalogue_keys ON catalogue_keys.value IS NOT DISTINCT FROM catalogue_groups.key_value " +
		"ORDER BY catalogue_keys.rank"
	if layout.Split {
		text += ", catalogue_groups.split_rank NULLS LAST"
	}
	return Statement{Text: text, Args: c.args, Layout: layout}
}
