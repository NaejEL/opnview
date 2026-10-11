package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// A query, as GET /api/datasets/{name}/query carries it: one parameter, `parameters`, holding a
// JSON object, as GET /api/widget/{type} carries a widget's (docs/widget-api.md, "Data
// catalogue"):
//
//	{"period": "24h",
//	 "x": {"column": "observed_at", "bucket": "auto"},
//	 "y": [{"column": "bytes", "aggregation": "sum"}],
//	 "filters": [{"column": "action", "op": "eq", "value": "pass"}],
//	 "split_by": {"column": "protocol", "limit": 5},
//	 "order": "desc", "limit": 100}
//
// A value outside a key's vocabulary or of the wrong type takes the key's default and is
// reported, as the widget API reports it; a key it does not know is reported and ignored. What
// names the database -- a column, an aggregation, an operator -- or bounds the work is never
// defaulted: anything the dataset's declaration does not hold is a refusal with a closed code, and
// no statement is built.

// The refusal codes, a closed list.
const (
	// RefusalUnknownDataset is a dataset name the catalogue does not hold.
	RefusalUnknownDataset = "unknown_dataset"
	// RefusalMalformedParameters is a parameters value that is not one JSON object, or a key whose
	// value has not the shape the query reads (an object, a list of objects).
	RefusalMalformedParameters = "malformed_parameters"
	// RefusalUnknownColumn is a column the dataset does not declare.
	RefusalUnknownColumn = "unknown_column"
	// RefusalColumnRoleNotAdmitted is a column used where its role is not: a measure as x, a time
	// column as split_by.
	RefusalColumnRoleNotAdmitted = "column_role_not_admitted"
	// RefusalAggregationNotAdmitted is an aggregation the column does not admit.
	RefusalAggregationNotAdmitted = "aggregation_not_admitted"
	// RefusalOperatorNotAdmitted is a filter operator outside the list, or one the column's type does
	// not admit.
	RefusalOperatorNotAdmitted = "operator_not_admitted"
	// RefusalValueTypeMismatch is a filter value that is not of its column's type.
	RefusalValueTypeMismatch = "value_type_mismatch"
	// RefusalBucketNotAdmitted is a bucket that is not auto nor a whole number of seconds of at
	// least MinBucketSeconds.
	RefusalBucketNotAdmitted = "bucket_not_admitted"
	// RefusalTooManyBuckets is a time axis of more than MaxBuckets buckets.
	RefusalTooManyBuckets = "too_many_buckets"
	// RefusalTooManySeries is more than MaxSeries series: measures times split values.
	RefusalTooManySeries = "too_many_series"
	// RefusalTooManyRows is a dimension axis of more than MaxRows rows, or a filter of more than
	// MaxFilterValues values.
	RefusalTooManyRows = "too_many_rows"
)

// RefusalCodes is every refusal code, for the document and its test.
func RefusalCodes() []string {
	return []string{RefusalUnknownDataset, RefusalMalformedParameters, RefusalUnknownColumn,
		RefusalColumnRoleNotAdmitted, RefusalAggregationNotAdmitted, RefusalOperatorNotAdmitted,
		RefusalValueTypeMismatch, RefusalBucketNotAdmitted, RefusalTooManyBuckets, RefusalTooManySeries,
		RefusalTooManyRows}
}

// The bounds, each written once here and documented in docs/widget-api.md, "Data catalogue".
const (
	// MaxBuckets is the most buckets one series may have.
	MaxBuckets = 1000
	// MaxSeries is the most series a query may draw: its measures times its split values, the
	// remainder entry aside.
	MaxSeries = 20
	// MaxRows is the most rows a dimension axis may have.
	MaxRows = 1000
	// MaxFilterValues is the most values an in or not_in filter may list.
	MaxFilterValues = 100
	// MinBucketSeconds is the narrowest bucket.
	MinBucketSeconds = 60
	// StatementTimeout bounds one catalogue statement, in milliseconds.
	StatementTimeout = 5000
)

