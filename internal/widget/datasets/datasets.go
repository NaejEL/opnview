package datasets

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// The data catalogue's answers (P8 of specs/SPEC-backend-completion-plugins.md, docs/widget-api.md,
// "Data catalogue"): the listing of every dataset, and one query against one, answered in the
// widget API's envelope. The compiler is internal/dataset's; this file reads what the envelope
// carries -- the aggregate mode, the window and its coverage, the gaps, the availability -- and
// turns the rows into buckets, where a bucket the collection did not cover, or an empty bucket of a
// sample dataset, is a gap and never a zero.

// ListedDataset is one dataset of the listing.
type ListedDataset struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// Label is the catalogue key of its name.
	Label string `json:"label"`
	// Nature is event (a covered bucket with no row is a true zero) or sample (a bucket with no
	// row is a gap).
	Nature     string  `json:"nature"`
	TimeColumn *string `json:"time_column"`
	// RetentionSeconds is how long a row is kept, null when it is kept without limit or the dataset
	// has no time column; RetentionHorizonAt is the instant before which rows may be gone.
	RetentionSeconds   *int64           `json:"retention_seconds"`
	RetentionHorizonAt *int64           `json:"retention_horizon_at"`
	Freshness          DatasetFreshness `json:"freshness"`
	Columns            []ListedColumn   `json:"columns"`
	// LossDetectable is present on a dataset a push source feeds: whether a loss between the firewall
	// and opnview is detected and recorded as a gap (docs/plugins.md, "Push sources").
	LossDetectable *bool `json:"loss_detectable,omitempty"`
}

// DatasetFreshness is the provider whose last successful pass dates a dataset.
type DatasetFreshness struct {
	ProviderKind string  `json:"provider_kind"`
	ProviderKey  *string `json:"provider_key"`
	// LastSuccessAt is when that provider's last pass ended healthy, or null when its last pass
	// did not, or none ran.
	LastSuccessAt *int64 `json:"last_success_at"`
	// Condition is the provider kind's condition (docs/widget-api.md, "Condition codes").
	Condition string `json:"condition"`
}

// ListedColumn is one column of a listed dataset.
type ListedColumn struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Role          string   `json:"role"`
	Unit          *string  `json:"unit"`
	Nullable      bool     `json:"nullable"`
	CarriesDomain bool     `json:"carries_domain"`
	Aggregations  []string `json:"aggregations"`
}

// DatasetListing is GET /api/datasets.
type DatasetListing struct {
	Datasets []ListedDataset `json:"datasets"`
}

// datasetFreshness finds the provider that dates a dataset: the named one, or the kind's active
// one.
func FreshnessOf(freshness dataset.Freshness, providers []envelope.ProviderAvailability) DatasetFreshness {
	result := DatasetFreshness{ProviderKind: freshness.Kind,
		Condition: request.KindCondition(providers, freshness.Kind)}
	for _, provider := range providers {
		if provider.Kind != freshness.Kind {
			continue
		}
		if freshness.ProviderKey != "" && provider.ProviderKey != freshness.ProviderKey {
			continue
		}
		if freshness.ProviderKey == "" && !provider.Active {
			continue
		}
		key := provider.ProviderKey
		result.ProviderKey = &key
		if provider.Outcome != nil && provider.CheckedAt != nil &&
			(*provider.Outcome == string(vocab.OutcomeHealthy) || *provider.Outcome == string(vocab.OutcomeHealthyEmpty)) {
			at := *provider.CheckedAt
			result.LastSuccessAt = &at
		}
		break
	}
	return result
}

// RetentionSeconds reads the retention_seconds setting; 0 is unlimited.
func RetentionSeconds(r *request.Request) (int64, error) {
	text, err := r.Setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
	if err != nil {
		return 0, err
	}
	seconds, _ := strconv.ParseInt(text, 10, 64)
	return seconds, nil
}

