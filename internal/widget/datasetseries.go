package widget

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/NaejEL/opnview/internal/dataset"
)

// custom_chart generalised (P8 of specs/SPEC-backend-completion-plugins.md): a series' source may
// be any dataset of the data catalogue with a time column, its measure any measure column of it,
// and its aggregation one that column admits. The five sources of docs/widget-api.md, "Series of
// custom_chart", keep their own reads, so their answers are unchanged byte for byte; a dataset that
// shares a name with one of them is read as that source.

// seriesDataset returns the catalogue dataset a series entry's source names, when it names one
// that is not one of the five sources and has a time column.
func seriesDataset(raw json.RawMessage, datasets *dataset.Catalogue) (dataset.Declaration, bool) {
	if datasets == nil || raw == nil {
		return dataset.Declaration{}, false
	}
	var name string
	if json.Unmarshal(raw, &name) != nil || slices.Contains(SeriesSources, name) {
		return dataset.Declaration{}, false
	}
	declaration, found := datasets.Lookup(name)
	if !found {
		return dataset.Declaration{}, false
	}
	if _, timed := declaration.TimeColumn(); !timed {
		return dataset.Declaration{}, false
	}
	return declaration, true
}

// measureColumns are a dataset's measure columns, in order.
func measureColumns(declaration dataset.Declaration) []dataset.Column {
	var result []dataset.Column
	for _, column := range declaration.Columns {
		if column.Role == dataset.RoleMeasure {
			result = append(result, column)
		}
	}
	return result
}

// parseDatasetSeries parses the keys of a series entry read from a dataset: measure among its
// measure columns, aggregation among what that column admits, and the rendering keys as for any
// series. A key that applies to the five sources only -- metric, a reference -- is invalid here.
func parseDatasetSeries(path string, fields map[string]json.RawMessage, entry map[string]any,
	declaration dataset.Declaration, invalid *[]InvalidValue) {
	text := func(field string, vocabulary []string, fallback string) {
		value, present := fields[field]
		if !present || isJSONNull(value) {
			if fallback != "" {
				entry[field] = fallback
			}
			return
		}
		var given string
		reason := ""
		switch {
		case json.Unmarshal(value, &given) != nil:
			reason = ReasonWrongType
		case vocabulary != nil && !Contains(vocabulary, given):
			reason = ReasonNotInVocabulary
		}
		if reason != "" {
			*invalid = append(*invalid, InvalidValue{Path: path + "." + field, Value: value, Reason: reason})
			if fallback != "" {
				entry[field] = fallback
			}
			return
		}
		entry[field] = given
	}
	measures := measureColumns(declaration)
	names := make([]string, 0, len(measures))
	for _, column := range measures {
		names = append(names, column.Name)
	}
	fallback := ""
	if len(names) > 0 {
		fallback = names[0]
	}
	text("measure", names, fallback)
	measure, _ := entry["measure"].(string)
	admitted := []string{dataset.AggregationCount}
	if column, found := declaration.Column(measure); found {
		admitted = column.Aggregations
	}
	text("aggregation", admitted, defaultDatasetAggregation(declaration, admitted))
	text("axis", SeriesAxes, "left")
	text("label", nil, "")
	text("render", SeriesRenders, "line")
	var inapplicable []string
	for field := range fields {
		if field == "metric" || slices.Contains(seriesReferenceKeys, field) {
			inapplicable = append(inapplicable, field)
		}
	}
	sort.Strings(inapplicable)
	for _, field := range inapplicable {
		if !isJSONNull(fields[field]) {
			*invalid = append(*invalid, InvalidValue{Path: path + "." + field, Value: fields[field],
				Reason: ReasonNotInVocabulary})
		}
	}
}