// The filter operators, a closed list.
const (
	OperatorEq        = "eq"
	OperatorNe        = "ne"
	OperatorIn        = "in"
	OperatorNotIn     = "not_in"
	OperatorLt        = "lt"
	OperatorLe        = "le"
	OperatorGt        = "gt"
	OperatorGe        = "ge"
	OperatorIsNull    = "is_null"
	OperatorIsNotNull = "is_not_null"
)

// Operators is every filter operator.
func Operators() []string {
	return []string{OperatorEq, OperatorNe, OperatorIn, OperatorNotIn, OperatorLt, OperatorLe, OperatorGt,
		OperatorGe, OperatorIsNull, OperatorIsNotNull}
}

// Periods are the periods a query may read, the widget API's own.
var Periods = []string{"1h", "24h", "7d", "30d"}

// PeriodSeconds is each period's rolling-window duration, as the widget API reads it.
var PeriodSeconds = map[string]int64{"1h": 3600, "24h": 86400, "7d": 7 * 86400, "30d": 30 * 86400}

// AutoBucketSeconds is the bucket an auto bucket takes in each period, the widget API's own
// (decision 8): 1 m, 15 m, 1 h and 24 h.
var AutoBucketSeconds = map[string]int64{"1h": 60, "24h": 900, "7d": 3600, "30d": 86400}

// The defaults.
const (
	DefaultPeriod     = "24h"
	DefaultSplitLimit = 10
	DefaultLimit      = 100
	OrderDescending   = "desc"
	OrderAscending    = "asc"
)

// Refusal is a query refused, with its closed code and the path of what it names.
type Refusal struct {
	Code string `json:"condition"`
	Path string `json:"path,omitempty"`
	// Reason details a malformed value: not_json, not_an_object, trailing_data or wrong_shape.
	Reason string `json:"reason,omitempty"`
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("dataset: %s at %q %s", r.Code, r.Path, r.Reason)
}

func refuse(code, path string) *Refusal { return &Refusal{Code: code, Path: path} }

// Invalid is a value that took its default.
type Invalid struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value"`
	Reason string          `json:"reason"`
}

// The reasons a value took its default.
const (
	ReasonNotInVocabulary = "not_in_vocabulary"
	ReasonWrongType       = "wrong_type"
	ReasonOutOfRange      = "out_of_range"
)

// Measure is one y entry: a column and its aggregation. An empty column counts the rows.
type Measure struct {
	Column      string `json:"column,omitempty"`
	Aggregation string `json:"aggregation"`
}

// Axis is x: the time column with a bucket, or one dimension.
type Axis struct {
	Column string `json:"column"`
	// Bucket is "auto" or a number of seconds, on a time axis.
	Bucket any `json:"bucket,omitempty"`
}

// Filter is one filter.
type Filter struct {
	Column   string `json:"column"`
	Operator string `json:"op"`
	Value    any    `json:"value,omitempty"`
}

// Split is split_by.
type Split struct {
	Column string `json:"column"`
	Limit  int    `json:"limit"`
}

// Query is a parsed query, with what the parse reported.
type Query struct {
	Period  string
	X       Axis
	Y       []Measure
	Filters []Filter
	SplitBy *Split
	Order   string
	Limit   int

	// Unknown are the keys the query does not read, by path.
	Unknown map[string]json.RawMessage
	// Invalid are the values that took their default.
	Invalid []Invalid

	// timeAxis says x is the time column; bucketSeconds is its bucket width, or alignment one
	// of the calendar alignments.
	timeAxis      bool
	bucketSeconds int64
	alignment     string
	// values are the filters' values as bound, by filter.
	values [][]any
}

// Effective is the query as it was applied, for the response's parameters.
func (q *Query) Effective() map[string]any {
	effective := map[string]any{"period": q.Period, "x": q.X, "y": q.Y, "filters": q.Filters}
	if q.Y == nil {
		effective["y"] = []Measure{}
	}
	if q.Filters == nil {
		effective["filters"] = []Filter{}
	}
	if q.SplitBy != nil {
		effective["split_by"] = *q.SplitBy
	} else {
		effective["split_by"] = nil
	}
	if !q.timeAxis {
		effective["order"], effective["limit"] = q.Order, q.Limit
	}
	return effective
}