// DatasetResponse is one catalogue query's answer: the widget API's envelope, naming the dataset
// where a widget's names its type.
type DatasetResponse struct {
	Dataset           string                          `json:"dataset"`
	Parameters        map[string]any                  `json:"parameters"`
	UnknownParameters map[string]json.RawMessage      `json:"unknown_parameters"`
	InvalidParameters []dataset.Invalid               `json:"invalid_parameters"`
	MissingParameters []string                        `json:"missing_parameters"`
	Window            *envelope.WindowReport          `json:"window"`
	Gaps              []envelope.Gap                  `json:"gaps"`
	Availability      []envelope.ProviderAvailability `json:"availability"`
	Condition         string                          `json:"condition"`
	Freshness         []envelope.Freshness            `json:"freshness"`
	AggregateMode     string                          `json:"aggregate_mode"`
	Withheld          []string                        `json:"withheld"`
	States            []string                        `json:"states"`
	Data              any                             `json:"data"`
}

// DatasetTimeData is the data of a query on the time axis: one series per measure, or per measure
// and split value with the remainder last, every series on the same buckets.
type DatasetTimeData struct {
	X      DatasetTimeAxis `json:"x"`
	Series []DatasetSeries `json:"series"`
}

// DatasetTimeAxis is the time axis: its column, and its buckets' width and alignment. A calendar
// bucket -- an ISO week, a month -- has no fixed width.
type DatasetTimeAxis struct {
	Column        string `json:"column"`
	BucketSeconds *int64 `json:"bucket_seconds"`
	Alignment     string `json:"alignment"`
}

// DatasetSeries is one series.
type DatasetSeries struct {
	Key         string           `json:"key"`
	Column      *string          `json:"column"`
	Aggregation string           `json:"aggregation"`
	Unit        *string          `json:"unit"`
	Split       *DatasetSplit    `json:"split"`
	Buckets     []traffic.Bucket `json:"buckets"`
}

// DatasetSplit is the split value a series or a row is for: one value of the split column, or the
// remainder of every other value.
type DatasetSplit struct {
	Column    string `json:"column"`
	Value     any    `json:"value"`
	Remainder bool   `json:"remainder"`
}

// DatasetRowsData is the data of a query on a dimension axis: the ranked keys, how many keys there
// are in all, and one value per measure.
type DatasetRowsData struct {
	X        DatasetDimensionAxis `json:"x"`
	Members  int64                `json:"members"`
	Measures []DatasetMeasure     `json:"measures"`
	Rows     []DatasetRow         `json:"rows"`
}

// DatasetDimensionAxis is the dimension axis.
type DatasetDimensionAxis struct {
	Column string `json:"column"`
}

// DatasetMeasure names one measure of the rows.
type DatasetMeasure struct {
	Column      *string `json:"column"`
	Aggregation string  `json:"aggregation"`
	Unit        *string `json:"unit"`
}

// DatasetRow is one key, and one split value of it when the query splits.
type DatasetRow struct {
	Key    any           `json:"key"`
	Split  *DatasetSplit `json:"split,omitempty"`
	Values []*float64    `json:"values"`
}

// cellNumber reads a numeric cell.
func cellNumber(cell any) (float64, bool) {
	switch value := cell.(type) {
	case int64:
		return float64(value), true
	case float64:
		return value, true
	case int32:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		return parsed, err == nil
	}
	return 0, false
}

// additive reports whether an aggregation of no row is a true zero.
func additive(aggregation string) bool {
	return aggregation == dataset.AggregationSum || aggregation == dataset.AggregationCount ||
		aggregation == dataset.AggregationCountDistinct
}

// measureUnit is a measure's declared unit, or null.
func measureUnit(declaration dataset.Declaration, measure dataset.Measure) (*string, *string) {
	if measure.Column == "" {
		return nil, nil
	}
	column := measure.Column
	declared, _ := declaration.Column(measure.Column)
	if declared.Unit == "" || measure.Aggregation == dataset.AggregationCount ||
		measure.Aggregation == dataset.AggregationCountDistinct {
		return &column, nil
	}
	unit := declared.Unit
	return &column, &unit
}

// datasetBuckets are the buckets of a window on a time axis, each clipped to it.
func datasetBuckets(window envelope.Interval, width int64, alignment string) []envelope.Interval {
	var period vocab.Period
	switch alignment {
	case dataset.AlignmentISOWeek:
		period = vocab.PeriodWeek
	case dataset.AlignmentCalendarMonth:
		period = vocab.PeriodMonth
	default:
		return traffic.BucketIntervals(window, width)
	}
	var result []envelope.Interval
	for _, start := range period.Slots(window.From, window.To) {
		result = append(result, envelope.Interval{From: max(window.From, start), To: min(window.To, period.SlotEnd(start))})
	}
	return result
}