// defaultDatasetAggregation is a dataset series' aggregation when its entry names none: a sum over
// an event dataset, a mean over a sample one, where the column admits it, else its first.
func defaultDatasetAggregation(declaration dataset.Declaration, admitted []string) string {
	preferred := dataset.AggregationMean
	if declaration.Nature == dataset.NatureEvent {
		preferred = dataset.AggregationSum
	}
	if slices.Contains(admitted, preferred) || len(admitted) == 0 {
		return preferred
	}
	return admitted[0]
}

// SeriesDataset returns the catalogue dataset a parsed series entry reads, if it reads one.
func (r *Request) SeriesDataset(entry SeriesEntry) (dataset.Declaration, bool) {
	if slices.Contains(SeriesSources, entry.Source) {
		return dataset.Declaration{}, false
	}
	return r.datasets.Lookup(entry.Source)
}

// FillDatasetSeries reads one custom_chart series from a catalogue dataset onto the chart's
// buckets: the window's coverage from the dataset's oldest row, the gaps already in the series,
// and a bucket with no row a zero only where the dataset is an event dataset and the aggregation
// is a sum or a count -- a gap otherwise.
func (r *Request) FillDatasetSeries(series *Series, entry SeriesEntry, width int64) (bool, error) {
	declaration, found := r.SeriesDataset(entry)
	if !found {
		return false, nil
	}
	timeColumn, _ := declaration.TimeColumn()
	measure := dataset.Measure{Column: entry.Measure, Aggregation: entry.Aggregation}
	if _, declared := declaration.Column(entry.Measure); !declared {
		measure.Column = ""
	}
	parameters, _ := json.Marshal(map[string]any{
		"period": r.Params.Text("period"),
		"x":      map[string]any{"column": timeColumn.Name, "bucket": width},
		"y":      []dataset.Measure{measure},
	})
	query, refusal := dataset.Parse(string(parameters), declaration)
	if refusal != nil {
		// The series was parsed against the same declaration; a refusal here is a defect.
		return false, refusal
	}
	window := series.Window
	earliest, has, err := r.datasetEarliest(declaration)
	if err != nil {
		return false, err
	}
	if has {
		covered := &Interval{From: max(r.Window.From, earliest), To: min(r.Window.To, r.Now)}
		if covered.To <= covered.From {
			covered = nil
		}
		window.Covered = covered
		switch {
		case covered == nil:
			window.Coverage = CoverageNone
		case covered.From <= window.Requested.From && covered.To >= window.Requested.To:
			window.Coverage = CoverageFull
		default:
			window.Coverage = CoveragePartial
		}
	}
	statement := dataset.Compile(declaration, query, dataset.Window{From: r.Window.From, To: r.Window.To})
	var rows [][]any
	if err := r.Db.CatalogueRows(r.Ctx, statement.Text, statement.Args, dataset.StatementTimeout,
		func(cells []any) error {
			rows = append(rows, cells)
			return nil
		}); err != nil {
		return false, err
	}
	data := timeData(declaration, query, statement.Layout, rows, r.Window, window.Covered, series.Gaps)
	series.Measure = entry.Measure
	if column, declared := declaration.Column(entry.Measure); declared {
		series.Unit = column.Unit
	}
	series.Buckets = []Bucket{}
	var total float64
	for _, bucket := range data.Series[0].Buckets {
		if bucket.Value != nil {
			total += *bucket.Value
		}
		series.Buckets = append(series.Buckets, bucket)
	}
	if additive(entry.Aggregation) {
		series.Total = &total
	}
	return len(rows) > 0, nil
}

// datasetEarliest reads the instant of a dataset's oldest row.
func (r *Request) datasetEarliest(declaration dataset.Declaration) (int64, bool, error) {
	text := dataset.EarliestStatement(declaration)
	if text == "" {
		return 0, false, nil
	}
	var (
		earliest int64
		has      bool
	)
	err := r.Db.CatalogueRows(r.Ctx, text, nil, dataset.StatementTimeout, func(cells []any) error {
		if value, ok := cellNumber(cells[0]); ok {
			earliest, has = int64(value), true
		}
		return nil
	})
	return earliest, has, err
}