// TimeAxis reports whether x is the time column, and its bucket.
func (q *Query) TimeAxis() (bool, int64, string) { return q.timeAxis, q.bucketSeconds, q.alignment }

// The alignments of a time bucket.
const (
	// AlignmentEpoch is a multiple of the bucket width from the epoch, in UTC.
	AlignmentEpoch = "epoch"
	// AlignmentISOWeek is the ISO week, Monday 00:00 UTC, as the 7d aggregate period aligns it.
	AlignmentISOWeek = "iso_week"
	// AlignmentCalendarMonth is the calendar month, the 1st 00:00 UTC, as the 30d aggregate period
	// aligns it.
	AlignmentCalendarMonth = "calendar_month"
)

// The bucket widths that take a calendar alignment, so a week or a month bucket starts where the
// volume aggregates' period starts (docs/widget-catalogue.md, "Custom chart").
const (
	weekSeconds  = 7 * 86400
	monthSeconds = 30 * 86400
)

// Parse reads a query's parameters against a dataset's declaration. It returns the query, or a
// refusal; the query carries the unknown keys and the values that took their default.
func Parse(raw string, declaration Declaration) (*Query, *Refusal) {
	query := &Query{Unknown: map[string]json.RawMessage{}, Invalid: []Invalid{}}
	given, refusal := object(raw)
	if refusal != nil {
		return nil, refusal
	}
	known := []string{"period", "x", "y", "filters", "split_by", "order", "limit"}
	for key, value := range given {
		if !slices.Contains(known, key) {
			query.Unknown[key] = value
		}
	}

	query.Period = DefaultPeriod
	if value, present := given["period"]; present && !isNull(value) {
		var period string
		switch {
		case json.Unmarshal(value, &period) != nil:
			query.Invalid = append(query.Invalid, Invalid{Path: "period", Value: value, Reason: ReasonWrongType})
		case !slices.Contains(Periods, period):
			query.Invalid = append(query.Invalid, Invalid{Path: "period", Value: value, Reason: ReasonNotInVocabulary})
		default:
			query.Period = period
		}
	}

	timeColumn, timed := declaration.TimeColumn()
	if refusal := query.parseAxis(given["x"], declaration, timeColumn, timed); refusal != nil {
		return nil, refusal
	}
	if refusal := query.parseMeasures(given["y"], declaration); refusal != nil {
		return nil, refusal
	}
	if refusal := query.parseFilters(given["filters"], declaration); refusal != nil {
		return nil, refusal
	}
	if refusal := query.parseSplit(given["split_by"], declaration); refusal != nil {
		return nil, refusal
	}
	if !query.timeAxis {
		query.Order = OrderDescending
		if value, present := given["order"]; present && !isNull(value) {
			var order string
			switch {
			case json.Unmarshal(value, &order) != nil:
				query.Invalid = append(query.Invalid, Invalid{Path: "order", Value: value, Reason: ReasonWrongType})
			case order != OrderDescending && order != OrderAscending:
				query.Invalid = append(query.Invalid, Invalid{Path: "order", Value: value,
					Reason: ReasonNotInVocabulary})
			default:
				query.Order = order
			}
		}
		query.Limit = DefaultLimit
		if value, present := given["limit"]; present && !isNull(value) {
			limit, ok := wholeNumber(value)
			switch {
			case !ok:
				query.Invalid = append(query.Invalid, Invalid{Path: "limit", Value: value, Reason: ReasonWrongType})
			case limit < 1:
				query.Invalid = append(query.Invalid, Invalid{Path: "limit", Value: value, Reason: ReasonOutOfRange})
			case limit > MaxRows:
				return nil, refuse(RefusalTooManyRows, "limit")
			default:
				query.Limit = int(limit)
			}
		}
	} else {
		for _, key := range []string{"order", "limit"} {
			if value, present := given[key]; present && !isNull(value) {
				// order and limit shape a dimension axis; on a time axis they are not read.
				query.Unknown[key] = value
			}
		}
	}

	series := len(query.Y)
	if query.SplitBy != nil {
		series *= query.SplitBy.Limit
	}
	if series > MaxSeries {
		return nil, refuse(RefusalTooManySeries, "y")
	}
	if query.timeAxis {
		if buckets := query.bucketCount(); buckets > MaxBuckets {
			return nil, refuse(RefusalTooManyBuckets, "x.bucket")
		}
	}
	sort.Slice(query.Invalid, func(i, j int) bool { return query.Invalid[i].Path < query.Invalid[j].Path })
	return query, nil
}