// bucketStart is the start of the bucket an instant falls in.
func bucketStart(at, width int64, alignment string) int64 {
	switch alignment {
	case dataset.AlignmentISOWeek:
		return vocab.PeriodWeek.SlotStart(at)
	case dataset.AlignmentCalendarMonth:
		return vocab.PeriodMonth.SlotStart(at)
	}
	return at - traffic.FloorMod(at, width)
}

// splitIdentity is one split value of a time series: its rank, or the remainder's.
type splitIdentity struct {
	rank  int64
	value any
}

// TimeData turns a time axis' rows into series.
func TimeData(declaration dataset.Declaration, query *dataset.Query, layout dataset.Layout, rows [][]any,
	window envelope.Interval, covered *envelope.Interval, gaps []envelope.Gap) DatasetTimeData {
	_, width, alignment := query.TimeAxis()
	axis := DatasetTimeAxis{Column: query.X.Column, Alignment: alignment}
	if alignment == dataset.AlignmentEpoch {
		bucketWidth := width
		axis.BucketSeconds = &bucketWidth
	}
	offset := 1
	if layout.Split {
		offset = 3
	}
	// The values, by split and bucket start, then measure.
	values := map[splitIdentity]map[int64][]*float64{}
	var splits []splitIdentity
	for _, row := range rows {
		start, _ := cellNumber(row[0])
		identity := splitIdentity{rank: -1}
		if layout.Split {
			if rank, ok := cellNumber(row[1]); ok {
				identity = splitIdentity{rank: int64(rank), value: row[2]}
			}
		}
		if values[identity] == nil {
			values[identity] = map[int64][]*float64{}
			splits = append(splits, identity)
		}
		measured := make([]*float64, layout.Measures)
		for index := range layout.Measures {
			if value, ok := cellNumber(row[offset+index]); ok {
				measured[index] = &value
			}
		}
		values[identity][int64(start)] = measured
	}
	if !layout.Split {
		splits = []splitIdentity{{rank: -1}}
		if values[splits[0]] == nil {
			values[splits[0]] = map[int64][]*float64{}
		}
	}
	slices.SortFunc(splits, func(a, b splitIdentity) int {
		// The ranked values in their rank, the remainder last.
		switch {
		case a.rank == b.rank:
			return 0
		case a.rank < 0:
			return 1
		case b.rank < 0:
			return -1
		case a.rank < b.rank:
			return -1
		}
		return 1
	})
	data := DatasetTimeData{X: axis, Series: []DatasetSeries{}}
	intervals := datasetBuckets(window, width, alignment)
	for measureIndex, measure := range query.Y {
		column, unit := measureUnit(declaration, measure)
		for _, identity := range splits {
			series := DatasetSeries{Key: "y" + strconv.Itoa(measureIndex), Column: column,
				Aggregation: measure.Aggregation, Unit: unit, Buckets: []traffic.Bucket{}}
			if layout.Split {
				split := &DatasetSplit{Column: query.SplitBy.Column}
				if identity.rank < 0 {
					split.Remainder = true
					series.Key += ":remainder"
				} else {
					split.Value = identity.value
					series.Key += ":split:" + strconv.FormatInt(identity.rank, 10)
				}
				series.Split = split
			}
			for _, interval := range intervals {
				bucket := traffic.Bucket{From: interval.From, To: interval.To, Gap: traffic.IsGapBucket(interval, covered, gaps)}
				measured, present := values[identity][bucketStart(interval.From, width, alignment)]
				switch {
				case present:
					bucket.Value = measured[measureIndex]
				case declaration.Nature == dataset.NatureSample:
					// A sample dataset's bucket with no reading is a gap, never a zero.
					bucket.Gap = true
				case !bucket.Gap && additive(measure.Aggregation):
					zero := 0.0
					bucket.Value = &zero
				}
				series.Buckets = append(series.Buckets, bucket)
			}
			data.Series = append(data.Series, series)
		}
	}
	return data
}

