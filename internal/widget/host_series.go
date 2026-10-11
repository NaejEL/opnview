package widget

import (
	"github.com/dop251/goja"
)

// host.series: what a chart of series reads beside its buckets (series.go, trafficseries.go,
// datasetseries.go): the traffic of the window per bucket, a data-catalogue dataset read as a series,
// and the envelope of a series of its own -- its providers, its condition, its unresolved references
// -- for a chart whose series each degrade on their own.
//
//	host.series.traffic(filter, width)       the traffic of the window per bucket start and traffic
//	                                         direction, in the measure the request is answered under:
//	                                         {buckets, covered}. buckets maps a bucket start to
//	                                         {direction: summable figures}; covered is the interval read,
//	                                         null for none. filter is {interfaces, client_id, rule_id, asn,
//	                                         country, site}, each null or left out for no restriction, and
//	                                         netflow: true to read the netflow measure whatever the request
//	                                         is answered under (a series whose measure is netflow_bytes or
//	                                         netflow_packets).
//	                                         Whole-hour buckets the flows no longer hold are read from the
//	                                         1 h slots where only interfaces restrict the series, and say so
//	                                         by a named state
//	host.series.netflowState()               null when NetFlow can answer the window (one Observation
//	                                         Point established throughout it, measure.go); otherwise the
//	                                         named state, which the answer then carries: a series of the
//	                                         netflow measure carries no figure
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
	Buckets map[int64]map[string]summableFigures `json:"buckets"`
	Covered *Interval                            `json:"covered"`
}

// trafficFilter is a traffic series' scope as a script writes it.
type trafficFilter struct {
	Interfaces []int64 `json:"interfaces"`
	ClientID   *int64  `json:"client_id"`
	RuleID     *int64  `json:"rule_id"`
	Asn        *int64  `json:"asn"`
	Country    *string `json:"country"`
	Site       *string `json:"site"`
	Netflow    bool    `json:"netflow"`
}

// seriesDatasetRef names the dataset a series reads.
type seriesDatasetRef struct {
	Name          string `json:"name"`
	FreshnessKind string `json:"freshness_kind"`
}

// datasetSeriesRead is a dataset series as a script reads it.
type datasetSeriesRead struct {
	Window  *WindowReport `json:"window"`
	Measure string        `json:"measure"`
	Unit    string        `json:"unit"`
	Buckets []Bucket      `json:"buckets"`
	Total   *float64      `json:"total"`
	Rows    bool          `json:"rows"`
}

func init() {
	registerHostArea("series", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "traffic", func(arguments goja.FunctionCall) goja.Value {
			var filter trafficFilter
			if !absent(arguments.Argument(0)) {
				call.decodeArgument(arguments.Argument(0), "the filter", &filter)
			}
			width := call.integerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.throw("the bucket width is %d, not a positive number of seconds", width)
			}
			if !r.Preset.Windowed {
				call.throw("series.traffic reads a rolling window, and the widget %s is not windowed", r.Preset.Type)
			}
			buckets, covered, err := r.TrafficBuckets(TrafficFilter{Interfaces: filter.Interfaces,
				ClientID: filter.ClientID, RuleID: filter.RuleID, Asn: filter.Asn, Country: filter.Country,
				Site: filter.Site, Netflow: filter.Netflow}, width)
			if err != nil {
				return call.fail(err)
			}
			read := trafficRead{Buckets: map[int64]map[string]summableFigures{}, Covered: covered}
			for start, directions := range buckets {
				read.Buckets[start] = map[string]summableFigures{}
				for direction, figures := range directions {
					read.Buckets[start][direction] = summable(figures)
				}
			}
			return call.value(read)
		})
		call.function(area, "netflowState", func(goja.FunctionCall) goja.Value {
			state, err := r.NetflowWindowState()
			if err != nil {
				return call.fail(err)
			}
			if state == "" {
				return goja.Null()
			}
			r.netflowState(state, false)
			return call.vm.ToValue(state)
		})
		call.function(area, "providers", func(goja.FunctionCall) goja.Value {
			providers, err := ReadProviders(r.Ctx, r.Db)
			if err != nil {
				return call.fail(err)
			}
			if providers == nil {
				providers = []ProviderAvailability{}
			}
			return call.value(providers)
		})
		call.function(area, "kindCondition", func(arguments goja.FunctionCall) goja.Value {
			var availability []ProviderAvailability
			if !absent(arguments.Argument(0)) {
				call.decodeArgument(arguments.Argument(0), "the availability", &availability)
			}
			return call.vm.ToValue(KindCondition(availability, call.textOf(arguments.Argument(1), "the provider kind")))
		})
		call.function(area, "bind", func(arguments goja.FunctionCall) goja.Value {
			kinds := call.textsOf(arguments.Argument(0), "the provider kinds")
			if err := r.readAvailability(kinds); err != nil {
				return call.fail(err)
			}
			return goja.Undefined()
		})
		call.function(area, "window", func(goja.FunctionCall) goja.Value {
			if r.Response.Window == nil {
				return goja.Null()
			}
			return call.value(r.Response.Window)
		})
		call.function(area, "unresolved", func(goja.FunctionCall) goja.Value {
			unresolved := r.Response.UnresolvedReferences
			if unresolved == nil {
				unresolved = []UnresolvedReference{}
			}
			return call.value(unresolved)
		})
		call.function(area, "dataset", func(arguments goja.FunctionCall) goja.Value {
			source := call.textOf(arguments.Argument(0), "the series source")
			declaration, found := r.SeriesDataset(SeriesEntry{Source: source})
			if !found {
				return goja.Null()
			}
			return call.value(seriesDatasetRef{Name: declaration.Name, FreshnessKind: declaration.Freshness.Kind})
		})
		call.function(area, "fillDataset", func(arguments goja.FunctionCall) goja.Value {
			index := int(call.integerOf(arguments.Argument(0), "the series entry index"))
			entries := r.Params.Entries("series")
			if index < 0 || index >= len(entries) {
				call.throw("the request holds no series entry %d", index)
			}
			var report WindowReport
			call.decodeArgument(arguments.Argument(1), "the series window", &report)
			var gaps []Gap
			if !absent(arguments.Argument(2)) {
				call.decodeArgument(arguments.Argument(2), "the series gaps", &gaps)
			}
			width := call.integerOf(arguments.Argument(3), "the width")
			if width <= 0 {
				call.throw("the bucket width is %d, not a positive number of seconds", width)
			}
			if _, found := r.SeriesDataset(entries[index]); !found {
				return goja.Null()
			}
			series := Series{Window: &report, Gaps: gaps}
			rows, err := r.FillDatasetSeries(&series, entries[index], width)
			if err != nil {
				return call.fail(err)
			}
			return call.value(datasetSeriesRead{Window: series.Window, Measure: series.Measure, Unit: series.Unit,
				Buckets: series.Buckets, Total: series.Total, Rows: rows})
		})
	})
}
