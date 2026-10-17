package traffic

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/datasets"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.series: what a chart of series reads beside its buckets (internal/widget/params/series.go,
// internal/widget/traffic/trafficseries.go, internal/widget/datasets/datasetseries.go): the traffic
// of the window per bucket, a data-catalogue dataset read as a series, and the envelope of a series
// of its own -- its providers, its condition, its unresolved references -- for a chart whose series
// each degrade on their own.
//
//	host.series.traffic(filter, width)       the traffic of the window per bucket start and traffic
//	                                         direction, in the measure the request is answered under:
//	                                         {buckets, covered}. buckets maps a bucket start to
//	                                         {direction: summable figures}; covered is the interval read,
//	                                         null for none. filter is {interfaces, client_id, rule_id, asn,
//	                                         country, site}, each null or left out for no restriction, and
//	                                         measure: the name of a declared measure to read whatever the
//	                                         request is answered under (a series whose measure is
//	                                         <measure>_bytes or <measure>_packets).
//	                                         Whole-hour buckets the flows no longer hold are read from the
//	                                         1 h slots where only interfaces restrict the series, and say so
//	                                         by a named state
//	host.series.measureState(measure)        null when a declared measure can answer the window (its
//	                                         window states recorded as covered throughout it,
//	                                         internal/widget/request/measure.go); otherwise the named state,
//	                                         which the answer then carries: a series of the measure carries
//	                                         no figure. A name no plugin declares is the script's mistake
//	host.series.providers()                  every registered provider with its availability and condition
//	host.series.kindCondition(availability, kind)
//	                                         the condition of a provider kind among an availability list:
//	                                         its active providers' best
//	host.series.bind(kinds)                  adds every registered provider of those kinds to the
//	                                         response's availability: the kinds a widget binds only once it
//	                                         has read its parameters
//	host.series.window()                     the response's window report: {requested, covered, coverage,
//	                                         flow_horizon_at}, null for a widget that is not windowed
//	host.series.unresolved()                 the response's unresolved references, series entries' included
//	host.series.dataset(source)              the data-catalogue dataset a series source names, when it is
//	                                         not one of the five sources: {name, freshness_kind}, or null
//	host.series.fillDataset(index, window, gaps, width)
//	                                         reads series entry index from its dataset onto the window's
//	                                         buckets: {window, measure, unit, buckets, total, rows}, window
//	                                         being the series' report with the dataset's coverage, total
//	                                         null unless the aggregation adds up; null when the entry names
//	                                         no dataset

// trafficRead is a traffic series' figures as a script reads them.
type trafficRead struct {
	Buckets map[int64]map[string]traffic.SummableFigures `json:"buckets"`
	Covered *envelope.Interval                           `json:"covered"`
}

// trafficFilter is a traffic series' scope as a script writes it.
type trafficFilter struct {
	Interfaces []int64 `json:"interfaces"`
	ClientID   *int64  `json:"client_id"`
	RuleID     *int64  `json:"rule_id"`
	Asn        *int64  `json:"asn"`
	Country    *string `json:"country"`
	Site       *string `json:"site"`
	Measure    string  `json:"measure"`
}

// seriesDatasetRef names the dataset a series reads.
type seriesDatasetRef struct {
	Name          string `json:"name"`
	FreshnessKind string `json:"freshness_kind"`
}

// datasetSeriesRead is a dataset series as a script reads it.
type datasetSeriesRead struct {
	Window  *envelope.WindowReport `json:"window"`
	Measure string                 `json:"measure"`
	Unit    string                 `json:"unit"`
	Buckets []traffic.Bucket       `json:"buckets"`
	Total   *float64               `json:"total"`
	Rows    bool                   `json:"rows"`
}