// RowsData turns a dimension axis' rows into ranked rows.
func RowsData(declaration dataset.Declaration, query *dataset.Query, layout dataset.Layout,
	rows [][]any) DatasetRowsData {
	data := DatasetRowsData{X: DatasetDimensionAxis{Column: query.X.Column}, Measures: []DatasetMeasure{},
		Rows: []DatasetRow{}}
	for _, measure := range query.Y {
		column, unit := measureUnit(declaration, measure)
		data.Measures = append(data.Measures, DatasetMeasure{Column: column, Aggregation: measure.Aggregation,
			Unit: unit})
	}
	offset := 3
	if layout.Split {
		offset = 5
	}
	for _, row := range rows {
		if members, ok := cellNumber(row[1]); ok {
			data.Members = int64(members)
		}
		result := DatasetRow{Key: row[2], Values: make([]*float64, layout.Measures)}
		if layout.Split {
			split := &DatasetSplit{Column: query.SplitBy.Column}
			if _, ranked := cellNumber(row[3]); ranked {
				split.Value = row[4]
			} else {
				split.Remainder = true
			}
			result.Split = split
		}
		for index := range layout.Measures {
			if value, ok := cellNumber(row[offset+index]); ok {
				result.Values[index] = &value
			}
		}
		data.Rows = append(data.Rows, result)
	}
	return data
}

// The states of a latest read.
const (
	LatestCurrent = "current"
	LatestNoRows  = "no_rows"
	// LatestUnknownDataset is the data catalogue API's refusal code for a name it does not hold
	// (GET /api/datasets/<name>/query), so a script and an API client read one word for it.
	LatestUnknownDataset = dataset.RefusalUnknownDataset
)

// LatestRows is what host.datasets.latest answers.
type LatestRows struct {
	Dataset   string           `json:"dataset"`
	State     string           `json:"state"`
	At        *int64           `json:"at"`
	Condition *string          `json:"condition"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
	Withheld  []string         `json:"withheld"`
}

// LatestRows reads a dataset's latest rows; a refusal is a filter the catalogue does not admit.
func ReadLatestRows(r *request.Request, name string, filter json.RawMessage) (LatestRows, *dataset.Refusal, error) {
	result := LatestRows{Dataset: name, State: LatestUnknownDataset, Rows: []map[string]any{}, Withheld: []string{}}
	declaration, found := r.Datasets.Lookup(name)
	if !found {
		return result, nil, nil
	}
	query, refusal := dataset.ParseFilters(filter, declaration)
	if refusal != nil {
		return result, refusal, nil
	}
	providers, err := request.ReadProviders(r.Ctx, r.Db)
	if err != nil {
		return result, nil, err
	}
	condition := request.KindCondition(providers, declaration.Freshness.Kind)
	result.Condition = &condition
	if r.NoDomains() && query.FiltersTouchDomain(declaration) {
		result.State = envelope.ConditionWithheldByAggregateMode
		return result, nil, nil
	}
	columns := make([]string, 0, len(declaration.Columns))
	for _, column := range declaration.Columns {
		if r.NoDomains() && column.CarriesDomain {
			result.Withheld = append(result.Withheld, column.Name)
			continue
		}
		columns = append(columns, column.Name)
	}
	window := dataset.Window{From: 0, To: r.Now + 1}
	retention := r.Retention
	if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
		retention = declaration.Retention.Seconds
	}
	if retention > 0 && r.Now > retention {
		window.From = r.Now - retention
	}
	statement, layout := dataset.CompileLatest(declaration, query, window, columns, dataset.MaxRows+1)
	err = r.Db.CatalogueRows(r.Ctx, statement.Text, statement.Args, dataset.StatementTimeout,
		func(cells []any) error {
			if len(result.Rows) == dataset.MaxRows {
				result.Truncated = true
				return nil
			}
			if at, ok := cellNumber(cells[0]); ok {
				instant := int64(at)
				result.At = &instant
			}
			row := make(map[string]any, len(layout.Columns))
			for index, column := range layout.Columns {
				row[column] = cells[index+1]
			}
			result.Rows = append(result.Rows, row)
			return nil
		})
	if err != nil {
		return result, nil, err
	}
	result.State = LatestNoRows
	if len(result.Rows) > 0 {
		result.State = LatestCurrent
	}
	return result, nil, nil
}
