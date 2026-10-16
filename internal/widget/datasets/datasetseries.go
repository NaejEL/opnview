package datasets

import (
	"encoding/json"
	"slices"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// custom_chart generalised (P8 of specs/SPEC-backend-completion-plugins.md): a series' source may
// be any dataset of the data catalogue with a time column, its measure any measure column of it,
// and its aggregation one that column admits. The five sources of docs/widget-api.md, "Series of
// custom_chart", keep their own reads, so their answers are unchanged byte for byte; a dataset that
// shares a name with one of them is read as that source.

// SeriesDataset returns the catalogue dataset a parsed series entry reads, if it reads one.
func SeriesDataset(r *request.Request, entry params.SeriesEntry) (dataset.Declaration, bool) {
	if slices.Contains(params.SeriesSources, entry.Source) {
		return dataset.Declaration{}, false
	}
	return r.Datasets.Lookup(entry.Source)
}

// FillDatasetSeries reads one custom_chart series from a catalogue dataset onto the chart's
// buckets: the window's coverage from the dataset's oldest row, the gaps already in the series,
// and a bucket with no row a zero only where the dataset is an event dataset and the aggregation
// is a sum or a count -- a gap otherwise.
func FillDatasetSeries(r *request.Request, series *traffic.Series, entry params.SeriesEntry, width int64) (bool, error) {
	declaration, found := SeriesDataset(r, entry)
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
	earliest, has, err := DatasetEarliest(r, declaration)
	if err != nil {
		return false, err
	}
	if has {
		covered := &envelope.Interval{From: max(r.Window.From, earliest), To: min(r.Window.To, r.Now)}
		if covered.To <= covered.From {
			covered = nil
		}
		window.Covered = covered
		switch {
		case covered == nil:
			window.Coverage = request.CoverageNone
		case covered.From <= window.Requested.From && covered.To >= window.Requested.To:
			window.Coverage = request.CoverageFull
		default:
			window.Coverage = request.CoveragePartial
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
	data := TimeData(declaration, query, statement.Layout, rows, r.Window, window.Covered, series.Gaps)
	series.Measure = entry.Measure
	if column, declared := declaration.Column(entry.Measure); declared {
		series.Unit = column.Unit
	}
	series.Buckets = []traffic.Bucket{}
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

// DatasetEarliest reads the instant of a dataset's oldest row.
func DatasetEarliest(r *request.Request, declaration dataset.Declaration) (int64, bool, error) {
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