// bucketCount is how many buckets the time axis has over the period, at most.
func (q *Query) bucketCount() int64 {
	period := PeriodSeconds[q.Period]
	switch q.alignment {
	case AlignmentISOWeek:
		return period/weekSeconds + 2
	case AlignmentCalendarMonth:
		return period/(28*86400) + 2
	}
	return period/q.bucketSeconds + 1
}

func (q *Query) parseAxis(raw json.RawMessage, declaration Declaration, timeColumn Column, timed bool) *Refusal {
	fields := map[string]json.RawMessage{}
	if raw != nil && !isNull(raw) {
		if json.Unmarshal(raw, &fields) != nil {
			return &Refusal{Code: RefusalMalformedParameters, Path: "x", Reason: "wrong_shape"}
		}
	}
	for key, value := range fields {
		if key != "column" && key != "bucket" {
			q.Unknown["x."+key] = value
		}
	}
	column := ""
	if value, present := fields["column"]; present && !isNull(value) {
		if json.Unmarshal(value, &column) != nil {
			return refuse(RefusalUnknownColumn, "x.column")
		}
	}
	if column == "" {
		if timed {
			column = timeColumn.Name
		} else {
			for _, candidate := range declaration.Columns {
				if candidate.Role == RoleDimension {
					column = candidate.Name
					break
				}
			}
		}
	}
	declared, found := declaration.Column(column)
	if !found {
		return refuse(RefusalUnknownColumn, "x.column")
	}
	q.X = Axis{Column: column}
	switch declared.Role {
	case RoleMeasure:
		return refuse(RefusalColumnRoleNotAdmitted, "x.column")
	case RoleDimension:
		if value, present := fields["bucket"]; present && !isNull(value) {
			q.Unknown["x.bucket"] = value
		}
		return nil
	}
	q.timeAxis = true
	q.alignment = AlignmentEpoch
	q.bucketSeconds = AutoBucketSeconds[q.Period]
	q.X.Bucket = "auto"
	value, present := fields["bucket"]
	if !present || isNull(value) {
		return nil
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		if text != "auto" {
			return refuse(RefusalBucketNotAdmitted, "x.bucket")
		}
		return nil
	}
	seconds, ok := wholeNumber(value)
	if !ok || seconds < MinBucketSeconds {
		return refuse(RefusalBucketNotAdmitted, "x.bucket")
	}
	q.X.Bucket = seconds
	q.bucketSeconds = seconds
	switch seconds {
	case weekSeconds:
		q.alignment = AlignmentISOWeek
	case monthSeconds:
		q.alignment = AlignmentCalendarMonth
	}
	return nil
}

