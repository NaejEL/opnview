package widget

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/store"
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

// Datasets returns the catalogue the service answers from.
func (s *Service) Datasets() *dataset.Catalogue { return s.datasets }

// ListDatasets lists every dataset of the catalogue.
func (s *Service) ListDatasets(ctx context.Context) (DatasetListing, error) {
	request := &Request{Ctx: ctx, Db: s.store, Now: s.Now().Unix()}
	retention, err := request.retentionSeconds()
	if err != nil {
		return DatasetListing{}, err
	}
	providers, err := ReadProviders(ctx, s.store)
	if err != nil {
		return DatasetListing{}, err
	}
	listing := DatasetListing{Datasets: []ListedDataset{}}
	for _, declaration := range s.datasets.Declarations() {
		listed := ListedDataset{Name: declaration.Name, Origin: declaration.Origin, Label: declaration.Label,
			Nature: declaration.Nature, Columns: []ListedColumn{},
			Freshness: datasetFreshness(declaration.Freshness, providers), LossDetectable: declaration.LossDetectable}
		if column, timed := declaration.TimeColumn(); timed {
			name := column.Name
			listed.TimeColumn = &name
			seconds := retention
			if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
				seconds = declaration.Retention.Seconds
			}
			if seconds > 0 {
				horizon := request.Now - seconds
				listed.RetentionSeconds, listed.RetentionHorizonAt = &seconds, &horizon
			}
		}
		for _, column := range declaration.Columns {
			listedColumn := ListedColumn{Name: column.Name, Type: column.Type, Role: column.Role,
				Nullable: column.Nullable, CarriesDomain: column.CarriesDomain,
				Aggregations: append([]string{}, column.Aggregations...)}
			if column.Unit != "" {
				unit := column.Unit
				listedColumn.Unit = &unit
			}
			listed.Columns = append(listed.Columns, listedColumn)
		}
		listing.Datasets = append(listing.Datasets, listed)
	}
	return listing, nil
}

// datasetFreshness finds the provider that dates a dataset: the named one, or the kind's active
// one.
func datasetFreshness(freshness dataset.Freshness, providers []ProviderAvailability) DatasetFreshness {
	result := DatasetFreshness{ProviderKind: freshness.Kind,
		Condition: KindCondition(providers, freshness.Kind)}
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
			(*provider.Outcome == string(store.OutcomeHealthy) || *provider.Outcome == string(store.OutcomeHealthyEmpty)) {
			at := *provider.CheckedAt
			result.LastSuccessAt = &at
		}
		break
	}
	return result
}

// retentionSeconds reads the retention_seconds setting; 0 is unlimited.
func (r *Request) retentionSeconds() (int64, error) {
	text, err := r.setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
	if err != nil {
		return 0, err
	}
	seconds, _ := strconv.ParseInt(text, 10, 64)
	return seconds, nil
}