func init() {
	script.RegisterHostArea("series", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "traffic", func(arguments goja.FunctionCall) goja.Value {
			var filter trafficFilter
			if !script.Absent(arguments.Argument(0)) {
				call.DecodeFlat(arguments.Argument(0), "the filter", &filter)
			}
			width := call.IntegerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.Throw("the bucket width is %d, not a positive number of seconds", width)
			}
			if !r.Preset.Windowed {
				call.Throw("series.traffic reads a rolling window, and the widget %s is not windowed", r.Preset.Type)
			}
			if _, declared := vocab.MeasureNamed(filter.Measure); filter.Measure != "" && !declared {
				call.Throw("no plugin declares the measure %q", filter.Measure)
			}
			buckets, covered, err := traffic.TrafficBuckets(r, traffic.TrafficFilter{Interfaces: filter.Interfaces,
				ClientID: filter.ClientID, RuleID: filter.RuleID, Asn: filter.Asn, Country: filter.Country,
				Site: filter.Site, Measure: filter.Measure}, width)
			if err != nil {
				return call.Fail(err)
			}
			read := trafficRead{Buckets: map[int64]map[string]traffic.SummableFigures{}, Covered: covered}
			for start, directions := range buckets {
				read.Buckets[start] = map[string]traffic.SummableFigures{}
				for direction, figures := range directions {
					read.Buckets[start][direction] = traffic.Summable(figures)
				}
			}
			return call.Value(read)
		})
		call.Function(area, "measureState", func(arguments goja.FunctionCall) goja.Value {
			name := call.TextOf(arguments.Argument(0), "the measure")
			measure, declared := vocab.MeasureNamed(name)
			if !declared {
				call.Throw("no plugin declares the measure %q", name)
			}
			state, err := r.MeasureWindowState(measure)
			if err != nil {
				return call.Fail(err)
			}
			if state == "" {
				return goja.Null()
			}
			r.MeasureState(measure, state, false)
			return call.VM.ToValue(state)
		})
		call.Function(area, "providers", func(goja.FunctionCall) goja.Value {
			providers, err := request.ReadProviders(r.Ctx, r.Db)
			if err != nil {
				return call.Fail(err)
			}
			if providers == nil {
				providers = []envelope.ProviderAvailability{}
			}
			return call.Value(providers)
		})
		call.Function(area, "kindCondition", func(arguments goja.FunctionCall) goja.Value {
			var availability []envelope.ProviderAvailability
			if !script.Absent(arguments.Argument(0)) {
				call.DecodeFlat(arguments.Argument(0), "the availability", &availability)
			}
			return call.VM.ToValue(request.KindCondition(availability, call.TextOf(arguments.Argument(1), "the provider kind")))
		})
		call.Function(area, "bind", func(arguments goja.FunctionCall) goja.Value {
			kinds := call.TextsOf(arguments.Argument(0), "the provider kinds")
			if err := r.ReadAvailability(kinds); err != nil {
				return call.Fail(err)
			}
			return goja.Undefined()
		})
		call.Function(area, "window", func(goja.FunctionCall) goja.Value {
			if r.Response.Window == nil {
				return goja.Null()
			}
			return call.Value(r.Response.Window)
		})
		call.Function(area, "unresolved", func(goja.FunctionCall) goja.Value {
			unresolved := r.Response.UnresolvedReferences
			if unresolved == nil {
				unresolved = []params.UnresolvedReference{}
			}
			return call.Value(unresolved)
		})
		call.Function(area, "dataset", func(arguments goja.FunctionCall) goja.Value {
			source := call.TextOf(arguments.Argument(0), "the series source")
			declaration, found := datasets.SeriesDataset(r, params.SeriesEntry{Source: source})
			if !found {
				return goja.Null()
			}
			return call.Value(seriesDatasetRef{Name: declaration.Name, FreshnessKind: declaration.Freshness.Kind})
		})
		call.Function(area, "fillDataset", func(arguments goja.FunctionCall) goja.Value {
			index := int(call.IntegerOf(arguments.Argument(0), "the series entry index"))
			entries := r.Params.Entries("series")
			if index < 0 || index >= len(entries) {
				call.Throw("the request holds no series entry %d", index)
			}
			var report envelope.WindowReport
			call.DecodeFlat(arguments.Argument(1), "the series window", &report)
			var gaps []envelope.Gap
			if !script.Absent(arguments.Argument(2)) {
				call.DecodeFlat(arguments.Argument(2), "the series gaps", &gaps)
			}
			width := call.IntegerOf(arguments.Argument(3), "the width")
			if width <= 0 {
				call.Throw("the bucket width is %d, not a positive number of seconds", width)
			}
			if _, found := datasets.SeriesDataset(r, entries[index]); !found {
				return goja.Null()
			}
			series := traffic.Series{Window: &report, Gaps: gaps}
			rows, err := datasets.FillDatasetSeries(r, &series, entries[index], width)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(datasetSeriesRead{Window: series.Window, Measure: series.Measure, Unit: series.Unit,
				Buckets: series.Buckets, Total: series.Total, Rows: rows})
		})
	})
}