func (q *Query) parseMeasures(raw json.RawMessage, declaration Declaration) *Refusal {
	if raw == nil || isNull(raw) {
		q.Y = []Measure{{Aggregation: AggregationCount}}
		return nil
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return &Refusal{Code: RefusalMalformedParameters, Path: "y", Reason: "wrong_shape"}
	}
	if len(entries) == 0 {
		q.Y = []Measure{{Aggregation: AggregationCount}}
		return nil
	}
	if len(entries) > MaxSeries {
		return refuse(RefusalTooManySeries, "y")
	}
	for index, fields := range entries {
		path := fmt.Sprintf("y[%d]", index)
		for key, value := range fields {
			if key != "column" && key != "aggregation" {
				q.Unknown[path+"."+key] = value
			}
		}
		measure := Measure{}
		if value, present := fields["column"]; present && !isNull(value) {
			if json.Unmarshal(value, &measure.Column) != nil {
				return refuse(RefusalUnknownColumn, path+".column")
			}
		}
		if value, present := fields["aggregation"]; present && !isNull(value) {
			if json.Unmarshal(value, &measure.Aggregation) != nil {
				return refuse(RefusalAggregationNotAdmitted, path+".aggregation")
			}
		}
		if measure.Column == "" {
			// No column counts the rows.
			if measure.Aggregation == "" {
				measure.Aggregation = AggregationCount
			}
			if measure.Aggregation != AggregationCount {
				return refuse(RefusalAggregationNotAdmitted, path+".aggregation")
			}
			q.Y = append(q.Y, measure)
			continue
		}
		column, found := declaration.Column(measure.Column)
		if !found {
			return refuse(RefusalUnknownColumn, path+".column")
		}
		if column.Role == RoleTime {
			return refuse(RefusalColumnRoleNotAdmitted, path+".column")
		}
		if measure.Aggregation == "" && len(column.Aggregations) > 0 {
			measure.Aggregation = column.Aggregations[0]
		}
		if !column.Admits(measure.Aggregation) {
			return refuse(RefusalAggregationNotAdmitted, path+".aggregation")
		}
		q.Y = append(q.Y, measure)
	}
	return nil
}

func (q *Query) parseFilters(raw json.RawMessage, declaration Declaration) *Refusal {
	if raw == nil || isNull(raw) {
		return nil
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return &Refusal{Code: RefusalMalformedParameters, Path: "filters", Reason: "wrong_shape"}
	}
	for index, fields := range entries {
		path := fmt.Sprintf("filters[%d]", index)
		for key, value := range fields {
			if key != "column" && key != "op" && key != "value" {
				q.Unknown[path+"."+key] = value
			}
		}
		var name, operator string
		if json.Unmarshal(fields["column"], &name) != nil {
			return refuse(RefusalUnknownColumn, path+".column")
		}
		column, found := declaration.Column(name)
		if !found {
			return refuse(RefusalUnknownColumn, path+".column")
		}
		if json.Unmarshal(fields["op"], &operator) != nil || !slices.Contains(Operators(), operator) {
			return refuse(RefusalOperatorNotAdmitted, path+".op")
		}
		value, present := fields["value"]
		if present && isNull(value) {
			present = false
		}
		var bound []any
		switch operator {
		case OperatorIsNull, OperatorIsNotNull:
			if !column.Nullable {
				return refuse(RefusalOperatorNotAdmitted, path+".op")
			}
			if present {
				return refuse(RefusalValueTypeMismatch, path+".value")
			}
		case OperatorLt, OperatorLe, OperatorGt, OperatorGe:
			if readType(column.Type) == TypeText {
				return refuse(RefusalOperatorNotAdmitted, path+".op")
			}
			typed, ok := typedValue(value, column.Type)
			if !present || !ok {
				return refuse(RefusalValueTypeMismatch, path+".value")
			}
			bound = []any{typed}
		case OperatorEq, OperatorNe:
			typed, ok := typedValue(value, column.Type)
			if !present || !ok {
				return refuse(RefusalValueTypeMismatch, path+".value")
			}
			bound = []any{typed}
		case OperatorIn, OperatorNotIn:
			var list []json.RawMessage
			if !present || json.Unmarshal(value, &list) != nil || len(list) == 0 {
				return refuse(RefusalValueTypeMismatch, path+".value")
			}
			if len(list) > MaxFilterValues {
				return refuse(RefusalTooManyRows, path+".value")
			}
			for _, member := range list {
				typed, ok := typedValue(member, column.Type)
				if !ok {
					return refuse(RefusalValueTypeMismatch, path+".value")
				}
				bound = append(bound, typed)
			}
		}
		filter := Filter{Column: name, Operator: operator}
		if present {
			var echoed any
			_ = json.Unmarshal(value, &echoed)
			filter.Value = echoed
		}
		q.Filters = append(q.Filters, filter)
		q.values = append(q.values, bound)
	}
	return nil
}

