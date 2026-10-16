package widget

import (
	"context"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/widget/datasets"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// Datasets returns the catalogue the service answers from.
func (s *Service) Datasets() *dataset.Catalogue { return s.datasets }

// ListDatasets lists every dataset of the catalogue.
func (s *Service) ListDatasets(ctx context.Context) (datasets.DatasetListing, error) {
	widgetRequest := &request.Request{Ctx: ctx, Db: s.store, Now: s.Now().Unix()}
	retention, err := datasets.RetentionSeconds(widgetRequest)
	if err != nil {
		return datasets.DatasetListing{}, err
	}
	providers, err := request.ReadProviders(ctx, s.store)
	if err != nil {
		return datasets.DatasetListing{}, err
	}
	listing := datasets.DatasetListing{Datasets: []datasets.ListedDataset{}}
	for _, declaration := range s.datasets.Declarations() {
		listed := datasets.ListedDataset{Name: declaration.Name, Origin: declaration.Origin, Label: declaration.Label,
			Nature: declaration.Nature, Columns: []datasets.ListedColumn{},
			Freshness: datasets.FreshnessOf(declaration.Freshness, providers), LossDetectable: declaration.LossDetectable}
		if column, timed := declaration.TimeColumn(); timed {
			name := column.Name
			listed.TimeColumn = &name
			seconds := retention
			if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
				seconds = declaration.Retention.Seconds
			}
			if seconds > 0 {
				horizon := widgetRequest.Now - seconds
				listed.RetentionSeconds, listed.RetentionHorizonAt = &seconds, &horizon
			}
		}
		for _, column := range declaration.Columns {
			listedColumn := datasets.ListedColumn{Name: column.Name, Type: column.Type, Role: column.Role,
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

// QueryDataset answers one catalogue query. A refusal is returned as such, with no statement run.
func (s *Service) QueryDataset(ctx context.Context, name, rawParameters string) (datasets.DatasetResponse, *dataset.Refusal,
	error) {
	declaration, found := s.datasets.Lookup(name)
	if !found {
		return datasets.DatasetResponse{}, &dataset.Refusal{Code: dataset.RefusalUnknownDataset}, nil
	}
	query, refusal := dataset.Parse(rawParameters, declaration)
	if refusal != nil {
		return datasets.DatasetResponse{}, refusal, nil
	}
	widgetRequest := &request.Request{Ctx: ctx, Db: s.store, Now: s.Now().Unix()}
	mode, err := widgetRequest.Setting(config.KeyAggregateMode, request.AggregateModeFull)
	if err != nil {
		return datasets.DatasetResponse{}, nil, err
	}
	widgetRequest.Mode = mode
	response := datasets.DatasetResponse{Dataset: name, Parameters: query.Effective(), UnknownParameters: query.Unknown,
		InvalidParameters: query.Invalid, MissingParameters: []string{}, Gaps: []envelope.Gap{},
		Availability: []envelope.ProviderAvailability{}, Freshness: []envelope.Freshness{}, AggregateMode: mode, Withheld: []string{},
		States: []string{}}
	kind := declaration.Freshness.Kind
	providers, err := request.ReadProviders(ctx, s.store)
	if err != nil {
		return datasets.DatasetResponse{}, nil, err
	}
	for _, provider := range providers {
		if provider.Kind == kind {
			response.Availability = append(response.Availability, provider)
		}
	}
	if widgetRequest.NoDomains() && query.TouchesDomain(declaration) {
		response.Condition = envelope.ConditionWithheldByAggregateMode
		response.Withheld = append(response.Withheld, "data")
		return response, nil, nil
	}

	window := dataset.Window{}
	var covered *envelope.Interval
	if _, timed := declaration.TimeColumn(); timed {
		window = dataset.Window{From: widgetRequest.Now - dataset.PeriodSeconds[query.Period], To: widgetRequest.Now}
		interval := envelope.Interval{From: window.From, To: window.To}
		report := &envelope.WindowReport{Requested: interval, Coverage: request.CoverageNone}
		retention, err := datasets.RetentionSeconds(widgetRequest)
		if err != nil {
			return datasets.DatasetResponse{}, nil, err
		}
		if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
			retention = declaration.Retention.Seconds
		}
		if retention > 0 {
			horizon := widgetRequest.Now - retention
			report.FlowHorizonAt = &horizon
		}
		response.Gaps, err = widgetRequest.ReadGaps([]string{kind}, interval)
		if err != nil {
			return datasets.DatasetResponse{}, nil, err
		}
		earliest, has, err := datasets.DatasetEarliest(widgetRequest, declaration)
		if err != nil {
			return datasets.DatasetResponse{}, nil, err
		}
		if has {
			candidate := envelope.Interval{From: max(window.From, earliest), To: min(window.To, widgetRequest.Now)}
			if candidate.To > candidate.From {
				covered = &candidate
			}
		}
		switch {
		case covered == nil:
			report.Coverage = request.CoverageNone
		case covered.From <= interval.From && covered.To >= interval.To:
			report.Covered, report.Coverage = covered, request.CoverageFull
		default:
			report.Covered, report.Coverage = covered, request.CoveragePartial
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
		return datasets.DatasetResponse{}, nil, err
	}
	var data any
	if statement.Layout.TimeAxis {
		data = datasets.TimeData(declaration, query, statement.Layout, rows, envelope.Interval{From: window.From, To: window.To},
			covered, response.Gaps)
	} else {
		data = datasets.RowsData(declaration, query, statement.Layout, rows)
	}
	hasRows := len(rows) > 0
	response.Condition = request.KindCondition(response.Availability, kind)
	if response.Condition == envelope.ConditionReachable && !hasRows {
		response.Condition = envelope.ConditionReachableNoRows
	}
	if response.Condition != envelope.ConditionReachable && response.Condition != envelope.ConditionReachableNoRows && !hasRows {
		// A degraded source with nothing stored: no data at all, never a zero.
		return response, nil, nil
	}
	response.Data = data
	return response, nil, nil
}
