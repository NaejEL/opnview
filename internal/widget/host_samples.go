package widget

import (
	"github.com/dop251/goja"
)

// host.samples: the firewall's sampled readings (samples.go) -- its own gauges and its interface
// counters, as measurement_sample holds them. A sample is a value at an instant: a bucket of a longer
// period wants an aggregation, never a count, and a bucket with no sample is a gap, never a 0.
//
//	host.samples.metrics                     per health metric, the subject kind and the measures the
//	                                         sampler stores it under: {kind, measures}
//	host.samples.subjects(metric)            the subjects that reported one of a metric's measures in the
//	                                         window: [{kind, key, measure, unit}], by measure then key
//	host.samples.points(subject)             one subject's samples over the window, oldest first:
//	                                         [{at, value}]
//	host.samples.throughput(device, measure, direction)
//	                                         one interface counter as points: {points, resets}. measure is
//	                                         bits_per_second, bytes, packets or errors and direction is in
//	                                         or out; a point is the per-second rate (times 8 for bits) or
//	                                         the increase between two samples, stamped at the later one,
//	                                         and a counter that went down is a reset, counted and never a
//	                                         point
//	host.samples.aggregate(window, width, points, aggregation)
//	                                         points cut into the window's buckets, each combined by mean,
//	                                         min, max, sum or last: [{from, to, gap, value}], a bucket no
//	                                         point falls in being a gap with no value

// sampledSubject is one measured subject as a script reads it.
type sampledSubject struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Measure string `json:"measure"`
	Unit    string `json:"unit"`
}

// sampledMetric is a health metric's mapping as a script reads it.
type sampledMetric struct {
	Kind     string   `json:"kind"`
	Measures []string `json:"measures"`
}

// throughputRead is one counter's points as a script reads them.
type throughputRead struct {
	Points []Point `json:"points"`
	Resets int     `json:"resets"`
}

func init() {
	registerHostArea("samples", func(call *scriptCall, area *goja.Object) {
		r := call.request
		metrics := map[string]sampledMetric{}
		for name, mapping := range MetricMeasures {
			measures := make([]string, 0, len(mapping.Measures))
			for _, measure := range mapping.Measures {
				measures = append(measures, string(measure))
			}
			metrics[name] = sampledMetric{Kind: string(mapping.Kind), Measures: measures}
		}
		_ = area.Set("metrics", call.value(metrics))
		call.function(area, "subjects", func(arguments goja.FunctionCall) goja.Value {
			metric := call.textOf(arguments.Argument(0), "the metric")
			if _, known := MetricMeasures[metric]; !known {
				call.throw("%q is not a health metric", metric)
			}
			subjects, err := r.MeasuredSubjects(metric)
			if err != nil {
				return call.fail(err)
			}
			read := make([]sampledSubject, 0, len(subjects))
			for _, item := range subjects {
				read = append(read, sampledSubject{Kind: item.Kind, Key: item.Key, Measure: item.Measure, Unit: item.Unit})
			}
			return call.value(read)
		})
		call.function(area, "points", func(arguments goja.FunctionCall) goja.Value {
			var item sampledSubject
			call.decodeArgument(arguments.Argument(0), "the subject", &item)
			points, err := r.SamplesOf(subject{Kind: item.Kind, Key: item.Key, Measure: item.Measure, Unit: item.Unit})
			if err != nil {
				return call.fail(err)
			}
			if points == nil {
				points = []Point{}
			}
			return call.value(points)
		})
		call.function(area, "throughput", func(arguments goja.FunctionCall) goja.Value {
			device := call.textOf(arguments.Argument(0), "the device")
			measure := call.textOf(arguments.Argument(1), "the throughput measure")
			direction := call.textOf(arguments.Argument(2), "the direction")
			mapping, known := ThroughputMeasures[measure]
			if !known {
				call.throw("%q is not a throughput measure", measure)
			}
			counter := mapping.In
			switch direction {
			case "in":
			case "out":
				counter = mapping.Out
			default:
				call.throw("the direction is %q, not in or out", direction)
			}
			points, resets, err := r.ThroughputPoints(device, counter, mapping.PerSecond, mapping.Factor)
			if err != nil {
				return call.fail(err)
			}
			if points == nil {
				points = []Point{}
			}
			return call.value(throughputRead{Points: points, Resets: resets})
		})
		call.function(area, "aggregate", func(arguments goja.FunctionCall) goja.Value {
			window := call.intervalOf(arguments.Argument(0), "the window")
			width := call.integerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.throw("the bucket width is %d, not a positive number of seconds", width)
			}
			var points []Point
			call.decodeArgument(arguments.Argument(2), "the points", &points)
			aggregation := call.textOf(arguments.Argument(3), "the aggregation")
			buckets := AggregateBuckets(window, width, points, aggregation)
			if buckets == nil {
				return goja.Null()
			}
			return call.value(buckets)
		})
	})
}