func (q *Query) parseSplit(raw json.RawMessage, declaration Declaration) *Refusal {
	if raw == nil || isNull(raw) {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return &Refusal{Code: RefusalMalformedParameters, Path: "split_by", Reason: "wrong_shape"}
	}
	for key, value := range fields {
		if key != "column" && key != "limit" {
			q.Unknown["split_by."+key] = value
		}
	}
	var name string
	if json.Unmarshal(fields["column"], &name) != nil {
		return refuse(RefusalUnknownColumn, "split_by.column")
	}
	column, found := declaration.Column(name)
	if !found {
		return refuse(RefusalUnknownColumn, "split_by.column")
	}
	if column.Role != RoleDimension || name == q.X.Column {
		return refuse(RefusalColumnRoleNotAdmitted, "split_by.column")
	}
	split := &Split{Column: name, Limit: DefaultSplitLimit}
	if value, present := fields["limit"]; present && !isNull(value) {
		limit, ok := wholeNumber(value)
		switch {
		case !ok:
			q.Invalid = append(q.Invalid, Invalid{Path: "split_by.limit", Value: value, Reason: ReasonWrongType})
		case limit < 1:
			q.Invalid = append(q.Invalid, Invalid{Path: "split_by.limit", Value: value, Reason: ReasonOutOfRange})
		case limit > MaxSeries:
			return refuse(RefusalTooManySeries, "split_by.limit")
		default:
			split.Limit = int(limit)
		}
	}
	q.SplitBy = split
	return nil
}

// Columns returns every column the query reads, filters or splits on.
func (q *Query) Columns() []string {
	columns := []string{q.X.Column}
	for _, measure := range q.Y {
		if measure.Column != "" {
			columns = append(columns, measure.Column)
		}
	}
	for _, filter := range q.Filters {
		columns = append(columns, filter.Column)
	}
	if q.SplitBy != nil {
		columns = append(columns, q.SplitBy.Column)
	}
	return columns
}

// TouchesDomain reports whether the query reads, filters or splits on a column carrying a domain.
func (q *Query) TouchesDomain(declaration Declaration) bool {
	for _, name := range q.Columns() {
		if column, found := declaration.Column(name); found && column.CarriesDomain {
			return true
		}
	}
	return false
}

// object decodes a parameters payload into its keys. An empty payload is the empty object.
func object(raw string) (map[string]json.RawMessage, *Refusal) {
	given := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) == "" {
		return given, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, &Refusal{Code: RefusalMalformedParameters, Reason: "not_json"}
	}
	if decoder.More() {
		return nil, &Refusal{Code: RefusalMalformedParameters, Reason: "trailing_data"}
	}
	if _, isObject := value.(map[string]any); !isObject {
		return nil, &Refusal{Code: RefusalMalformedParameters, Reason: "not_an_object"}
	}
	if err := json.Unmarshal([]byte(raw), &given); err != nil {
		return nil, &Refusal{Code: RefusalMalformedParameters, Reason: "not_an_object"}
	}
	return given, nil
}

// isNull reports whether a raw value is JSON null.
func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// wholeNumber reads a JSON whole number.
func wholeNumber(raw json.RawMessage) (int64, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	value, err := number.Int64()
	return value, err == nil
}

// typedValue reads a filter value as its column's type: a whole number for an integer column, a
// number for a real one, a string for a text one. Nothing else is accepted, and nothing is
// converted: "5" is not 5.
func typedValue(raw json.RawMessage, columnType string) (any, bool) {
	if raw == nil || isNull(raw) {
		return nil, false
	}
	if columnType == TypeBoolean {
		// A boolean column is read as the text true or false, and filtered by a JSON boolean.
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		if value {
			return "true", true
		}
		return "false", true
	}
	switch readType(columnType) {
	case TypeInteger:
		value, ok := wholeNumber(raw)
		return value, ok
	case TypeReal:
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var number json.Number
		if decoder.Decode(&number) != nil {
			return nil, false
		}
		value, err := number.Float64()
		return value, err == nil
	case TypeText:
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, false
		}
		return text, true
	}
	return nil, false
}
