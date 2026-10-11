package widget

import (
	"github.com/dop251/goja"
)

// host.buckets: the time buckets of a windowed widget's series, as the core cuts them.
//
//	host.buckets.width()                       the bucket width of the request's period, in seconds
//	host.buckets.intervals(window, width)      the buckets of a window, each clipped to it: [{from, to}]
//	host.buckets.floorMod(value, width)        the non-negative remainder of value by width
//	host.buckets.isGap(bucket, covered, gaps)  whether a bucket lies outside the covered interval (null
//	                                           for none) or meets one of the gaps

func init() {
	registerHostArea("buckets", func(call *scriptCall, area *goja.Object) {
		call.function(area, "width", func(goja.FunctionCall) goja.Value {
			return call.vm.ToValue(call.request.BucketWidth())
		})
		call.function(area, "intervals", func(arguments goja.FunctionCall) goja.Value {
			window := call.intervalOf(arguments.Argument(0), "the window")
			width := call.integerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.throw("the bucket width is %d, not a positive number of seconds", width)
			}
			intervals := BucketIntervals(window, width)
			if intervals == nil {
				intervals = []Interval{}
			}
			return call.value(intervals)
		})
		call.function(area, "floorMod", func(arguments goja.FunctionCall) goja.Value {
			width := call.integerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.throw("the width is %d, not a positive number", width)
			}
			return call.vm.ToValue(FloorMod(call.integerOf(arguments.Argument(0), "the value"), width))
		})
		call.function(area, "isGap", func(arguments goja.FunctionCall) goja.Value {
			bucket := call.intervalOf(arguments.Argument(0), "the bucket")
			covered := call.optionalIntervalOf(arguments.Argument(1), "the covered interval")
			var gaps []Gap
			if !absent(arguments.Argument(2)) {
				call.decodeArgument(arguments.Argument(2), "the gaps", &gaps)
			}
			return call.vm.ToValue(IsGapBucket(bucket, covered, gaps))
		})
	})
}