// DatasetResponse is one catalogue query's answer: the widget API's envelope, naming the dataset
// where a widget's names its type.
type DatasetResponse struct {
	Dataset           string                     `json:"dataset"`
	Parameters        map[string]any             `json:"parameters"`
	UnknownParameters map[string]json.RawMessage `json:"unknown_parameters"`
	InvalidParameters []dataset.Invalid          `json:"invalid_parameters"`
	MissingParameters []string                   `json:"missing_parameters"`
	Window            *WindowReport              `json:"window"`
	Gaps              []Gap                      `json:"gaps"`
	Availability      []ProviderAvailability     `json:"availability"`
	Condition         string                     `json:"condition"`
	Freshness         []Freshness                `json:"freshness"`
	AggregateMode     string                     `json:"aggregate_mode"`
	Withheld          []string                   `json:"withheld"`
	States            []string                   `json:"states"`
	Data              any                        `json:"data"`
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
	Key         string        `json:"key"`
	Column      *string       `json:"column"`
	Aggregation string        `json:"aggregation"`
	Unit        *string       `json:"unit"`
	Split       *DatasetSplit `json:"split"`
	Buckets     []Bucket      `json:"buckets"`
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

// QueryDataset answers one catalogue query. A refusal is returned as such, with no statement run.
func (s *Service) QueryDataset(ctx context.Context, name, rawParameters string) (DatasetResponse, *dataset.Refusal,
	error) {
	declaration, found := s.datasets.Lookup(name)
	if !found {
		return DatasetResponse{}, &dataset.Refusal{Code: dataset.RefusalUnknownDataset}, nil
	}
	query, refusal := dataset.Parse(rawParameters, declaration)
	if refusal != nil {
		return DatasetResponse{}, refusal, nil
	}
	request := &Request{Ctx: ctx, Db: s.store, Now: s.Now().Unix()}
	mode, err := request.setting(config.KeyAggregateMode, aggregateModeFull)
	if err != nil {
		return DatasetResponse{}, nil, err
	}
	request.mode = mode
	response := DatasetResponse{Dataset: name, Parameters: query.Effective(), UnknownParameters: query.Unknown,
		InvalidParameters: query.Invalid, MissingParameters: []string{}, Gaps: []Gap{},
		Availability: []ProviderAvailability{}, Freshness: []Freshness{}, AggregateMode: mode, Withheld: []string{},
		States: []string{}}
	kind := declaration.Freshness.Kind
	providers, err := ReadProviders(ctx, s.store)
	if err != nil {
		return DatasetResponse{}, nil, err
	}
	for _, provider := range providers {
		if provider.Kind == kind {
			response.Availability = append(response.Availability, provider)
		}
	}
	if request.NoDomains() && query.TouchesDomain(declaration) {
		response.Condition = ConditionWithheldByAggregateMode
		response.Withheld = append(response.Withheld, "data")
		return response, nil, nil
	}

	window := dataset.Window{}
	var covered *Interval
	if _, timed := declaration.TimeColumn(); timed {
		window = dataset.Window{From: request.Now - dataset.PeriodSeconds[query.Period], To: request.Now}
		interval := Interval{From: window.From, To: window.To}
		report := &WindowReport{Requested: interval, Coverage: CoverageNone}
		retention, err := request.retentionSeconds()
		if err != nil {
			return DatasetResponse{}, nil, err
		}
		if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
			retention = declaration.Retention.Seconds
		}
		if retention > 0 {
			horizon := request.Now - retention
			report.FlowHorizonAt = &horizon
		}
		response.Gaps, err = request.ReadGaps([]string{kind}, interval)
		if err != nil {
			return DatasetResponse{}, nil, err
		}
		earliest, has, err := request.datasetEarliest(declaration)
		if err != nil {
			return DatasetResponse{}, nil, err
		}
		if has {
			candidate := Interval{From: max(window.From, earliest), To: min(window.To, request.Now)}
			if candidate.To > candidate.From {
				covered = &candidate
			}
		}
		switch {
		case covered == nil:
			report.Coverage = CoverageNone
		case covered.From <= interval.From && covered.To >= interval.To:
			report.Covered, report.Coverage = covered, CoverageFull
		default:
			report.Covered, report.Coverage = covered, CoveragePartial
		}
		response.Window = report
	}

	statement := dataset.Compile(declaration, query, window)
	var rows [][]any
	if err := s.store.CatalogueRows(ctx, statement.Text, statement.Args, dataset.StatementTimeout,
		func(cells []any) error {
			rows = append(rows, cells)
			return nil
		}); err != nil {
		return DatasetResponse{}, nil, err
	}
	var data any
	if statement.Layout.TimeAxis {
		data = timeData(declaration, query, statement.Layout, rows, Interval{From: window.From, To: window.To},
			covered, response.Gaps)
	} else {
		data = rowsData(declaration, query, statement.Layout, rows)
	}
	hasRows := len(rows) > 0
	response.Condition = KindCondition(response.Availability, kind)
	if response.Condition == ConditionReachable && !hasRows {
		response.Condition = ConditionReachableNoRows
	}
	if response.Condition != ConditionReachable && response.Condition != ConditionReachableNoRows && !hasRows {
		// A degraded source with nothing stored: no data at all, never a zero.
		return response, nil, nil
	}
	response.Data = data
	return response, nil, nil
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
func datasetBuckets(window Interval, width int64, alignment string) []Interval {
	var period store.Period
	switch alignment {
	case dataset.AlignmentISOWeek:
		period = store.PeriodWeek
	case dataset.AlignmentCalendarMonth:
		period = store.PeriodMonth
	default:
		return BucketIntervals(window, width)
	}
	var result []Interval
	for _, start := range period.Slots(window.From, window.To) {
		result = append(result, Interval{From: max(window.From, start), To: min(window.To, period.SlotEnd(start))})
	}
	return result
}

// bucketStart is the start of the bucket an instant falls in.
func bucketStart(at, width int64, alignment string) int64 {
	switch alignment {
	case dataset.AlignmentISOWeek:
		return store.PeriodWeek.SlotStart(at)
	case dataset.AlignmentCalendarMonth:
		return store.PeriodMonth.SlotStart(at)
	}
	return at - FloorMod(at, width)
}

// splitIdentity is one split value of a time series: its rank, or the remainder's.
type splitIdentity struct {
	rank  int64
	value any
}

// timeData turns a time axis' rows into series.
func timeData(declaration dataset.Declaration, query *dataset.Query, layout dataset.Layout, rows [][]any,
	window Interval, covered *Interval, gaps []Gap) DatasetTimeData {
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
				Aggregation: measure.Aggregation, Unit: unit, Buckets: []Bucket{}}
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
				bucket := Bucket{From: interval.From, To: interval.To, Gap: IsGapBucket(interval, covered, gaps)}
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

// rowsData turns a dimension axis' rows into ranked rows.
func rowsData(declaration dataset.Declaration, query *dataset.Query, layout dataset.Layout,
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
